package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

// Registry contains fixed host-owned implementations referenced by stable ID
// in a configuration document. Neither SQL source nor executable functions
// are read from JSON. Maps may be nil when the document uses no such IDs.
type Registry struct {
	// Predicates resolves named developer-authored TrustedSQL conditions.
	Predicates map[string]sql.SQLPredicate
	// Methods resolves SQL extension translators to registered Go functions.
	Methods map[string]sql.MethodImplementation
}

// Loaded contains the validated engine and PostgreSQL Build configurations.
// Construct an Engine with engine.New(engine.WithConfig(&loaded.Engine)), then
// pass loaded.SQL to sql.Build with sql.WithConfig(&loaded.SQL).
type Loaded struct {
	Engine engine.Config
	SQL    sql.Config
}

// MaxDocumentBytes bounds the encoded JSON document accepted by Decode.
const MaxDocumentBytes = 4 << 20

// MaxDocumentDepth bounds nested JSON arrays and objects, counting the root
// object as depth one.
const MaxDocumentDepth = 512

type document struct {
	Version int         `json:"version"`
	Engine  engineSpec  `json:"engine"`
	Backend backendSpec `json:"backend"`
}

type engineSpec struct {
	Schema          typeSpec     `json:"schema"`
	Capabilities    []string     `json:"capabilities"`
	Limits          limitsSpec   `json:"limits"`
	UnlimitedLimits bool         `json:"unlimitedLimits,omitempty"`
	Methods         []methodSpec `json:"methods,omitempty"`
}

type limitsSpec struct {
	MaxQueryBytes      int `json:"maxQueryBytes,omitempty"`
	MaxSyntaxDepth     int `json:"maxSyntaxDepth,omitempty"`
	MaxCallbackDepth   int `json:"maxCallbackDepth,omitempty"`
	MaxCompileSteps    int `json:"maxCompileSteps,omitempty"`
	MaxInputDepth      int `json:"maxInputDepth,omitempty"`
	MaxInputValues     int `json:"maxInputValues,omitempty"`
	MaxJSONDepth       int `json:"maxJSONDepth,omitempty"`
	MaxSteps           int `json:"maxSteps,omitempty"`
	MaxGeneratedValues int `json:"maxGeneratedValues,omitempty"`
}

type methodSpec struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Receiver   typeSpec   `json:"receiver"`
	Parameters []typeSpec `json:"parameters,omitempty"`
	Optional   []typeSpec `json:"optional,omitempty"`
	Variadic   *typeSpec  `json:"variadic,omitempty"`
	Result     typeSpec   `json:"result"`
	NullPolicy string     `json:"nullPolicy"`
	BaseCost   int        `json:"baseCost"`
}

type backendSpec struct {
	Kind            string              `json:"kind"`
	Bindings        []bindingSpec       `json:"bindings"`
	TranslatorIDs   []string            `json:"translatorIDs,omitempty"`
	ExecutionLimits executionLimitsSpec `json:"executionLimits"`
}

type executionLimitsSpec struct {
	MaxSteps           int `json:"maxSteps,omitempty"`
	MaxGeneratedValues int `json:"maxGeneratedValues,omitempty"`
}

func encodeLimits(l hff.Limits) limitsSpec {
	return limitsSpec{l.MaxQueryBytes, l.MaxSyntaxDepth, l.MaxCallbackDepth, l.MaxCompileSteps,
		l.MaxInputDepth, l.MaxInputValues, l.MaxJSONDepth, l.MaxSteps, l.MaxGeneratedValues}
}
func (s limitsSpec) value() hff.Limits {
	return hff.Limits{MaxQueryBytes: s.MaxQueryBytes, MaxSyntaxDepth: s.MaxSyntaxDepth,
		MaxCallbackDepth: s.MaxCallbackDepth, MaxCompileSteps: s.MaxCompileSteps,
		MaxInputDepth: s.MaxInputDepth, MaxInputValues: s.MaxInputValues,
		MaxJSONDepth: s.MaxJSONDepth, MaxSteps: s.MaxSteps, MaxGeneratedValues: s.MaxGeneratedValues}
}

