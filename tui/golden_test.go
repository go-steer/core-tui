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

// Golden-file infrastructure (issue #101). The package had no
// golden corpus at all, so every rendering assertion was a
// strings.Contains on a token — which cannot see a modal two
// columns too wide, a footer wrapped to the wrong column, or an
// inner ANSI reset that drops the outer color for the rest of a
// line. Goldens capture the bytes, escapes included.
//
// Regenerate with:
//
//	go test ./tui -update
//
// Two pinning rules make the corpus stable enough to live with,
// both called out in issue #101:
//
//   - Every golden renders through goldenTheme() — one explicit
//     Theme literal owned by this file. Never m.styles (which is
//     whatever NewModel happened to seed) and never the
//     background-detected theme. A later change to DefaultTheme
//     or to the provider palettes therefore churns zero goldens.
//   - Syntax highlighting is pinned to a fixed named chroma style
//     via pinChromaStyle. The production default lives in
//     syntax_cache.go and is free to change without rewriting the
//     corpus.
//
// Lipgloss v2 renders at TrueColor regardless of TERM / NO_COLOR
// (styles carry their own profile), so the bytes are stable across
// developer machines and CI.

package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

// updateGolden regenerates the corpus instead of comparing to it.
var updateGolden = flag.Bool("update", false, "regenerate the golden files under tui/testdata")

// goldenChromaStyle is the chroma style name every golden that can
// reach the syntax highlighter is pinned to. Named explicitly here
// rather than read from chromaSyntaxStyle so a production retheme
// is not a corpus-wide rewrite.
const goldenChromaStyle = "github"

// goldenTheme is THE theme for the corpus: a full, explicit Theme
// literal with no reference to DefaultTheme or any provider
// palette. Every field is spelled out on purpose — a new Theme
// field added upstream should show up here as a deliberate choice
// (and as a reviewable golden churn), not silently inherit.
func goldenTheme() Theme {
	return Theme{
		Name:            "golden",
		Primary:         lipgloss.Color("#8B5CF6"),
		Secondary:       lipgloss.Color("#EC4899"),
		Accent:          lipgloss.Color("#8B5CF6"),
		Success:         lipgloss.Color("#5FD787"),
		Warning:         lipgloss.Color("#FFD75F"),
		Error:           lipgloss.Color("#FF5F5F"),
		Info:            lipgloss.Color("#A8A8A8"),
		FgBase:          lipgloss.Color("#D0D0D0"),
		FgMuted:         lipgloss.Color("#9A9A9A"),
		FgSubtle:        lipgloss.Color("#6C6C6C"),
		BgBase:          nil,
		BgElevated:      lipgloss.Color("#1E1E1E"),
		BgOverlay:       lipgloss.Color("#2A2A2A"),
		BorderActive:    lipgloss.Color("#8B5CF6"),
		BorderQuiet:     lipgloss.Color("#3A3A3A"),
		DiffAddBg:       lipgloss.Color("#1B2D1B"),
		DiffDelBg:       lipgloss.Color("#3A1E1E"),
		DiffAddGutterBg: lipgloss.Color("#102010"),
		DiffDelGutterBg: lipgloss.Color("#2A1010"),
	}
}

// goldenStyles is the Styles bundle every golden renders through.
// Dark is pinned true; the light variant is a different corpus and
// is deliberately not captured (it would double the file count for
// the same layout arithmetic).
func goldenStyles() Styles {
	return NewStylesWithTheme(true, goldenTheme())
}

// pinChromaStyle forces the package-level syntax style to
// goldenChromaStyle for the duration of the test and restores it
// afterwards, so a golden that reaches the highlighter can't be
// perturbed by the production default moving.
func pinChromaStyle(t *testing.T) {
	t.Helper()
	prev := chromaSyntaxStyle
	chromaSyntaxStyle = styles.Get(goldenChromaStyle)
	// The highlighter memoizes per (lang, bg, line); a swapped
	// style must not read back a value cached under the other one.
	syntaxCache.Clear()
	t.Cleanup(func() {
		chromaSyntaxStyle = prev
		syntaxCache.Clear()
	})
}

