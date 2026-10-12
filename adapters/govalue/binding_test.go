package govalue

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

type BindingEmbedded struct {
	Active bool `json:"active"`
}
type bindingRecord struct {
	*BindingEmbedded
	Name    string   `json:"jsonName" hff:"name"`
	Age     int      `json:"age,omitempty"`
	Tags    []string `json:"tags"`
	Ignored chan int `hff:"-"`
}

func TestBindingInferenceAndTypedMatch(t *testing.T) {
	b, err := NewBinding[bindingRecord]()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	value, err := b.Convert(ctx, bindingRecord{Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	want := hff.Object(map[string]hff.Value{"name": hff.String("Alice"), "tags": hff.Null()})
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("got %v want %v", value, want)
	}
	e, err := engine.New(engine.WithSchema(b.Schema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `name="Alice" age=null tags=null`)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := b.Build(ctx, program)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := filter.Match(ctx, bindingRecord{Name: "Alice"})
	if err != nil || !matched {
		t.Fatalf("match %v %v", matched, err)
	}
	matched, err = filter.Match(ctx, bindingRecord{Name: "Bob"})
	if err != nil || matched {
		t.Fatalf("match %v %v", matched, err)
	}
}
func TestBindingRejectsRecursiveTypesAndAmbiguity(t *testing.T) {
	type node struct{ Next *node }
	if _, err := NewBinding[node](); err == nil {
		t.Fatal("recursive type accepted")
	}
	type a struct {
		A int `json:"same"`
		B int `hff:"same"`
	}
	if _, err := NewBinding[a](); err == nil {
		t.Fatal("ambiguous type accepted")
	}
	if _, err := NewBinding[map[int]string](); err == nil {
		t.Fatal("invalid map accepted")
	}
}
func TestBindingOmitEmptyAndNilContainers(t *testing.T) {
	type record struct {
		Flag    bool           `json:"flag,omitempty"`
		Name    string         `json:"name,omitempty"`
		List    []int          `json:"list"`
		Map     map[string]int `json:"map"`
		Pointer *int           `json:"pointer"`
	}
	b, err := NewBinding[record]()
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.Convert(context.Background(), record{List: []int{}, Map: map[string]int{}})
	if err != nil {
		t.Fatal(err)
	}
	want := hff.Object(map[string]hff.Value{"list": hff.List(), "map": hff.Object(nil), "pointer": hff.Null()})
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("got %v", value)
	}
}

func TestBindingNestedTypesAndSnapshot(t *testing.T) {
	type child struct {
		Name string `json:"name"`
	}
	type record struct {
		Children []child           `json:"children"`
		Lookup   map[string]*child `json:"lookup"`
		Data     []byte            `json:"data"`
	}
	b, err := NewBinding[record]()
	if err != nil {
		t.Fatal(err)
	}
	source := record{Children: []child{{Name: "before"}}, Lookup: map[string]*child{"a": {Name: "before"}}, Data: []byte{1, 2}}
	value, err := b.Convert(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	source.Children[0].Name = "after"
	source.Lookup["a"].Name = "after"
	source.Data[0] = 9
	children, _ := value.Lookup("children")
	list, _ := children.AsList()
	name, _ := list[0].Lookup("name")
	text, _ := name.AsString()
	if text != "before" {
		t.Fatal("list aliases source")
	}
	data, _ := value.Lookup("data")
	bytes, _ := data.AsBytes()
	if bytes[0] != 1 {
		t.Fatal("bytes alias source")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Convert(ctx, source); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestBindingConcurrentReuse(t *testing.T) {
	b, err := NewBinding[bindingRecord]()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := b.Convert(context.Background(), bindingRecord{Name: "Alice"}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestBindingPromotedTraversalRespectsLimits(t *testing.T) {
	type leaf struct{ Name string }
	type middle struct{ leaf }
	type outer struct{ middle }
	b, err := NewBinding[outer](WithLimits(hff.Limits{MaxInputDepth: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Convert(context.Background(), outer{middle{leaf{"Alice"}}}); err == nil {
		t.Fatal("promoted depth ignored")
	}
	var zero Binding[outer]
	if _, err := zero.Convert(context.Background(), outer{}); err == nil {
		t.Fatal("zero binding accepted")
	}
}
