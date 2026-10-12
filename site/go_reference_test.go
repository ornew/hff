package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedGoReferenceIncludesAliasesExamplesAndSourceLinks(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "go.mod", "module github.com/ornew/hff\n\ngo 1.27.1\n")
	write(t, repo, "value.go", `// Package hff demonstrates the public source reference.
package hff
import "github.com/ornew/hff/internal/core"
// Problem exposes the underlying diagnostic fields and methods.
type Problem = core.Problem
// Box stores one typed value.
type Box[T any] struct { Value T; private bool }
// Get returns the stored value.
func (b Box[T]) Get() T { return b.Value }
// Mode selects an execution mode.
type Mode string
// Standard is the ordinary mode.
const Standard Mode = "standard"
`)
	write(t, repo, "value_test.go", `package hff_test
import "github.com/ornew/hff"
// This example demonstrates a generic method.
func ExampleBox_Get() { b := hff.Box[int]{Value: 42}; println(b.Get()) }
// A type example shows the diagnostic Code field.
func ExampleProblem() { _ = hff.Problem{Code: "problem"} }
// A package example links the public import.
func Example() { _ = hff.Standard }

`)
	write(t, repo, "internal/core/errors.go", `package core
// Problem describes the cause. Callers may inspect Code.
type Problem struct { Code string; private string }
// Error returns the diagnostic message.
func (p *Problem) Error() string {return p.Code}
`)
	for _, dir := range referencePackages[1:] {
		name := filepath.Base(dir)
		write(t, repo, dir+"/api.go", "// Package "+name+" documents this API.\npackage "+name+"\n")
	}
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	body := read(t, out, "docs/reference/go/hff/index.html")
	for _, required := range []string{`id="Box.Get"`, `id="Problem.Error"`, "Callers may inspect Code", "A type example", "A package example", `href="#Standard"`, `Box[int]`, "value.go#L", `errors.go#L`, "Generated from this checkout"} {
		if !strings.Contains(body, required) {
			t.Errorf("reference missing %q", required)
		}
	}
	for _, forbidden := range []string{"return b.Value", "private bool", `blob/main/docs/reference/go/hff/README.md`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("reference exposed %q", forbidden)
		}
	}
	if !strings.Contains(read(t, out, "search-index.js"), "Box.Get") {
		t.Fatal("generated methods absent from search")
	}
}
