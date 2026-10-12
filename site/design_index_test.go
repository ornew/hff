package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func record(title, status, author string) string {
	return "# " + title + "\n\n- **Status**: " + status + "\n- **Date**: 2026-10-12\n- **Author**: " + author + "\n"
}

func TestDesignIndexGeneratedFromRecords(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "docs/design/README.md", "# Stale introduction\n\nOutdated contents.\n")
	write(t, repo, "docs/design/002-second.md", record("002: Second Record", "Proposed", "@second"))
	write(t, repo, "docs/design/001-first.md", record("001: First Record", "Accepted", "@first"))
	write(t, repo, "docs/design/000-template.md", "# Record template\n")
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	body := read(t, out, "docs/design/index.html")
	for _, want := range []string{"<th>Record</th>", "<th>Status</th>", "<th>Date</th>", "<th>Author</th>",
		"001-first.html", "002-second.html", "Accepted", "Proposed", "@first", "@second", "2026-10-12", "000-template.html"} {
		if !strings.Contains(body, want) {
			t.Errorf("generated page lacks %q", want)
		}
	}
	if strings.Contains(body, "Outdated contents") || strings.Count(body, "<tr>") != 3 {
		t.Fatal("generated introduction used stale contents or listed the template as a record")
	}
	if strings.Index(body, "001-first.html") > strings.Index(body, "002-second.html") {
		t.Fatal("records are not ordered by number")
	}
	// Record changes are reflected without manually editing the introduction.
	if err := os.Remove(filepath.Join(repo, "docs/design/001-first.md")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "docs/design/003-third.md", record("003: Third Record", "Accepted", "@third"))
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	body = read(t, out, "docs/design/index.html")
	if strings.Contains(body, "001-first.html") || !strings.Contains(body, "003-third.html") {
		t.Fatal("generated index does not reflect record addition/removal")
	}
	if !strings.Contains(read(t, out, "search-index.js"), "Third Record") {
		t.Fatal("new record is absent from search")
	}
}

func TestDesignIndexWithoutCheckedInIntroduction(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "docs/design/001-first.md", record("001: First Record", "Accepted", "@first"))
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, out, "docs/design/index.html"), "<h1 id=\"introduction\">Introduction</h1>") {
		t.Fatal("synthetic Introduction is missing")
	}
}

func TestDesignIndexInvalidMetadataPreservesSite(t *testing.T) {
	for _, test := range []struct {
		name, contents, additional, want string
	}{
		{"missing title", "- **Status**: Accepted\n", "", "title"},
		{"missing metadata", "# 001: Record\n", "", "Status"},
		{"empty author", record("001: Record", "Accepted", ""), "", "Author"},
		{"duplicate metadata", record("001: Record", "Accepted", "@first") + "- **Status**: Proposed\n", "", "duplicate Status"},
		{"duplicate number", record("001: Record", "Accepted", "@first"), "001-other.md", "duplicate record number"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := fixture(t)
			write(t, repo, "docs/design/001-record.md", record("001: Record", "Accepted", "@first"))
			out := filepath.Join(t.TempDir(), "output")
			if _, err := build(repo, out); err != nil {
				t.Fatal(err)
			}
			before := read(t, out, "docs/design/index.html")
			write(t, repo, "docs/design/001-record.md", test.contents)
			if test.additional != "" {
				write(t, repo, "docs/design/"+test.additional, record("001: Duplicate", "Accepted", "@first"))
			}
			if _, err := build(repo, out); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
			if read(t, out, "docs/design/index.html") != before {
				t.Fatal("failed generation changed existing output")
			}
		})
	}
}

func TestDesignIndexEscapesMetadata(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "docs/design/001-record.md", record("001: A | B <script> x [y]", "Accepted | Review", "@first <b>"))
	out := filepath.Join(t.TempDir(), "output")
	if _, err := build(repo, out); err != nil {
		t.Fatal(err)
	}
	body := read(t, out, "docs/design/index.html")
	if !strings.Contains(body, "Accepted | Review") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "@first &lt;b&gt;") {
		t.Fatal("metadata punctuation was not preserved as literal text")
	}
	if strings.Count(body, "<td>") != 4 {
		t.Fatal("metadata created extra table columns")
	}
}

func TestDesignIndexIgnoresExampleMetadata(t *testing.T) {
	repo := fixture(t)
	write(t, repo, "docs/design/001-record.md", record("001: Record", "Accepted", "@first")+
		"\n```markdown\n- **Status**: Proposed\n- **Author**: @example\n```\n\n> - **Status**: Proposed\n")
	src, err := designIndexMarkdown(repo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "Proposed") || strings.Contains(string(src), "@example") {
		t.Fatal("example metadata contaminated the index")
	}
}
