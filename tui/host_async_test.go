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

// Tests for issue #114 — no host capability method may run on the
// Update goroutine, and View() may not call the host at all.
//
// The instrument is slowAgent: every capability method it implements
// sleeps slowHostDelay before answering. Any call site still running
// inline shows up as an Update or View that takes that long, which is
// two orders of magnitude past the budget these tests assert.

package tui

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// slowHostDelay is what a wedged host feels like. Long enough that an
// inline call can't hide inside the assertion budget, short enough
// that the suite stays quick.
const slowHostDelay = 500 * time.Millisecond

// nonBlockingBudget is the ceiling for one Update or View call. An
// order of magnitude under slowHostDelay, so a call site that is
// still inline cannot squeeze under it, and comfortably above what a
// full sidebar-layout paint costs under -race (~15ms on CI) so the
// assertion isn't measuring glamour instead of the host.
const nonBlockingBudget = slowHostDelay / 10

// slowAgent implements every capability whose call sites issue #114
// moved off the Update loop, and sleeps in all of them.
type slowAgent struct {
	id string

	models   []ModelInfo
	sessions []SessionInfo
	subs     []SubagentInfo
	specs    []SlashCommandSpec

	nextAgent Agent
	switchErr error

	// calls counts every capability method entered, so a test can
	// prove a call happened (or didn't) without racing the sleep.
	calls atomic.Int64
}

func (a *slowAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}

func (a *slowAgent) sleep() {
	a.calls.Add(1)
	time.Sleep(slowHostDelay)
}

func (a *slowAgent) AvailableModels() []ModelInfo {
	a.sleep()
	return a.models
}

func (a *slowAgent) SwitchModel(id string) (Agent, error) {
	a.sleep()
	if a.switchErr != nil {
		return nil, a.switchErr
	}
	if a.nextAgent != nil {
		return a.nextAgent, nil
	}
	return &slowAgent{id: id}, nil
}

func (a *slowAgent) Sessions() []SessionInfo {
	a.sleep()
	return a.sessions
}

func (a *slowAgent) SwitchToSession(id string) (SwitchTarget, error) {
	a.sleep()
	if a.switchErr != nil {
		return SwitchTarget{}, a.switchErr
	}
	return SwitchTarget{Agent: &bareAgent{id: id}, Note: "Attached to " + id}, nil
}

func (a *slowAgent) Subagents() []SubagentInfo {
	a.sleep()
	return a.subs
}

func (a *slowAgent) SlashCommands() []SlashCommandSpec {
	a.sleep()
	return a.specs
}

func (a *slowAgent) InvokeSlash(context.Context, string, string) (SlashResult, error) {
	return SlashResult{}, nil
}

// readyModelPicker builds a model picker with the host list already
// snapshotted — the test-side shorthand for Open + the
// modelsLoadedMsg round trip, for tests about what happens AFTER the
// list lands.
func readyModelPicker(m *Model) *modelPickerDialog {
	d := newModelPickerDialog()
	if sw, ok := m.opts.Agent.(ModelSwapper); ok {
		d.applyModels(sw.AvailableModels(), m.displayModelName())
	}
	return d
}

// readySessionPicker is readyModelPicker's session-list twin.
func readySessionPicker(m *Model) *sessionPickerDialog {
	d := newSessionPickerDialog()
	if sw, ok := m.opts.Agent.(SessionSwitcher); ok {
		d.applySessions(sw.Sessions())
	}
	return d
}

// mustBeFast fails when fn takes longer than nonBlockingBudget.
func mustBeFast(t *testing.T, what string, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	if elapsed := time.Since(start); elapsed > nonBlockingBudget {
		t.Fatalf("%s took %s, want under %s — a host call is still running inline",
			what, elapsed, nonBlockingBudget)
	}
}

// pressKey drives one keystroke through Update the way bubble-tea
// does, returning the new model and the Cmd.
func pressKey(m Model, key tea.Key) (Model, tea.Cmd) {
	out, cmd := m.Update(tea.KeyPressMsg(key))
	return out.(Model), cmd
}

