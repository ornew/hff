// Command site renders repository Markdown into a standalone static site.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

//go:embed layout.html landing.html style.css site.js theme.js
var assets embed.FS

type heading struct{ ID, Title string }
type page struct {
	Source, URL, Title string
	Body               template.HTML
	Headings           []heading
	doc                ast.Node
	src                []byte
	anchors            map[string]bool
	Home               bool
	Intro, Example     string
	HomeAnchor         string
	Features           []heading
}
type navSection struct {
	Name                string
	Pages               []*page
	Children            []navSection
	Collapsible, Active bool
}

func sectionIndex(source string) int {
	switch {
	case source == "README.md":
		return 0
	case strings.HasPrefix(source, "docs/tutorials/"):
		return 1
	case strings.HasPrefix(source, "docs/guides/"):
		return 2
	case strings.HasPrefix(source, "docs/cookbook/"):
		return 3
	case strings.HasPrefix(source, "docs/reference/"):
		return 5
	case strings.HasPrefix(source, "docs/backends/"):
		return 4
	case strings.HasPrefix(source, "spec/"):
		return 6
	case strings.HasPrefix(source, "docs/design/"):
		return 7
	default:
		return 8
	}
}

func navigation(pages []*page, current *page) []navSection {
	sections := []navSection{
		{Name: "Overview"},
		{Name: "Tutorials"},
		{Name: "Guides"},
		{Name: "Cookbook"},
		{Name: "Backends"},
		{Name: "Reference"},
		{Name: "Specification"},
		{Name: "Design Records", Collapsible: true},
		{Name: "Development", Collapsible: true},
	}
	for _, p := range pages {
		i := sectionIndex(p.Source)
		sections[i].Pages = append(sections[i].Pages, p)
		if p == current {
			sections[i].Active = true
		}
	}
	for i := range sections {
		sort.SliceStable(sections[i].Pages, func(a, b int) bool {
			return path.Base(sections[i].Pages[a].Source) == "README.md" && path.Base(sections[i].Pages[b].Source) != "README.md"
		})
	}
	backends := &sections[4]
	var localPages, postgresPages []*page
	for _, p := range backends.Pages {
		switch {
		case p.Source == "docs/backends/README.md":
		case strings.HasPrefix(p.Source, "docs/backends/local/"):
			localPages = append(localPages, p)
		case strings.HasPrefix(p.Source, "docs/backends/postgresql/"):
			postgresPages = append(postgresPages, p)
		}
	}
	child := func(name string, pages []*page) navSection {
		sort.SliceStable(pages, func(a, b int) bool {
			return path.Base(pages[a].Source) == "README.md" && path.Base(pages[b].Source) != "README.md"
		})
		section := navSection{Name: name, Pages: pages, Collapsible: true}
		for _, p := range pages {
			section.Active = section.Active || p == current
		}
		return section
	}
	backends.Children = []navSection{child("Local", localPages), child("PostgreSQL", postgresPages)}
	for _, p := range backends.Pages {
		if p.Source == "docs/backends/README.md" {
			backends.Pages = []*page{p}
			break
		}
	}
	var result []navSection
	for _, section := range sections {
		if len(section.Pages) > 0 {
			result = append(result, section)
		}
	}
	return result
}

type view struct {
	Sections   []navSection
	Current    *page
	Pages      []*page
	Root       string
	Prev, Next *page
}

func main() {
	repo := flag.String("repo", "..", "repository root")
	out := flag.String("out", "dist", "generated site directory")
	serve := flag.String("serve", "", "serve after building, e.g. localhost:8080")
	flag.Parse()
	n, err := build(*repo, *out)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("built %d pages in %s", n, *out)
	if *serve != "" {
		log.Printf("preview: http://%s/", *serve)
		log.Fatal(http.ListenAndServe(*serve, http.FileServer(http.Dir(*out))))
	}
}

