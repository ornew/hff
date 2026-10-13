package sql

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
)

type literalConversion struct {
	value   hff.Value
	handled bool
	err     error
}

// A failed conversion is HFF null, but its SQL carrier still has the type
// established by ordinary lowering. Extension functions may be overloaded or
// polymorphic, so an untyped SQL NULL is not an interchangeable argument.
func (b *builder) foldedParameter(n hff.Node, value hff.Value) (expression, error) {
	result, err := b.parameter(n, value)
	if err != nil || value.Kind() != hff.NullKind || n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType {
		return result, err
	}
	if kind, ok := computedScalarKind(n.Type()); ok {
		typ := sqlType(kind)
		if kind == hff.TimestampType {
			typ = "numeric"
		}
		result.code = "NULL::" + typ
	} else if method, ok := n.Method(); ok {
		signatures := method.Signatures()
		if len(signatures) == 1 && signatures[0].ID() == "string.number" {
			// An unresolved Int/Float result uses JSON in ordinary lowering.
			result.code = "NULL::json"
		}
	}
	return result, nil
}

// constantValue recognizes a closed set of builtin conversions without
// binding parameters or invoking backend extensions. Eligibility and lowering
// share this cache, so exact duration parsing happens once per plan node.
func (b *builder) constantValue(n hff.Node) (hff.Value, bool, error) {
	if value, ok := n.Literal(); ok {
		return value, true, nil
	}
	if n.Kind() != hff.CallNode || n.ChildCount() != 1 {
		return hff.Value{}, false, nil
	}
	if cached, ok := b.literalConversions[n]; ok {
		return cached.value, cached.handled, cached.err
	}
	value, handled, err := b.convertLiteral(n)
	if b.literalConversions == nil {
		b.literalConversions = make(map[hff.Node]literalConversion)
	}
	b.literalConversions[n] = literalConversion{value, handled, err}
	return value, handled, err
}

func (b *builder) convertLiteral(n hff.Node) (hff.Value, bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return hff.Value{}, false, err
	}
	method, ok := n.Method()
	if !ok {
		return hff.Value{}, false, nil
	}
	signatures := method.Signatures()
	if len(signatures) != 1 {
		return hff.Value{}, false, nil
	}
	if _, custom := signatures[0].Declaration(); custom {
		return hff.Value{}, false, nil
	}
	id := signatures[0].ID()
	switch id {
	case "string.bool", "string.number", "string.duration", "string.timestamp",
		"string.frombase64", "string.frombase64r", "bytes.base64", "bytes.base64r", "any.string":
	default:
		return hff.Value{}, false, nil
	}
	receiver, _ := n.Child(0)
	if id == "string.timestamp" && receiver.Kind() != hff.LiteralNode {
		// Keep the existing literal-only timestamp support boundary.
		return hff.Value{}, false, nil
	}
	value, handled, err := b.constantValue(receiver)
	if !handled || err != nil {
		return hff.Value{}, handled, err
	}
	text, stringValue := value.AsString()
	// Preserve text parameter rejection even when decoding would yield null.
	// The existing timestamp literal parser consumes text without binding it.
	if stringValue && id != "string.timestamp" && (!utf8.ValidString(text) || strings.ContainsRune(text, 0)) {
		return hff.Value{}, true, b.failure(receiver, "PostgreSQL text cannot encode invalid UTF-8 or NUL")
	}
	if id == "any.string" {
		switch value.Kind() {
		case hff.NullKind:
			text = "null"
		case hff.BoolKind:
			v, _ := value.AsBool()
			text = strconv.FormatBool(v)
		case hff.IntKind:
			v, _ := value.AsInt()
			text = strconv.FormatInt(v, 10)
		case hff.StringKind:
		case hff.BytesKind:
			v, _ := value.AsBytes()
			text = base64.StdEncoding.EncodeToString(v)
		case hff.TimestampKind:
			v, _ := value.AsTimestamp()
			text = v.UTC().Format(time.RFC3339Nano)
		default:
			// Float formatting belongs to the backend model; containers and
			// durations retain their existing serialization kernels.
			return hff.Value{}, false, nil
		}
		return hff.String(text), true, nil
	}
	if value.Kind() == hff.NullKind {
		return hff.Null(), true, nil
	}
	if id == "bytes.base64" || id == "bytes.base64r" {
		data, ok := value.AsBytes()
		if !ok {
			return hff.Value{}, false, nil
		}
		encoding := base64.StdEncoding
		if id == "bytes.base64r" {
			encoding = base64.RawStdEncoding
		}
		return hff.String(encoding.EncodeToString(data)), true, nil
	}
	if !stringValue {
		return hff.Value{}, false, nil
	}
	result := hff.Null()
	switch id {
	case "string.bool":
		if text == "true" || text == "false" {
			result = hff.Bool(text == "true")
		}
	case "string.number":
		// Never replace native PostgreSQL Float parsing with Go parsing.
		if strings.ContainsAny(text, ".eE") {
			return hff.Value{}, false, nil
		}
		if len(text) > 0 && (text[0] == '-' || text[0] >= '0' && text[0] <= '9') && text[len(text)-1] >= '0' && text[len(text)-1] <= '9' && json.Valid([]byte(text)) {
			if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
				result = hff.Int(integer)
			}
		}
	case "string.duration":
		result = hff.Value(core.ParseDuration(text))
	case "string.timestamp":
		if stamp, ok := parseTimestampLiteral(text); ok {
			result = hff.Timestamp(stamp)
		}
	case "string.frombase64", "string.frombase64r":
		encoding := base64.StdEncoding.Strict()
		if id == "string.frombase64r" {
			encoding = base64.RawStdEncoding.Strict()
		}
		if !strings.ContainsAny(text, "\r\n") {
			if data, err := encoding.DecodeString(text); err == nil {
				result = hff.Bytes(data)
			}
		}
	}
	if err := checkContext(b.ctx); err != nil {
		return hff.Value{}, true, err
	}
	return result, true, nil
}