func encodeMethod(m hff.MethodDeclaration) (methodSpec, error) {
	if !m.Valid() {
		return methodSpec{}, fmt.Errorf("invalid extension declaration")
	}
	receiver, err := encodeType(m.Receiver())
	if err != nil {
		return methodSpec{}, err
	}
	result, err := encodeType(m.Result())
	if err != nil {
		return methodSpec{}, err
	}
	s := methodSpec{ID: m.ID(), Name: m.Name(), Receiver: receiver, Result: result, BaseCost: m.BaseCost(), NullPolicy: "propagate"}
	if m.NullPolicy() == hff.InvokeNull {
		s.NullPolicy = "invoke"
	}
	for _, typ := range m.Parameters() {
		encoded, err := encodeType(typ)
		if err != nil {
			return methodSpec{}, err
		}
		s.Parameters = append(s.Parameters, encoded)
	}
	for _, typ := range m.Optional() {
		encoded, err := encodeType(typ)
		if err != nil {
			return methodSpec{}, err
		}
		s.Optional = append(s.Optional, encoded)
	}
	if m.Variadic().Kind() != hff.InvalidType {
		encoded, err := encodeType(m.Variadic())
		if err != nil {
			return methodSpec{}, err
		}
		s.Variadic = &encoded
	}
	return s, nil
}

func decodeMethod(s methodSpec) (hff.MethodDeclaration, error) {
	receiver, err := decodeType(s.Receiver)
	if err != nil {
		return hff.MethodDeclaration{}, err
	}
	result, err := decodeType(s.Result)
	if err != nil {
		return hff.MethodDeclaration{}, err
	}
	c := hff.MethodConfig{ID: s.ID, Name: s.Name, Receiver: receiver, Result: result, BaseCost: s.BaseCost}
	switch s.NullPolicy {
	case "propagate":
		c.NullPolicy = hff.PropagateNull
	case "invoke":
		c.NullPolicy = hff.InvokeNull
	default:
		return hff.MethodDeclaration{}, fmt.Errorf("unknown null policy %q", s.NullPolicy)
	}
	for _, spec := range s.Parameters {
		typ, err := decodeType(spec)
		if err != nil {
			return hff.MethodDeclaration{}, err
		}
		c.Parameters = append(c.Parameters, typ)
	}
	for _, spec := range s.Optional {
		typ, err := decodeType(spec)
		if err != nil {
			return hff.MethodDeclaration{}, err
		}
		c.Optional = append(c.Optional, typ)
	}
	if s.Variadic != nil {
		c.Variadic, err = decodeType(*s.Variadic)
		if err != nil {
			return hff.MethodDeclaration{}, err
		}
	}
	return hff.NewMethodDeclaration(c)
}

// Encode serializes a validated engine configuration and its PostgreSQL
// bindings into deterministic version-1 JSON. Developer-authored TrustedSQL
// conditions must match exactly one Registry.Predicates entry; only the ID is
// written. Executable SQL translators are represented by declaration ID.
// Build still validates whether a particular Program can use the bindings.
func Encode(e engine.Config, backend sql.Config, registry Registry) ([]byte, error) {
	if _, err := engine.New(engine.WithConfig(&e)); err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	if backend.Dialect != sql.PostgreSQL {
		return nil, fmt.Errorf("backend: PostgreSQL dialect required")
	}
	if backend.ExecutionLimits.MaxSteps != 0 || backend.ExecutionLimits.MaxGeneratedValues != 0 {
		return nil, fmt.Errorf("backend: PostgreSQL cannot enforce HFF execution budgets")
	}
	schema, err := encodeType(e.Schema)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	d := document{Version: 1, Engine: engineSpec{Schema: schema, Limits: encodeLimits(e.Limits), UnlimitedLimits: e.UnlimitedLimits}, Backend: backendSpec{Kind: "postgresql", ExecutionLimits: executionLimitsSpec{backend.ExecutionLimits.MaxSteps, backend.ExecutionLimits.MaxGeneratedValues}}}
	for _, id := range e.Capabilities.IDs() {
		d.Engine.Capabilities = append(d.Engine.Capabilities, string(id))
	}
	if d.Engine.Capabilities == nil {
		d.Engine.Capabilities = []string{}
	}
	for _, declaration := range e.Methods {
		method, err := encodeMethod(declaration)
		if err != nil {
			return nil, fmt.Errorf("method %q: %w", declaration.ID(), err)
		}
		d.Engine.Methods = append(d.Engine.Methods, method)
	}
	for _, binding := range backend.Bindings {
		view := binding.View()
		encoded, err := encodeBinding(view, registry)
		if err != nil {
			return nil, fmt.Errorf("binding %v: %w", view.Path, err)
		}
		d.Backend.Bindings = append(d.Backend.Bindings, encoded)
	}
	if d.Backend.Bindings == nil {
		d.Backend.Bindings = []bindingSpec{}
	}
	for _, method := range backend.Methods {
		if !method.Valid() || method.Dialect() != sql.PostgreSQL {
			return nil, fmt.Errorf("invalid PostgreSQL translator")
		}
		registered, ok := registry.Methods[method.Declaration().ID()]
		if !ok || !method.SameRegistration(registered) {
			return nil, fmt.Errorf("SQL translator %q differs from its host registry entry", method.Declaration().ID())
		}
		d.Backend.TranslatorIDs = append(d.Backend.TranslatorIDs, method.Declaration().ID())
	}
	if err := validateUTF8Strings(reflect.ValueOf(d)); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("configuration exceeds %d bytes", MaxDocumentBytes)
	}
	if _, err := Decode(data, registry); err != nil {
		return nil, fmt.Errorf("configuration cannot be restored: %w", err)
	}
	return data, nil
}