func pageURL(source string) string {
	if source == "README.md" {
		return "index.html"
	}
	if path.Base(source) == "README.md" {
		return path.Join(path.Dir(source), "index.html")
	}
	return strings.TrimSuffix(source, ".md") + ".html"
}

// Relative URLs support subpath hosting and directly opened HTML files.
func relative(from, to string) string {
	r, _ := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(to))
	return filepath.ToSlash(r)
}

func build(repo, out string) (int, error) {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return 0, err
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return 0, err
	}
	sources := []string{"README.md"}
	for _, dir := range []string{"spec", "docs"} {
		err := filepath.WalkDir(filepath.Join(repo, dir), func(p string, d os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				r, err := filepath.Rel(repo, p)
				if err != nil {
					return err
				}
				sources = append(sources, filepath.ToSlash(r))
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	order := []string{
		"README.md",
		"spec/README.md", "spec/syntax.md", "spec/types.md", "spec/evaluation.md", "spec/methods.md", "spec/capabilities.md",
		"docs/tutorials/README.md",
		"docs/guides/README.md", "docs/guides/getting-started.md", "docs/guides/compiling.md", "docs/guides/schemas-and-values.md", "docs/guides/scalar-methods.md", "docs/guides/collections.md", "docs/guides/callbacks.md", "docs/guides/json.md", "docs/guides/temporal.md",
		"docs/cookbook/README.md",
		"docs/reference/README.md", "docs/reference/methods.md", "docs/reference/go-api.md", "docs/reference/program-ir.md",
		"docs/backends/README.md", "docs/backends/local/README.md", "docs/backends/local/configuration.md", "docs/backends/local/extensions.md", "docs/backends/postgresql/README.md",
		"docs/design/README.md",
		"docs/development/README.md", "docs/development/parser.md", "docs/development/parsing.md", "docs/development/site.md",
	}
	rank := func(s string) int {
		for i, v := range order {
			if v == s {
				return i
			}
		}
		return len(order)
	}
	sort.Slice(sources, func(i, j int) bool {
		if rank(sources[i]) != rank(sources[j]) {
			return rank(sources[i]) < rank(sources[j])
		}
		return sources[i] < sources[j]
	})
	md := goldmark.New(goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	var pages []*page
	bySource := map[string]*page{}
	for _, source := range sources {
		src, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(source)))
		if err != nil {
			return 0, err
		}
		p := &page{Source: source, URL: pageURL(source), src: src, anchors: map[string]bool{}}
		p.doc = md.Parser().Parse(text.NewReader(src))
		ast.Walk(p.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if h, ok := n.(*ast.Heading); ok && entering {
				id, _ := h.AttributeString("id")
				key := string(id.([]byte))
				title := string(h.Text(src))
				p.anchors[key] = true
				if h.Level == 1 && p.Title == "" {
					p.Title = title
					p.HomeAnchor = key
				}
				if h.Level == 2 || h.Level == 3 {
					p.Headings = append(p.Headings, heading{key, title})
				}
			}
			return ast.WalkContinue, nil
		})
		if p.Title == "" {
			return 0, fmt.Errorf("%s: missing level-one title", source)
		}
		pages = append(pages, p)
		bySource[source] = p
	}
	for _, p := range pages {
		err := ast.Walk(p.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			var dest *[]byte
			switch n := n.(type) {
			case *ast.Link:
				dest = &n.Destination
			case *ast.Image:
				dest = &n.Destination
			default:
				return ast.WalkContinue, nil
			}
			u, err := url.Parse(string(*dest))
			if err != nil {
				return ast.WalkStop, err
			}
			if u.IsAbs() || u.Host != "" {
				return ast.WalkContinue, nil
			}
			target := p.Source
			if u.Path != "" {
				target = path.Clean(path.Join(path.Dir(p.Source), u.Path))
			}
			t := bySource[target]
			if t == nil {
				if target == "LICENSE" {
					u.Path = relative(p.URL, "LICENSE")
					*dest = []byte(u.String())
					return ast.WalkContinue, nil
				}
				return ast.WalkStop, fmt.Errorf("%s: unrendered local link %q; use URLs for external references", p.Source, *dest)
			}
			if u.Fragment != "" && !t.anchors[u.Fragment] {
				return ast.WalkStop, fmt.Errorf("%s: missing anchor %q in %s", p.Source, u.Fragment, target)
			}
			u.Path = relative(p.URL, t.URL)
			*dest = []byte(u.String())
			return ast.WalkContinue, nil
		})
		if err != nil {
			return 0, err
		}
		var b bytes.Buffer
		if err := md.Renderer().Render(&b, p.src, p.doc); err != nil {
			return 0, err
		}
		p.Body = template.HTML(b.String()) // Renderer output; raw source HTML is disabled.
		if p.Source == "README.md" {
			p.Home = true
			if err := prepareLanding(md, p); err != nil {
				return 0, err
			}
		}
	}
	// Refuse source directories and unrelated nonempty output directories.
	for _, protected := range append(sources, "site", ".git", "CLAUDE.local.md") {
		p := filepath.Join(repo, filepath.FromSlash(protected))
		r, err := filepath.Rel(out, p)
		if err != nil {
			return 0, err
		}
		if r == "." || (r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))) {
			return 0, fmt.Errorf("output directory contains source: %s", out)
		}
	}
	if info, err := os.Lstat(out); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("output must not be a symlink: %s", out)
	}
	entries, err := os.ReadDir(out)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if len(entries) > 0 {
		marker, err := os.ReadFile(filepath.Join(out, ".hff-site"))
		if err != nil || string(marker) != "Generated by HFF site\n" {
			return 0, fmt.Errorf("refusing to replace unmarked output directory: %s", out)
		}
		if err := os.RemoveAll(out); err != nil {
			return 0, err
		}
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(out, ".hff-site"), []byte("Generated by HFF site\n"), 0644); err != nil {
		return 0, err
	}
	tmpl, err := template.New("layout.html").Funcs(template.FuncMap{"rel": relative}).ParseFS(assets, "layout.html", "landing.html")
	if err != nil {
		return 0, err
	}
	for _, name := range []string{"style.css", "site.js", "theme.js"} {
		data, err := assets.ReadFile(name)
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(out, name), data, 0644); err != nil {
			return 0, err
		}
	}
	if err := writeSearchIndex(out, pages); err != nil {
		return 0, err
	}
	license, err := os.ReadFile(filepath.Join(repo, "LICENSE"))
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(out, "LICENSE"), license, 0644); err != nil {
		return 0, err
	}
	for i, p := range pages {
		var b bytes.Buffer
		v := view{Current: p, Pages: pages, Sections: navigation(pages, p), Root: relative(p.URL, "index.html")}
		for j := i - 1; j >= 0; j-- {
			if sectionIndex(pages[j].Source) == sectionIndex(p.Source) {
				v.Prev = pages[j]
				break
			}
		}
		for j := i + 1; j < len(pages); j++ {
			if sectionIndex(pages[j].Source) == sectionIndex(p.Source) {
				v.Next = pages[j]
				break
			}
		}
		if err := tmpl.Execute(&b, v); err != nil {
			return 0, err
		}
		file := filepath.Join(out, filepath.FromSlash(p.URL))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(file, b.Bytes(), 0644); err != nil {
			return 0, err
		}
	}
	for old, target := range map[string]string{
		"docs/guides/local-evaluation.html": "docs/backends/local/index.html",
		"docs/guides/configuration.html":    "docs/backends/local/configuration.html",
		"docs/guides/extensions.html":       "docs/backends/local/extensions.html",
		"docs/guides/sql.html":              "docs/backends/postgresql/index.html",
		"docs/guides/program-ir.html":       "docs/reference/program-ir.html",
	} {
		file := filepath.Join(out, filepath.FromSlash(old))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return 0, err
		}
		href := relative(old, target)
		body := fmt.Sprintf("<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta http-equiv=\"refresh\" content=\"0; url=%s\"><link rel=\"canonical\" href=\"%s\"><title>Page moved</title><p>This page moved to <a href=\"%s\">its new location</a>.</p></html>\n", html.EscapeString(href), html.EscapeString(href), html.EscapeString(href))
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			return 0, err
		}
	}
	return len(pages), nil
}

