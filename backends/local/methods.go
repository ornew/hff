package local

import (
	"fmt"
	"strings"

	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

func supportedMethod(id string) bool {
	switch id {
	case "string.fromjson", "any.json", "any.string", "any.type", "any.exists", "any.in", "bool.and", "bool.or", "bool.not", "number.round", "number.ceil", "number.floor", "string.len", "bytes.len", "string.contains", "string.matches", "string.startswith", "string.endswith", "string.lower", "string.upper", "string.bool", "string.number", "string.concat":
		return true
	case "list.size", "list.has", "list.contains", "list.unique", "list.sort", "list.concat", "object.size", "object.has", "object.get", "object.keys", "object.values", "object.items":
		return true
	case "list.any", "list.all", "list.map", "list.flatmap", "list.foldl", "list.foldr":
		return true
	case "string.frombase64", "bytes.base64":
		return true
	case "string.timestamp", "string.duration", "timestamp.year", "timestamp.month", "timestamp.dayofmonth", "timestamp.dayofyear", "timestamp.hours", "timestamp.minutes", "timestamp.seconds", "timestamp.milliseconds", "timestamp.string", "timestamp.after", "timestamp.before", "timestamp.add", "duration.hours", "duration.minutes", "duration.seconds", "duration.milliseconds", "duration.microseconds", "duration.nanoseconds":
		return true
	}
	return false
}
func receiverFamily(value core.Value, family string) bool {
	switch family {
	case "any":
		return true
	case "number":
		return numeric(value)
	case "bool":
		return value.Kind() == core.BoolKind
	case "string":
		return value.Kind() == core.StringKind
	case "bytes":
		return value.Kind() == core.BytesKind
	case "list":
		return value.Kind() == core.ListKind
	case "object":
		return value.Kind() == core.ObjectKind
	case "timestamp":
		return value.Kind() == core.TimestampKind
	case "duration":
		return value.Kind() == core.DurationKind
	}
	return false
}
func (e *evaluation) call(n plan.Node) (result, error) {
	method, _ := n.Method()
	name := method.Name()
	receiver, err := e.child(n, 0)
	if err != nil {
		return result{}, err
	}
	if _, extension := method.SignaturesView()[0].Declaration(); extension {
		return e.extension(n, method, receiver)
	}
	if !receiver.present {
		if name == "exists" {
			return ordinary(core.Bool(false)), nil
		}
		return receiver, nil
	}
	if receiver.value.Kind() == core.NullKind {
		switch name {
		case "exists":
			return ordinary(core.Bool(false)), nil
		case "type", "json":
			return ordinary(core.String("null")), nil
		case "string":
			if n.ChildCount() != 1 {
				return null(), nil
			}
			return ordinary(core.String("null")), nil
		}
		return null(), nil
	}
	var signature plan.Signature
	found := false
	for _, candidate := range method.SignaturesView() {
		family, _, _ := strings.Cut(candidate.ID(), ".")
		if receiverFamily(receiver.value, family) {
			signature = candidate
			found = true
			break
		}
	}
	if !found {
		return null(), nil
	}
	if higherMethod(signature.ID()) {
		return e.higher(n, receiver.value, signature.ID())
	}
	// Boolean methods preserve receiver-null propagation but evaluate their
	// argument only when the receiver does not already determine the result.
	if name == "and" || name == "or" {
		b, _ := receiver.value.AsBool()
		if (name == "and" && !b) || (name == "or" && b) {
			return ordinary(core.Bool(b)), nil
		}
	}
	var buffer [4]core.Value
	args := buffer[:0]
	parameters := signature.ParametersView()
	id := signature.ID()
	for i := 1; i < n.ChildCount(); i++ {
		arg, err := e.child(n, i)
		if err != nil || !arg.present {
			return arg, err
		}
		if arg.value.Kind() == core.NullKind && name != "in" && id != "list.contains" {
			return null(), nil
		}
		index := i - 1
		if signature.Variadic() {
			index = 0
		}
		valid, err := e.matchesType(arg.value, parameters[index])
		if err != nil {
			return result{}, err
		}
		if !valid {
			return null(), nil
		}
		args = append(args, arg.value)
	}
	if strings.HasPrefix(id, "list.") || strings.HasPrefix(id, "object.") {
		return e.collectionMethod(id, receiver.value, args)
	}
	if id == "any.in" {
		for _, arg := range args {
			yes, err := e.equal(receiver.value, arg)
			if err != nil {
				return result{}, err
			}
			if yes {
				return ordinary(core.Bool(true)), nil
			}
		}
		return ordinary(core.Bool(false)), nil
	}
	if id == "string.matches" {
		text, _ := receiver.value.AsString()
		pattern, _ := args[0].AsString()
		return e.regex(text, pattern, false)
	}
	switch id {
	case "string.fromjson":
		text, _ := receiver.value.AsString()
		return e.decodeJSON(text, e.limits.MaxJSONDepth)
	case "any.json":
		return e.encodeJSON(receiver.value, e.limits.MaxJSONDepth)
	case "any.string":
		if receiver.value.Kind() == core.ListKind || receiver.value.Kind() == core.ObjectKind {
			return e.encodeJSON(receiver.value, e.limits.MaxJSONDepth)
		}
		text, err := e.scalarText(receiver.value)
		if err != nil {
			return result{}, err
		}
		return ordinary(core.String(text)), nil
	}
	if err := e.scalarCost(id, receiver.value, args); err != nil {
		return result{}, err
	}
	if temporalMethod(id) {
		return e.temporal(id, receiver.value, args)
	}
	value, available := scalarMethod(id, receiver.value, args)
	if !available {
		return result{}, &core.Error{Code: core.BackendError, Message: fmt.Sprintf("local backend does not support %s", id)}
	}
	return ordinary(value), nil
}
func (e *evaluation) scalarCost(id string, receiver core.Value, args []core.Value) error {
	if id == "bytes.base64" {
		data, _ := receiver.BytesView()
		return e.charge(len(data))
	}
	if !strings.HasPrefix(id, "string.") {
		return nil
	}
	if s, ok := receiver.AsString(); ok {
		if err := e.charge(len(s)); err != nil {
			return err
		}
	}
	for _, arg := range args {
		if s, ok := arg.AsString(); ok {
			if err := e.charge(len(s)); err != nil {
				return err
			}
		}
	}
	// Multi-needle containment scans the receiver once per needle.
	if id == "string.contains" {
		text, _ := receiver.AsString()
		for i := 1; i < len(args); i++ {
			if err := e.charge(len(text)); err != nil {
				return err
			}
		}
	}
	return nil
}
