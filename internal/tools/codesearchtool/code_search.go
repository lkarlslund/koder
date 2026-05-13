package codesearchtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type searchOptions struct {
	Query string
	Path  string
	Kind  string
	Exact bool
	Limit int
}

type declaration struct {
	Path      string
	Line      int
	Kind      string
	Name      string
	Signature string
	Doc       string
}

const defaultLimit = 100

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Search Go code",
		Description: "Find Go declarations by symbol name.",
		Usage:       "Search Go source files for declarations by symbol name using the Go parser. Use this when you need to find where a function, method, type, interface, struct, const, or var is declared before reading the file. Use grep for arbitrary text and read to inspect the returned locations. Matching is case-insensitive substring matching by default; set exact for exact symbol names. path optionally narrows the search to a workspace file or directory.",
		Parameters:  `{"type":"object","properties":{"query":{"type":"string","description":"Symbol name or name fragment to find, for example \"RunPrompt\", \"Chat\", or \"ToolKind\"."},"path":{"type":"string","description":"Optional workspace Go file or directory to search from."},"kind":{"type":"string","enum":["function","method","type","struct","interface","const","var"],"description":"Optional declaration kind filter."},"exact":{"type":"boolean","description":"Whether query must exactly match the declaration name."},"limit":{"type":"integer","description":"Maximum number of declarations to return. Defaults to 100."}},"required":["query"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindCodeSearch }
func (tool) BypassesPermission() bool { return false }

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(tools.FirstArg(args, "query", "symbol", "name"))
	if query == "" {
		return nil, errors.New("query is empty")
	}
	out := map[string]string{"query": query}
	if path := tools.NormalizePathInput(tools.FirstArg(args, "path", "root", "dir", "file")); path != "" {
		out["path"] = path
	}
	if kind := strings.ToLower(strings.TrimSpace(tools.FirstArg(args, "kind", "type"))); kind != "" {
		if !validKind(kind) {
			return nil, errors.New("kind must be one of: function, method, type, struct, interface, const, var")
		}
		out["kind"] = kind
	}
	if rawExact := strings.TrimSpace(tools.FirstArg(args, "exact")); rawExact != "" {
		exact, err := parseBool(rawExact)
		if err != nil {
			return nil, errors.New("exact must be true or false")
		}
		out["exact"] = strconv.FormatBool(exact)
	}
	if rawLimit := strings.TrimSpace(tools.FirstArg(args, "limit", "head_limit")); rawLimit != "" {
		limit, err := tools.ParseFlexibleInt(rawLimit)
		if err != nil || limit <= 0 {
			return nil, errors.New("limit must be a positive integer")
		}
		out["limit"] = strconv.Itoa(limit)
	}
	return out, nil
}

func (tool) Preview(req tools.Request) string { return req.Args["query"] }

func (tool) Presentation(req tools.Request) tools.Presentation {
	subtitle := strings.TrimSpace(req.Args["query"])
	if kind := strings.TrimSpace(req.Args["kind"]); kind != "" {
		subtitle += " " + kind
	}
	if path := strings.TrimSpace(req.Args["path"]); path != "" {
		subtitle += " in " + path
	}
	return tools.Presentation{Title: "Search Go code", Subtitle: subtitle, Preview: subtitle}
}

func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	options, err := optionsFromRequest(req)
	if err != nil {
		return tools.Result{}, err
	}
	rootAbs, rootLabel, files, err := searchFiles(runtime.Workdir, options.Path)
	if err != nil {
		return tools.Result{}, err
	}
	fset := token.NewFileSet()
	var matches []declaration
	for _, file := range files {
		decls, err := declarationsInFile(fset, rootAbs, file)
		if err != nil {
			return tools.Result{}, err
		}
		for _, decl := range decls {
			if !matchesKind(decl.Kind, options.Kind) || !matchesQuery(decl.Name, options.Query, options.Exact) {
				continue
			}
			matches = append(matches, decl)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})
	total := len(matches)
	limited := false
	if options.Limit > 0 && len(matches) > options.Limit {
		matches = matches[:options.Limit]
		limited = true
	}
	body := formatMatches(matches)
	if total == 0 {
		body = fmt.Sprintf("No Go declarations matching %q found in %s.", options.Query, rootLabel)
	} else if limited {
		body += fmt.Sprintf("\n... truncated to first %d of %d declarations ...", options.Limit, total)
	}
	text, byteLimited := tools.TruncateText(body, tools.DefaultToolOutputLimit)
	return tools.Result{
		Output: text,
		Meta: map[string]string{
			"query":     options.Query,
			"path":      rootLabel,
			"kind":      options.Kind,
			"exact":     strconv.FormatBool(options.Exact),
			"matches":   strconv.Itoa(total),
			"truncated": tools.BoolString(limited || byteLimited),
		},
	}, nil
}

func optionsFromRequest(req tools.Request) (searchOptions, error) {
	options := searchOptions{
		Query: strings.TrimSpace(req.Args["query"]),
		Path:  strings.TrimSpace(req.Args["path"]),
		Kind:  strings.TrimSpace(req.Args["kind"]),
		Limit: defaultLimit,
	}
	if options.Query == "" {
		return searchOptions{}, errors.New("query is empty")
	}
	if rawExact := strings.TrimSpace(req.Args["exact"]); rawExact != "" {
		exact, err := parseBool(rawExact)
		if err != nil {
			return searchOptions{}, errors.New("exact must be true or false")
		}
		options.Exact = exact
	}
	if rawLimit := strings.TrimSpace(req.Args["limit"]); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit <= 0 {
			return searchOptions{}, errors.New("limit must be a positive integer")
		}
		options.Limit = limit
	}
	return options, nil
}

func searchFiles(workdir, rawPath string) (rootAbs string, rootLabel string, files []string, err error) {
	if strings.TrimSpace(rawPath) == "" {
		rootAbs, rootLabel, err = tools.WorkspaceDir(workdir, "")
		if err != nil {
			return "", "", nil, err
		}
		files, err = goFilesUnder(rootAbs)
		return rootAbs, rootLabel, files, err
	}
	abs, label, err := tools.WorkspacePath(workdir, rawPath)
	if err != nil {
		return "", "", nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", nil, err
	}
	if !info.IsDir() {
		if filepath.Ext(abs) != ".go" {
			return "", "", nil, fmt.Errorf("%q is not a Go source file", label)
		}
		return filepath.Dir(abs), label, []string{abs}, nil
	}
	files, err = goFilesUnder(abs)
	return abs, label, files, err
}

func goFilesUnder(root string) ([]string, error) {
	var files []string
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || strings.HasPrefix(name, ".") && path != "." {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, filepath.Join(root, path))
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func declarationsInFile(fset *token.FileSet, rootAbs, abs string) ([]declaration, error) {
	file, err := parser.ParseFile(fset, abs, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	var decls []declaration
	for _, item := range file.Decls {
		switch decl := item.(type) {
		case *ast.FuncDecl:
			decls = append(decls, funcDeclaration(fset, rel, decl))
		case *ast.GenDecl:
			decls = append(decls, genDeclarations(fset, rel, decl)...)
		}
	}
	return decls, nil
}

func funcDeclaration(fset *token.FileSet, rel string, decl *ast.FuncDecl) declaration {
	kind := "function"
	name := decl.Name.Name
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		kind = "method"
		name = receiverName(decl.Recv.List[0].Type) + "." + name
	}
	return declaration{
		Path:      rel,
		Line:      fset.Position(decl.Pos()).Line,
		Kind:      kind,
		Name:      name,
		Signature: renderNode(fset, decl.Type),
		Doc:       firstDocLine(decl.Doc),
	}
}

func genDeclarations(fset *token.FileSet, rel string, decl *ast.GenDecl) []declaration {
	var out []declaration
	for _, spec := range decl.Specs {
		switch typed := spec.(type) {
		case *ast.TypeSpec:
			kind := typeKind(typed.Type)
			out = append(out, declaration{
				Path:      rel,
				Line:      fset.Position(typed.Pos()).Line,
				Kind:      kind,
				Name:      typed.Name.Name,
				Signature: renderNode(fset, typed.Type),
				Doc:       firstDocLine(docForSpec(decl.Doc, typed.Doc)),
			})
		case *ast.ValueSpec:
			kind := strings.ToLower(decl.Tok.String())
			for _, name := range typed.Names {
				out = append(out, declaration{
					Path:      rel,
					Line:      fset.Position(name.Pos()).Line,
					Kind:      kind,
					Name:      name.Name,
					Signature: renderNode(fset, typed.Type),
					Doc:       firstDocLine(docForSpec(decl.Doc, typed.Doc)),
				})
			}
		}
	}
	return out
}

func typeKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return "type"
	}
}

func receiverName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverName(typed.X)
	case *ast.IndexExpr:
		return receiverName(typed.X)
	case *ast.IndexListExpr:
		return receiverName(typed.X)
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return renderNode(token.NewFileSet(), expr)
	}
}

func docForSpec(groupDoc, specDoc *ast.CommentGroup) *ast.CommentGroup {
	if specDoc != nil {
		return specDoc
	}
	return groupDoc
}

func firstDocLine(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(group.Text()), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func renderNode(fset *token.FileSet, node any) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

func matchesKind(declKind, filter string) bool {
	if filter == "" {
		return true
	}
	if filter == "type" {
		return declKind == "type" || declKind == "struct" || declKind == "interface"
	}
	return declKind == filter
}

func matchesQuery(name, query string, exact bool) bool {
	if exact {
		return name == query || shortName(name) == query
	}
	return strings.Contains(strings.ToLower(name), strings.ToLower(query))
}

func shortName(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func formatMatches(matches []declaration) string {
	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		line := fmt.Sprintf("%s:%d: %s %s", match.Path, match.Line, match.Kind, match.Name)
		if match.Signature != "" {
			line += " " + match.Signature
		}
		if match.Doc != "" {
			line += " - " + match.Doc
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func validKind(kind string) bool {
	switch kind {
	case "function", "method", "type", "struct", "interface", "const", "var":
		return true
	default:
		return false
	}
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, errors.New("invalid boolean")
	}
}
