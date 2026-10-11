package local

import (
	"encoding/base64"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ornew/hff/internal/core"
)

type jsonReader struct {
	e         *evaluation
	text      string
	pos       int
	bad       bool
	maxDepth  int
	err       error
	nextCheck int
}

// checkpoint checks long scans without charging the already counted input bytes again.
func (r *jsonReader) checkpoint() bool {
	if r.err != nil {
		return false
	}
	if r.pos >= r.nextCheck {
		r.err = cancelled(r.e.ctx)
		r.nextCheck = r.pos + 4096
	}
	return r.err == nil
}

func (r *jsonReader) whitespace() {
	for r.pos < len(r.text) && strings.ContainsRune(" \t\r\n", rune(r.text[r.pos])) {
		if !r.checkpoint() {
			return
		}
		r.pos++
	}
}
func (r *jsonReader) consume(ch byte) bool {
	r.whitespace()
	if r.err == nil && r.pos < len(r.text) && r.text[r.pos] == ch {
		r.pos++
		return true
	}
	return false
}
func (r *jsonReader) quoted() string {
	start := r.pos
	if r.pos >= len(r.text) || r.text[r.pos] != '"' {
		r.bad = true
		return ""
	}
	r.pos++
	for r.pos < len(r.text) {
		if !r.checkpoint() {
			return ""
		}
		ch := r.text[r.pos]
		r.pos++
		if ch == '\\' {
			r.pos++
			continue
		}
		if ch == '"' {
			value, err := core.DecodeJSONString(r.text[start:r.pos])
			r.err = cancelled(r.e.ctx)
			r.bad = r.bad || err != nil
			return value
		}
	}
	r.bad = true
	return ""
}
func (r *jsonReader) value(depth int) (core.Value, error) {
	if err := r.e.charge(1); err != nil {
		return core.Value{}, err
	}
	if r.maxDepth > 0 && depth > r.maxDepth {
		return core.Value{}, &core.Error{Code: core.LimitError, Message: "JSON depth limit exceeded"}
	}
	if err := r.e.allocate(1); err != nil {
		return core.Value{}, err
	}
	r.whitespace()
	if r.err != nil {
		return core.Value{}, r.err
	}
	if r.pos >= len(r.text) {
		r.bad = true
		return core.Null(), nil
	}
	switch r.text[r.pos] {
	case '"':
		text := r.quoted()
		if r.err != nil {
			return core.Value{}, r.err
		}
		return core.String(text), nil
	case '[':
		r.pos++
		values := []core.Value{}
		if r.consume(']') {
			return core.OwnedList(values), nil
		}
		for {
			v, err := r.value(depth + 1)
			if err != nil || r.bad {
				return core.Null(), err
			}
			values = append(values, v)
			if r.consume(']') {
				return core.OwnedList(values), nil
			}
			if !r.consume(',') {
				r.bad = true
				return core.Null(), nil
			}
		}
	case '{':
		r.pos++
		values := map[string]core.Value{}
		if r.consume('}') {
			return core.OwnedObject(values), nil
		}
		for {
			r.whitespace()
			key := r.quoted()
			if r.err != nil {
				return core.Value{}, r.err
			}
			if _, exists := values[key]; exists {
				r.bad = true
			}
			if r.bad || !r.consume(':') {
				r.bad = true
				return core.Null(), nil
			}
			v, err := r.value(depth + 1)
			if err != nil || r.bad {
				return core.Null(), err
			}
			values[key] = v
			if r.consume('}') {
				return core.OwnedObject(values), nil
			}
			if !r.consume(',') {
				r.bad = true
				return core.Null(), nil
			}
		}
	}
	start := r.pos
	for r.pos < len(r.text) && !strings.ContainsRune(" \t\r\n,]}", rune(r.text[r.pos])) {
		if !r.checkpoint() {
			return core.Value{}, r.err
		}
		r.pos++
	}
	token := r.text[start:r.pos]
	switch token {
	case "null":
		return core.Null(), nil
	case "true":
		return core.Bool(true), nil
	case "false":
		return core.Bool(false), nil
	}
	value := parseNumber(token)
	if value.Kind() == core.NullKind {
		r.bad = true
	}
	return value, nil
}

func (e *evaluation) decodeJSON(text string, maxDepth int) (result, error) {
	if err := e.charge(len(text)); err != nil {
		return result{}, err
	}
	valid, err := core.ValidUTF8(e.ctx, text)
	if err != nil {
		return result{}, err
	}
	if !valid {
		return null(), nil
	}
	r := jsonReader{e: e, text: text, maxDepth: maxDepth}
	value, err := r.value(1)
	if err != nil {
		return result{}, err
	}
	if r.err != nil {
		return result{}, r.err
	}
	r.whitespace()
	if r.err != nil {
		return result{}, r.err
	}
	if err := cancelled(e.ctx); err != nil {
		return result{}, err
	}
	if r.bad || r.pos != len(text) {
		return null(), nil
	}
	return ordinary(value), nil
}

