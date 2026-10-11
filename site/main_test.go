package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	write(t, repo, "README.md", "# HFF\n\n[Syntax](spec/syntax.md#operators)\n\n[License](LICENSE)\n")
	write(t, repo, "spec/README.md", "# Specification\n\n[Syntax](syntax.md)\n")
	write(t, repo, "spec/syntax.md", "# Syntax\n\n## Operators\n\n[Home](../README.md)\n\n| Operator | Meaning |\n|---|---|\n| a=b | Equality |\n")
	write(t, repo, "LICENSE", "License text\n")
	return repo
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildNavigationAndLinks(t *testing.T) {
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "output")
	n, err := build(repo, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("got %d pages, want 3", n)
	}
	home := read(t, out, "index.html")
	for _, want := range []string{
		"spec/syntax.html#operators", "spec/index.html",
		"style.css", "aria-current=", "Skip to content",
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	syntax := read(t, out, "spec/syntax.html")
	for _, want := range []string{"../index.html", "../style.css", "id=\"operators\"", "<table>"} {
		if !strings.Contains(syntax, want) {
			t.Errorf("syntax lacks %q", want)
		}
	}
	if read(t, out, "LICENSE") != "License text\n" {
		t.Fatal("license was not copied")
	}
}

func TestBrokenLinksPreserveOutput(t *testing.T) {
	for _, dest := range []string{"spec/absent.md", "spec/syntax.md#absent"} {
		t.Run(dest, func(t *testing.T) {
			repo := fixture(t)
			out := filepath.Join(t.TempDir(), "output")
			if _, err := build(repo, out); err != nil {
				t.Fatal(err)
			}
			before := read(t, out, "index.html")
			write(t, repo, "README.md", "# HFF\n\n[Broken]("+dest+")\n")
			if _, err := build(repo, out); err == nil {
				t.Fatal("broken link accepted")
			}
			if read(t, out, "index.html") != before {
				t.Fatal("failed build changed output")
			}
		})
	}
}

func TestRebuildRemovesStalePages(t *testing.T) {
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "output")
	write(t, repo, "docs/old.md", "# Old page\n")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "docs", "old.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "docs", "old.html")); !os.IsNotExist(err) {
		t.Fatalf("stale page remains: %v", err)
	}
}

func TestRejectUnrelatedAndSourceOutput(t *testing.T) {
	repo := fixture(t)
	unrelated := t.TempDir()
	write(t, unrelated, "keep.txt", "keep")
	if _, err := build(repo, unrelated); err == nil {
		t.Fatal("unrelated output accepted")
	}
	if read(t, unrelated, "keep.txt") != "keep" {
		t.Fatal("unrelated data changed")
	}
	for _, out := range []string{repo, filepath.Join(repo, "spec"), filepath.Join(repo, "site")} {
		if _, err := build(repo, out); err == nil {
			t.Errorf("source output accepted: %s", out)
		}
	}
	if !strings.Contains(read(t, repo, "README.md"), "# HFF") {
		t.Fatal("source changed")
	}
	symlink := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(unrelated, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := build(repo, symlink); err == nil {
		t.Fatal("symlink output accepted")
	}
}

func TestLandingAndSearchIndex(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "README.md", "# HFF\n\nReadable <filters>.\n\n```hff\nage>10\n```\n\n## Why HFF\n\n- **Pure and safe.** Preserve `values` across engines.\n- **One Language. Many Backends.** Keep the contract.\n\n## Next\n\n[Syntax](spec/syntax.md#operators)\n")
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	home := read(t, out, "index.html")
	for _, want := range []string{"Pure and safe", "Preserve values across engines.", "One Language. Many Backends", "Keep the contract.", "age&gt;10", "search-index.js", "theme.js", "site.js"} {
		if !strings.Contains(home, want) {
			t.Errorf("landing lacks %q", want)
		}
	}
	var entries []searchEntry
	raw := read(t, out, "search-index.js")
	data := strings.TrimSuffix(strings.TrimPrefix(raw, "window.HFF_SEARCH_INDEX = "), ";\n")
	if err := json.Unmarshal([]byte(data), &entries); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.URL == "index.html" && strings.Contains(entry.Text, "Preserve values across engines.") {
			found = true
		}
	}
	if !found {
		t.Fatal("landing features missing from search")
	}
	for _, name := range []string{"style.css", "theme.js", "site.js"} {
		if read(t, out, name) == "" {
			t.Errorf("empty asset: %s", name)
		}
	}
}

func TestCenteredReadmeHeader(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "README.md", "<div align=\"center\">\n\n# HFF\n\n**Human-Friendly Filter**\n\n**Write intuitively. Read clearly.**\n\n[Website](https://hff.ornew.net/)\n\n</div>\n\nActual project introduction.\n\n```hff\nstatus=\"open\"\n```\n\n## Why HFF\n\n- **Typed filters.** Check the schema first.\n")
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	home := read(t, out, "index.html")
	if !strings.Contains(home, `<p class="hero-description">Actual project introduction.</p>`) {
		t.Fatal("centered header replaced project introduction")
	}
	if !strings.Contains(home, `status=&#34;open&#34;`) {
		t.Fatal("README query missing from landing")
	}
}

func TestGroupedNavigationAndRemovedPages(t *testing.T) {
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "output")
	write(t, repo, "spec/open-questions.md", "# Old decisions\n")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "spec/open-questions.md")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "docs/design/001-test.md", "# 001: A Design Record\n")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "spec/open-questions.html")); !os.IsNotExist(err) {
		t.Fatal("removed decisions page still exists")
	}
	for _, name := range []string{"spec/syntax.html", "docs/design/001-test.html"} {
		page := read(t, out, name)
		for _, want := range []string{"<h2>Language</h2>", "<summary>Design Records</summary>"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s lacks grouped navigation %q", name, want)
			}
		}
	}
	if !strings.Contains(read(t, out, "docs/design/001-test.html"), "<details class=\"nav-section\" open>") {
		t.Fatal("active design-record group is collapsed")
	}
}
