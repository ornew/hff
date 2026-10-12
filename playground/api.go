// The playground bridge is private to the documentation site, not a JavaScript SDK.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/schema"
)

const maxRequestBytes = 1 << 20

type field struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Optional bool    `json:"optional,omitempty"`
	Fields   []field `json:"fields,omitempty"`
}
type request struct {
	Query   string            `json:"query"`
	Schema  []field           `json:"schema"`
	Record  json.RawMessage   `json:"record"`
	Records []json.RawMessage `json:"records"`
	Without []capabilities.ID `json:"without,omitempty"`
}
type failure struct {
	Stage   string        `json:"stage"`
	Code    hff.ErrorCode `json:"code"`
	Message string        `json:"message"`
	Line    int           `json:"line,omitempty"`
	Column  int           `json:"column,omitempty"`
	Path    hff.Path      `json:"path,omitempty"`
	Record  int           `json:"record,omitempty"`
}
type timings struct {
	Compile  float64 `json:"compile_ms"`
	Build    float64 `json:"build_ms"`
	Evaluate float64 `json:"evaluate_ms"`
}
type batchResult struct {
	Filtered []string `json:"filtered"`
	Total    int      `json:"total"`
	Timings  timings  `json:"timings"`
}
type response struct {
	OK      bool         `json:"ok"`
	Matched *bool        `json:"matched,omitempty"`
	Error   *failure     `json:"error,omitempty"`
	Batch   *batchResult `json:"batch,omitempty"`
}

func handle(ctx context.Context, raw []byte) []byte {
	r := run(ctx, raw)
	out, _ := json.Marshal(r)
	return out
}
func failed(stage string, err error) response {
	f := &failure{Stage: stage, Code: hff.InputError, Message: err.Error()}
	var detail *hff.Error
	if errors.As(err, &detail) {
		f.Code = detail.Code
		f.Line = detail.Line
		f.Column = detail.Column
		f.Path = detail.Path
	}
	return response{Error: f}
}
func run(ctx context.Context, raw []byte) response {
	admitted, err := decode(ctx, raw)
	if err != nil {
		return failed("request", err)
	}
	var req request
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		return failed("request", err)
	}
	object, _ := admitted.AsObject()
	records, batch := object["records"]
	_, single := object["record"]
	if batch == single {
		return failed("request", fmt.Errorf("provide either record or records"))
	}
	var values []hff.Value
	if batch {
		var ok bool
		values, ok = records.AsList()
		if !ok {
			return failed("request", fmt.Errorf("records must be a JSON array"))
		}
	}
	if err := capabilities.Standard().Allow(req.Without...).Check(); err != nil {
		return failed("configuration", &hff.Error{Code: hff.ConfigurationError, Message: err.Error()})
	}
	t, err := structType(ctx, req.Schema)
	if err != nil {
		return failed("schema", err)
	}
	e, err := engine.New(engine.WithSchema(t), engine.WithCapabilities(capabilities.Standard().Without(req.Without...)))
	if err != nil {
		return failed("configuration", err)
	}
	start := time.Now()
	p, err := e.Compile(ctx, req.Query)
	compileTime := float64(time.Since(start).Nanoseconds()) / 1e6
	if err != nil {
		return failed("compile", err)
	}
	start = time.Now()
	f, err := local.Build(ctx, p)
	buildTime := float64(time.Since(start).Nanoseconds()) / 1e6
	if err != nil {
		return failed("build", err)
	}
	if batch {
		out := &batchResult{Filtered: []string{}, Total: len(values), Timings: timings{Compile: compileTime, Build: buildTime}}
		start = time.Now()
		for i, value := range values {
			matched, err := f.Match(ctx, value)
			if err != nil {
				r := failed("match", err)
				r.Error.Record = i + 1
				return r
			}
			if matched {
				out.Filtered = append(out.Filtered, string(req.Records[i]))
			}
		}
		if err := core.CheckContext(ctx); err != nil {
			return failed("match", err)
		}
		out.Timings.Evaluate = float64(time.Since(start).Nanoseconds()) / 1e6
		return response{OK: true, Batch: out}
	}
	v, err := decode(ctx, req.Record)
	if err != nil {
		return failed("input", err)
	}
	matched, err := f.Match(ctx, v)
	if err != nil {
		return failed("match", err)
	}
	return response{OK: true, Matched: &matched}
}

