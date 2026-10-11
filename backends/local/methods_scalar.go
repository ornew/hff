package local

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff/internal/core"
)

// scalarMethod implements pure scalar kernels after runtime type dispatch.
func scalarMethod(id string, receiver core.Value, args []core.Value) (core.Value, bool) {
	switch id {
	case "any.type":
		name := receiver.Kind().String()
		if numeric(receiver) {
			name = "number"
		}
		return core.String(name), true
	case "any.exists":
		return core.Bool(receiver.Kind() != core.NullKind), true
	case "bool.not":
		x, ok := receiver.AsBool()
		if !ok {
			return core.Null(), true
		}
		return core.Bool(!x), true
	case "bool.and", "bool.or":
		x, ok := receiver.AsBool()
		y, ok2 := args[0].AsBool()
		if !ok || !ok2 {
			return core.Null(), true
		}
		if id == "bool.and" {
			return core.Bool(x && y), true
		}
		return core.Bool(x || y), true
	case "number.round", "number.ceil", "number.floor":
		if _, ok := receiver.AsInt(); ok {
			return receiver, true
		}
		x, ok := receiver.AsFloat()
		if !ok {
			return core.Null(), true
		}
		switch id {
		case "number.round":
			x = math.Round(x)
		case "number.ceil":
			x = math.Ceil(x)
		case "number.floor":
			x = math.Floor(x)
		}
		return core.Float(x), true
	case "bytes.len":
		b, ok := receiver.BytesView()
		if !ok {
			return core.Null(), true
		}
		return core.Int(int64(len(b))), true
	case "string.len":
		s, ok := receiver.AsString()
		if !ok {
			return core.Null(), true
		}
		return core.Int(int64(utf8.RuneCountInString(s))), true
	case "string.lower", "string.upper":
		s, ok := receiver.AsString()
		if !ok {
			return core.Null(), true
		}
		if id == "string.lower" {
			s = strings.ToLower(s)
		} else {
			s = strings.ToUpper(s)
		}
		return core.String(s), true
	case "string.contains":
		s, ok := receiver.AsString()
		if !ok {
			return core.Null(), true
		}
		for _, v := range args {
			needle, ok := v.AsString()
			if !ok {
				return core.Null(), true
			}
			if !strings.Contains(s, needle) {
				return core.Bool(false), true
			}
		}
		return core.Bool(true), true
	case "string.startswith", "string.endswith", "string.concat":
		s, ok := receiver.AsString()
		arg, ok2 := args[0].AsString()
		if !ok || !ok2 {
			return core.Null(), true
		}
		switch id {
		case "string.startswith":
			return core.Bool(strings.HasPrefix(s, arg)), true
		case "string.endswith":
			return core.Bool(strings.HasSuffix(s, arg)), true
		default:
			return core.String(s + arg), true
		}
	case "string.number":
		s, ok := receiver.AsString()
		if !ok {
			return core.Null(), true
		}
		return parseNumber(s), true
	case "string.bool":
		s, ok := receiver.AsString()
		if !ok {
			return core.Null(), true
		}
		if s == "true" {
			return core.Bool(true), true
		}
		if s == "false" {
			return core.Bool(false), true
		}
		return core.Null(), true
	}
	return core.Value{}, false
}

func parseNumber(s string) core.Value {
	if len(s) == 0 || !(s[0] == '-' || (s[0] >= '0' && s[0] <= '9')) || s[len(s)-1] < '0' || s[len(s)-1] > '9' || !json.Valid([]byte(s)) {
		return core.Null()
	}
	if strings.ContainsAny(s, ".eE") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return core.Null()
		}
		return core.Float(f)
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return core.Null()
	}
	return core.Int(i)
}
