package main

import (
	"encoding/json"
	"os"
	"path"
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
		for _, want := range []string{"<h2 class=\"nav-heading\">Specification</h2>", "<summary class=\"nav-heading\"><h2>Design Records</h2></summary>"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s lacks grouped navigation %q", name, want)
			}
		}
	}
	if !strings.Contains(read(t, out, "docs/design/001-test.html"), "<details class=\"nav-section\" open>") {
		t.Fatal("active design-record group is collapsed")
	}
}

func TestNavigationPutsSectionIntroductionsFirst(t *testing.T) {
	current := &page{Source: "docs/tutorials/01-first-filter.md", URL: "docs/tutorials/01-first-filter.html"}
	pages := []*page{
		current,
		{Source: "docs/tutorials/README.md", URL: "docs/tutorials/index.html"},
		{Source: "README.md", URL: "index.html"},
		{Source: "spec/syntax.md", URL: "spec/syntax.html"},
		{Source: "spec/README.md", URL: "spec/index.html"},
		{Source: "docs/design/001-record.md", URL: "docs/design/001-record.html"},
		{Source: "docs/design/README.md", URL: "docs/design/index.html"},
		{Source: "docs/guides/first.md", URL: "docs/guides/first.html"},
		{Source: "docs/guides/README.md", URL: "docs/guides/index.html"},
		{Source: "docs/cookbook/recipe.md", URL: "docs/cookbook/recipe.html"},
		{Source: "docs/cookbook/README.md", URL: "docs/cookbook/index.html"},
		{Source: "docs/reference/methods.md", URL: "docs/reference/methods.html"},
		{Source: "docs/reference/README.md", URL: "docs/reference/index.html"},
		{Source: "docs/backends/local/README.md", URL: "docs/backends/local/index.html"},
		{Source: "docs/backends/README.md", URL: "docs/backends/index.html"},
		{Source: "docs/development/checks.md", URL: "docs/development/checks.html"},
		{Source: "docs/development/finite-limits.md", URL: "docs/development/finite-limits.html"},
		{Source: "docs/development/README.md", URL: "docs/development/index.html"},
	}
	sections := navigation(pages, current)
	for _, name := range []string{"Tutorials", "Guides", "Cookbook", "Backends", "Reference", "Specification", "Design Records", "Development"} {
		var found *navSection
		for i := range sections {
			if sections[i].Name == name {
				found = &sections[i]
				break
			}
		}
		if found == nil || len(found.Pages) == 0 || path.Base(found.Pages[0].Source) != "README.md" {
			t.Errorf("%s introduction is not first: %#v", name, found)
		}
	}
	for i := range sections {
		if sections[i].Name != "Development" {
			continue
		}
		want := []string{"docs/development/README.md", "docs/development/checks.md", "docs/development/finite-limits.md"}
		if len(sections[i].Pages) < len(want) {
			t.Fatalf("Development section has too few pages: %#v", sections[i].Pages)
		}
		for j, source := range want {
			if sections[i].Pages[j].Source != source {
				t.Errorf("Development page %d = %q, want %q", j, sections[i].Pages[j].Source, source)
			}
		}
	}
	for _, section := range sections {
		if section.Name == "Tutorials" && !section.Active {
			t.Fatal("current tutorials section is not active")
		}
	}
}

func TestBackendsNavigationGroupsAndExpandsCurrentBackend(t *testing.T) {
	localIntro := &page{Source: "docs/backends/local/README.md", URL: "docs/backends/local/index.html"}
	localConfig := &page{Source: "docs/backends/local/configuration.md", URL: "docs/backends/local/configuration.html"}
	pgIntro := &page{Source: "docs/backends/postgresql/README.md", URL: "docs/backends/postgresql/index.html"}
	backendIntro := &page{Source: "docs/backends/README.md", URL: "docs/backends/index.html"}
	current := localConfig
	sections := navigation([]*page{pgIntro, localConfig, backendIntro, localIntro}, current)
	var backends *navSection
	for i := range sections {
		if sections[i].Name == "Backends" {
			backends = &sections[i]
			break
		}
	}
	if backends == nil || len(backends.Pages) != 1 || backends.Pages[0] != backendIntro {
		t.Fatalf("backend introduction is not the parent entry: %#v", backends)
	}
	if len(backends.Children) != 2 || backends.Children[0].Name != "Local" || backends.Children[1].Name != "PostgreSQL" {
		t.Fatalf("unexpected backend groups: %#v", backends.Children)
	}
	if !backends.Active || !backends.Children[0].Active || backends.Children[1].Active {
		t.Fatalf("active backend group mismatch: %#v", backends)
	}
	if backends.Children[0].Pages[0] != localIntro {
		t.Fatal("local introduction is not first within its group")
	}
}

