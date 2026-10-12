package sql

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
)

type builder struct {
	ctx               context.Context
	program           hff.Program
	bindings          map[string]string
	args              []any
	steps             []string
	reserved          map[string]bool
	alias             int
	methods           map[string]MethodImplementation
	compositeBindings map[string]bool
	jsonBindings      map[string]JSONFormat
	structBindings    map[string]Binding
	relationBindings  map[string]Binding
	jsonValues        map[string]JSONFormat
	parameters        map[hff.BindingID]jsonParameter
	sharedPaths       map[string]expression
}

// present tracks HFF's internal no-value separately from SQL NULL.
type expression struct {
	code    string
	present string
	kind    hff.TypeKind
}

func scalarKind(typ hff.Type) (hff.TypeKind, bool) {
	if typ.Kind() == hff.UnionType {
		kind := hff.NullType
		for _, member := range typ.Members() {
			if member.Kind() == hff.NullType {
				continue
			}
			k, ok := scalarKind(member)
			if !ok || (kind != hff.NullType && kind != k) {
				return hff.InvalidType, false
			}
			kind = k
		}
		return kind, true
	}
	switch typ.Kind() {
	case hff.NullType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType:
		return typ.Kind(), true
	default:
		return hff.InvalidType, false
	}
}

func (b *builder) failure(n hff.Node, message string) error {
	span := n.Span()
	start := span.Start
	source := b.program.Source()
	if start < 0 {
		start = 0
	}
	if start > len(source) {
		start = len(source)
	}
	line, column := 1, 1
	for _, r := range source[:start] {
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return &hff.Error{Code: hff.BackendError, Message: message, Span: &span, Line: line, Column: column}
}

func (b *builder) expression(n hff.Node) (expression, error) {
	if err := checkContext(b.ctx); err != nil {
		return expression{}, err
	}
	key, share := b.shareablePath(n)
	if share {
		if value, exists := b.sharedPaths[key]; exists {
			return value, nil
		}
	}
	value, err := b.lower(n)
	if err != nil {
		return expression{}, err
	}
	// A typed parameter reference is already an immutable SQL value. Keep it
	// bound directly; projecting it adds a relation without preventing any
	// duplicated computation. Computed expressions still need their barriers.
	if n.Kind() == hff.LiteralNode {
		return value, nil
	}
	// Give each value and any dynamic no-value flag a relational name. Parent
	// nodes reference these names rather than expanding child SQL repeatedly.
	// Preserve constant presence so it simplifies surrounding conditions.
	if value.kind == hff.NullType && value.present == "TRUE" {
		if share {
			b.rememberPath(key, value)
		}
		return value, nil
	}
	format, isJSON := b.jsonValues[value.code]
	alias := b.nextAlias()
	if value.kind == hff.NullType {
		b.steps = append(b.steps, "(SELECT "+value.present+" AS p OFFSET 0) AS "+alias)
		value.present = alias + ".p"
		return value, nil
	}
	// OFFSET 0 keeps PostgreSQL from pulling these projections into their
	// consumers and recursively duplicating a chained expression during
	// planning. Each step still returns exactly one row.
	projection := "(SELECT " + value.code + " AS v"
	if value.present != "TRUE" {
		projection += ", " + value.present + " AS p"
		value.present = alias + ".p"
	}
	b.steps = append(b.steps, projection+" OFFSET 0) AS "+alias)
	value.code = alias + ".v"
	if isJSON {
		b.jsonValues[value.code] = format
	}
	if share {
		b.rememberPath(key, value)
	}
	return value, nil
}

// Share unguarded bound paths only inside their current projection scope.
// Parameter identity distinguishes captures from shadowed callback names.
func (b *builder) shareablePath(n hff.Node) (string, bool) {
	root := n
	var fields []string
	for root.Kind() == hff.FieldNode {
		field, _ := root.Field()
		fields = append(fields, field)
		root, _ = root.Child(0)
	}
	if root.Kind() == hff.ParameterNode {
		binding, ok := root.Binding()
		if !ok {
			return "", false
		}
		if _, exists := b.parameters[binding.ID()]; !exists {
			return "", false
		}
		return fmt.Sprint("parameter:", binding.ID(), ":", pathKey(fields), ":", n.Type().Key()), true
	}
	if root.Kind() != hff.VariableNode {
		return "", false
	}
	path, ok := n.Path()
	if !ok {
		return "", false
	}
	if _, bound := b.bindings[pathKey(path)]; bound {
		return "schema:" + pathKey(path) + ":" + n.Type().Key(), true
	}
	for size := len(path); size > 0; size-- {
		if _, bound := b.jsonBindings[pathKey(path[:size])]; bound {
			return "schema:" + pathKey(path) + ":" + n.Type().Key(), true
		}
	}
	return "", false
}

func (b *builder) rememberPath(key string, value expression) {
	if b.sharedPaths == nil {
		b.sharedPaths = make(map[string]expression)
	}
	b.sharedPaths[key] = value
}

func (b *builder) lower(n hff.Node) (expression, error) {
	switch n.Kind() {
	case hff.MatchAllNode:
		return boolean("TRUE"), nil
	case hff.LiteralNode:
		return b.literal(n)
	case hff.ParameterNode:
		binding, ok := n.Binding()
		if !ok {
			return expression{}, b.failure(n, "callback parameter has no binding")
		}
		parameter, ok := b.parameters[binding.ID()]
		if !ok {
			return expression{}, b.failure(n, "unbound callback parameter")
		}
		typ := n.Type()
		if parameter.typ.Kind() != hff.InvalidType {
			typ = parameter.typ
		}
		return b.jsonDecode(n, parameter.code, parameter.format, typ, "TRUE")
	case hff.VariableNode, hff.FieldNode:
		if value, handled, err := b.jsonPath(n); handled {
			return value, err
		}
		path, ok := n.Path()
		if !ok {
			return expression{}, b.failure(n, "SQL requires a statically bound schema path")
		}
		if n.Kind() == hff.FieldNode {
			// Do not flatten away guards or dynamic accesses on a parent.
			parent, _ := n.Child(0)
			for parent.Valid() {
				if parent.Kind() != hff.VariableNode && parent.Kind() != hff.FieldNode {
					return expression{}, b.failure(n, "guarded parent paths require a structured SQL representation")
				}
				parent, _ = parent.Child(0)
			}
		}
		column, ok := b.bindings[pathKey(path)]
		if !ok {
			return expression{}, b.failure(n, "unbound schema path")
		}
		kind, ok := scalarKind(n.Type())
		if !ok {
			return expression{}, b.failure(n, "SQL column requires one scalar type, optionally nullable")
		}
		return expression{column, "TRUE", kind}, nil
	case hff.BoolNode, hff.NotNode:
		child, _ := n.Child(0)
		value, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		code := truth(value)
		if n.Kind() == hff.NotNode {
			code = "(NOT " + code + ")"
		}
		return boolean(code), nil
	case hff.AndNode, hff.OrNode:
		parts := make([]string, n.ChildCount())
		for i := range parts {
			child, _ := n.Child(i)
			value, err := b.expression(child)
			if err != nil {
				return expression{}, err
			}
			parts[i] = truth(value)
		}
		join := " AND "
		if n.Kind() == hff.OrNode {
			join = " OR "
		}
		return boolean("(" + strings.Join(parts, join) + ")"), nil
	case hff.GuardNode:
		child, _ := n.Child(0)
		value, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		if acceptsKind(child.Type(), hff.NullType) {
			value.present = both(value.present, "("+value.code+" IS NOT NULL)")
		}
		return value, nil
	case hff.IndexNode:
		return b.jsonIndex(n)
	case hff.TypeGuardNode:
		child, _ := n.Child(0)
		value, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		constraint, _ := n.Constraint()
		// A schema-compatible value already satisfies an identical constraint.
		// Preserve its existing no-value state without rechecking its contents.
		if child.Type().Equal(constraint) {
			return value, nil
		}
		if format, structured := b.jsonValues[value.code]; structured {
			return b.jsonTypeGuard(n, value, format, child.Type(), constraint)
		}
		acceptNull := acceptsKind(constraint, hff.NullType)
		acceptValue := acceptsKind(constraint, value.kind)
		test := "FALSE"
		if acceptNull && acceptValue {
			test = "TRUE"
		} else if acceptNull {
			if acceptsKind(child.Type(), hff.NullType) {
				test = "(" + value.code + " IS NULL)"
			}
		} else if acceptValue {
			test = "TRUE"
			if acceptsKind(child.Type(), hff.NullType) {
				test = "(" + value.code + " IS NOT NULL)"
			}
		}
		value.present = both(value.present, test)
		kind, ok := scalarKind(n.Type())
		if !ok {
			return expression{}, b.failure(n, "SQL type guards require a supported scalar result")
		}
		if kind != value.kind {
			// A disjoint non-null guard can only yield no-value (or null when
			// explicitly permitted); do not cast an incompatible SQL value.
			value.code = "NULL"
			switch kind {
			case hff.IntType:
				value.code = "NULL::bigint"
			case hff.FloatType:
				value.code = "NULL::double precision"
			case hff.BoolType:
				value.code = "NULL::boolean"
			case hff.StringType:
				value.code = "NULL::text"
			}
			value.kind = kind
		}
		return value, nil
	case hff.NegateNode:
		child, _ := n.Child(0)
		value, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		switch value.kind {
		case hff.NullType:
			return value, nil
		case hff.IntType:
			value.code = "(CASE WHEN " + value.code + " = '-9223372036854775808'::bigint THEN NULL ELSE -(" + value.code + ") END)"
		case hff.FloatType:
			value.code = "(-(" + value.code + "))"
		default:
			return expression{}, b.failure(n, "unsupported numeric negation")
		}
		return value, nil
	case hff.NullTestNode:
		child, _ := n.Child(0)
		value, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		test := " IS NULL"
		op, _ := n.Operator()
		if !acceptsKind(child.Type(), hff.NullType) {
			if op == hff.NotEqual {
				return boolean(value.present), nil
			}
			return boolean("FALSE"), nil
		}
		if op == hff.NotEqual {
			test = " IS NOT NULL"
		}
		return boolean(both(value.present, "("+value.code+test+")")), nil
	case hff.BinaryNode:
		return b.binary(n)
	case hff.CallNode:
		return b.call(n)
	case hff.ListNode, hff.ObjectNode:
		return b.jsonLiteral(n)
	default:
		return expression{}, b.failure(n, "PostgreSQL does not yet support "+n.Kind().String())
	}
}

func boolean(code string) expression { return expression{code, "TRUE", hff.BoolType} }

// project names an internal SQL-only intermediate using the same projection
// barrier as typed plan nodes. It does not introduce an HFF value or parameter.
func (b *builder) project(code string) string {
	name := b.nextAlias()
	b.steps = append(b.steps, "(SELECT "+code+" AS v OFFSET 0) AS "+name)
	return name + ".v"
}

func (b *builder) nextAlias() string {
	for {
		b.alias++
		name := fmt.Sprintf("__hff_%d", b.alias)
		if !b.reserved[name] {
			return `"` + name + `"`
		}
	}
}

func acceptsKind(typ hff.Type, kind hff.TypeKind) bool {
	if typ.Kind() == hff.AnyType {
		return true
	}
	if typ.Kind() == hff.UnionType {
		for _, member := range typ.Members() {
			if acceptsKind(member, kind) {
				return true
			}
		}
		return false
	}
	return typ.Kind() == kind
}

func both(a, b string) string {
	if a == "TRUE" {
		return b
	}
	if b == "TRUE" {
		return a
	}
	return "(" + a + " AND " + b + ")"
}

func truth(value expression) string {
	var test string
	switch value.kind {
	case hff.NullType:
		test = "FALSE"
	case hff.BoolType:
		test = value.code
	case hff.IntType, hff.FloatType:
		test = "(" + value.code + " <> 0)"
	case hff.ListType:
		test = "(" + value.code + "::jsonb <> '[]'::jsonb)"
	case hff.StructType, hff.MapType:
		test = "(" + value.code + "::jsonb <> '{}'::jsonb)"
	case hff.StringType:
		test = "(" + value.code + " COLLATE \"C\" <> '' COLLATE \"C\")"
	}
	return both(value.present, "COALESCE("+test+", FALSE)")
}

func (b *builder) literal(n hff.Node) (expression, error) {
	value, _ := n.Literal()
	return b.parameter(n, value)
}

func (b *builder) parameter(n hff.Node, value hff.Value) (expression, error) {
	var arg any
	var typ string
	kind := hff.InvalidType
	switch value.Kind() {
	case hff.NullKind:
		return expression{"NULL", "TRUE", hff.NullType}, nil
	case hff.BoolKind:
		arg, _ = value.AsBool()
		typ = "boolean"
		kind = hff.BoolType
	case hff.IntKind:
		arg, _ = value.AsInt()
		typ = "bigint"
		kind = hff.IntType
	case hff.FloatKind:
		f, _ := value.AsFloat()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return expression{}, b.failure(n, "non-finite float parameter")
		}
		arg = f
		typ = "double precision"
		kind = hff.FloatType
	case hff.StringKind:
		s, _ := value.AsString()
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return expression{}, b.failure(n, "PostgreSQL text cannot encode invalid UTF-8 or NUL")
		}
		arg = s
		typ = "text"
		kind = hff.StringType
	default:
		return expression{}, b.failure(n, "unsupported SQL parameter type")
	}
	b.args = append(b.args, arg)
	return expression{fmt.Sprintf("($%d::%s)", len(b.args), typ), "TRUE", kind}, nil
}

