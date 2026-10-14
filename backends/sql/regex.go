package sql

import (
	"fmt"
	"regexp/syntax"
	"strings"

	"github.com/ornew/hff"
)

// Translate a conservative RE2 language subset rather than forwarding pattern
// spelling to a different regex dialect. Captures and greediness do not affect
// Boolean match existence, but anchors and character families do.
func postgresRegex(re *syntax.Regexp) (string, error) {
	unsupported := func() (string, error) { return "", fmt.Errorf("unsupported RE2 pattern construct: %s", re.Op) }
	switch re.Op {
	case syntax.OpEmptyMatch:
		return "(?:)", nil
	case syntax.OpNoMatch:
		return "(?!)", nil
	case syntax.OpBeginText:
		return `\A`, nil
	case syntax.OpEndText:
		return `\Z`, nil
	case syntax.OpLiteral, syntax.OpCharClass:
		if re.Op == syntax.OpCharClass && len(re.Rune) == 0 {
			return "(?!)", nil
		}
		if re.Flags&syntax.FoldCase != 0 {
			return unsupported()
		}
		var out strings.Builder
		if re.Op == syntax.OpCharClass {
			out.WriteByte('[')
		}
		for i, r := range re.Rune {
			if r == 0 || r > 127 {
				return unsupported()
			}
			if re.Op == syntax.OpCharClass && i%2 == 1 {
				out.WriteByte('-')
			}
			fmt.Fprintf(&out, `\u%04X`, r)
		}
		if re.Op == syntax.OpCharClass {
			out.WriteByte(']')
		}
		return out.String(), nil
	case syntax.OpCapture:
		return postgresRegex(re.Sub[0])
	case syntax.OpConcat, syntax.OpAlternate:
		parts := make([]string, len(re.Sub))
		for i, sub := range re.Sub {
			part, err := postgresRegex(sub)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		separator := ""
		if re.Op == syntax.OpAlternate {
			separator = "|"
		}
		return "(?:" + strings.Join(parts, separator) + ")", nil
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		if regexContainsAnchor(re.Sub[0]) || (re.Op == syntax.OpRepeat && (re.Min > 255 || re.Max > 255)) {
			return unsupported()
		}
		part, err := postgresRegex(re.Sub[0])
		if err != nil {
			return "", err
		}
		quantifier := map[syntax.Op]string{syntax.OpStar: "*", syntax.OpPlus: "+", syntax.OpQuest: "?"}[re.Op]
		if re.Op == syntax.OpRepeat {
			if re.Max < 0 {
				quantifier = fmt.Sprintf("{%d,}", re.Min)
			} else {
				quantifier = fmt.Sprintf("{%d,%d}", re.Min, re.Max)
			}
		}
		return "(?:" + part + ")" + quantifier, nil
	default:
		return unsupported()
	}
}

func regexContainsAnchor(re *syntax.Regexp) bool {
	if re.Op == syntax.OpBeginText || re.Op == syntax.OpEndText {
		return true
	}
	for _, sub := range re.Sub {
		if regexContainsAnchor(sub) {
			return true
		}
	}
	return false
}

// A malformed pattern evaluates to null, but a valid pattern outside the
// supported RE2 subset is a Build error. Validate the original spelling even
// when only its translated value will be bound to a SQL parameter.
func (b *builder) literalRegexPattern(n hff.Node) (string, bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", false, err
	}
	value, literal := n.Literal()
	if !literal {
		return "", false, b.failure(n, "PostgreSQL regex requires a literal pattern")
	}
	text, stringPattern := value.AsString()
	if !stringPattern {
		return "", false, nil
	}
	if err := b.checkTextParameter(n, text); err != nil {
		return "", false, err
	}
	parsed, err := syntax.Parse(text, syntax.Perl)
	if err != nil {
		return "", false, nil
	}
	translated, err := postgresRegex(parsed)
	if err != nil {
		return "", false, b.failure(n, "PostgreSQL regex supports only literal ASCII patterns, absolute anchors, ASCII classes, alternation, and repetition through 255")
	}
	return translated, true, nil
}

func (b *builder) regexCall(n hff.Node, operator hff.Operator, receiver, pattern expression) (expression, error) {
	if operator == "" && (n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType) {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	patternNode, _ := n.Child(1)
	if _, literal := patternNode.Literal(); !literal {
		// Preserve the whole-expression diagnostic for dynamic patterns.
		return expression{}, b.failure(n, "PostgreSQL regex requires a literal pattern")
	}
	translated, valid, err := b.literalRegexPattern(patternNode)
	if err != nil {
		return expression{}, err
	}
	match := "NULL::boolean"
	if valid {
		arg, err := b.parameter(patternNode, hff.String(translated))
		if err != nil {
			return expression{}, err
		}
		input, err := b.stringConversionInput(n, receiver)
		if err != nil {
			return expression{}, err
		}
		match = "regexp_like(" + input + " COLLATE \"C\"," + arg.code + ",'cs')"
		if operator == hff.NotMatch {
			match = "(NOT " + match + ")"
		}
	}
	if operator == hff.Match || operator == hff.NotMatch {
		code := "(CASE WHEN NOT " + receiver.present + " OR " + receiver.code + " IS NULL OR NOT " + pattern.present + " OR " + pattern.code + " IS NULL THEN FALSE ELSE " + match + " END)"
		return expression{code, "TRUE", hff.BoolType}, nil
	}
	present := presenceBefore(receiver, pattern.present)
	return expression{match, present, hff.BoolType}, nil
}
