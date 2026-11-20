// Copyright 2026 The go-steer team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	coreTUIModulePath           = "github.com/go-steer/core-tui"
	coreTUIModulePrefix         = coreTUIModulePath + "/"
	tailLineThreshold           = 150
	maxNonTestFuncsOver150Lines = 4 // (*model).handle, (*model).dispatchBuiltinSlash, (*model).handleKey, (*model).applySwitchTarget
)

// packageLayer defines strict acyclic architecture layers across core-tui
// (higher layers may only import strictly lower layers).
var packageLayer = map[string]int{
	"tui":                          0,
	"examples/core-agent/fakehost": 0,
	"tui/testagent":                1,
	"examples/core-agent":          2,
	"examples/local":               2,
	"examples/notifier-smoke":      2,
}

func TestArchitectureLayeringAndComplexityTail(t *testing.T) {
	t.Parallel()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	seenPackages := make(map[string]bool, len(packageLayer))
	var over150 []string

	err = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipArchDir(repoRoot, path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, raw, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}

		pkgRel := filepath.ToSlash(filepath.Dir(rel))
		seenPackages[pkgRel] = true
		verifyPackageImports(t, rel, pkgRel, f, packageLayer)

		lines := strings.Split(string(raw), "\n")
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			start := fset.Position(fn.Pos()).Line
			end := fset.Position(fn.End()).Line
			codeLines := countCodeLines(lines, start, end)
			if codeLines > tailLineThreshold {
				over150 = append(over150, fmt.Sprintf("%s:%s (%d)", rel, qualFuncName(fn), codeLines))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, pkg := range sortedKeys(packageLayer) {
		if !seenPackages[pkg] {
			t.Errorf("packageLayer entry %q has no non-test .go files on disk", pkg)
		}
	}

	if len(over150) > maxNonTestFuncsOver150Lines {
		t.Errorf("non-test functions over %d code lines = %d (ratchet ceiling %d): %v",
			tailLineThreshold, len(over150), maxNonTestFuncsOver150Lines, over150)
	}
}

func TestArchitectureValidatorsRejectViolations(t *testing.T) {
	t.Parallel()

	layers := map[string]int{
		"tui":            0,
		"examples/fake":  0,
		"tui/testagent":  1,
		"examples/local": 2,
	}

	parseSrc := func(t *testing.T, src string) *ast.File {
		t.Helper()
		f, err := parser.ParseFile(token.NewFileSet(), "sample.go", src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse sample: %v", err)
		}
		return f
	}

	t.Run("valid downward and external imports pass", func(t *testing.T) {
		f := parseSrc(t, `package main
import (
	"fmt"
	"github.com/go-steer/core-tui/tui"
	"github.com/go-steer/core-tui/tui/testagent"
	"charm.land/bubbletea/v2"
)
`)
		if errs := checkPackageImports("examples/local/main.go", "examples/local", f, layers); len(errs) != 0 {
			t.Fatalf("unexpected import errors: %v", errs)
		}
	})

	t.Run("unregistered source package rejected", func(t *testing.T) {
		f := parseSrc(t, `package extra`)
		errs := checkPackageImports("extra/x.go", "extra", f, layers)
		if len(errs) != 1 || !strings.Contains(errs[0], `package "extra" is not registered`) {
			t.Fatalf("errs = %v, want unregistered source package error", errs)
		}
	})

	t.Run("unregistered destination and root module imports rejected", func(t *testing.T) {
		f := parseSrc(t, `package main
import (
	"github.com/go-steer/core-tui"
	"github.com/go-steer/core-tui/unknown"
)
`)
		errs := checkPackageImports("examples/local/main.go", "examples/local", f, layers)
		if len(errs) != 2 {
			t.Fatalf("errs = %v (len %d), want 2 unregistered import errors", errs, len(errs))
		}
	})

	t.Run("same-layer and upward-layer imports rejected", func(t *testing.T) {
		sameLayer := parseSrc(t, `package tui
import "github.com/go-steer/core-tui/examples/fake"
`)
		if errs := checkPackageImports("tui/bad.go", "tui", sameLayer, layers); len(errs) != 1 || !strings.Contains(errs[0], "layering violation") {
			t.Fatalf("same-layer errs = %v, want 1 layering violation", errs)
		}

		upward := parseSrc(t, `package testagent
import "github.com/go-steer/core-tui/examples/local"
`)
		if errs := checkPackageImports("tui/testagent/bad.go", "tui/testagent", upward, layers); len(errs) != 1 || !strings.Contains(errs[0], "layering violation") {
			t.Fatalf("upward-layer errs = %v, want 1 layering violation", errs)
		}
	})

	t.Run("countCodeLines ignores blanks and line comments and enforces threshold", func(t *testing.T) {
		srcLines := []string{
			"package p",
			"",
			"func sample() {",
			"	// leading comment",
			"",
			"	x := 1",
			"	   ",
			"	// another comment",
			"	_ = x",
			"}",
		}
		if got := countCodeLines(srcLines, 3, 10); got != 4 {
			t.Fatalf("countCodeLines = %d, want 4", got)
		}
		if got := countCodeLines(srcLines, 0, 999); got != 5 {
			t.Fatalf("clamped countCodeLines = %d, want 5", got)
		}

		body := make([]string, 0, tailLineThreshold+5)
		body = append(body, "func big() {")
		for i := range tailLineThreshold - 1 {
			body = append(body, fmt.Sprintf("\t_ = %d", i))
		}
		body = append(body, "\t// comment does not count", "", "}")
		if got := countCodeLines(body, 1, len(body)); got != tailLineThreshold+1 {
			t.Fatalf("big func countCodeLines = %d, want %d", got, tailLineThreshold+1)
		}
	})

	t.Run("qualFuncName formats functions and methods", func(t *testing.T) {
		f := parseSrc(t, `package p
func plain() {}
func (v val) byVal() {}
func (p *ptr) byPtr() {}
`)
		var got []string
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				got = append(got, qualFuncName(fn))
			}
		}
		want := []string{"plain", "val.byVal", "(*ptr).byPtr"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("qualFuncName = %v, want %v", got, want)
		}
	})

	t.Run("shouldSkipArchDir skips worktrees and nested go modules", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/root\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(root, "docs", "site")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/root/docs/site\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pkgDir := filepath.Join(root, "tui")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}

		if shouldSkipArchDir(root, root, filepath.Base(root)) {
			t.Fatal("repoRoot itself must not be skipped")
		}
		if shouldSkipArchDir(root, pkgDir, "tui") {
			t.Fatal("in-module package dir must not be skipped")
		}
		if !shouldSkipArchDir(root, nested, "site") {
			t.Fatal("nested Go module dir must be skipped")
		}
		for _, skip := range []string{".git", ".worktrees", ".claude", "bin", "dist", "vendor", "testdata", "node_modules"} {
			if !shouldSkipArchDir(root, filepath.Join(root, skip), skip) {
				t.Fatalf("expected dir %q to be skipped", skip)
			}
		}
	})
}