func (b *builder) binary(n hff.Node) (expression, error) {
	op, _ := n.Operator()
	switch op {
	case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
	case hff.Add, hff.Subtract, hff.Multiply, hff.Divide, hff.Remainder:
	default:
		return expression{}, b.failure(n, "PostgreSQL does not yet support operator "+string(op))
	}
	l, _ := n.Child(0)
	r, _ := n.Child(1)
	left, err := b.expression(l)
	if err != nil {
		return expression{}, err
	}
	right, err := b.expression(r)
	if err != nil {
		return expression{}, err
	}
	if op == hff.Add || op == hff.Subtract || op == hff.Multiply || op == hff.Divide || op == hff.Remainder {
		return b.arithmetic(n, left, right, op)
	}
	if left.kind == hff.NullType || right.kind == hff.NullType {
		return boolean("FALSE"), nil
	}
	if structuredKind(left.kind) && structuredKind(right.kind) {
		return b.jsonComparison(n, l, r, left, right, op)
	}
	if left.kind != right.kind {
		if (left.kind == hff.IntType && right.kind == hff.FloatType) || (left.kind == hff.FloatType && right.kind == hff.IntType) {
			return b.mixedComparison(left, right, op), nil
		}
		return expression{}, b.failure(n, "mixed SQL scalar comparisons require exact numeric lowering")
	}
	leftCode, rightCode := left.code, right.code
	if left.kind == hff.StringType {
		leftCode += " COLLATE \"C\""
		rightCode += " COLLATE \"C\""
	}
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	return boolean(both(both(left.present, right.present), "COALESCE(("+leftCode+" "+operator+" "+rightCode+"), FALSE)")), nil
}