func TestBackendDisclosuresWithinPlainSections(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "docs/backends/README.md", "# Backends\n")
	write(t, repo, "docs/backends/local/README.md", "# Local backend\n")
	write(t, repo, "docs/backends/local/configuration.md", "# Configuration\n")
	write(t, repo, "docs/backends/postgresql/README.md", "# PostgreSQL\n")
	write(t, repo, "docs/reference/README.md", "# Reference\n")
	write(t, repo, "docs/reference/methods.md", "# Methods\n")
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	local := read(t, out, "docs/backends/local/configuration.html")
	for _, want := range []string{"<h2 class=\"nav-heading\">Backends</h2>", "<summary>Local</summary>", "<summary>PostgreSQL</summary>", "aria-current=\"page\""} {
		if !strings.Contains(local, want) {
			t.Errorf("local backend sidebar lacks %q", want)
		}
	}
	if !strings.Contains(local, `<details class="nav-subsection" open><summary>Local</summary>`) {
		t.Fatal("active backend child is not expanded")
	}
	reference := read(t, out, "docs/reference/methods.html")
	if !strings.Contains(reference, `<h2 class="nav-heading">Reference</h2>`) {
		t.Fatal("Reference must remain a plain section heading")
	}
}

func TestSectionClassificationOrderAndPager(t *testing.T) {
	want := map[string]int{
		"README.md":                  0,
		"docs/tutorials/README.md":   1,
		"docs/guides/README.md":      2,
		"docs/cookbook/README.md":    3,
		"docs/backends/README.md":    4,
		"docs/reference/README.md":   5,
		"spec/README.md":             6,
		"docs/design/README.md":      7,
		"docs/development/README.md": 8,
	}
	for source, index := range want {
		if got := sectionIndex(source); got != index {
			t.Errorf("sectionIndex(%q) = %d, want %d", source, got, index)
		}
	}
	repo := fixture(t)
	write(t, repo, "docs/tutorials/README.md", "# Tutorials\n")
	write(t, repo, "docs/tutorials/01.md", "# First tutorial\n")
	write(t, repo, "docs/tutorials/02.md", "# Second tutorial\n")
	write(t, repo, "docs/guides/README.md", "# Guides\n")
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	first := read(t, out, "docs/tutorials/01.html")
	if !strings.Contains(first, "<small>Next</small>Second tutorial") {
		t.Fatal("tutorial pager lacks next page in the same section")
	}
	if strings.Contains(first, "<small>Next</small>Guides") {
		t.Fatal("tutorial pager crossed into Guides")
	}
}

func TestMovedPagesHaveLocalRedirectsAndStayOutOfSearch(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "docs/backends/local/README.md", "# Local backend\n")
	write(t, repo, "docs/backends/local/configuration.md", "# Configuration\n")
	write(t, repo, "docs/backends/local/extensions.md", "# Extensions\n")
	write(t, repo, "docs/backends/postgresql/README.md", "# PostgreSQL\n")
	write(t, repo, "docs/reference/program-ir.md", "# Program reference\n")
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	redirects := map[string]string{
		"docs/guides/local-evaluation.html": "../backends/local/index.html",
		"docs/guides/configuration.html":    "../backends/local/configuration.html",
		"docs/guides/extensions.html":       "../backends/local/extensions.html",
		"docs/guides/sql.html":              "../backends/postgresql/index.html",
		"docs/guides/program-ir.html":       "../reference/program-ir.html",
	}
	for old, target := range redirects {
		if body := read(t, out, old); !strings.Contains(body, target) {
			t.Errorf("redirect %s does not target %s", old, target)
		}
	}
	if strings.Contains(read(t, out, "search-index.js"), "docs/guides/local-evaluation.html") {
		t.Fatal("redirect alias was added to search index")
	}
}
