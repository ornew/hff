# Syntax

HFF has one syntax across services and backends. Capability selection changes
which expressions compile, not which expressions can be parsed.

Input must be valid UTF-8. Escapes must decode to Unicode scalar values; paired
surrogate escapes are decoded together, and unpaired surrogates are rejected.
Strings use Unicode code points without implicit
normalization. String length is a code-point count, not a byte or grapheme count.

## Names and access

```hff
foo
foo.age
foo.father.name
`service.name`
`null`
```

`foo` names a schema-declared variable. A dot accesses a field of its receiver.
Backticks quote a name that would otherwise contain punctuation or be interpreted
as a literal keyword. Thus `` `service.name` `` is one name, while
`service.name` accesses `name` on `service`. `` `null` `` names a variable;
`null` is the null literal.

Unquoted names are case-sensitive ASCII identifiers: the first character is a
letter or underscore; following characters are letters, digits, or underscores.
Other names use backticks. The words `true`, `false`, and `null` are reserved
literal keywords. Inside a quoted name, backslash escapes only a backtick or
another backslash; other escapes are syntax errors. Quoted names may contain
any Unicode scalar value, including line breaks, and may be empty. Bare words are variable
references, not implicit string literals: use `name="Alice"` for a comparison.

## Values

```hff
10
1.23
"a"
true
false
null
{"a": 10}
[1, "a", true]
{"a": "12h":duration()}
[1:tostring()]
```

The value families are number, string, bool, null, object, and list. Bytes,
timestamps, and durations can be supplied by the host or produced by methods.
Object values and list elements can contain expressions and method calls.
The last example uses the `tostring` conversion in the [method catalog](methods.md).

Numbers and double-quoted strings use JSON lexical forms, including numeric
exponents and JSON string escapes. Negative numbers use unary minus. Empty
collections are allowed. Duplicate object literal keys are compile-time errors.
Comments and trailing commas are not supported.
Integer spelling selects int64; a decimal point or exponent selects float64.
Access punctuation, call punctuation, and prefix/postfix operators are compact:
`a .b`, `a :len()`, and `! a` are not alternative spellings.

## Operators

| Form | Meaning |
|:--|:--|
| `!a` | Logical NOT after boolean conversion |
| `a\|b` | Logical OR |
| `a b` | Logical AND |
| `a=b`, `a!=b` | Equality, inequality |
| `a~b`, `a!~b` | RE2 regular-expression match, non-match |
| `a<b`, `a<=b` | Less than, less than or equal |
| `a>b`, `a>=b` | Greater than, greater than or equal |
| `a+b`, `a-b` | Addition, subtraction |
| `-a` | Arithmetic negation |
| `a*b`, `a/b`, `a%b` | Multiplication, division, remainder |

Binary value expressions are compact: do not put spaces between an operand and
an arithmetic or comparison operator. Spaces between predicates mean AND.
This is a readability rule: a compact expression forms one visual condition,
and whitespace separates conditions. `age>10 name="Alice"` exposes these units
without requiring the reader to infer which spaces are inside an expression.
`age > 10` is rejected. `a -b` joins predicates; `a-b` is subtraction.

```hff
foo.age>10 foo.father.name~"^A"
```

Spaces, tabs, and line breaks between predicates mean AND. Whitespace inside
string literals does not join predicates. Padding is allowed around `|`, inside
grouping parentheses, and around collection and argument separators; arithmetic
and comparison operators must remain adjacent to their operands. Empty or
whitespace-only input matches every record.

## Grouping and precedence

AND binds more tightly than OR. Parentheses control grouping.

```hff
(age>=10 | age<=5) name="Alice"
age>=10 | age<=5 name="Alice"
```

For a record with `age=10` and `name="Bob"`, the first filter is false and the
second is true. The second groups as `age>=10 | (age<=5 name="Alice")`.

Prefix `!` applies to a comparison as a whole:

```hff
!sex="Female"
!(sex="Female")
```

These two expressions have the same grouping. They are equivalent to
`sex!="Female"` for non-null values; for null, the negated equality is true
and the inequality is false.

The precedence table, from weakest to strongest, is:

| Level | Operations | Associativity |
|--:|:--|:--|
| 1 | `\|` | Left |
| 2 | Predicate separation (AND) | Left |
| 3 | Prefix `!` | Right |
| 4 | `=`, `!=`, `~`, `!~`, `<`, `<=`, `>`, `>=` | Non-associative |
| 5 | `+`, `-` | Left |
| 6 | `*`, `/`, `%` | Left |
| 7 | Prefix `-` | Right |
| 8 | Field access, index access, method calls, postfix `!` | Left |

`age+10<20` means `(age+10)<20`, and `!a b` means `(!a) b`.
A chain such as `a<b<c` is rejected; write separate predicates instead.

## Indexing

```hff
a[i]
a[0]
a[-1]
```

List indices accept numbers whose represented value is an integer, including
`1.0`. They are zero-based. A negative index counts backward from the end:
`-1` denotes the last element. Out-of-range and non-integer indices yield null.
Objects also support string-key indexing: `labels[key]`. Missing keys yield
null. Statically incompatible receiver/index types are rejected; incompatible
dynamic combinations yield null. Struct dot access requires a declared field;
a dynamic string-key lookup can report absence at runtime.

## Method calls

```hff
a:b(a1, a2, a3)
name:lower():startswith("a")
```

The expression before `:` is the receiver. Arguments are comma-separated.
Whether a method is available depends on the receiver's schema type, the
configured capabilities, and the selected backend. See [methods](methods.md).
Ordinary free-function calls are not part of the supplied design.

Arguments, indices, collection values, grouped expressions, and callback bodies
can contain logical expressions as well as arithmetic and comparisons. For
example, `items:map(x=>x>10)` produces booleans and `flag=(1<2)` compares to a
computed boolean. Method argument types are still checked strictly.

Higher-order methods use named arrow callbacks:

```hff
people:map(x=>x.age)
scores:foldl(0,(acc,x)=>acc+x)
scores:foldr(0,(x,acc)=>x+acc)
```

Parameters are inferred from the receiver and accumulator. Callbacks may capture
outer schema variables. They are method arguments, not general function values;
HFF provides no function declarations or recursion. Padding around `=>` is
allowed. Callback scope and nesting are specified in the type-system contract.

## Guarded paths

Postfix `!` checks that the value exists and is not null; it is distinct from
prefix logical NOT. It can guard successive receivers:

```hff
n1!.n2!.n3!.nullvalue=null
n1!.n2!.n3!.nullvalue:exists()
```

Their required predicate expansions are given in
[evaluation](evaluation.md#guarded-paths). Guards are also supported within value
expressions, with the no-value propagation described in evaluation.

## PEGO grammar and typed parsing

The syntax is defined using a `.pego` grammar with declared AST
types and actions, from which PEGO generates a typed Go parser using `-types`.
Typed parsing produces syntax nodes; it does not replace HFF's schema checking.

PEGO supports explicit grammar rules, typed structs and unions, and Pratt
operator levels. HFF must preserve meaningful predicate whitespace: a blanket
Pratt `skip ws` would lose the AND operator. The grammar should separate logical
predicate joining from compact value expressions and treat collection and
argument whitespace locally.

The grammar must encode precedence, distinguish prefix and postfix `!`,
recognize longer operators before their prefixes, and require complete input
consumption. Parsing must remain identical under every capability configuration;
unsupported features are diagnosed after parsing.
