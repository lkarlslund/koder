package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedStoragePackagesStayRemoved(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range []string{
		filepath.Join(root, "internal", "chatstore"),
		filepath.Join(root, "internal", "sessionstore"),
	} {
		if _, err := os.Stat(dir); err == nil {
			t.Fatalf("removed storage package directory still exists: %s", dir)
		}
	}
}

func TestPlanningPackageHasNoStoreAPI(t *testing.T) {
	for _, name := range []string{
		"PlanCollection",
		"TodoCollection",
		"TaskCollection",
		"PutPlan",
		"GetPlan",
		"PutTodo",
		"ListTodos",
		"AddTodoItems",
		"UpdateTodoItem",
		"PutTask",
		"ListTasks",
	} {
		assertNoPlanningSelector(t, rootDir(t), name)
	}
}

func rootDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func assertNoPlanningSelector(t *testing.T, root, selector string) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch filepath.Base(path) {
			case ".git", "research":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		planningNames := map[string]struct{}{}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath != "github.com/lkarlslund/koder/internal/planning" {
				continue
			}
			if imp.Name != nil && imp.Name.Name != "." && imp.Name.Name != "_" {
				planningNames[imp.Name.Name] = struct{}{}
			} else {
				planningNames["planning"] = struct{}{}
			}
		}
		if len(planningNames) == 0 {
			return nil
		}
		file, err = parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != selector {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if _, ok := planningNames[ident.Name]; ok {
				t.Fatalf("%s uses removed planning storage API %s.%s", path, ident.Name, selector)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
