package core

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// DecodeJSONString preserves JSON semantics while rejecting unpaired surrogate escapes.
func DecodeJSONString(raw string) (string, error) {
	if !utf8.ValidString(raw) || len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("invalid JSON string")
	}
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw)-1 {
			return "", fmt.Errorf("incomplete escape")
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return "", fmt.Errorf("incomplete Unicode escape")
		}
		value, err := strconv.ParseUint(raw[i+1:i+5], 16, 16)
		if err != nil {
			return "", err
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return "", fmt.Errorf("unpaired low surrogate")
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1:i+3] != "\\u" {
				return "", fmt.Errorf("unpaired high surrogate")
			}
			low, err := strconv.ParseUint(raw[i+3:i+7], 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return "", fmt.Errorf("unpaired high surrogate")
			}
			i += 6
		}
	}
	var result string
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return "", err
	}
	return result, nil
}
