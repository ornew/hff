package sql

import (
	"strings"
	"unicode"
)

// SQL translate applies one-to-one code point substitutions without locale
// case rules. Use the same versioned Unicode tables as the local evaluator.
var simpleLower = unicodeCaseMapping(unicode.ToLower)
var simpleUpper = unicodeCaseMapping(unicode.ToUpper)

type caseMapping struct{ from, to string }

func unicodeCaseMapping(convert func(rune) rune) caseMapping {
	var from, to strings.Builder
	for _, entry := range unicode.CaseRanges {
		for point := entry.Lo; point <= entry.Hi; point++ {
			r := rune(point)
			mapped := convert(r)
			if mapped != r {
				from.WriteRune(r)
				to.WriteRune(mapped)
			}
		}
	}
	return caseMapping{from.String(), to.String()}
}