// Landing content comes from the README so the site and repository share copy.
func prepareLanding(md goldmark.Markdown, p *page) error {
	var rest bytes.Buffer
	section := ""
	centeredHeader := false
	for n := p.doc.FirstChild(); n != nil; n = n.NextSibling() {
		if block, ok := n.(*ast.HTMLBlock); ok && section == "" {
			text := string(block.Text(p.src))
			if strings.Contains(text, `<div align="center">`) {
				centeredHeader = true
			}
			if strings.Contains(text, "</div>") {
				centeredHeader = false
			}
			continue
		}
		if centeredHeader {
			continue
		}
		if h, ok := n.(*ast.Heading); ok {
			if h.Level == 1 {
				continue
			}
			if h.Level == 2 {
				section = string(h.Text(p.src))
			}
		}
		if section == "" {
			if para, ok := n.(*ast.Paragraph); ok && p.Intro == "" {
				p.Intro = string(para.Text(p.src))
			}
			if code, ok := n.(*ast.FencedCodeBlock); ok && p.Example == "" {
				for i := 0; i < code.Lines().Len(); i++ {
					line := code.Lines().At(i)
					p.Example += string(line.Value(p.src))
				}
			}
			continue
		}
		if section == "Why HFF" {
			if list, ok := n.(*ast.List); ok {
				for item := list.FirstChild(); item != nil; item = item.NextSibling() {
					var rendered bytes.Buffer
					if err := md.Renderer().Render(&rendered, p.src, item); err != nil {
						return err
					}
					s := strings.Join(strings.Fields(html.UnescapeString(htmlTags.ReplaceAllString(rendered.String(), " "))), " ")
					title, body, found := strings.Cut(s, ". ")
					// The emphasized label may contain sentences; retain the whole label.
					ast.Walk(item, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
						if label, ok := n.(*ast.Emphasis); ok && entering && label.Level == 2 {
							text := strings.Join(strings.Fields(string(label.Text(p.src))), " ")
							title = strings.TrimSuffix(text, ".")
							body = strings.TrimSpace(strings.TrimPrefix(s, text))
							found = true
							return ast.WalkStop, nil
						}
						return ast.WalkContinue, nil
					})
					if !found {
						title, body = "Built for filters", s
					}
					p.Features = append(p.Features, heading{title, body})
				}
			}
			continue
		}
		if err := md.Renderer().Render(&rest, p.src, n); err != nil {
			return err
		}
	}
	if len(p.Features) > 0 {
		p.Body = template.HTML(rest.String())
	}
	return nil
}

type searchEntry struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

var htmlTags = regexp.MustCompile(`<[^>]*>`)

func writeSearchIndex(out string, pages []*page) error {
	entries := make([]searchEntry, 0, len(pages))
	for _, p := range pages {
		plain := strings.Join(strings.Fields(html.UnescapeString(htmlTags.ReplaceAllString(string(p.Body), " "))), " ")
		if p.Home {
			plain = p.Intro + " " + p.Example + " " + plain
			for _, feature := range p.Features {
				plain += " " + feature.ID + " " + feature.Title
			}
		}
		entries = append(entries, searchEntry{p.Title, p.URL, plain})
		for _, h := range p.Headings {
			entries = append(entries, searchEntry{h.Title, p.URL + "#" + h.ID, plain})
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "search-index.js"), append(append([]byte("window.HFF_SEARCH_INDEX = "), data...), ';', '\n'), 0644)
}
