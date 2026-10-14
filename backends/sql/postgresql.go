package sql

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
)

type builder struct {
	predicateFailure      error
	predicates            map[*predicateNode]string
	ctx                   context.Context
	program               hff.Program
	bindings              map[string]string
	qualifiedNativePaths  map[string]string
	physicalBindings      []Binding
	args                  []any
	steps                 []string
	reserved              map[string]bool
	alias                 int
	methods               map[string]MethodImplementation
	compositeBindings     map[string]bool
	jsonBindings          map[string]JSONFormat
	structBindings        map[string]Binding
	relationBindings      map[string]Binding
	jsonValues            map[string]JSONFormat
	normalizedJSONValues  map[string]normalizedJSON
	parameters            map[hff.BindingID]jsonParameter
	sharedPaths           map[string]expression
	physicalParameters    map[hff.BindingID]Binding
	parameterItems        map[hff.BindingID]Binding
	parameterProvenance   map[hff.BindingID]Binding
	activeRelationAliases map[string]int
	orderBlind            map[hff.Node]bool
	literalConversions    map[hff.Node]literalConversion
	identityAliases       map[hff.Node]bool
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
	case hff.NullType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType, hff.BytesType, hff.DurationType:
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
	// Parsed JSON has a stable validated alias even when separate AST nodes
	// describe the same call. Share its decoded carrier and literal fields
	// only after lowering establishes their type, format, and presence.
	if parsedKey, parsed := b.shareableParsedValue(n, value); parsed {
		key, share = parsedKey, true
		if cached, exists := b.sharedPaths[key]; exists {
			return cached, nil
		}
	}
	// A typed parameter reference is already an immutable SQL value. Keep it
	// bound directly; projecting it adds a relation without preventing any
	// duplicated computation. Identity aliases reuse their surviving operand's
	// projection; other computed expressions still need their barriers.
	if n.Kind() == hff.LiteralNode || b.literalConversions[n].handled || b.identityAliases[n] {
		return value, nil
	}
	// A qualified raw column is already named in its caller-owned row scope.
	// Repeating that reference does not repeat computation. Keep computed
	// values and no-value flags behind their existing projection barriers.
	if b.qualifiedNativePath(n, value) {
		if share {
			b.rememberPath(key, value)
		}
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
	source := value.code
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
		if n.Kind() != hff.GuardNode && n.Kind() != hff.TypeGuardNode {
			b.copyNormalizedJSON(source, value.code, format)
		}
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
		return fmt.Sprint("parameter:", binding.ID(), ":", pathKey(fields), ":", n.Type().Key(), b.pathOrderClass(n)), true
	}
	if root.Kind() != hff.VariableNode {
		return "", false
	}
	path, ok := n.Path()
	if !ok {
		return "", false
	}
	if _, bound := b.bindings[pathKey(path)]; bound {
		return "schema:" + pathKey(path) + ":" + n.Type().Key() + b.pathOrderClass(n), true
	}
	for size := len(path); size > 0; size-- {
		if _, bound := b.jsonBindings[pathKey(path[:size])]; bound {
			return "schema:" + pathKey(path) + ":" + n.Type().Key() + b.pathOrderClass(n), true
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
		if item, exists := b.parameterItems[binding.ID()]; exists {
			// Native Duration items already carry integral nanoseconds. Passing
			// them through JSON would discard their HFF type (and currently
			// rejects the representation entirely).
			if kind, scalar := scalarKind(parameter.typ); scalar && kind == hff.DurationType && item.format == "" && !item.composite && item.structure == nil && item.relation == nil {
				column, err := quoteColumn(item.column)
				if err != nil {
					return expression{}, err
				}
				return expression{code: column, present: "TRUE", kind: hff.DurationType}, nil
			}
			if item.timestamp {
				code, err := timestampBindingExpression(item)
				if err != nil {
					return expression{}, err
				}
				return expression{code: code, present: "TRUE", kind: hff.TimestampType}, nil
			}
			code, format, err := b.physicalItemJSON(n, item, parameter.typ)
			if err != nil {
				return expression{}, err
			}
			parameter.code, parameter.format = code, format
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
		if !ok && timestampSchema(n.Type()) {
			kind, ok = hff.TimestampType, true
		}
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
		code := b.truth(child, value)
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
			parts[i] = b.truth(child, value)
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
		kind, ok := computedScalarKind(n.Type())
		if !ok {
			return expression{}, b.failure(n, "SQL type guards require a supported scalar result")
		}
		if kind != value.kind {
			// A disjoint non-null guard can only yield no-value (or null when
			// explicitly permitted); do not cast an incompatible SQL value.
			value.code = "NULL"
			switch kind {
			case hff.IntType, hff.DurationType:
				value.code = "NULL::bigint"
			case hff.TimestampType:
				value.code = "NULL::numeric"
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
		case hff.AnyType:
			return b.dynamicJSONNegation(n, value)
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
	projected := name + ".v"
	b.copyNormalizedJSON(code, projected, JSON)
	return projected
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

func (b *builder) truth(n hff.Node, value expression) string {
	var test string
	switch value.kind {
	case hff.AnyType:
		raw := "(" + value.code + "::json)"
		text := "(" + raw + " #>> '{}'::text[])"
		stringTest := text + "<>''"
		if jsonRootHasKind(b.jsonStoredType(n), hff.DurationType) {
			// Duration(0) is false even though its canonical JSON string is
			// nonempty. A recoverable union can use the JSON string class for
			// exactly one tagged HFF family at this position.
			stringTest = "(" + b.parseDurationInline(text) + "<>0)"
		}
		test = "(CASE json_typeof(" + raw + ") WHEN 'null' THEN FALSE WHEN 'boolean' THEN " + text + "::boolean WHEN 'number' THEN " + jsonFloatCast(text) + "<>0 WHEN 'string' THEN " + stringTest + " WHEN 'array' THEN json_array_length(" + raw + ")>0 WHEN 'object' THEN EXISTS(SELECT 1 FROM json_object_keys(" + raw + ")) ELSE FALSE END)"

	case hff.NullType:
		test = "FALSE"
	case hff.BoolType:
		test = value.code
	case hff.IntType, hff.FloatType, hff.DurationType:
		test = "(" + value.code + " <> 0)"
	case hff.ListType:
		test = "(json_array_length(" + value.code + "::json)>0)"
	case hff.StructType, hff.MapType:
		test = "EXISTS(SELECT 1 FROM json_object_keys(" + value.code + "::json))"
	case hff.BytesType:
		test = "(octet_length(" + value.code + ")>0)"
	case hff.TimestampType:
		test = "(" + value.code + " IS NOT NULL)"
	case hff.StringType:
		test = "(" + value.code + " COLLATE \"C\" <> '' COLLATE \"C\")"
	}
	return both(value.present, "COALESCE("+test+", FALSE)")
}

func (b *builder) literal(n hff.Node) (expression, error) {
	value, handled, err := b.constantValue(n)
	if err != nil {
		return expression{}, err
	}
	if !handled {
		return expression{}, b.failure(n, "expected a supported constant SQL value")
	}
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
	case hff.DurationKind:
		duration, _ := value.AsDuration()
		arg = int64(duration)
		typ = "bigint"
		kind = hff.DurationType
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
		if err := b.checkTextParameter(n, s); err != nil {
			return expression{}, err
		}
		arg = s
		typ = "text"
		kind = hff.StringType
	case hff.BytesKind:
		bytes, _ := value.AsBytes()
		b.args = append(b.args, hex.EncodeToString(bytes))
		return expression{fmt.Sprintf("decode($%d::text,'hex')", len(b.args)), "TRUE", hff.BytesType}, nil
	case hff.TimestampKind:
		stamp, _ := value.AsTimestamp()
		if stamp.UTC().Year() < 0 || stamp.UTC().Year() > 9999 {
			return expression{"NULL::numeric", "TRUE", hff.TimestampType}, nil
		}
		b.args = append(b.args, timestampNanoseconds(stamp))
		return expression{fmt.Sprintf("($%d::numeric)", len(b.args)), "TRUE", hff.TimestampType}, nil
	default:
		return expression{}, b.failure(n, "unsupported SQL parameter type")
	}
	b.args = append(b.args, arg)
	return expression{fmt.Sprintf("($%d::%s)", len(b.args), typ), "TRUE", kind}, nil
}

// Validate text even when a lowering path replaces it with a translated
// parameter or constant result, so unused source values keep their contract.
func (b *builder) checkTextParameter(n hff.Node, text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return b.failure(n, "PostgreSQL text cannot encode invalid UTF-8 or NUL")
	}
	return nil
}

func (b *builder) binary(n hff.Node) (expression, error) {
	delete(b.identityAliases, n)
	op, _ := n.Operator()
	switch op {
	case hff.Match, hff.NotMatch, hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
	case hff.Add, hff.Subtract, hff.Multiply, hff.Divide, hff.Remainder:
	default:
		return expression{}, b.failure(n, "PostgreSQL does not yet support operator "+string(op))
	}
	if identity, ok := b.identityPlan(n); ok {
		return b.lowerIdentity(n, identity)
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
	if op == hff.Match || op == hff.NotMatch {
		return b.regexCall(n, op, left, right)
	}
	if op == hff.Add || op == hff.Subtract || op == hff.Multiply || op == hff.Divide || op == hff.Remainder {
		return b.arithmetic(n, left, right, op)
	}
	if left.kind == hff.NullType || right.kind == hff.NullType {
		return boolean("FALSE"), nil
	}
	if left.kind == hff.AnyType || right.kind == hff.AnyType {
		return b.dynamicJSONComparison(n, left, right, op)
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
	return b.scalarComparison(left, right, op), nil
}

func (b *builder) arithmetic(n hff.Node, left, right expression, op hff.Operator) (expression, error) {
	present := both(left.present, "("+left.code+" IS NULL OR "+right.present+")")
	if left.kind == hff.NullType {
		return expression{"NULL", left.present, hff.NullType}, nil
	}
	if right.kind == hff.NullType {
		return expression{"NULL", present, hff.NullType}, nil
	}
	if left.kind == hff.AnyType || right.kind == hff.AnyType {
		return b.dynamicJSONArithmetic(n, left, right, op)
	}
	if left.kind != hff.IntType || right.kind != hff.IntType {
		if (left.kind == hff.IntType || left.kind == hff.FloatType) && (right.kind == hff.IntType || right.kind == hff.FloatType) {
			return b.floatArithmetic(n, left, right, op)
		}
		return expression{}, b.failure(n, "arithmetic requires recoverable SQL numbers")
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
	comparison := b.mixedNumericOrder(integer.code, floating.code)
	if reversed {
		comparison = "(-(" + comparison + "))"
	}
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	return boolean(both(both(left.present, right.present), "COALESCE(("+comparison+" "+operator+" 0), FALSE)"))
}

// mixedNumericOrder shares native numeric intermediates within the current
// projection scope. The ordered carrier pair determines its -1/0/1 result;
// each comparison retains its own operator, reversal and presence checks.
func (b *builder) mixedNumericOrder(i, f string) string {
	pairKey := "mixed-numeric-order:" + pathKey([]string{i, f})
	if cached, ok := b.sharedPaths[pairKey]; ok {
		return cached.code
	}
	// The positive bound is 2^63, not float64(MaxInt64), which rounds up to
	// this bound. Inside [−2^63,2^63), floor(f) is an exactly representable
	// integer and the bigint cast is safe and exact. No int64 is rounded to
	// float64, and no float64 is rounded through PostgreSQL numeric.
	const low = "'-9223372036854775808'::double precision"
	const high = "'9223372036854775808'::double precision"
	floorKey := "mixed-numeric-floor:" + f
	floor := b.sharedPaths[floorKey].code
	if floor == "" {
		floor = b.project("(CASE WHEN " + f + " >= " + low + " AND " + f + " < " + high + " THEN floor(" + f + ")::bigint ELSE NULL END)")
		b.rememberPath(floorKey, expression{floor, "TRUE", hff.IntType})
	}
	comparison := b.project("(CASE WHEN " + i + " IS NULL OR " + f + " IS NULL THEN NULL WHEN " + f + " >= " + high + " THEN -1 WHEN " + f + " < " + low + " THEN 1 WHEN " + i + " < " + floor + " THEN -1 WHEN " + i + " > " + floor + " THEN 1 WHEN " + f + " > floor(" + f + ") THEN -1 ELSE 0 END)")
	b.rememberPath(pairKey, expression{comparison, "TRUE", hff.IntType})
	return comparison
}

func (b *builder) call(n hff.Node) (expression, error) {
	method, _ := n.Method()
	name := method.Name()
	if name == "string" && n.ChildCount() != 1 {
		return expression{}, b.failure(n, "PostgreSQL does not yet support timestamp string formatting with a time zone")
	}
	if signatures := method.Signatures(); len(signatures) > 0 {
		if _, custom := signatures[0].Declaration(); custom {
			return b.extension(n, method)
		}
	}
	if value, handled, err := b.constantValue(n); handled || err != nil {
		if err != nil {
			return expression{}, err
		}
		return b.foldedParameter(n, value)
	}
	if name == "timestamp" {
		return b.timestampLiteralCall(n)
	}
	if name == "any" || name == "all" {
		return b.jsonPredicate(n, name)
	}
	if name == "size" {
		if value, handled, err := b.relationSize(n); handled {
			return value, err
		}
	}
	if name == "has" {
		if value, handled, err := b.relationHas(n); handled {
			return value, err
		}
	}
	if name == "map" || name == "flatmap" {
		return b.jsonTransform(n, name)
	}
	if name == "foldl" || name == "foldr" {
		return b.jsonFold(n, name)
	}
	if name == "size" {
		if value, handled, err := b.relationSize(n); handled {
			return value, err
		}
	}
	if name == "has" {
		if value, handled, err := b.relationHas(n); handled {
			return value, err
		}
	}
	switch name {
	case "duration", "hours", "minutes", "seconds", "milliseconds", "microseconds", "nanoseconds", "year", "month", "dayofmonth", "dayofyear", "before", "after", "add", "matches", "base64", "base64r", "frombase64", "frombase64r", "lower", "upper", "round", "ceil", "floor", "bool", "number", "fromjson", "json", "string", "exists", "type", "len", "contains", "startswith", "endswith", "concat", "not", "and", "or", "size", "has", "get", "keys", "values", "items", "in", "unique", "sort":
	default:
		return expression{}, b.failure(n, "PostgreSQL does not yet support method "+name)
	}
	if n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType {
		switch name {
		case "year", "month", "dayofmonth", "dayofyear", "before", "after", "add":
			receiverNode, _ := n.Child(0)
			if receiverNode.Type().Kind() == hff.NullType || receiverNode.Type().Kind() == hff.NeverType {
				receiver, err := b.expression(receiverNode)
				if err != nil {
					return expression{}, err
				}
				return expression{"NULL", receiver.present, hff.NullType}, nil
			}
		}
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
	if receiver.kind == hff.TimestampType {
		return b.timestampCall(n, name, values)
	}
	if receiver.kind == hff.AnyType && (name == "year" || name == "month" || name == "dayofmonth" || name == "dayofyear" || name == "hours" || name == "minutes" || name == "seconds" || name == "milliseconds" || name == "before" || name == "after" || name == "add") {
		receiverNode, _ := n.Child(0)
		if jsonRootHasKind(b.jsonStoredType(receiverNode), hff.TimestampType) {
			if format, ok := b.jsonValues[receiver.code]; ok {
				class := string(format) + "_typeof(" + receiver.code + ")"
				text := "(CASE WHEN " + class + "='string' THEN " + receiver.code + " #>> '{}'::text[] ELSE NULL::text END)"
				values[0] = expression{jsonTimestampNanoseconds(text), receiver.present, hff.TimestampType}
				return b.timestampCall(n, name, values)
			}
		}
	}
	if name == "duration" || name == "hours" || name == "minutes" || name == "seconds" || name == "milliseconds" || name == "microseconds" || name == "nanoseconds" {
		return b.durationCall(n, name, receiver)
	}
	if name == "sort" {
		return b.jsonSort(n, receiver)
	}
	if name == "matches" {
		return b.regexCall(n, "", receiver, values[1])
	}
	if name == "base64" || name == "base64r" || name == "frombase64" || name == "frombase64r" || (name == "len" && receiver.kind == hff.BytesType) {
		return b.bytesCall(n, name, receiver)
	}
	if name == "round" || name == "ceil" || name == "floor" {
		return b.numericRounding(n, name, receiver)
	}
	if name == "bool" || name == "number" {
		return b.primitiveConversion(n, name, receiver)
	}
	if name == "fromjson" {
		return b.jsonParseCall(n, receiver)
	}
	if name == "get" || name == "values" || name == "items" {
		return b.jsonObjectAccess(n, name, values)
	}
	if name == "size" || name == "has" || name == "keys" {
		for _, value := range values {
			if value.kind == hff.AnyType {
				return b.dynamicJSONShape(n, name, values)
			}
		}
	}
	if name == "concat" {
		for _, value := range values {
			if value.kind == hff.AnyType {
				return b.dynamicJSONConcat(n, values)
			}
		}
	}
	if name == "contains" {
		for _, value := range values {
			if value.kind == hff.AnyType {
				return b.dynamicJSONContains(n, values)
			}
		}
	}
	if name == "json" || name == "string" {
		return b.serialize(n, receiver, name)
	}
	if name == "in" {
		if receiver.kind == hff.DurationType {
			return b.durationIn(n, values)
		}
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
		case hff.BytesType:
			typ = "bytes"
		case hff.DurationType:
			typ = "duration"
		case hff.TimestampType:
			typ = "timestamp"
		}
		return expression{"(CASE WHEN " + receiver.code + " IS NULL THEN 'null' ELSE '" + typ + "' END)", receiver.present, hff.StringType}, nil
	}
	if receiver.kind == hff.NullType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	if name == "not" || name == "and" || name == "or" {
		for _, value := range values {
			if value.kind == hff.AnyType {
				return b.dynamicJSONBooleanCall(n, name, values)
			}
		}
	}
	if receiver.kind == hff.BoolType {
		return b.booleanCall(n, name, values)
	}
	if name == "lower" || name == "upper" || name == "len" || name == "startswith" || name == "endswith" {
		for _, value := range values {
			if value.kind == hff.AnyType {
				return b.dynamicJSONStringCall(n, name, values)
			}
		}
	}
	return b.stringCall(n, name, values)
}

// Reuse an unordered materialization only within the same order-demand class.
// Otherwise an earlier membership test could corrupt a later list index.
func (b *builder) pathOrderClass(n hff.Node) string {
	// Only a materialized relation path can differ across order contexts.
	if len(b.relationBindings) == 0 && len(b.structBindings) == 0 && len(b.physicalParameters) == 0 {
		return ""
	}
	if n.Type().Kind() != hff.ListType && n.Type().Kind() != hff.UnionType {
		return ""
	}
	mapping, ok := b.relationMapping(n, b.physicalParameters)
	if !ok || mapping.relation == nil || len(mapping.relation.config.order) == 0 {
		return ""
	}
	if b.orderBlind[n] {
		return ":unordered"
	}
	return ":ordered"
}