// goldenDir is the absolute path to tui/testdata, resolved at
// package-variable initialization — i.e. before any test can call
// t.Chdir. The full-frame goldens deliberately move the process
// into a synthetic working directory (see pinCwd), so a relative
// "testdata" would resolve somewhere else by the time the file is
// read or written.
var goldenDir = func() string {
	abs, err := filepath.Abs("testdata")
	if err != nil {
		panic("resolving tui/testdata: " + err.Error())
	}
	return abs
}()

// pinCwd gives the test a synthetic HOME and working directory.
// The status header renders os.Getwd() shortened against
// os.UserHomeDir(), so without this the full-frame goldens capture
// the checkout path of whoever ran -update: "~/projects/wt-frame/tui"
// on a laptop, "~/work/core-tui/core-tui/tui" on a CI runner. The
// synthetic home is symlink-resolved because os.Getwd returns a
// resolved path and the prefix match in displayCwd is textual.
func pinCwd(t *testing.T) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving temp home: %v", err)
	}
	proj := filepath.Join(home, "core-tui")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("creating synthetic project dir: %v", err)
	}
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows
	t.Chdir(proj)
}

// assertGolden compares got against tui/testdata/<name>.golden,
// writing the file instead when -update is passed. Content is
// stored verbatim — ANSI escapes and all — because the escapes are
// most of what the corpus exists to pin. `.gitattributes` marks
// `*.golden -diff` so a churned escape sequence doesn't drown a
// review.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating testdata dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s (run `go test ./tui -update` to create it): %v", path, err)
	}
	if string(want) == got {
		return
	}
	t.Errorf("golden mismatch for %s\n%s\nRe-run with `go test ./tui -update` if the change is intended.",
		path, goldenDiffHint(string(want), got))
}

// goldenDiffHint produces a short, escape-aware description of the
// first divergence. Dumping two ANSI-bearing blobs into the test
// log is unreadable; the line number plus both quoted lines is
// what actually identifies the regression.
func goldenDiffHint(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	var b strings.Builder
	if len(wl) != len(gl) {
		b.WriteString("line count: want ")
		b.WriteString(strconv.Itoa(len(wl)))
		b.WriteString(", got ")
		b.WriteString(strconv.Itoa(len(gl)))
		b.WriteString("\n")
	}
	n := len(wl)
	if len(gl) < n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		if wl[i] != gl[i] {
			b.WriteString("first differing line ")
			b.WriteString(strconv.Itoa(i))
			b.WriteString(":\n  want ")
			b.WriteString(strconv.Quote(wl[i]))
			b.WriteString("\n   got ")
			b.WriteString(strconv.Quote(gl[i]))
			return b.String()
		}
	}
	if b.Len() == 0 {
		b.WriteString("(content differs only in trailing lines)")
	}
	return b.String()
}

// goldenWidths are the three widths the full-frame corpus is
// captured at: narrow, typical, wide.
var goldenWidths = []int{60, 100, 160}

// TestGolden_Frame pins the whole composed frame at three widths.
// This is the assertion the 202 strings.Contains checks could not
// make: it fails on a column shift, a dropped reset, or a modal
// that grew by two cells.
func TestGolden_Frame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	for _, w := range goldenWidths {
		t.Run("width-"+strconv.Itoa(w), func(t *testing.T) {
			m := goldenModel(t, w, 24)
			assertGolden(t, "frame_w"+strconv.Itoa(w), m.View().Content)
		})
	}
}

// TestGolden_HelpFrame pins the composed frame with the help panel
// open. goldenModel never set helpOpen, so the panel — the one piece
// of chrome that was 38 rows and 81 cells at every terminal size
// (issue #119) — had no golden coverage at all, and goldenWidths
// already brackets the width it overflowed at.
//
// Three shapes are captured. The 24-row cells are the terminal the
// defect was measured on and show the panel paginated to the rows the
// chrome budget left it; the tall cell shows the whole panel in one
// page, which is the layout the paging vocabulary must stay out of;
// and page 2 pins that a page past the first composes a frame like
// any other.
func TestGolden_HelpFrame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	for _, w := range goldenWidths {
		t.Run("width-"+strconv.Itoa(w), func(t *testing.T) {
			m := goldenHelpModel(t, w, 24, 0)
			assertGolden(t, "help_frame_w"+strconv.Itoa(w), m.View().Content)
		})
	}
	t.Run("page-2", func(t *testing.T) {
		m := goldenHelpModel(t, 100, 24, 1)
		assertGolden(t, "help_frame_w100_page2", m.View().Content)
	})
	t.Run("tall", func(t *testing.T) {
		m := goldenHelpModel(t, 100, 60, 0)
		assertGolden(t, "help_frame_w100_h60", m.View().Content)
	})
}