// TestView_NeverCallsHost — View() must return promptly even when
// every capability it used to read is wedged. Covers the three
// documented violations: the sidebar's Subagents(), the model
// picker's AvailableModels(), and the session picker's Sessions().
func TestView_NeverCallsHost(t *testing.T) {
	agent := &slowAgent{
		id:       "slow",
		models:   []ModelInfo{{ID: "m1"}, {ID: "m2"}},
		sessions: []SessionInfo{{ID: "s1", Current: true}, {ID: "s2"}},
		subs:     []SubagentInfo{{Name: "probe", Status: "running"}},
	}
	m := NewModel(Options{Agent: agent, StatusLayout: StatusSidebar})
	m.width, m.height = 120, 40
	m.viewport.SetWidth(80)
	m.resize()

	// Warm the render caches so the timings below measure the host
	// question, not glamour's first pass.
	_ = m.View()

	before := agent.calls.Load()
	mustBeFast(t, "View with no dialog", func() { _ = m.View() })

	m.overlayStack.Open(newModelPickerDialog())
	mustBeFast(t, "View with a loading model picker", func() { _ = m.View() })
	m.overlayStack.Close(modelPickerDialogID)

	m.overlayStack.Open(newSessionPickerDialog())
	mustBeFast(t, "View with a loading session picker", func() { _ = m.View() })
	m.overlayStack.Close(sessionPickerDialogID)

	if got := agent.calls.Load(); got != before {
		t.Errorf("View() made %d host call(s); the contract is zero", got-before)
	}

	// Snapshot installed: still zero host calls, now with real rows.
	picker := readyModelPicker(&m)
	before = agent.calls.Load()
	m.overlayStack.Open(picker)
	mustBeFast(t, "View with a populated model picker", func() { _ = m.View() })
	if got := agent.calls.Load(); got != before {
		t.Errorf("a populated picker made %d host call(s) from View()", got-before)
	}
}

