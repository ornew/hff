// Command site renders repository Markdown into a standalone static site.
package main

import (
	"bytes"
	"embed"
	"flag"
	"html"
	"html/template"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ornew/nanosite/render"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
)

//go:embed layout.html landing.html playground.html playground.js playground-worker.js
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
	Playground         bool
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
	case source == "README.md" || source == "docs/playground.md":
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
	designIndex := flag.Bool("design-index", false, "write the generated Design Records introduction to stdout")
	flag.Parse()
	if *designIndex {
		src, err := designIndexMarkdown(*repo)
		if err != nil {
			log.Fatal(err)
		}
		if src == nil {
			log.Fatal("repository has no docs/design directory")
		}
		if _, err := os.Stdout.Write(src); err != nil {
			log.Fatal(err)
		}
		return
	}
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
	return render.MarkdownOutput(source, false, []string{"README.md"})
}

// Relative URLs support subpath hosting and directly opened HTML files.
func relative(from, to string) string {
	r, _ := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(to))
	return filepath.ToSlash(r)
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
