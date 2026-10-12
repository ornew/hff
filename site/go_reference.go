package main

import (
	"bytes"
	"fmt"
	"go/ast"
	gobuild "go/build"
	"go/doc"
	"go/doc/comment"
	"go/parser"
	"go/printer"
	"go/token"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ornew/nanosite/sitegen"
)

const referenceModule = "github.com/ornew/hff"

var referencePackages = []string{".", "engine", "schema", "capabilities", "backends/local", "backends/sql", "adapters/govalue"}

type referencePackage struct {
	set   *token.FileSet
	files []*ast.File
	pkg   *doc.Package
	dir   string
}
type referenceGenerator struct {
	repo     string
	packages map[string]*referencePackage
}

func referenceURL(dir string) string {
	if dir == "." {
		dir = "hff"
	}
	return "docs/reference/go/" + dir + "/index.html"
}
func (g *referenceGenerator) load(dir string) (*referencePackage, error) {
	if p := g.packages[dir]; p != nil {
		return p, nil
	}
	bp, err := gobuild.ImportDir(filepath.Join(g.repo, dir), gobuild.ImportComment)
	if err != nil {
		return nil, err
	}
	set := token.NewFileSet()
	var files []*ast.File
	names := append(append(append([]string(nil), bp.GoFiles...), bp.TestGoFiles...), bp.XTestGoFiles...)
	for _, name := range names {
		file, err := parser.ParseFile(set, filepath.Join(g.repo, dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	importPath := referenceModule
	if dir != "." {
		importPath += "/" + dir
	}
	pkg, err := doc.NewFromFiles(set, files, importPath)
	if err != nil {
		return nil, err
	}
	p := &referencePackage{set: set, files: files, pkg: pkg, dir: dir}
	g.packages[dir] = p
	return p, nil
}
func referenceFuncID(f *doc.Func) string {
	recv := strings.TrimPrefix(f.Recv, "*")
	if at := strings.IndexByte(recv, '['); at >= 0 {
		recv = recv[:at]
	}
	if recv != "" {
		return recv + "." + f.Name
	}
	return f.Name
}
func referenceDecl(pkg *referencePackage, decl ast.Decl) (string, error) {
	var node ast.Node = decl
	switch d := decl.(type) {
	case *ast.FuncDecl:
		c := *d
		c.Doc = nil
		c.Body = nil
		node = &c
	case *ast.GenDecl:
		c := *d
		c.Doc = nil
		node = &c
	}
	var out bytes.Buffer
	if err := printer.Fprint(&out, pkg.set, node); err != nil {
		return "", err
	}
	return "<pre><code class=\"language-go\">" + html.EscapeString(out.String()) + "</code></pre>\n", nil
}
func (g *referenceGenerator) alias(owner *referencePackage, typ *doc.Type) (*referencePackage, *doc.Type, error) {
	for _, spec := range typ.Decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok || ts.Name.Name != typ.Name || !ts.Assign.IsValid() {
			continue
		}
		sel, ok := ts.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		name, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}
		for _, file := range owner.files {
			if file.Pos() > ts.Pos() || file.End() < ts.End() {
				continue
			}
			for _, imp := range file.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return nil, nil, err
				}
				alias := filepath.Base(path)
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				if alias != name.Name {
					continue
				}
				if path != referenceModule && !strings.HasPrefix(path, referenceModule+"/") {
					return nil, nil, nil
				}
				dir := strings.TrimPrefix(strings.TrimPrefix(path, referenceModule), "/")
				if dir == "" {
					dir = "."
				}
				target, err := g.load(dir)
				if err != nil {
					return nil, nil, err
				}
				for _, candidate := range target.pkg.Types {
					if candidate.Name == sel.Sel.Name {
						return target, candidate, nil
					}
				}
			}
		}
	}
	return nil, nil, nil
}
func referenceDoc(pkg *referencePackage, text, currentURL string) string {
	if text == "" {
		return ""
	}
	pr := pkg.pkg.Printer()
	pr.HeadingLevel = 5
	pr.DocLinkURL = func(link *comment.DocLink) string {
		id := link.Name
		if link.Recv != "" {
			id = link.Recv + "." + id
		}
		importPath := link.ImportPath
		if importPath == "" {
			importPath = pkg.pkg.ImportPath
		}
		if strings.HasPrefix(importPath, referenceModule+"/internal/") {
			importPath = referenceModule
		}
		for _, dir := range referencePackages {
			path := referenceModule
			if dir != "." {
				path += "/" + dir
			}
			if path == importPath {
				return relative(currentURL, referenceURL(dir)) + "#" + id
			}
		}
		return "https://pkg.go.dev/" + importPath + "#" + id
	}
	return string(pr.HTML(pkg.pkg.Parser().Parse(text)))
}
func (g *referenceGenerator) render(pkg *referencePackage) (string, []sitegen.Heading, error) {
	var out strings.Builder
	var headings []sitegen.Heading
	heading := func(level int, id, title string) {
		fmt.Fprintf(&out, "<h%d id=\"%s\">%s</h%d>\n", level, html.EscapeString(id), html.EscapeString(title), level)
		headings = append(headings, sitegen.Heading{ID: id, Title: title, Level: level})
	}
	symbol := func(owner *referencePackage, level int, id, label string, decl ast.Decl, text string) error {
		heading(level, id, label)
		pos := owner.set.Position(decl.Pos())
		file, _ := filepath.Rel(g.repo, pos.Filename)
		fmt.Fprintf(&out, "<p><a href=\"https://github.com/ornew/hff/blob/main/%s#L%d\">Source</a></p>\n", html.EscapeString(filepath.ToSlash(file)), pos.Line)
		code, err := referenceDecl(owner, decl)
		if err != nil {
			return err
		}
		out.WriteString(code)
		out.WriteString(referenceDoc(owner, text, referenceURL(pkg.dir)))
		return nil
	}
	values := func(list []*doc.Value) error {
		for _, v := range list {
			for _, name := range v.Names {
				fmt.Fprintf(&out, "<span id=\"%s\"></span>", html.EscapeString(name))
			}
			pos := pkg.set.Position(v.Decl.Pos())
			file, _ := filepath.Rel(g.repo, pos.Filename)
			fmt.Fprintf(&out, "<p><a href=\"https://github.com/ornew/hff/blob/main/%s#L%d\">Source</a></p>\n", html.EscapeString(filepath.ToSlash(file)), pos.Line)
			code, err := referenceDecl(pkg, v.Decl)
			if err != nil {
				return err
			}
			out.WriteString(code)
			out.WriteString(referenceDoc(pkg, v.Doc, referenceURL(pkg.dir)))
		}
		return nil
	}
	funcs := func(owner *referencePackage, list []*doc.Func, alias string) error {
		for _, f := range list {
			id := referenceFuncID(f)
			if alias != "" {
				id = alias + "." + f.Name
			}
			if err := symbol(owner, 3, id, "func "+id, f.Decl, f.Doc); err != nil {
				return err
			}
			examples, err := referenceExamples(owner, f.Examples, referenceURL(pkg.dir))
			if err != nil {
				return err
			}
			out.WriteString(examples)
		}
		return nil
	}
	heading(1, "package", "Package "+pkg.pkg.Name)
	fmt.Fprintf(&out, "<pre><code class=\"language-go\">import %q</code></pre>\n", pkg.pkg.ImportPath)
	out.WriteString("<p>Generated from this checkout's Go source. Released versions are also on <a href=\"https://pkg.go.dev/" + pkg.pkg.ImportPath + "\">pkg.go.dev</a>.</p>\n")
	heading(2, "overview", "Overview")
	out.WriteString(referenceDoc(pkg, pkg.pkg.Doc, referenceURL(pkg.dir)))
	var index strings.Builder
	index.WriteString("<h2 id=\"index\">Index</h2><ul>")
	indexItem := func(id, label string) {
		fmt.Fprintf(&index, "<li><a href=\"#%s\"><code>%s</code></a></li>", html.EscapeString(id), html.EscapeString(label))
	}
	for _, list := range [][]*doc.Value{pkg.pkg.Consts, pkg.pkg.Vars} {
		for _, v := range list {
			for _, name := range v.Names {
				indexItem(name, name)
			}
		}
	}
	for _, f := range pkg.pkg.Funcs {
		indexItem(referenceFuncID(f), "func "+f.Name)
	}
	for _, typ := range pkg.pkg.Types {
		indexItem(typ.Name, "type "+typ.Name)
		for _, list := range [][]*doc.Value{typ.Consts, typ.Vars} {
			for _, value := range list {
				for _, name := range value.Names {
					indexItem(name, name)
				}
			}
		}
		for _, f := range append(append([]*doc.Func(nil), typ.Funcs...), typ.Methods...) {
			indexItem(referenceFuncID(f), f.Name)
		}
		alias, definition, err := g.alias(pkg, typ)
		if err != nil {
			return "", nil, err
		}
		if alias != nil {
			for _, f := range definition.Methods {
				indexItem(typ.Name+"."+f.Name, typ.Name+"."+f.Name)
			}
		}
	}
	index.WriteString("</ul>")
	out.WriteString(index.String())
	headings = append(headings, sitegen.Heading{ID: "index", Title: "Index", Level: 2})
	if len(pkg.pkg.Consts) > 0 {
		heading(2, "constants", "Constants")
		if err := values(pkg.pkg.Consts); err != nil {
			return "", nil, err
		}
	}
	if len(pkg.pkg.Vars) > 0 {
		heading(2, "variables", "Variables")
		if err := values(pkg.pkg.Vars); err != nil {
			return "", nil, err
		}
	}
	if len(pkg.pkg.Funcs) > 0 {
		heading(2, "functions", "Functions")
		if err := funcs(pkg, pkg.pkg.Funcs, ""); err != nil {
			return "", nil, err
		}
	}
	heading(2, "types", "Types")
	for _, typ := range pkg.pkg.Types {
		if err := symbol(pkg, 3, typ.Name, "type "+typ.Name, typ.Decl, typ.Doc); err != nil {
			return "", nil, err
		}
		examples, err := referenceExamples(pkg, typ.Examples, referenceURL(pkg.dir))
		if err != nil {
			return "", nil, err
		}
		out.WriteString(examples)
		if err := values(typ.Consts); err != nil {
			return "", nil, err
		}
		if err := values(typ.Vars); err != nil {
			return "", nil, err
		}
		if err := funcs(pkg, typ.Funcs, ""); err != nil {
			return "", nil, err
		}
		if err := funcs(pkg, typ.Methods, ""); err != nil {
			return "", nil, err
		}
		alias, definition, err := g.alias(pkg, typ)
		if err != nil {
			return "", nil, err
		}
		if alias != nil {
			out.WriteString("<p>This alias exposes the following definition and methods.</p>\n")
			code, err := referenceDecl(alias, definition.Decl)
			if err != nil {
				return "", nil, err
			}
			out.WriteString(code)
			out.WriteString(referenceDoc(alias, definition.Doc, referenceURL(pkg.dir)))
			if err := funcs(alias, definition.Methods, typ.Name); err != nil {
				return "", nil, err
			}
		}
	}
	examples, err := referenceExamples(pkg, pkg.pkg.Examples, referenceURL(pkg.dir))
	if err != nil {
		return "", nil, err
	}
	out.WriteString(examples)
	return out.String(), headings, nil
}
func addGoReference(ctx *sitegen.Context) error {
	if _, err := os.Stat(filepath.Join(ctx.Repo, "go.mod")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	generator := referenceGenerator{repo: ctx.Repo, packages: make(map[string]*referencePackage)}
	var index strings.Builder
	index.WriteString("<h1 id=\"go-reference\">Go Reference</h1><p>Public Go packages, declarations, methods, and examples generated from this checkout.</p><table><thead><tr><th>Package</th><th>Description</th></tr></thead><tbody>")
	for _, dir := range referencePackages {
		pkg, err := generator.load(dir)
		if err != nil {
			return fmt.Errorf("Go reference %s: %w", dir, err)
		}
		body, headings, err := generator.render(pkg)
		if err != nil {
			return err
		}
		page, err := ctx.AddHTMLPage(strings.TrimSuffix(referenceURL(dir), "index.html")+"README.md", referenceURL(dir), "Package "+pkg.pkg.Name, []byte(body))
		if err != nil {
			return err
		}
		if page != nil {
			page.Headings = headings
			page.NavTitle = dir
			if dir == "." {
				page.NavTitle = "hff"
			}
			page.Description = doc.Synopsis(pkg.pkg.Doc)
		}
		fmt.Fprintf(&index, "<tr><td><a href=\"%s\"><code>%s</code></a></td><td>%s</td></tr>", relative("docs/reference/go/index.html", referenceURL(dir)), pkg.pkg.ImportPath, html.EscapeString(doc.Synopsis(pkg.pkg.Doc)))
	}
	index.WriteString("</tbody></table>")
	page, err := ctx.AddHTMLPage("docs/reference/go/README.md", "docs/reference/go/index.html", "Go Reference", []byte(index.String()))
	if page != nil {
		page.NavTitle = "Overview"
	}
	return err
}

func referenceExamples(owner *referencePackage, examples []*doc.Example, currentURL string) (string, error) {
	var out strings.Builder
	for _, example := range examples {
		out.WriteString("<h4>Example</h4>\n")
		out.WriteString(referenceDoc(owner, example.Doc, currentURL))
		var code bytes.Buffer
		if err := printer.Fprint(&code, owner.set, example.Code); err != nil {
			return "", err
		}
		out.WriteString("<pre><code class=\"language-go\">" + html.EscapeString(code.String()) + "</code></pre>\n")
		if example.Output != "" {
			out.WriteString("<p>Output:</p><pre>" + html.EscapeString(example.Output) + "</pre>\n")
		}
	}
	return out.String(), nil
}
