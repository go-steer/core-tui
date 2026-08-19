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

// Golden coverage for the permission prompt, both layouts.
//
// The corpus had none, which is the gap docs/design-question-dialogs.md
// §14 Q4 named as the precondition for migrating this modal: it is the
// highest-stakes surface in the product and it currently works, so the
// migration needs something that fails on a changed pixel rather than
// on a changed token. These captures are taken from the pre-migration
// renderers on purpose — they are the BEFORE, and the migration is
// required to leave them byte-identical.
//
// Both layouts are here because they are two different renderers over
// one request (renderPermissionInline draws a gutter block inside the
// chat flow, renderPermissionModal draws a centered frame), they share
// only permissionKeyHint and renderPermissionDetail, and the inline one
// is the DEFAULT — so a corpus that captured only the modal would be
// pinning the layout most operators never see.
//
// Three detail kinds appear across the cells: a diff (the Glamour
// path), a shell command (the bordered-block path) and a plain body
// with no detail at all. The verb is set in the diff cells and empty in
// the shell cell, which is what makes the six-key legend and the
// five-key legend both captured bytes.

package tui

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// goldenPermissionDiff is the payload for the DetailDiff cells. Kept
// separate from goldenDiff so a change to the tool-preview corpus does
// not silently re-diff the permission corpus as well.
//
// Space-indented rather than tab-indented, and short enough to fit a
// 24-row terminal without windowing. Tabs inside a Glamour code fence
// are their own rendering question (issues #216, #217) and windowing is
// captured by TestGolden_PermissionOverlayScrolled; either one in this
// payload would make every cell here a capture of that instead of of
// the prompt's own layout.
const goldenPermissionDiff = `--- a/config.go
+++ b/config.go
@@ -10,4 +10,4 @@
-    return parse(b)
+    return parse(b), nil
 }
`

// goldenPermissionModel is goldenModel with a permission prompt on
// screen under layout.
//
// The request goes in through permissionRequestMsg rather than by
// assigning the field, so the capture includes whatever the arrival
// path does to the viewport — the force-snap to the bottom is part of
// what the inline layout looks like, and a golden that skipped it
// would pin a frame the operator never gets.
func goldenPermissionModel(t *testing.T, w, h int, layout PermissionLayout, req PermissionRequest) model {
	t.Helper()
	m := goldenLiveModel(t, w, h)
	m.opts.PermissionLayout = layout
	out, _ := m.Update(permissionRequestMsg{req: req})
	return out.(model)
}

// TestGolden_PermissionInlineFrame pins the default layout: a gutter
// block in the chat flow, drawn in place of the spinner line, under
// the streamed assistant text it belongs to.
func TestGolden_PermissionInlineFrame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	cases := []struct {
		name string
		req  PermissionRequest
	}{
		{"diff", PermissionRequest{
			ToolName:   "edit_file",
			Detail:     goldenPermissionDiff,
			DetailKind: DetailDiff,
			Verb:       "edit",
		}},
		{"shell", PermissionRequest{
			ToolName:   "bash",
			Detail:     "rm -rf ./build && go build ./...",
			DetailKind: DetailShell,
		}},
		{"bare", PermissionRequest{
			ToolName: "fetch_url",
			Source:   "researcher",
		}},
	}
	for _, tc := range cases {
		for _, w := range goldenWidths {
			t.Run(tc.name+"/width-"+strconv.Itoa(w), func(t *testing.T) {
				m := goldenPermissionModel(t, w, 24, PermissionInline, tc.req)
				assertGolden(t, "permission_inline_"+tc.name+"_w"+strconv.Itoa(w), m.View().Content)
			})
		}
	}
}

// TestGolden_PermissionOverlayFrame pins the centered layout, which is
// the one with chrome to get wrong: a title rule sized against the
// tool name, a windowed body, and a footer legend that wraps on narrow
// terminals and gains a scroll hint when the body overflows.
func TestGolden_PermissionOverlayFrame(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	cases := []struct {
		name string
		req  PermissionRequest
	}{
		{"diff", PermissionRequest{
			ToolName:   "edit_file",
			Detail:     goldenPermissionDiff,
			DetailKind: DetailDiff,
			Verb:       "edit",
		}},
		{"shell", PermissionRequest{
			ToolName:   "bash",
			Detail:     "rm -rf ./build && go build ./...",
			DetailKind: DetailShell,
		}},
		{"bare", PermissionRequest{
			ToolName: "fetch_url",
			Source:   "researcher",
		}},
	}
	for _, tc := range cases {
		for _, w := range goldenWidths {
			t.Run(tc.name+"/width-"+strconv.Itoa(w), func(t *testing.T) {
				m := goldenPermissionModel(t, w, 24, PermissionOverlay, tc.req)
				assertGolden(t, "permission_overlay_"+tc.name+"_w"+strconv.Itoa(w), m.View().Content)
			})
		}
	}
}

// TestGolden_PermissionOverlayScrolled pins the windowed body and the
// scroll hint the footer grows when it overflows — the state a 200-line
// diff puts the modal in, and the one where the arithmetic that decides
// how many rows the body gets is actually load-bearing.
func TestGolden_PermissionOverlayScrolled(t *testing.T) {
	pinChromaStyle(t)
	pinCwd(t)
	long := ""
	for i := range 60 {
		long += "+\tline " + strconv.Itoa(i) + " of a diff that does not fit\n"
	}
	req := PermissionRequest{
		ToolName:   "edit_file",
		Detail:     "--- a/big.go\n+++ b/big.go\n@@ -1,60 +1,60 @@\n" + long,
		DetailKind: DetailDiff,
		Verb:       "edit",
	}
	m := goldenPermissionModel(t, 100, 24, PermissionOverlay, req)
	assertGolden(t, "permission_overlay_scrolled_top", m.View().Content)

	// And the same modal a few rows down, which is the frame the arrow
	// keys produce and the only one that proves the window moves.
	out, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = out.(model)
	out, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = out.(model)
	assertGolden(t, "permission_overlay_scrolled_down", m.View().Content)
}