// Type strings use HFF's existing type-guard grammar. Exact plan shape prevents
// a descriptor from injecting another expression into the schema request.
func descriptor(ctx context.Context, text string) (hff.Type, error) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		return hff.Type{}, err
	}
	p, err := e.Compile(ctx, "x:["+text+"]")
	if err != nil {
		return hff.Type{}, err
	}
	n := p.Root()
	if n.Kind() == hff.BoolNode && n.ChildCount() == 1 {
		n, _ = n.Child(0)
	}
	c, _ := n.Child(0)
	path, ok := c.Path()
	if n.Kind() != hff.TypeGuardNode || n.ChildCount() != 1 || c.Kind() != hff.VariableNode || !ok || len(path) != 1 || path[0] != "x" {
		return hff.Type{}, fmt.Errorf("invalid type descriptor")
	}
	t, ok := n.Constraint()
	if !ok {
		return hff.Type{}, fmt.Errorf("invalid type descriptor")
	}
	return t, nil
}
func structType(ctx context.Context, fields []field) (hff.Type, error) {
	out := make([]hff.Field, 0, len(fields))
	for _, f := range fields {
		var t hff.Type
		var err error
		if f.Type == "struct" {
			t, err = structType(ctx, f.Fields)
		} else {
			if len(f.Fields) > 0 {
				return hff.Type{}, fmt.Errorf("fields require struct type")
			}
			t, err = descriptor(ctx, f.Type)
		}
		if err != nil {
			return hff.Type{}, err
		}
		out = append(out, hff.Field{Name: f.Name, Type: t, Optional: f.Optional})
	}
	return schema.Struct(out...), nil
}

// Decode tokens directly: passing JSON through float64 or JavaScript objects
// would lose large integer precision and the spelling-based numeric subtype.
func decode(ctx context.Context, raw []byte) (hff.Value, error) {
	if len(raw) > maxRequestBytes {
		return hff.Value{}, &hff.Error{Code: hff.LimitError, Message: "playground JSON exceeds 1 MiB"}
	}
	if !utf8.Valid(raw) {
		return hff.Value{}, fmt.Errorf("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	count := 0
	var token func() (json.Token, error)
	token = func() (json.Token, error) {
		if err := core.CheckContext(ctx); err != nil {
			return nil, err
		}
		start := d.InputOffset()
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		if _, ok := tok.(string); ok {
			quoted := strings.TrimLeft(string(raw[start:d.InputOffset()]), ",: \t\r\n")
			if _, err := core.DecodeJSONString(quoted); err != nil {
				return nil, err
			}
		}
		return tok, nil
	}
	var value func(int) (hff.Value, error)
	value = func(depth int) (hff.Value, error) {
		count++
		if depth > 64 || count > 100000 {
			return hff.Value{}, &hff.Error{Code: hff.LimitError, Message: "playground JSON depth or value limit exceeded"}
		}
		tok, err := token()
		if err != nil {
			return hff.Value{}, err
		}
		switch x := tok.(type) {
		case nil:
			return hff.Null(), nil
		case bool:
			return hff.Bool(x), nil
		case string:
			return hff.String(x), nil
		case json.Number:
			if strings.ContainsAny(string(x), ".eE") {
				n, err := strconv.ParseFloat(string(x), 64)
				if err != nil {
					return hff.Value{}, err
				}
				return hff.Float(n), nil
			}
			n, err := strconv.ParseInt(string(x), 10, 64)
			if err != nil {
				return hff.Value{}, err
			}
			return hff.Int(n), nil
		case json.Delim:
			switch x {
			case '[':
				var values []hff.Value
				for d.More() {
					v, err := value(depth + 1)
					if err != nil {
						return hff.Value{}, err
					}
					values = append(values, v)
				}
				if _, err := token(); err != nil {
					return hff.Value{}, err
				}
				return hff.List(values...), nil
			case '{':
				values := map[string]hff.Value{}
				for d.More() {
					key, err := token()
					if err != nil {
						return hff.Value{}, err
					}
					name := key.(string)
					if _, ok := values[name]; ok {
						return hff.Value{}, fmt.Errorf("duplicate JSON key %q", name)
					}
					v, err := value(depth + 1)
					if err != nil {
						return hff.Value{}, err
					}
					values[name] = v
				}
				if _, err := token(); err != nil {
					return hff.Value{}, err
				}
				return hff.Object(values), nil
			}
		}
		return hff.Value{}, fmt.Errorf("invalid JSON value")
	}
	v, err := value(0)
	if err != nil {
		return hff.Value{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return hff.Value{}, fmt.Errorf("expected exactly one JSON value")
	}
	return v, nil
}