func (b *builder) arithmetic(n hff.Node, left, right expression, op hff.Operator) (expression, error) {
	present := both(left.present, "("+left.code+" IS NULL OR "+right.present+")")
	if left.kind == hff.NullType {
		return expression{"NULL", left.present, hff.NullType}, nil
	}
	if right.kind == hff.NullType {
		return expression{"NULL", present, hff.NullType}, nil
	}
	if left.kind != hff.IntType || right.kind != hff.IntType {
		return expression{}, b.failure(n, "PostgreSQL floating-point arithmetic requires finite-result and underflow lowering")
	}
	l, r := left.code, right.code
	if op == hff.Divide {
		// Integer inputs converted to float64 before division have no overflow
		// or underflow risk. The smallest nonzero quotient is at least 1/2^63.
		return expression{"(" + l + "::double precision / NULLIF(" + r + "::double precision, 0))", present, hff.FloatType}, nil
	}
	if op == hff.Remainder {
		r = "NULLIF(" + r + "::numeric, 0)"
	} else {
		r += "::numeric"
	}
	intermediate := b.project("(" + l + "::numeric " + string(op) + " " + r + ")")
	code := "(CASE WHEN " + intermediate + " BETWEEN '-9223372036854775808'::numeric AND '9223372036854775807'::numeric THEN " + intermediate + "::bigint ELSE NULL END)"
	return expression{code, present, hff.IntType}, nil
}

