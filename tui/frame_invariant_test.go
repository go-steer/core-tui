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

// Frame-invariant grid (issue #101). `package tui` had 202
// strings.Contains assertions and five tests that called View() at
// all — a combination that proves a token is present somewhere in
// the output and says nothing about where it landed or how wide it
// is. This file is the machine-checked definition of a correct
// frame:
//
//	1. every rendered line's ansi.StringWidth is <= m.width
//	2. the frame's total line count is <= m.height
//
// Both hold across a width x height matrix and across every UI
// state that composes chrome differently — base chat, permission
// overlay, elicit modal, help panel, and both status layouts.
//
// The invariants are enforced by the clipping post-pass at the end
// of View() (issue #102). Without that post-pass 83 of this grid's
// 200 cells fail: 82 columns of output in a 40-column terminal, 62
// rows in a 24-row one, and 10 rows at height 4 — and Bubble Tea
// drops the TOP lines when a frame is too tall, so the failure mode
// is the status header silently vanishing.

package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// frameWidths / frameHeights are the matrix axes. The realistic
// sizes (80x24, 100x50, 120x50) are what operators actually run;
// the hostile ones (40 cols, 4 rows) are where the composition
// arithmetic in resize() runs out of budget and every join starts
// overflowing.
var (
	frameWidths  = []int{40, 80, 100, 120, 200}
	frameHeights = []int{4, 10, 24, 50}
)

// frameState is one named UI configuration to drive the grid over.
// setup receives a model already sized to (w, h) and returns the
// model whose View() gets measured.
type frameState struct {
	name  string
	setup func(t *testing.T, m Model, w, h int) Model
}

// frameStates enumerates the UI states the invariants must hold in.
// Each one composes chrome through a different path in View():
// the plain JoinVertical stack, the JoinHorizontal sidebar split,
// and the three lipgloss.Place overlay arms.
func frameStates() []frameState {
	return []frameState{
		{
			name: "base-chat-empty",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				return m
			},
		},
		{
			name: "base-chat-transcript",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				return withHostileTranscript(m)
			},
		},
		{
			name: "permission-modal",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				out, _ := m.Update(permissionRequestMsg{req: PermissionRequest{
					Kind:     PermissionKindBash,
					ToolName: "bash",
					Verb:     "rm",
					Detail:   "rm -rf /tmp/a-really-quite-long-path/that/keeps/going/well/past/any/sensible/terminal/width",
				}})
				return out.(Model)
			},
		},
		{
			name: "elicit-modal",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				out, _ := m.Update(elicitRequestMsg{
					serverName: "an-mcp-server-with-a-long-name",
					req: ElicitRequest{
						Mode:        ElicitFormMode,
						Title:       "Confirm the deployment target for this rollout",
						Description: "The server needs a project and a region before it can continue the rollout.",
						Fields: []ElicitField{
							{Name: "project", Description: "GCP project id", Type: ElicitFieldString, Required: true},
							{Name: "region", Description: "Compute region", Type: ElicitFieldEnum, EnumChoices: []string{"us-central1", "europe-west4"}},
						},
					},
				})
				return out.(Model)
			},
		},
		{
			name: "help-panel",
			setup: func(_ *testing.T, m Model, _, _ int) Model {
				m = withHostileTranscript(m)
				m.helpOpen = true
				m.resize()
				m.refreshViewport()
				return m
			},
		},
	}
}

// withHostileTranscript seeds a transcript whose content is chosen
// to stress the width budget: an unbreakable token far longer than
// any terminal, a wide fenced code block, and enough rows to fill
// the tallest viewport in the matrix.
func withHostileTranscript(m Model) Model {
	m.history.Append(Message{
		Role:     RoleUser,
		Text:     "please read the file",
		Rendered: "please read the file",
	})
	long := strings.Repeat("wide-unbreakable-token-", 12)
	m.history.Append(Message{
		Role:     RoleAssistant,
		Text:     long,
		Rendered: long,
	})
	code := "func main() { fmt.Println(\"" + strings.Repeat("x", 180) + "\") }"
	m.history.Append(Message{
		Role:     RoleAssistant,
		Text:     code,
		Rendered: code,
	})
	for i := 0; i < 60; i++ {
		line := "transcript row " + strconv.Itoa(i) + " " + strings.Repeat("filler ", 20)
		m.history.Append(Message{Role: RoleAssistant, Text: line, Rendered: line})
	}
	m.refreshViewport()
	return m
}

// newFrameModel builds a model sized to (w, h) in the requested
// status layout. The theme is pinned so a palette change can't turn
// this into a flaky test through some width-carrying token.
func newFrameModel(layout StatusLayout, w, h int) Model {
	m := NewModel(Options{
		Agent:            &bareAgent{id: "frame"},
		StatusLayout:     layout,
		PermissionLayout: PermissionOverlay,
	})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model)
}

// assertFrameFits is the invariant itself. Kept deliberately small
// and free of Contains-style assertions: it measures geometry only.
func assertFrameFits(t *testing.T, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	// A frame ending in a newline yields a trailing empty element
	// that costs no terminal row.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) > h {
		t.Errorf("frame is %d lines at height %d — Bubble Tea drops the TOP %d, so the header vanishes silently",
			len(lines), h, len(lines)-h)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > w {
			t.Errorf("line %d is %d cols at width %d (overflow %d): %q",
				i, got, w, got-w, ansi.Truncate(line, w+20, "…"))
		}
	}
}

// TestFrameInvariants_Grid is the core of issue #101: every cell of
// the width x height x state x layout matrix must produce a frame
// that fits the terminal in both dimensions.
func TestFrameInvariants_Grid(t *testing.T) {
	layouts := []struct {
		name   string
		layout StatusLayout
	}{
		{"header", StatusHeader},
		{"sidebar", StatusSidebar},
	}
	for _, lay := range layouts {
		for _, st := range frameStates() {
			for _, w := range frameWidths {
				for _, h := range frameHeights {
					name := lay.name + "/" + st.name + "/" +
						strconv.Itoa(w) + "x" + strconv.Itoa(h)
					t.Run(name, func(t *testing.T) {
						m := newFrameModel(lay.layout, w, h)
						m = st.setup(t, m, w, h)
						assertFrameFits(t, m.View().Content, w, h)
					})
				}
			}
		}
	}
}

// TestFrameInvariants_ResizeSequence walks a single model through a
// sequence of resizes rather than building a fresh one per size.
// State that survives a resize (viewport offset, cached renders,
// the textarea's own width) is exactly where a clamp regression
// would hide from the fresh-model grid above.
func TestFrameInvariants_ResizeSequence(t *testing.T) {
	m := newFrameModel(StatusSidebar, 120, 40)
	m = withHostileTranscript(m)

	seq := []struct{ w, h int }{
		{120, 40}, {80, 24}, {40, 4}, {200, 50}, {100, 10}, {41, 5}, {120, 40},
	}
	for _, s := range seq {
		out, _ := m.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
		m = out.(Model)
		t.Run(strconv.Itoa(s.w)+"x"+strconv.Itoa(s.h), func(t *testing.T) {
			assertFrameFits(t, m.View().Content, s.w, s.h)
		})
	}
}

// TestFrameInvariants_ZeroSize pins the degenerate cases the clip
// post-pass has to guard: an unsized model renders empty, and a
// model whose Update never delivered a WindowSizeMsg must not panic
// through the truncation path.
func TestFrameInvariants_ZeroSize(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "zero"}})
	if got := m.View().Content; got != "" {
		t.Errorf("unsized model should render empty, got %q", got)
	}
}
