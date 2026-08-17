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
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The tests in this file keep docs/design.md §3.3 and §3.5 honest
// against the declarations they describe.
//
// Those two sections are normative: §3.3 is what a host implementer
// reads to learn the optional-capability contract, and §3.5 is what it
// reads to wire the prompter and the elicitor. Both quote Go
// declarations, and quoted declarations rot silently — nothing in the
// build has ever compared them to package tui. Silence is the whole
// problem, because capabilities are detected by type assertion: an
// adapter written against a method set that no longer matches does not
// fail to compile, it comes up with the capability quietly declined.
// Issue #212 is that failure, and the reference host has already been
// bitten by it.
//
// So the comparison is mechanical here rather than editorial. A
// declaration quoted in either section must exist in the package with
// the same method set or the same fields, the roster of capabilities
// §3.3 declares must be the roster docs/api-surface.md derives from the
// source, and a declaration the prose marks as specified-but-unshipped
// must genuinely not exist yet.

// docNotShipped are the types §3.3 and §3.5 quote that package tui
// does NOT declare, because the requirement behind each is specified
// and not yet built. The prose says so at every one of them; this list
// is the machine-readable half of that statement, and the test asserts
// the absence rather than merely tolerating it, so that shipping one of
// these fails here until the "NOT SHIPPED" note comes off the doc.
var docNotShipped = map[string]string{
	// R-AT-4, the multi-source @ palette. There is no
	// Options.MentionProviders field either.
	"MentionProvider": "R-AT-4",
	"MentionMatch":    "R-AT-4",
	// R-PROMPT-1, agent-initiated multiple-choice questions. §3.5
	// has carried the "SPECIFIED, NOT SHIPPED" note since v0.19.0.
	"UserPrompter":       "R-PROMPT-1",
	"UserPromptRequest":  "R-PROMPT-1",
	"UserChoice":         "R-PROMPT-1",
	"UserPromptResponse": "R-PROMPT-1",
}

// TestDesignDocDeclarationsMatchSource compares every type quoted in
// design.md §3.3 and §3.5 against the same-named type in package tui.
func TestDesignDocDeclarationsMatchSource(t *testing.T) {
	pkg := parsePackageTypes(t)

	for _, section := range []string{"3.3", "3.5"} {
		docTypes := parseDocTypes(t, designSection(t, section))
		if len(docTypes) == 0 {
			t.Fatalf("§%s: no type declarations found; the section or its code fences moved", section)
		}
		for _, name := range sortedKeys(docTypes) {
			doc := docTypes[name]
			src, declared := pkg[name]
			if req, unshipped := docNotShipped[name]; unshipped {
				if declared {
					t.Errorf("§%s quotes %s as specified-but-unshipped (%s), but package tui declares it now; drop the NOT SHIPPED note from the doc and %s from docNotShipped",
						section, name, req, name)
				}
				continue
			}
			if !declared {
				t.Errorf("§%s declares %s, which package tui does not; either the doc is stale or %s belongs in docNotShipped with the requirement it is waiting on",
					section, name, name)
				continue
			}
			for _, diff := range diffTypes(name, doc, src) {
				t.Errorf("§%s: %s", section, diff)
			}
		}
	}
}

// TestDesignDocDeclaresEveryCapability checks §3.3 against the roster
// in api-surface.md, which is derived from the source. §3.3's own
// promise is that a capability absent from it is not part of the
// plug-in surface, and that promise is only worth something if the
// section is complete.
func TestDesignDocDeclaresEveryCapability(t *testing.T) {
	docTypes := parseDocTypes(t, designSection(t, "3.3"))
	roster := capabilityRoster(t)
	if len(roster) == 0 {
		t.Fatal("no capability interfaces found in api-surface.md; its Optional capability table moved")
	}
	for _, name := range roster {
		if _, ok := docTypes[name]; !ok {
			t.Errorf("api-surface.md classifies %s as a §3.3 capability interface, but §3.3 does not declare it", name)
		}
	}
}

// designSection returns the body of the design.md subsection whose
// heading number is num (e.g. "3.3"), up to the next heading.
func designSection(t *testing.T, num string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "docs", "design.md"))
	if err != nil {
		t.Fatalf("read design.md: %v", err)
	}
	var out []string
	in := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") {
			if in {
				break
			}
			in = strings.HasPrefix(line, "### "+num+" ")
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	if !in {
		t.Fatalf("design.md has no §%s heading", num)
	}
	return strings.Join(out, "\n")
}