func floatText(f float64) string {
	text := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text
}
func durationText(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	n := uint64(d)
	prefix := ""
	if d < 0 {
		prefix = "-"
		n = uint64(-(d + 1)) + 1
	}
	hours := n / uint64(time.Hour)
	n %= uint64(time.Hour)
	minutes := n / uint64(time.Minute)
	n %= uint64(time.Minute)
	var out strings.Builder
	out.WriteString(prefix)
	if hours > 0 {
		out.WriteString(strconv.FormatUint(hours, 10))
		out.WriteByte('h')
	}
	if minutes > 0 {
		out.WriteString(strconv.FormatUint(minutes, 10))
		out.WriteByte('m')
	}
	if n > 0 || (hours == 0 && minutes == 0) {
		out.WriteString(strconv.FormatUint(n/uint64(time.Second), 10))
		if fraction := n % uint64(time.Second); fraction > 0 {
			digits := strconv.FormatUint(fraction+1000000000, 10)[1:]
			out.WriteByte('.')
			out.WriteString(strings.TrimRight(digits, "0"))
		}
		out.WriteByte('s')
	}
	return out.String()
}

func (e *evaluation) quoteJSON(text string) (string, error) {
	var out strings.Builder
	out.WriteByte('"')
	const hex = "0123456789abcdef"
	for i := 0; i < len(text); i++ {
		if i%4096 == 0 {
			if err := cancelled(e.ctx); err != nil {
				return "", err
			}
		}
		ch := text[i]
		switch ch {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteByte(ch)
		case '\b':
			out.WriteString("\\b")
		case '\f':
			out.WriteString("\\f")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		default:
			if ch < 0x20 {
				out.WriteString("\\u00")
				out.WriteByte(hex[ch>>4])
				out.WriteByte(hex[ch&15])
			} else {
				out.WriteByte(ch)
			}
		}
	}
	out.WriteByte('"')
	if err := cancelled(e.ctx); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (e *evaluation) scalarText(v core.Value) (string, error) {
	switch v.Kind() {
	case core.NullKind:
		return "null", nil
	case core.BoolKind:
		b, _ := v.AsBool()
		return strconv.FormatBool(b), nil
	case core.IntKind:
		n, _ := v.AsInt()
		return strconv.FormatInt(n, 10), nil
	case core.FloatKind:
		n, _ := v.AsFloat()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "", &core.Error{Code: core.BackendError, Message: "non-finite value in serialization"}
		}
		return floatText(n), nil
	case core.StringKind:
		s, _ := v.AsString()
		if err := e.charge(len(s)); err != nil {
			return "", err
		}
		if !utf8.ValidString(s) {
			return "", &core.Error{Code: core.BackendError, Message: "invalid UTF-8 in serialization"}
		}
		return s, nil
	case core.BytesKind:
		b, _ := v.BytesView()
		if err := e.charge(len(b)); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(b), nil
	case core.TimestampKind:
		t, _ := v.AsTimestamp()
		return t.UTC().Format(time.RFC3339Nano), nil
	case core.DurationKind:
		d, _ := v.AsDuration()
		return durationText(d), nil
	}
	return "", &core.Error{Code: core.BackendError, Message: "invalid value in serialization"}
}

func (e *evaluation) encodeJSON(value core.Value, maxDepth int) (result, error) {
	var out strings.Builder
	write := func(s string) error {
		if err := e.charge(len(s)); err != nil {
			return err
		}
		out.WriteString(s)
		return nil
	}
	writeQuoted := func(s string) error {
		// Reserve escaped output work before constructing its intermediate string.
		if len(s) > (math.MaxInt-2)/6 {
			return &core.Error{Code: core.LimitError, Message: "JSON output length overflow"}
		}
		n := len(s) + 2
		for i := 0; i < len(s); i++ {
			if i%4096 == 0 {
				if err := cancelled(e.ctx); err != nil {
					return err
				}
			}
			switch s[i] {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				n++
			default:
				if s[i] < 0x20 {
					n += 5
				}
			}
		}
		if err := e.charge(n); err != nil {
			return err
		}
		quoted, err := e.quoteJSON(s)
		if err != nil {
			return err
		}
		out.WriteString(quoted)
		return nil
	}
	var visit func(core.Value, int) error
	visit = func(v core.Value, depth int) error {
		if err := e.charge(1); err != nil {
			return err
		}
		if maxDepth > 0 && depth > maxDepth {
			return &core.Error{Code: core.LimitError, Message: "JSON depth limit exceeded"}
		}
		switch v.Kind() {
		case core.ListKind:
			if err := write("["); err != nil {
				return err
			}
			items, _ := v.ListView()
			for i, item := range items {
				if i > 0 {
					if err := write(","); err != nil {
						return err
					}
				}
				if err := visit(item, depth+1); err != nil {
					return err
				}
			}
			return write("]")
		case core.ObjectKind:
			if err := write("{"); err != nil {
				return err
			}
			items, _ := v.ObjectView()
			keys, err := e.objectKeys(items)
			if err != nil {
				return err
			}
			for i, key := range keys {
				if i > 0 {
					if err := write(","); err != nil {
						return err
					}
				}
				if err := writeQuoted(key); err != nil {
					return err
				}
				if err := write(":"); err != nil {
					return err
				}
				if err := visit(items[key], depth+1); err != nil {
					return err
				}
			}
			return write("}")
		default:
			text, err := e.scalarText(v)
			if err != nil {
				return err
			}
			if v.Kind() == core.StringKind || v.Kind() == core.BytesKind || v.Kind() == core.TimestampKind || v.Kind() == core.DurationKind {
				return writeQuoted(text)
			}
			return write(text)
		}
	}
	if err := visit(value, 1); err != nil {
		return result{}, err
	}
	return ordinary(core.String(out.String())), nil
}