func (b *builder) mixedComparison(left, right expression, op hff.Operator) expression {
	integer, floating := left, right
	reversed := left.kind == hff.FloatType
	if reversed {
		integer, floating = right, left
	}
	i, f := integer.code, floating.code
	// The positive bound is 2^63, not float64(MaxInt64), which rounds up to
	// this bound. Inside [−2^63,2^63), floor(f) is an exactly representable
	// integer and the bigint cast is safe and exact. No int64 is rounded to
	// float64, and no float64 is rounded through PostgreSQL numeric.
	const low = "'-9223372036854775808'::double precision"
	const high = "'9223372036854775808'::double precision"
	floor := b.project("(CASE WHEN " + f + " >= " + low + " AND " + f + " < " + high + " THEN floor(" + f + ")::bigint ELSE NULL END)")
	comparison := b.project("(CASE WHEN " + i + " IS NULL OR " + f + " IS NULL THEN NULL WHEN " + f + " >= " + high + " THEN -1 WHEN " + f + " < " + low + " THEN 1 WHEN " + i + " < " + floor + " THEN -1 WHEN " + i + " > " + floor + " THEN 1 WHEN " + f + " > floor(" + f + ") THEN -1 ELSE 0 END)")
	if reversed {
		comparison = "(-(" + comparison + "))"
	}
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	return boolean(both(both(left.present, right.present), "COALESCE(("+comparison+" "+operator+" 0), FALSE)"))
}

