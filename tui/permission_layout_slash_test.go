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
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// permLayoutModel is a sized model for the /permissions layout tests.
// bareAgent has no PermissionController, which is the point: the
// layout is a TUI setting and must not need one (R-PERM-1a).
func permLayoutModel(opts Options) *model {
	if opts.Agent == nil {
		opts.Agent = &bareAgent{id: "a"}
	}
	m := newModel(opts)
	m.width, m.height = 100, 40
	m.resize()
	return m
}

// typeSlash submits line through the Enter handler, the same path an
// operator's keystroke takes.
func typeSlash(m *model, line string) (*model, tea.Cmd) {
	m.input.SetValue(line)
	return pressKey(m, tea.Key{Code: tea.KeyEnter})
}

// A bare subcommand toggles, both ways round, and says where it landed
// in /mouse's style.
func TestPermissionsLayoutSlash_Toggles(t *testing.T) {
	m := permLayoutModel(Options{})

	m, _ = typeSlash(m, "/permissions layout")
	if m.permLayout != PermissionOverlay {
		t.Fatalf("after one toggle permLayout = %d, want overlay", m.permLayout)
	}
	if got := lastText(m); got != "/permissions: prompt layout overlay" {
		t.Errorf("confirmation row = %q", got)
	}

	m, _ = typeSlash(m, "/permissions layout")
	if m.permLayout != PermissionInline {
		t.Fatalf("after two toggles permLayout = %d, want inline", m.permLayout)
	}
	if got := lastText(m); got != "/permissions: prompt layout inline" {
		t.Errorf("confirmation row = %q", got)
	}
	if m.input.Value() != "" {
		t.Errorf("input not cleared; got %q", m.input.Value())
	}
}

// A named layout sets rather than toggles, so repeating it is a no-op
// on the state, and the words are case-insensitive like the command.
func TestPermissionsLayoutSlash_ExplicitSet(t *testing.T) {
	m := permLayoutModel(Options{})
	for _, tc := range []struct {
		line string
		want PermissionLayout
	}{
		{"/permissions layout overlay", PermissionOverlay},
		{"/permissions layout overlay", PermissionOverlay},
		{"/permissions layout inline", PermissionInline},
		{"/perms LAYOUT Overlay", PermissionOverlay}, // alias + case fold
	} {
		m, _ = typeSlash(m, tc.line)
		if m.permLayout != tc.want {
			t.Errorf("%q: permLayout = %d, want %d", tc.line, m.permLayout, tc.want)
		}
	}
}

// An unknown word is a usage hint on a system row, not an error, and
// leaves the layout alone.
func TestPermissionsLayoutSlash_UnknownWordIsUsage(t *testing.T) {
	var calls int
	m := permLayoutModel(Options{
		PermissionLayout:        PermissionOverlay,
		PersistPermissionLayout: func(PermissionLayout) error { calls++; return nil },
	})
	m, cmd := typeSlash(m, "/permissions layout sideways")
	if cmd != nil {
		t.Error("a rejected word still produced a Cmd")
	}

	if m.permLayout != PermissionOverlay {
		t.Errorf("unknown word changed the layout to %d", m.permLayout)
	}
	msgs := m.history.Snapshot()
	last := msgs[len(msgs)-1]
	if last.Role != RoleSystem || !strings.Contains(last.Text, "usage: /permissions layout [inline|overlay]") {
		t.Errorf("last row = %v %q, want a system usage row", last.Role, last.Text)
	}
	if calls != 0 {
		t.Errorf("persist ran %d time(s) for a rejected word", calls)
	}
}

// The hook gets the new layout, off the Update goroutine.
func TestPermissionsLayoutSlash_PersistsTheNewLayout(t *testing.T) {
	var persisted []PermissionLayout
	m := permLayoutModel(Options{
		PersistPermissionLayout: func(l PermissionLayout) error {
			persisted = append(persisted, l)
			return nil
		},
	})
	m, cmd := typeSlash(m, "/permissions layout")
	if len(persisted) != 0 {
		t.Fatalf("PersistPermissionLayout ran on the Update goroutine: %v", persisted)
	}
	var sawPersist bool
	for _, msg := range runCmds(t, cmd) {
		if done, ok := msg.(persistDoneMsg); ok && done.what == "/permissions layout" {
			sawPersist = true
		}
	}
	if !sawPersist {
		t.Fatal("no persistDoneMsg for /permissions layout")
	}
	if len(persisted) != 1 || persisted[0] != PermissionOverlay {
		t.Errorf("PersistPermissionLayout got %v, want [overlay]", persisted)
	}

	_, cmd = typeSlash(m, "/permissions layout inline")
	runCmds(t, cmd)
	if len(persisted) != 2 || persisted[1] != PermissionInline {
		t.Errorf("PersistPermissionLayout got %v, want [overlay inline]", persisted)
	}
}