// encoding/json replaces malformed UTF-8 in Go strings with U+FFFD. Reject
// it before marshaling so a saved schema path or SQL identifier cannot change.
func validateUTF8Strings(value reflect.Value) error {
	switch value.Kind() {
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return fmt.Errorf("configuration contains invalid UTF-8")
		}
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return validateUTF8Strings(value.Elem())
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if err := validateUTF8Strings(value.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := validateUTF8Strings(value.Index(i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Decode parses one strict version-1 document into reusable Go configurations.
// Unknown versions, fields, kinds, capabilities, and registry IDs fail. It
// never accepts SQL source or executable functions from JSON; supplied registry
// entries are host-owned and must remain stable while the configurations are
// used. Decode does not contact a database or build a particular query.
func Decode(data []byte, registry Registry) (Loaded, error) {
	if len(data) > MaxDocumentBytes {
		return Loaded{}, fmt.Errorf("configuration exceeds %d bytes", MaxDocumentBytes)
	}
	if !utf8.Valid(data) {
		return Loaded{}, fmt.Errorf("configuration is not valid UTF-8")
	}
	if err := rejectInvalidUnicodeEscapes(data); err != nil {
		return Loaded{}, err
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Loaded{}, err
	}
	if err := requireDocumentFields(data); err != nil {
		return Loaded{}, err
	}
	if err := validateJSONKeys(data, reflect.TypeOf(document{})); err != nil {
		return Loaded{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var d document
	if err := decoder.Decode(&d); err != nil {
		return Loaded{}, fmt.Errorf("configuration JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Loaded{}, fmt.Errorf("configuration JSON must contain one document")
	}
	if d.Version != 1 {
		return Loaded{}, fmt.Errorf("unsupported configuration version %d", d.Version)
	}
	if d.Backend.Kind != "postgresql" {
		return Loaded{}, fmt.Errorf("unsupported backend kind %q", d.Backend.Kind)
	}
	schema, err := decodeType(d.Engine.Schema)
	if err != nil {
		return Loaded{}, fmt.Errorf("schema: %w", err)
	}
	e := engine.Config{Schema: schema, Capabilities: capabilities.None(), Limits: d.Engine.Limits.value(), UnlimitedLimits: d.Engine.UnlimitedLimits}
	seen := make(map[string]bool)
	for _, id := range d.Engine.Capabilities {
		if seen[id] {
			return Loaded{}, fmt.Errorf("duplicate capability %q", id)
		}
		seen[id] = true
		e.Capabilities = e.Capabilities.Allow(capabilities.ID(id))
	}
	for _, spec := range d.Engine.Methods {
		method, err := decodeMethod(spec)
		if err != nil {
			return Loaded{}, fmt.Errorf("method %q: %w", spec.ID, err)
		}
		e.Methods = append(e.Methods, method)
	}
	if _, err := engine.New(engine.WithConfig(&e)); err != nil {
		return Loaded{}, fmt.Errorf("engine: %w", err)
	}
	backend := sql.Config{Dialect: sql.PostgreSQL, ExecutionLimits: sql.ExecutionLimits{MaxSteps: d.Backend.ExecutionLimits.MaxSteps, MaxGeneratedValues: d.Backend.ExecutionLimits.MaxGeneratedValues}}
	if backend.ExecutionLimits.MaxSteps != 0 || backend.ExecutionLimits.MaxGeneratedValues != 0 {
		return Loaded{}, fmt.Errorf("backend: PostgreSQL cannot enforce HFF execution budgets")
	}
	for i, spec := range d.Backend.Bindings {
		binding, err := decodeBinding(spec, registry)
		if err != nil {
			return Loaded{}, fmt.Errorf("binding %d: %w", i, err)
		}
		backend.Bindings = append(backend.Bindings, binding)
	}
	if err := validateBindings(e.Schema, backend.Bindings); err != nil {
		return Loaded{}, fmt.Errorf("bindings: %w", err)
	}
	declarations := make(map[string]hff.MethodDeclaration, len(e.Methods))
	for _, method := range e.Methods {
		declarations[method.ID()] = method
	}
	seen = make(map[string]bool)
	for _, id := range d.Backend.TranslatorIDs {
		if id == "" || seen[id] {
			return Loaded{}, fmt.Errorf("invalid or duplicate translator ID %q", id)
		}
		seen[id] = true
		method, ok := registry.Methods[id]
		if !ok || !method.Valid() {
			return Loaded{}, fmt.Errorf("unregistered SQL translator ID %q", id)
		}
		declaration, declared := declarations[id]
		if !declared || !method.Declaration().Equal(declaration) || method.Dialect() != sql.PostgreSQL {
			return Loaded{}, fmt.Errorf("SQL translator %q does not match an engine declaration", id)
		}
		backend.Methods = append(backend.Methods, method)
	}
	return Loaded{Engine: e, SQL: backend}, nil
}

func requireDocumentFields(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("configuration JSON: %w", err)
	}
	if root == nil {
		return fmt.Errorf("configuration must be an object")
	}
	for _, name := range []string{"version", "engine", "backend"} {
		if missingJSONField(root[name]) {
			return fmt.Errorf("configuration needs %q", name)
		}
	}
	var engineFields map[string]json.RawMessage
	if err := json.Unmarshal(root["engine"], &engineFields); err != nil || engineFields == nil {
		return fmt.Errorf("engine must be an object")
	}
	for _, name := range []string{"schema", "capabilities"} {
		if missingJSONField(engineFields[name]) {
			return fmt.Errorf("engine needs %q", name)
		}
	}
	var backendFields map[string]json.RawMessage
	if err := json.Unmarshal(root["backend"], &backendFields); err != nil || backendFields == nil {
		return fmt.Errorf("backend must be an object")
	}
	for _, name := range []string{"kind", "bindings"} {
		if missingJSONField(backendFields[name]) {
			return fmt.Errorf("backend needs %q", name)
		}
	}
	return nil
}

func missingJSONField(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// encoding/json replaces unpaired surrogate escapes with U+FFFD. Identifier
// bytes are contractual, so reject those escapes before its lossy decoding.
func rejectInvalidUnicodeEscapes(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			i++
			if i >= len(data) {
				return fmt.Errorf("incomplete JSON escape")
			}
			if data[i] != 'u' {
				continue
			}
			if i+4 >= len(data) {
				return fmt.Errorf("incomplete Unicode escape")
			}
			value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
			if err != nil {
				return fmt.Errorf("invalid Unicode escape")
			}
			i += 4
			if value >= 0xdc00 && value <= 0xdfff {
				return fmt.Errorf("unpaired Unicode low surrogate")
			}
			if value < 0xd800 || value > 0xdbff {
				continue
			}
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return fmt.Errorf("unpaired Unicode high surrogate")
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired Unicode high surrogate")
			}
			i += 6
		}
	}
	return nil
}

func validateJSONKeys(data []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		if missingJSONField(data) {
			return nil
		}
		if typ.Kind() == reflect.Slice {
			var elements []json.RawMessage
			if err := json.Unmarshal(data, &elements); err != nil {
				return err
			}
			for _, element := range elements {
				if err := validateJSONKeys(element, typ.Elem()); err != nil {
					return err
				}
			}
			return nil
		}
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || missingJSONField(data) {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	known := make(map[string]reflect.Type, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "-" && name != "" {
			known[name] = field.Type
		}
	}
	for name, raw := range fields {
		fieldType, ok := known[name]
		if !ok {
			return fmt.Errorf("unknown or noncanonical configuration field %q", name)
		}
		if err := validateJSONKeys(raw, fieldType); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("configuration JSON must contain one document")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("configuration JSON: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth > MaxDocumentDepth {
		return fmt.Errorf("configuration exceeds JSON depth %d", MaxDocumentDepth)
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("configuration JSON: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("configuration JSON has a non-string key")
			}
			if keys[key] {
				return fmt.Errorf("configuration JSON has duplicate key %q", key)
			}
			keys[key] = true
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("configuration JSON has unexpected delimiter")
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("configuration JSON: %w", err)
	}
	closing := json.Delim('}')
	if delim == '[' {
		closing = ']'
	}
	if end != closing {
		return fmt.Errorf("configuration JSON has an unmatched delimiter")
	}
	return nil
}