func (b *builder) call(n hff.Node) (expression, error) {
	method, _ := n.Method()
	name := method.Name()
	if signatures := method.Signatures(); len(signatures) > 0 {
		if _, custom := signatures[0].Declaration(); custom {
			return b.extension(n, method)
		}
	}
	if name == "any" || name == "all" {
		return b.jsonPredicate(n, name)
	}
	if name == "map" || name == "flatmap" {
		return b.jsonTransform(n, name)
	}
	if name == "foldl" || name == "foldr" {
		return b.jsonFold(n, name)
	}
	switch name {
	case "tojson", "tostring", "exists", "type", "len", "contains", "startswith", "endswith", "concat", "not", "and", "or", "size", "has", "get", "keys", "values", "items", "in", "unique", "sort":
	default:
		return expression{}, b.failure(n, "PostgreSQL does not yet support method "+name)
	}
	values := make([]expression, n.ChildCount())
	for i := range values {
		child, _ := n.Child(i)
		value, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		values[i] = value
	}
	receiver := values[0]
	if name == "tojson" || name == "tostring" {
		return b.serialize(n, receiver, name)
	}
	if name == "in" {
		return b.jsonIn(n, values)
	}
	if format, structured := b.jsonValues[receiver.code]; structured {
		return b.jsonCall(n, name, values, format)
	}
	if name == "exists" {
		return boolean(both(receiver.present, "("+receiver.code+" IS NOT NULL)")), nil
	}
	if name == "type" {
		typ := "null"
		switch receiver.kind {
		case hff.BoolType:
			typ = "bool"
		case hff.IntType, hff.FloatType:
			typ = "number"
		case hff.StringType:
			typ = "string"
		}
		return expression{"(CASE WHEN " + receiver.code + " IS NULL THEN 'null' ELSE '" + typ + "' END)", receiver.present, hff.StringType}, nil
	}
	// A null value ends ordinary method argument evaluation. Later no-value
	// arguments must not turn that already determined null into no-value.
	present := "TRUE"
	for i := len(values) - 1; i >= 0; i-- {
		present = both(values[i].present, "("+values[i].code+" IS NULL OR "+present+")")
	}
	if receiver.kind == hff.NullType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	if receiver.kind == hff.BoolType {
		var code string
		switch name {
		case "not":
			code = "(NOT " + receiver.code + ")"
		case "and", "or":
			// HFF short-circuit and/or skip the argument (including no-value)
			// when the receiver already determines the result.
			arg := values[1]
			if arg.kind != hff.BoolType && arg.kind != hff.NullType {
				return expression{}, b.failure(n, "unsupported boolean method argument")
			}
			op, decisive := "AND", "FALSE"
			if name == "or" {
				op, decisive = "OR", "TRUE"
			}
			code = "(CASE WHEN " + receiver.code + " IS NULL THEN NULL ELSE (" + receiver.code + " " + op + " " + arg.code + ") END)"
			present = both(receiver.present, "("+receiver.code+" IS NULL OR "+receiver.code+" IS "+decisive+" OR "+arg.present+")")
		default:
			return expression{}, b.failure(n, "unsupported boolean method")
		}
		return expression{code, present, hff.BoolType}, nil
	}
	if receiver.kind != hff.StringType {
		return expression{}, b.failure(n, "method requires a supported scalar SQL receiver")
	}
	for _, arg := range values[1:] {
		if arg.kind != hff.StringType && arg.kind != hff.NullType {
			return expression{}, b.failure(n, "method requires string SQL arguments")
		}
	}
	code := receiver.code
	switch name {
	case "len":
		return expression{"char_length(" + code + ")::bigint", present, hff.IntType}, nil
	case "concat":
		return expression{"(" + code + " || " + values[1].code + ")", present, hff.StringType}, nil
	case "contains":
		parts := make([]string, len(values)-1)
		for i, arg := range values[1:] {
			parts[i] = "(strpos(" + code + " COLLATE \"C\", " + arg.code + " COLLATE \"C\") > 0)"
		}
		// Explicit null propagation matters when another argument does not match.
		nulls := make([]string, len(values))
		for i, value := range values {
			nulls[i] = value.code + " IS NULL"
		}
		return expression{"(CASE WHEN " + strings.Join(nulls, " OR ") + " THEN NULL ELSE (" + strings.Join(parts, " AND ") + ") END)", present, hff.BoolType}, nil
	case "startswith", "endswith":
		fn := "left"
		if name == "endswith" {
			fn = "right"
		}
		arg := values[1].code
		return expression{"(" + fn + "(" + code + ", char_length(" + arg + ")) COLLATE \"C\" = " + arg + " COLLATE \"C\")", present, hff.BoolType}, nil
	default:
		return expression{}, b.failure(n, "unsupported string method")
	}
}