// TestUpdate_ModelPickerOpensWithoutBlocking — Ctrl+G must return
// immediately with a Cmd that carries the AvailableModels() pull.
func TestUpdate_ModelPickerOpensWithoutBlocking(t *testing.T) {
	agent := &slowAgent{id: "slow", models: []ModelInfo{{ID: "m1"}, {ID: "m2"}}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	var cmd tea.Cmd
	mustBeFast(t, "ctrl+g", func() {
		m, cmd = pressKey(m, tea.Key{Code: 'g', Mod: tea.ModCtrl})
	})
	if !m.overlayStack.HasID(modelPickerDialogID) {
		t.Fatalf("ctrl+g did not open the model picker")
	}
	d := m.overlayStack.Get(modelPickerDialogID).(*modelPickerDialog)
	if d.loaded {
		t.Errorf("picker should open unloaded, before AvailableModels() answers")
	}
	if cmd == nil {
		t.Fatalf("ctrl+g returned no Cmd — the list would never arrive")
	}

	// Cursor keys against the loading list must also be instant.
	mustBeFast(t, "down on a loading picker", func() {
		m, _ = pressKey(m, tea.Key{Code: tea.KeyDown})
	})

	msg := cmd().(modelsLoadedMsg)
	if len(msg.models) != 2 {
		t.Fatalf("modelsLoadedMsg carried %d models, want 2", len(msg.models))
	}
	out, _ := m.Update(msg)
	m = out.(Model)
	d = m.overlayStack.Get(modelPickerDialogID).(*modelPickerDialog)
	if !d.loaded || len(d.rows()) != 2 {
		t.Fatalf("snapshot not installed: loaded=%v rows=%d", d.loaded, len(d.rows()))
	}
	if got := renderPlain(d, &m); !strings.Contains(got, "m1") {
		t.Errorf("picker render missing the snapshot rows:\n%s", got)
	}
}

// TestModelPicker_EnterSwitchesOffLoop — Enter dispatches a Cmd and
// leaves the dialog open showing progress; the attach happens in
// Update when modelSwitchedMsg lands.
func TestModelPicker_EnterSwitchesOffLoop(t *testing.T) {
	next := &bareAgent{id: "next"}
	agent := &slowAgent{
		id:        "slow",
		models:    []ModelInfo{{ID: "m1"}, {ID: "m2"}},
		nextAgent: next,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	d := readyModelPicker(&m)
	m.overlayStack.Open(d)
	d.idx = 1

	var act DialogAction
	mustBeFast(t, "enter on the model picker", func() {
		act = d.HandleKey("enter", &m)
	})
	if !act.Consumed || act.Close {
		t.Errorf("enter = %+v, want Consumed and NOT Close (the switch is in flight)", act)
	}
	if act.Cmd == nil {
		t.Fatalf("enter returned no Cmd")
	}
	if d.switching != "m2" {
		t.Errorf("switching = %q, want m2", d.switching)
	}
	if m.opts.Agent != Agent(agent) {
		t.Errorf("Agent swapped before the reply landed")
	}
	if got := renderPlain(d, &m); !strings.Contains(got, "switching to m2") {
		t.Errorf("in-flight render missing the progress line:\n%s", got)
	}

	msg := act.Cmd().(modelSwitchedMsg)
	out, _ := m.Update(msg)
	m = out.(Model)
	if m.opts.Agent != Agent(next) {
		t.Fatalf("Agent not swapped after modelSwitchedMsg: %v", m.opts.Agent)
	}
	if m.overlayStack.HasID(modelPickerDialogID) {
		t.Errorf("picker should close once the switch lands")
	}
	if snap := m.history.Snapshot(); !strings.Contains(snap[len(snap)-1].Text, "switched to m2") {
		t.Errorf("missing the confirmation row: %+v", snap)
	}
}

// TestModelSwitchedMsg_StaleGenDoesNotAttach — the load-bearing
// guard. SwitchModel REPLACES m.opts.Agent, so a reply that left
// under the previous session must not attach its agent to the one
// the operator switched into.
func TestModelSwitchedMsg_StaleGenDoesNotAttach(t *testing.T) {
	current := &bareAgent{id: "current"}
	m := NewModel(Options{Agent: current})
	m.viewport.SetWidth(80)
	m.sessionGen = 7

	stale := &bareAgent{id: "stale-from-the-old-session"}
	m.overlayStack.Open(newModelPickerDialog())

	out, _ := m.Update(modelSwitchedMsg{gen: 6, id: "m9", agent: stale})
	m = out.(Model)

	if m.opts.Agent != Agent(current) {
		t.Fatalf("stale modelSwitchedMsg attached its agent: %v", m.opts.Agent)
	}
	for _, msg := range m.history.Snapshot() {
		if strings.Contains(msg.Text, "m9") {
			t.Errorf("stale switch leaked a transcript row: %q", msg.Text)
		}
	}
	if !m.overlayStack.HasID(modelPickerDialogID) {
		t.Errorf("stale reply closed a dialog it doesn't own")
	}

	// The matching generation still lands.
	fresh := &bareAgent{id: "fresh"}
	out, _ = m.Update(modelSwitchedMsg{gen: 7, id: "m1", agent: fresh})
	m = out.(Model)
	if m.opts.Agent != Agent(fresh) {
		t.Fatalf("same-gen reply did not attach: %v", m.opts.Agent)
	}
}

// TestModelsLoadedMsg_RoutesToCoveredDialog — an async reply for a
// dialog another modal has since covered still belongs to it
// (dialog.go's Get contract).
func TestModelsLoadedMsg_RoutesToCoveredDialog(t *testing.T) {
	agent := &slowAgent{id: "slow", models: []ModelInfo{{ID: "m1"}}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	picker := newModelPickerDialog()
	m.overlayStack.Open(picker)
	m.overlayStack.Open(newToolCallDialog(1)) // covers the picker

	out, _ := m.Update(modelsLoadedMsg{gen: m.sessionGen, models: []ModelInfo{{ID: "m1"}}})
	m = out.(Model)

	if !picker.loaded || len(picker.rows()) != 1 {
		t.Fatalf("covered picker did not receive its snapshot: loaded=%v rows=%d",
			picker.loaded, len(picker.rows()))
	}
}

// TestSessionPicker_EnterSwitchesOffLoop — the session picker's Enter
// mirrors the model picker's: Cmd out, applySwitchTarget in Update.
func TestSessionPicker_EnterSwitchesOffLoop(t *testing.T) {
	agent := &slowAgent{
		id:       "slow",
		sessions: []SessionInfo{{ID: "cur", Current: true}, {ID: "other"}},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	d := readySessionPicker(&m)
	m.overlayStack.Open(d)
	d.idx = 1

	var act DialogAction
	mustBeFast(t, "enter on the session picker", func() {
		act = d.HandleKey("enter", &m)
	})
	if act.Cmd == nil || act.Close {
		t.Fatalf("enter = %+v, want a Cmd and NOT Close", act)
	}
	if d.switching != "other" {
		t.Errorf("switching = %q, want other", d.switching)
	}

	msg := act.Cmd().(sessionSwitchedMsg)
	out, _ := m.Update(msg)
	m = out.(Model)
	if got, ok := m.opts.Agent.(*bareAgent); !ok || got.id != "other" {
		t.Fatalf("session not attached: %v", m.opts.Agent)
	}
	if m.overlayStack.HasID(sessionPickerDialogID) {
		t.Errorf("picker should close once the switch lands")
	}
}

// TestSessionSwitchedMsg_StaleGenDropped — same guard as the model
// side; applySwitchTarget wipes history, so a stale reply landing
// would be destructive.
func TestSessionSwitchedMsg_StaleGenDropped(t *testing.T) {
	current := &bareAgent{id: "current"}
	m := NewModel(Options{Agent: current})
	m.viewport.SetWidth(80)
	m.sessionGen = 3

	out, _ := m.Update(sessionSwitchedMsg{
		gen:    2,
		id:     "old",
		target: SwitchTarget{Agent: &bareAgent{id: "old"}},
	})
	m = out.(Model)
	if m.opts.Agent != Agent(current) {
		t.Fatalf("stale sessionSwitchedMsg attached: %v", m.opts.Agent)
	}
	if m.sessionGen != 3 {
		t.Errorf("stale reply ran applySwitchTarget (gen = %d)", m.sessionGen)
	}
}

// TestFilePalette_OpensImmediatelyOnALargeTree — the `@` keystroke
// must not wait for filepath.WalkDir. The palette opens empty and
// loading, and fills in when fileItemsMsg lands.
func TestFilePalette_OpensImmediatelyOnALargeTree(t *testing.T) {
	root := t.TempDir()
	// A tree big enough that a synchronous walk is measurable.
	for i := 0; i < 40; i++ {
		dir := filepath.Join(root, "pkg", strings.Repeat("d", i%7+1), string(rune('a'+i%26)))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for j := 0; j < 10; j++ {
			f := filepath.Join(dir, string(rune('a'+j))+".go")
			if err := os.WriteFile(f, []byte("package x\n"), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}

	m := NewModel(Options{
		Agent:     &bareAgent{id: "a"},
		PathScope: PathScope{Roots: []string{root}},
	})
	m.width, m.height = 100, 30
	m.viewport.SetWidth(80)
	m.resize()

	var cmd tea.Cmd
	mustBeFast(t, "@ keypress", func() {
		m, cmd = pressKey(m, tea.Key{Code: '@', Text: "@"})
	})
	if m.palette == nil {
		t.Fatalf("@ did not open a palette")
	}
	if !m.palette.loading {
		t.Errorf("palette should open in the loading state")
	}
	if len(m.palette.items) != 0 {
		t.Errorf("palette should open empty, got %d items", len(m.palette.items))
	}
	if cmd == nil {
		t.Fatalf("@ returned no Cmd — the scan would never run")
	}
	// The panel renders while empty rather than showing "no matches".
	if got := m.renderPalette(60); !strings.Contains(got, "scanning") {
		t.Errorf("loading palette missing the scanning hint:\n%s", got)
	}

	msg := drainFileItems(t, cmd)
	if len(msg.items) == 0 {
		t.Fatalf("scan produced no items")
	}
	out, _ := m.Update(msg)
	m = out.(Model)
	if m.palette == nil || m.palette.loading {
		t.Fatalf("palette did not settle: %+v", m.palette)
	}
	if len(m.palette.items) != len(msg.items) {
		t.Errorf("palette items = %d, want %d", len(m.palette.items), len(msg.items))
	}
}

// drainFileItems runs a palette Cmd and asserts it yielded the scan.
func drainFileItems(t *testing.T, cmd tea.Cmd) fileItemsMsg {
	t.Helper()
	msg, ok := cmd().(fileItemsMsg)
	if !ok {
		t.Fatalf("palette Cmd produced %T, want fileItemsMsg", msg)
	}
	return msg
}

// TestFileItemsMsg_StaleSeqDropped — a scan that outlived its palette
// must not repopulate whatever is open now.
func TestFileItemsMsg_StaleSeqDropped(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.width, m.height = 100, 30
	m.viewport.SetWidth(80)
	m.resize()

	m, _ = pressKey(m, tea.Key{Code: '@', Text: "@"})
	if m.palette == nil {
		t.Fatalf("@ did not open a palette")
	}
	seq := m.palette.seq

	out, _ := m.Update(fileItemsMsg{
		gen:   m.sessionGen,
		seq:   seq - 1,
		items: []paletteItem{{Name: "ghost.go", Available: true}},
	})
	m = out.(Model)
	for _, it := range m.palette.items {
		if it.Name == "ghost.go" {
			t.Fatalf("stale scan repopulated the live palette")
		}
	}
}

// TestSlashPalette_OpensWithBuiltinsThenMergesHost — `/` paints the
// built-in catalog on the keystroke and merges the host's commands
// when SlashCommands() answers.
func TestSlashPalette_OpensWithBuiltinsThenMergesHost(t *testing.T) {
	agent := &slowAgent{
		id:    "slow",
		specs: []SlashCommandSpec{{Name: "btw", Description: "host command"}},
	}
	m := NewModel(Options{Agent: agent})
	m.width, m.height = 100, 30
	m.viewport.SetWidth(80)
	m.resize()

	var cmd tea.Cmd
	mustBeFast(t, "/ keypress", func() {
		m, cmd = pressKey(m, tea.Key{Code: '/', Text: "/"})
	})
	if m.palette == nil || m.palette.kind != paletteSlash {
		t.Fatalf("/ did not open a slash palette")
	}
	builtins := len(m.palette.items)
	if builtins == 0 {
		t.Fatalf("slash palette opened with no built-ins")
	}
	for _, it := range m.palette.items {
		if it.Name == "btw" {
			t.Fatalf("host command present before SlashCommands() answered")
		}
	}
	if cmd == nil {
		t.Fatalf("/ returned no Cmd — host commands would never merge")
	}

	msg, ok := cmd().(slashCommandsMsg)
	if !ok {
		t.Fatalf("slash Cmd produced %T, want slashCommandsMsg", msg)
	}
	out, _ := m.Update(msg)
	m = out.(Model)
	found := false
	for _, it := range m.palette.items {
		if it.Name == "btw" {
			found = true
		}
	}
	if !found {
		t.Errorf("host command did not merge into the open palette")
	}
	if len(m.palette.items) != builtins+1 {
		t.Errorf("palette items = %d, want %d", len(m.palette.items), builtins+1)
	}
	if m.palette.loading {
		t.Errorf("palette still loading after the merge")
	}
}

// TestSidebarSubagents_ReadsTheSnapshot — the sidebar's roster comes
// from hostSnapshot, refreshed off-loop, not from a View()-time
// Subagents() call.
func TestSidebarSubagents_ReadsTheSnapshot(t *testing.T) {
	agent := &slowAgent{id: "slow", subs: []SubagentInfo{{Name: "probe", Status: "running"}}}
	m := NewModel(Options{Agent: agent, StatusLayout: StatusSidebar})
	m.width, m.height = 120, 40
	m.viewport.SetWidth(80)
	m.resize()

	// Before the first refresh lands the section reads as pending,
	// not as "none" (which would be a lie) and not by calling the
	// host (which would block).
	if got := m.subagentSummary(); len(got) != 1 || got[0] != "…" {
		t.Errorf("pre-refresh summary = %v, want the pending placeholder", got)
	}

	cmd := m.refreshHostSnapshotCmd()
	if cmd == nil {
		t.Fatalf("a wired SubagentLister must start the snapshot cycle")
	}
	out, _ := m.Update(cmd())
	m = out.(Model)

	got := m.subagentSummary()
	if len(got) != 1 || !strings.Contains(got[0], "probe") {
		t.Fatalf("post-refresh summary = %v, want the probe row", got)
	}
	mustBeFast(t, "View with a populated roster", func() { _ = m.View() })
}

// slowReloader is a Reloader + PricingController that records the
// context deadline it was handed. The point of issue #114's "worst
// offenders": both methods TAKE a ctx, and both used to be given
// context.Background().
type slowReloader struct {
	bareAgent
	reloadDeadline  bool
	pricingDeadline bool
}

func (r *slowReloader) Reload(ctx context.Context) (ReloadResult, error) {
	_, r.reloadDeadline = ctx.Deadline()
	return ReloadResult{Note: "/reload: done"}, nil
}

func (r *slowReloader) Refresh(ctx context.Context) (string, error) {
	_, r.pricingDeadline = ctx.Deadline()
	return "prices refreshed", nil
}

func (r *slowReloader) Set(string, float64, float64) (string, error) { return "", nil }

// TestReloadAndPricingRefresh_GetBoundedContexts — both run off-loop
// and both receive a context with a deadline.
func TestReloadAndPricingRefresh_GetBoundedContexts(t *testing.T) {
	host := &slowReloader{bareAgent: bareAgent{id: "host"}}
	m := NewModel(Options{Agent: host})
	m.viewport.SetWidth(80)

	handled, out, cmd := m.dispatchBuiltinSlash("reload", "")
	if !handled || cmd == nil {
		t.Fatalf("/reload: handled=%v cmd=%v, want handled with a Cmd", handled, cmd)
	}
	m = out.(Model)
	if last := lastText(m); !strings.Contains(last, "rebuilding") {
		t.Errorf("/reload missing the immediate acknowledgement, got %q", last)
	}
	msg, ok := cmd().(reloadDoneMsg)
	if !ok {
		t.Fatalf("/reload Cmd produced %T, want reloadDoneMsg", msg)
	}
	if !host.reloadDeadline {
		t.Errorf("Reload was handed a context with no deadline")
	}
	out, _ = m.Update(msg)
	m = out.(Model)
	if last := lastText(m); !strings.Contains(last, "/reload: done") {
		t.Errorf("reload note not applied, got %q", last)
	}

	handled, out, cmd = m.dispatchBuiltinSlash("pricing", "refresh")
	if !handled || cmd == nil {
		t.Fatalf("/pricing refresh: handled=%v cmd=%v", handled, cmd)
	}
	m = out.(Model)
	pmsg, ok := cmd().(pricingRefreshedMsg)
	if !ok {
		t.Fatalf("/pricing refresh Cmd produced %T, want pricingRefreshedMsg", pmsg)
	}
	if !host.pricingDeadline {
		t.Errorf("PricingController.Refresh was handed a context with no deadline")
	}
	out, _ = m.Update(pmsg)
	m = out.(Model)
	if last := lastText(m); !strings.Contains(last, "prices refreshed") {
		t.Errorf("pricing summary not surfaced, got %q", last)
	}
}

// TestReloadDoneMsg_StaleGenDropped — Reload can replace the Agent
// too, so it carries the same guard.
func TestReloadDoneMsg_StaleGenDropped(t *testing.T) {
	current := &bareAgent{id: "current"}
	m := NewModel(Options{Agent: current})
	m.viewport.SetWidth(80)
	m.sessionGen = 4

	out, _ := m.Update(reloadDoneMsg{
		gen:    3,
		result: ReloadResult{Agent: &bareAgent{id: "stale"}, Note: "should not appear"},
	})
	m = out.(Model)
	if m.opts.Agent != Agent(current) {
		t.Fatalf("stale reloadDoneMsg swapped the Agent: %v", m.opts.Agent)
	}
	for _, msg := range m.history.Snapshot() {
		if strings.Contains(msg.Text, "should not appear") {
			t.Errorf("stale reload leaked a transcript row")
		}
	}
}

// TestSlashModelWithArg_RunsOffLoop — `/model <id>` goes through the
// same off-loop Cmd as the picker rather than calling SwitchModel
// inline.
func TestSlashModelWithArg_RunsOffLoop(t *testing.T) {
	next := &bareAgent{id: "next"}
	agent := &slowAgent{id: "slow", models: []ModelInfo{{ID: "m1"}}, nextAgent: next}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	var (
		handled bool
		out     tea.Model
		cmd     tea.Cmd
	)
	mustBeFast(t, "/model m1", func() {
		handled, out, cmd = m.dispatchBuiltinSlash("model", "m1")
	})
	if !handled || cmd == nil {
		t.Fatalf("/model <id>: handled=%v cmd=%v", handled, cmd)
	}
	m = out.(Model)
	if m.opts.Agent != Agent(agent) {
		t.Errorf("Agent swapped inline")
	}
	out, _ = m.Update(cmd())
	m = out.(Model)
	if m.opts.Agent != Agent(next) {
		t.Fatalf("Agent not swapped after the reply: %v", m.opts.Agent)
	}
}

// lastText returns the text of the last history row, or "".
func lastText(m Model) string {
	snap := m.history.Snapshot()
	if len(snap) == 0 {
		return ""
	}
	return snap[len(snap)-1].Text
}