// parseDocTypes extracts the Go fenced blocks from a doc section and
// returns the type declarations in them, keyed by name.
func parseDocTypes(t *testing.T, section string) map[string]*ast.TypeSpec {
	t.Helper()
	var src strings.Builder
	src.WriteString("package tui\n")
	fenced := false
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(line, "```go"):
			fenced = true
		case strings.HasPrefix(line, "```"):
			fenced = false
		case fenced:
			src.WriteString(line)
			src.WriteString("\n")
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "design.md", src.String(), parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("the Go blocks in this section do not parse: %v", err)
	}
	return collectTypes([]*ast.File{file})
}

// parsePackageTypes returns every type declared in the non-test files
// of package tui, keyed by name. Build constraints are ignored, which
// is deliberate: a declaration is part of the surface whichever
// platform's file carries it.
func parsePackageTypes(t *testing.T) map[string]*ast.TypeSpec {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no source files found in the working directory")
	}
	return collectTypes(files)
}

func collectTypes(files []*ast.File) map[string]*ast.TypeSpec {
	out := map[string]*ast.TypeSpec{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					out[ts.Name.Name] = ts
				}
			}
		}
	}
	return out
}

// diffTypes reports how the doc's declaration of name differs from the
// package's. Interfaces are compared by method set and structs by
// field set; any other kind is compared by rendered text.
func diffTypes(name string, doc, src *ast.TypeSpec) []string {
	docIface, docIsIface := doc.Type.(*ast.InterfaceType)
	srcIface, srcIsIface := src.Type.(*ast.InterfaceType)
	switch {
	case docIsIface != srcIsIface:
		return []string{name + " is an interface in one place and not the other"}
	case docIsIface:
		return diffMembers(name, "method", members(docIface.Methods), members(srcIface.Methods))
	}
	docStruct, docIsStruct := doc.Type.(*ast.StructType)
	srcStruct, srcIsStruct := src.Type.(*ast.StructType)
	switch {
	case docIsStruct != srcIsStruct:
		return []string{name + " is a struct in one place and not the other"}
	case docIsStruct:
		return diffMembers(name, "field", members(docStruct.Fields), members(srcStruct.Fields))
	}
	if render(doc.Type) != render(src.Type) {
		return []string{name + " is declared as `" + render(doc.Type) + "` but the source has `" + render(src.Type) + "`"}
	}
	return nil
}

// members flattens a field list to name → rendered type. Embedded
// entries (an embedded interface, an anonymous struct field) are keyed
// by their own rendered type.
func members(list *ast.FieldList) map[string]string {
	out := map[string]string{}
	if list == nil {
		return out
	}
	for _, f := range list.List {
		typ := render(f.Type)
		if len(f.Names) == 0 {
			out[typ] = typ
			continue
		}
		for _, n := range f.Names {
			out[n.Name] = typ
		}
	}
	return out
}

func diffMembers(name, kind string, doc, src map[string]string) []string {
	var out []string
	for _, m := range sortedKeys(doc) {
		switch got, ok := src[m]; {
		case !ok:
			out = append(out, name+" is documented with a "+kind+" `"+m+"` that the source does not have")
		case got != doc[m]:
			out = append(out, name+"."+m+" is documented as `"+doc[m]+"` but the source has `"+got+"`")
		}
	}
	for _, m := range sortedKeys(src) {
		if _, ok := doc[m]; !ok {
			out = append(out, name+" has a "+kind+" `"+m+" "+src[m]+"` the doc does not mention")
		}
	}
	return out
}

func render(n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), n); err != nil {
		return "<unprintable>"
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

// capabilityRoster reads the symbols api-surface.md classifies as
// "§3.3 capability interface" out of its Optional capability table.
func capabilityRoster(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "docs", "api-surface.md"))
	if err != nil {
		t.Fatalf("read api-surface.md: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) != 6 || !strings.Contains(cells[4], "§3.3 capability interface") {
			continue
		}
		out = append(out, strings.Trim(strings.TrimSpace(cells[1]), "`"))
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
