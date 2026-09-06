package leak_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nothing in Go forces a package to wire the check: a package added with tests
// and no TestMain is silently exempt, and the suite still looks complete. That
// omission is statically visible even though a leak is not, so this file is the
// check. The package list is the tree's, not one kept by hand, so a package
// added without the wiring fails here instead of shipping unwatched.

// TestEveryTestBinaryWiresTheCheck requires every directory holding test files
// to declare a TestMain that calls leak.VerifyTestMain. The unit is the
// directory because the unit is the test binary: Go builds one per package, and
// TestMain is the only hook it gives that binary to run something after all its
// tests.
func TestEveryTestBinaryWiresTheCheck(t *testing.T) {
	root := moduleRoot(t)

	var unwired []string
	for _, dir := range testDirs(t, root) {
		if !wiresCheck(t, filepath.Join(root, dir)) {
			unwired = append(unwired, dir)
		}
	}

	if len(unwired) > 0 {
		t.Errorf("test packages with no goroutine-leak check: %v\nadd leak_test.go with:\n\tfunc TestMain(m *testing.M) { leak.VerifyTestMain(m) }", unwired)
	}
}

// moduleRoot walks up from the package directory to the go.mod beside the tree
// being checked.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// testDirs lists the module-relative directories that hold test files. Dotted
// directories and testdata are not built, so they hold no test binary.
func testDirs(t *testing.T, root string) []string {
	t.Helper()

	seen := map[string]bool{}
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		dir, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return dirs
}

// wiresCheck reports whether any test file in dir declares a TestMain that
// calls leak.VerifyTestMain. A TestMain that runs the tests without the call is
// the silent exemption this guard exists to catch, so the call is what counts,
// not the function.
func wiresCheck(t *testing.T, dir string) bool {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", filepath.Join(dir, entry.Name()), err)
		}
		if callsVerify(file) {
			return true
		}
	}
	return false
}

// callsVerify reports whether the file's TestMain calls leak.VerifyTestMain.
func callsVerify(file *ast.File) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestMain" || fn.Body == nil {
			continue
		}

		found := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "VerifyTestMain" {
				return true
			}
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "leak" {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}