// goldenHelpModel is goldenModel with the help panel opened and
// walked to page, through the same advanceHelp the `?` key runs.
func goldenHelpModel(t *testing.T, w, h, page int) Model {
	t.Helper()
	m := goldenModel(t, w, h)
	m.advanceHelp()
	m.resize()
	for i := 0; i < page; i++ {
		m.advanceHelp()
		m.resize()
	}
	if !m.helpOpen {
		t.Fatalf("the panel closed before reaching page %d at %dx%d", page, w, h)
	}
	m.refreshViewport()
	return m
}

// goldenModel builds a deterministic, fully-seeded model: fixed
// theme, fixed transcript, and no environment-derived chrome.
//
// Two fields are pinned because NewModel seeds them from the
// environment and they both reach the frame. caps comes from
// DetectCapabilities() (COLORTERM / TERM / TERM_PROGRAM and a dozen
// terminal-specific vars), and newlineHint is derived from the same
// TERM_PROGRAM — the footer reads `ctrl+j newline` under a bare
// shell and `alt+enter newline` under VS Code's terminal, which is
// enough to churn every full-frame golden depending on where
// -update was run. Call pinCwd alongside this for the third
// environment input, the working directory in the status header.
func goldenModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := NewModel(Options{
		Agent: &bareAgent{id: "golden"},
		SeedHistory: []Message{
			{Role: RoleUser, Text: "read main.go", Rendered: "read main.go"},
			{Role: RoleAssistant, Text: "Here is the file.", Rendered: "Here is the file."},
			{Role: RoleSystem, Text: "session started", Rendered: "session started"},
		},
	})
	m.styles = goldenStyles()
	m.caps = TerminalCapabilities{}
	m.newlineHint = defaultNewlineHint("")
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model)
}

// --- Model-free renderer goldens -------------------------------
//
// These seven take a Styles, not a *Model, which is what makes
// them cheap to golden. Note that most are NOT width-parameterized
// — renderToolPreview, renderToolPreviewWithResult, renderDiffInline,
// renderCodeInline, renderLatencyBadge and renderReadPreview all
// cap by BYTES per line (perLineByteCap / detailValueByteCap) and
// by line count, never by terminal columns. That is a real finding
// and part of why frames overflow: an inline preview has no idea
// how wide the terminal is. It is recorded here by goldening each
// at its natural signature rather than invented width params;
// widening the signatures is issue #103's business, not this
// harness's.

// goldenDiff is a small, realistic unified diff — two hunks, an
// add, a delete, and context — chosen so the gutter, the bg tints,
// and the syntax highlighter all appear in the captured bytes.
const goldenDiff = `--- a/main.go
+++ b/main.go
@@ -1,6 +1,7 @@
 package main

-import "fmt"
+import (
+	"fmt"
+	"os"
+)

 func main() {
-	fmt.Println("hello")
+	fmt.Fprintln(os.Stdout, "hello")
 }
`

func TestGolden_RenderToolPreview(t *testing.T) {
	pinChromaStyle(t)
	s := goldenStyles()
	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"edit_file", "edit_file", map[string]any{
			"path":       "main.go",
			"old_string": "import \"fmt\"",
			"new_string": "import (\n\t\"fmt\"\n\t\"os\"\n)",
		}},
		{"apply_patch", "apply_patch", map[string]any{"path": "main.go", "patch": goldenDiff}},
		// Read-shaped tools delegate to renderReadPreview; captured
		// here too so a routing change shows up as a golden diff.
		{"read_file", "read_file", map[string]any{"path": "tui/view.go", "offset": 40, "limit": 20}},
		// The empty golden is the point: an unrecognized tool must
		// produce no preview row at all, not a stray blank line.
		{"unknown_tool", "not_a_known_tool", map[string]any{"whatever": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, "tool_preview_"+tc.name, renderToolPreview(tc.tool, tc.args, s))
		})
	}
}

