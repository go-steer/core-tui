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
//     background-detected theme. A later change to defaultTheme
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
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// updateGolden regenerates the corpus instead of comparing to it.
var updateGolden = flag.Bool("update", false, "regenerate the golden files under tui/testdata")

// goldenChromaStyle is the chroma style name every golden that can
// reach the syntax highlighter is pinned to. Named explicitly here
// and again in goldenTheme rather than inherited from whatever the
// builtin themes pick, so a production retheme is not a corpus-wide
// rewrite.
const goldenChromaStyle = "github"

// goldenTheme is THE theme for the corpus: a full, explicit Theme
// literal with no reference to defaultTheme or any provider
// palette. Every field is spelled out on purpose — a new Theme
// field added upstream should show up here as a deliberate choice
// (and as a reviewable golden churn), not silently inherit.
func goldenTheme() Theme {
	return Theme{
		Name:         "golden",
		Primary:      lipgloss.Color("#8B5CF6"),
		Secondary:    lipgloss.Color("#EC4899"),
		Accent:       lipgloss.Color("#8B5CF6"),
		Success:      lipgloss.Color("#5FD787"),
		Warning:      lipgloss.Color("#FFD75F"),
		Error:        lipgloss.Color("#FF5F5F"),
		Info:         lipgloss.Color("#A8A8A8"),
		FgBase:       lipgloss.Color("#D0D0D0"),
		FgMuted:      lipgloss.Color("#9A9A9A"),
		FgSubtle:     lipgloss.Color("#6C6C6C"),
		BgBase:       nil,
		BgElevated:   lipgloss.Color("#1E1E1E"),
		BgOverlay:    lipgloss.Color("#2A2A2A"),
		BorderActive: lipgloss.Color("#8B5CF6"),
		BorderQuiet:  lipgloss.Color("#3A3A3A"),
		// Pins the corpus to one chroma style. The builtins each
		// name their own now (Theme.ChromaStyleName), and the
		// normalizer's fallback is free to move; neither may reach
		// the seven chroma-bearing goldens.
		ChromaStyleName: goldenChromaStyle,
		// OnPrimary is deliberately NOT spelled out: it is the
		// token the normalizer derives, and leaving it zero here is
		// what makes the corpus exercise that derivation on the one
		// path (a bare Theme literal) that never sees defaultTheme.
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

// pinChromaStyle asserts the corpus theme still names the pinned
// chroma style, and is called by every golden that can reach the
// highlighter.
//
// It used to swap a package-level chromaSyntaxStyle var and restore
// it on cleanup. That var is gone: highlighting is theme-driven now
// (Theme.ChromaStyleName), the per-line cache key carries the style
// name so two styles can no longer read each other's entries, and
// the pin therefore lives in goldenTheme itself. What remains worth
// checking is that it stays there — silently dropping the field
// from goldenTheme would re-point seven goldens at whatever the
// normalizer defaults to, and the diff would look like a rendering
// change rather than a lost pin.
func pinChromaStyle(t *testing.T) {
	t.Helper()
	if got := goldenTheme().ChromaStyleName; got != goldenChromaStyle {
		t.Fatalf("goldenTheme().ChromaStyleName = %q, want %q — the corpus pin was dropped", got, goldenChromaStyle)
	}
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

// TestGolden_ModalFrame pins the composed frame with a modal open.
//
// The corpus had no capture of a modal at all before issue #142,
// which is a gap: modal sizing is the one place in the layout where a
// change is meant to bite only where the modal did not fit, and
// "meant to" is not a test.
//
// All three captures are 24 rows, well above modalFullscreenBelow, so
// the fullscreen degradation must not appear in any of them. The
// widths do different jobs. At 60 the theme descriptions wrap hard
// enough that the modal composed 26 rows in a 24-row terminal, so
// clipFrame took the footer rule and the key hint off the bottom —
// issue #142's defect at a perfectly normal HEIGHT, reached through
// width instead, and this capture is where it is visibly fixed. At 100
// and 160 the modal has room, and these were the no-regression control
// that matched main byte for byte.
//
// They no longer do, and #199 is why. The twelve built-in themes are
// exactly the window the height regime lends the picker at 24 rows, so
// the list is never windowed, so its rows are never cut to the body
// column and six of the descriptions wrap. That body was two rows
// inside the terminal; the box edge takes two rows; the spacing above
// the footer rule and under the title is what fitModalContent gives up
// to pay for it. The control these captures provide is now the shape
// of the loss rather than the absence of one — the shedding order is
// visible in them, and it is the documented order.
//
// The theme picker is the subject because it needs no host capability
// to populate itself and it is the tallest chrome of the four
// (modalChromeRows+1 for the filter row, issue #117).
func TestGolden_ModalFrame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	for _, w := range goldenWidths {
		t.Run("width-"+strconv.Itoa(w), func(t *testing.T) {
			m := goldenModel(t, w, 24)
			m.overlayStack.Open(newThemePickerDialog(m.themeName))
			assertGolden(t, "modal_frame_w"+strconv.Itoa(w), m.View().Content)
		})
	}
}

// TestGolden_LiveFrame pins the composed frame mid-LiveAgent stretch
// (issue #135).
//
// The rest of the frame corpus is captured from an idle model, which
// by construction cannot notice the in-progress block going missing —
// an idle model has no in-progress block to lose. This is the capture
// where the streamed prose, the spinner verb line and the elapsed
// suffix are all on screen at once, so a future gate change that
// swallows any of them shows up as a diff rather than as silence.
func TestGolden_LiveFrame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	for _, w := range goldenWidths {
		t.Run("width-"+strconv.Itoa(w), func(t *testing.T) {
			m := goldenLiveModel(t, w, 24)
			assertGolden(t, "live_frame_w"+strconv.Itoa(w), m.View().Content)
		})
	}
}

// goldenLiveModel is goldenModel put mid-stretch: liveMode on, one
// partial chunk accumulated, spinner running.
//
// The elapsed readout is time-dependent, so it is pinned through the
// injected clock (Model.now, issue #111) rather than suppressed —
// forty-two seconds is past turnElapsedFloor, which means the suffix
// is actually in the captured bytes. Left on the wall clock this
// corpus would re-diff on every run.
func goldenLiveModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := goldenModel(t, w, h)
	m.liveMode = true
	out, _ := m.Update(streamChunkMsg{
		gen:     m.sessionGen,
		text:    "Reading the package and working out what changed.",
		partial: true,
	})
	m = out.(Model)
	start := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	m.turnStarted = start
	m.now = func() time.Time { return start.Add(42 * time.Second) }
	m.refreshViewport()
	return m
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
// detectCapabilities() (COLORTERM / TERM / TERM_PROGRAM and a dozen
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
	m.caps = terminalCapabilities{}
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

// goldenHostile is the adversarial counterpart to goldenDiff: one
// payload carrying every class of byte the #108 escape set has an
// opinion about. Written with Go escapes rather than raw bytes so
// the fixture is readable in a diff and survives a copy-paste.
//
//   - `ESC[2J`          erase display
//   - `ESC]0;…BEL`      set window title
//   - `ESC[31m…ESC[0m`  SGR color (stripped, not escaped)
//   - `CR`              cursor to column 0, mid-row
//   - `BS`              overwrite the character to the left
//   - `U+009B`          the 8-bit CSI introducer, as a UTF-8 rune
//   - TAB               the exemption, and the reason the rest of
//     the corpus does not churn
const goldenHostile = "clean line\n" +
	"\x1b[2Jerased\n" +
	"\x1b]0;pwned\x07titled\n" +
	"\x1b[31mcolored\x1b[0m\n" +
	"spoofed\rSUCCESS\n" +
	"typo\x08\x08fixed\n" +
	"\u009b31mfake-color\n" +
	"\tindented with a tab\n"

// TestGolden_HostileContent pins the RENDERED form of hostile
// content — what an operator's terminal actually receives when a
// tool hands the TUI a payload that is trying to draw on the screen.
//
// The goldens are the readable half of the assertion; the loop that
// follows is the load-bearing half, because a golden can only prove
// the bytes did not change, not that they are safe. assertRenderSafe
// (sanitize_test.go) proves the second thing, so regenerating this
// corpus cannot quietly bless an escape sequence.
func TestGolden_HostileContent(t *testing.T) {
	pinChromaStyle(t)
	s := goldenStyles()

	// The diff form: every hostile line arrives as an added line, so
	// each one renders inside a background-tinted span. That is the
	// worst case — a CR or a cursor move with the tint still active
	// bleeds the color across whatever it lands on.
	diff := "--- a/hostile.txt\n+++ b/hostile.txt\n@@ -1,0 +1,8 @@\n"
	for _, line := range strings.Split(strings.TrimRight(goldenHostile, "\n"), "\n") {
		diff += "+" + line + "\n"
	}

	// CRLF gets its own fixture: the bug is that the CR survives to
	// end-of-line inside the tinted span, which only shows up when
	// the lines are actually CRLF-separated.
	crlfDiff := "--- a/crlf.go\n+++ b/crlf.go\r\n@@ -1,2 +1,2 @@\r\n-old\r\n+new\r\n context\r\n"

	cases := []struct {
		name string
		got  string
	}{
		{"diff_inline", renderDiffInline(diff, s, 0, "")},
		{"diff_inline_go", renderDiffInline(diff, s, 0, "Go")},
		{"diff_inline_crlf", renderDiffInline(crlfDiff, s, 0, "Go")},
		{"code_inline", renderCodeInline(goldenHostile, s, 0, "")},
		{"result_error", renderToolPreviewWithResult(
			"bash", map[string]any{"command": "\x1b[2Jrm -rf /"}, nil,
			"exit 1: \x1b]0;pwned\x07\rALL CLEAR", s)},
		{"bash_result", renderToolPreviewWithResult(
			"bash", map[string]any{"command": "cat hostile.txt"},
			map[string]any{"stdout": goldenHostile, "stderr": "\x1b[2Jwarning\rOK", "exit_code": 2}, "", s)},
		{"tool_detail", renderToolDetail(
			map[string]any{"path": "hostile.txt", "flag": "\u009b31m"},
			map[string]any{"content": goldenHostile}, "", s)},
		{"tool_detail_error", renderToolDetail(
			map[string]any{"path": "hostile.txt"}, nil,
			"open hostile.txt: \x1b[2Jno such file\rOK", s)},
		// A line of tabs is under the byte cap and four times over
		// the cell cap — the #107 accounting fix, captured.
		{"tab_overflow", renderCodeInline(strings.Repeat("\t", 300), s, 0, "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, "hostile_"+tc.name, tc.got)
			assertRenderSafe(t, tc.name, tc.got)
		})
	}
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