// A failed write surfaces as an error row, and the in-session switch
// stands — the same contract /mouse keeps.
func TestPermissionsLayoutSlash_PersistErrorSurfacedChangeKept(t *testing.T) {
	m := permLayoutModel(Options{
		PersistPermissionLayout: func(PermissionLayout) error { return errors.New("disk full") },
	})
	m, cmd := typeSlash(m, "/permissions layout overlay")
	for _, msg := range runCmds(t, cmd) {
		out, _ := m.Update(msg)
		m = out.(*model)
	}
	msgs := m.history.Snapshot()
	last := msgs[len(msgs)-1]
	if last.Role != RoleError || !strings.Contains(last.Text, "/permissions layout: persist failed: disk full") {
		t.Errorf("last row = %v %q, want the persist error", last.Role, last.Text)
	}
	if m.permLayout != PermissionOverlay {
		t.Errorf("persist failure rolled the layout back to %d", m.permLayout)
	}
}

// No hook: the switch is session-only and there is nothing to run.
func TestPermissionsLayoutSlash_NilPersistIsSessionOnly(t *testing.T) {
	m := permLayoutModel(Options{})
	m, cmd := typeSlash(m, "/permissions layout overlay")
	if cmd != nil {
		t.Errorf("nil PersistPermissionLayout still produced a Cmd")
	}
	if m.permLayout != PermissionOverlay {
		t.Errorf("permLayout = %d, want overlay", m.permLayout)
	}
	// The host's Options are not rewritten: the seed stays the seed.
	if m.opts.PermissionLayout != PermissionInline {
		t.Errorf("opts.PermissionLayout mutated to %d", m.opts.PermissionLayout)
	}
}

// The next prompt reads the runtime value, in both directions — the
// seed from Options is only where the session starts.
func TestPermissionsLayoutSlash_NextPromptUsesRuntimeLayout(t *testing.T) {
	req := PermissionRequest{ToolName: "bash", Verb: "run", Detail: "ls"}
	for _, tc := range []struct {
		seed   PermissionLayout
		line   string
		inline bool
	}{
		{PermissionInline, "/permissions layout overlay", false},
		{PermissionOverlay, "/permissions layout inline", true},
	} {
		m := permLayoutModel(Options{PermissionLayout: tc.seed})
		m, _ = typeSlash(m, tc.line)
		out, _ := m.Update(permissionRequestMsg{req: req})
		m = out.(*model)
		q := m.openPermission()
		if q == nil {
			t.Fatalf("%q: no permission prompt opened", tc.line)
		}
		if q.inline != tc.inline {
			t.Errorf("%q: prompt inline = %v, want %v", tc.line, q.inline, tc.inline)
		}
	}
}

// A prompt already open keeps the layout it opened with; the switch
// applies from the next one. Normally unreachable (the prompt owns the
// keys), but the behaviour is defined, so it is pinned.
func TestPermissionsLayoutSlash_OpenPromptKeepsItsLayout(t *testing.T) {
	m := permLayoutModel(Options{})
	out, _ := m.Update(permissionRequestMsg{req: PermissionRequest{ToolName: "bash"}})
	m = out.(*model)
	if q := m.openPermission(); q == nil || !q.inline {
		t.Fatal("setup: want an inline prompt open")
	}
	m.permissionLayoutSlash("overlay")
	if !m.openPermission().inline {
		t.Error("the open prompt changed layout under the operator")
	}
	if m.permLayout != PermissionOverlay {
		t.Errorf("permLayout = %d, want overlay for the next prompt", m.permLayout)
	}
}

// With a PermissionController wired, the subcommand still never asks
// the host for anything, and bare /permissions (or any other argument)
// keeps opening the approval log.
func TestPermissionsLayoutSlash_BareCommandUnchanged(t *testing.T) {
	m := permLayoutModel(Options{Agent: &slowAgent{id: "slow"}})

	m, cmd := typeSlash(m, "/permissions layout")
	if cmd != nil {
		t.Error("layout subcommand issued a host Cmd")
	}
	if m.permLayout != PermissionOverlay {
		t.Errorf("permLayout = %d, want overlay", m.permLayout)
	}

	for _, line := range []string{"/permissions", "/permissions something-else"} {
		m, cmd = typeSlash(m, line)
		if !strings.Contains(lastText(m), "reading the session approval log") {
			t.Errorf("%q: last row = %q, want the approval-log read", line, lastText(m))
		}
		if cmd == nil {
			t.Errorf("%q: no SessionApprovals Cmd", line)
		}
	}
}

// Mid-turn: bare /permissions queues (the review is a host round trip),
// but the layout subcommand is presentation only and dispatches now.
func TestPermissionsLayoutSlash_MidTurn(t *testing.T) {
	cases := []struct {
		line string
		want midTurnDisposition
	}{
		{"/permissions layout", midTurnDispatch},
		{"/permissions layout overlay", midTurnDispatch},
		{"/perms layout inline", midTurnDispatch},
		{"/permissions", midTurnQueue},
		{"/permissions layouts", midTurnQueue},
	}
	for _, tc := range cases {
		if got := midTurnSlashDisposition(tc.line); got != tc.want {
			t.Errorf("midTurnSlashDisposition(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}

	m := permLayoutModel(Options{})
	m.state = stateStreaming
	m, _ = typeSlash(m, "/permissions layout")
	if len(m.queue) != 0 {
		t.Errorf("/permissions layout was queued mid-turn; queue has %d entries", len(m.queue))
	}
	if m.permLayout != PermissionOverlay {
		t.Errorf("mid-turn switch did not apply; permLayout = %d", m.permLayout)
	}
}