func shouldSkipArchDir(repoRoot, path, name string) bool {
	switch name {
	case ".git", ".worktrees", ".claude", "bin", "dist", "vendor", "testdata", "node_modules":
		return true
	}
	if path != repoRoot {
		if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
			return true
		}
	}
	return false
}

func verifyPackageImports(t *testing.T, relFile, pkgRel string, f *ast.File, layers map[string]int) {
	t.Helper()
	for _, msg := range checkPackageImports(relFile, pkgRel, f, layers) {
		t.Error(msg)
	}
}

func checkPackageImports(relFile, pkgRel string, f *ast.File, layers map[string]int) []string {
	srcLayer, ok := layers[pkgRel]
	if !ok {
		return []string{fmt.Sprintf("%s: package %q is not registered in packageLayer architecture map", relFile, pkgRel)}
	}
	var errs []string
	for _, imp := range f.Imports {
		impPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			impPath = strings.Trim(imp.Path.Value, "\"`")
		}
		var dstPkg string
		switch {
		case impPath == coreTUIModulePath:
			dstPkg = "."
		case strings.HasPrefix(impPath, coreTUIModulePrefix):
			dstPkg = strings.TrimPrefix(impPath, coreTUIModulePrefix)
		default:
			continue
		}
		dstLayer, known := layers[dstPkg]
		if !known {
			errs = append(errs, fmt.Sprintf("%s: imports unregistered internal package %q", relFile, dstPkg))
			continue
		}
		if dstLayer >= srcLayer {
			errs = append(errs, fmt.Sprintf("layering violation in %s (%s, layer %d): cannot import %s (layer %d)",
				relFile, pkgRel, srcLayer, dstPkg, dstLayer))
		}
	}
	return errs
}

func countCodeLines(lines []string, startLine, endLine int) int {
	if startLine < 1 {
		startLine = 1
	}
	n := 0
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		s := strings.TrimSpace(lines[i])
		if s == "" || strings.HasPrefix(s, "//") {
			continue
		}
		n++
	}
	return n
}

func qualFuncName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch recv := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := recv.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return recv.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}