func TestGolden_RenderToolPreviewWithResult(t *testing.T) {
	pinChromaStyle(t)
	s := goldenStyles()
	cases := []struct {
		name     string
		tool     string
		args     map[string]any
		response map[string]any
		err      string
	}{
		{
			name:     "bash_success",
			tool:     "bash",
			args:     map[string]any{"command": "ls /tmp"},
			response: map[string]any{"output": "a.txt\nb.txt\nc.txt"},
		},
		{
			name: "bash_error",
			tool: "bash",
			args: map[string]any{"command": "false"},
			err:  "exit status 1",
		},
		{
			name:     "read_file",
			tool:     "read_file",
			args:     map[string]any{"path": "main.go"},
			response: map[string]any{"content": "package main\n\nfunc main() {}\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderToolPreviewWithResult(tc.tool, tc.args, tc.response, tc.err, s)
			assertGolden(t, "tool_preview_result_"+tc.name, got)
		})
	}
}

func TestGolden_RenderToolDetail(t *testing.T) {
	pinChromaStyle(t)
	s := goldenStyles()
	args := map[string]any{
		"command": "go test ./... -race",
		"timeout": 120,
		"env":     map[string]any{"CI": "true"},
	}
	t.Run("success", func(t *testing.T) {
		resp := map[string]any{"exit_code": 0, "output": "ok\tgithub.com/go-steer/core-tui/tui\t1.2s"}
		assertGolden(t, "tool_detail_success", renderToolDetail(args, resp, "", s))
	})
	t.Run("error", func(t *testing.T) {
		assertGolden(t, "tool_detail_error", renderToolDetail(args, nil, "context deadline exceeded", s))
	})
}

func TestGolden_RenderDiffInline(t *testing.T) {
	pinChromaStyle(t)
	s := goldenStyles()
	t.Run("go_uncapped", func(t *testing.T) {
		assertGolden(t, "diff_inline_go", renderDiffInline(goldenDiff, s, 0, "Go"))
	})
	t.Run("go_capped", func(t *testing.T) {
		assertGolden(t, "diff_inline_go_capped", renderDiffInline(goldenDiff, s, 5, "Go"))
	})
	t.Run("no_lang", func(t *testing.T) {
		assertGolden(t, "diff_inline_nolang", renderDiffInline(goldenDiff, s, 0, ""))
	})
}

func TestGolden_RenderCodeInline(t *testing.T) {
	pinChromaStyle(t)
	s := goldenStyles()
	code := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	t.Run("go_uncapped", func(t *testing.T) {
		assertGolden(t, "code_inline_go", renderCodeInline(code, s, 0, "Go"))
	})
	t.Run("go_capped", func(t *testing.T) {
		assertGolden(t, "code_inline_go_capped", renderCodeInline(code, s, 3, "Go"))
	})
	t.Run("no_lang", func(t *testing.T) {
		assertGolden(t, "code_inline_nolang", renderCodeInline(code, s, 0, ""))
	})
}

func TestGolden_RenderLatencyBadge(t *testing.T) {
	s := goldenStyles()
	for _, ms := range []int64{0, 12, 850, 2400, 95000} {
		t.Run(strconv.Itoa(int(ms))+"ms", func(t *testing.T) {
			assertGolden(t, "latency_badge_"+strconv.Itoa(int(ms)), renderLatencyBadge(ms, s))
		})
	}
}

func TestGolden_RenderReadPreview(t *testing.T) {
	s := goldenStyles()
	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"read_file", "read_file", map[string]any{"path": "internal/tui/view.go"}},
		{"read_many_files", "read_many_files", map[string]any{"paths": []any{"a.go", "b.go", "c.go"}}},
		{"grep", "grep", map[string]any{"pattern": "func Render", "path": "./tui"}},
		{"glob", "glob", map[string]any{"pattern": "**/*_test.go"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, "read_preview_"+tc.name, renderReadPreview(tc.tool, tc.args, s))
		})
	}
}
