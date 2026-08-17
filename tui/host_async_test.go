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
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// slowHostDelay is what a wedged host feels like. Long enough that an
// inline call can't hide inside the assertion budget, short enough
// that the suite stays quick.
const slowHostDelay = 500 * time.Millisecond

// nonBlockingBudget is the ceiling for one Update or View call. Half
// of slowHostDelay: an inline call site cannot squeeze under it, and
// nothing else in the frame comes anywhere near it.
//
// It was slowHostDelay/10 and tripped on a loaded macOS runner at
// 51.6ms — a populated sidebar paint under -race, not a host call.
// There is no useful signal in the gap between "a paint" and "half a
// wedged host": the real discriminator is the agent.calls counter
// these tests assert alongside the timing, which is exact. The clock
// is here to catch a blocking call that somehow does not go through
// the counted capability, so it should be set where a slow machine
// cannot reach it.
const nonBlockingBudget = slowHostDelay / 2

// slowAgent implements every capability whose call sites issue #114
// moved off the Update loop, and sleeps in all of them.
type slowAgent struct {
	id string

	models    []ModelInfo
	sessions  []SessionInfo
	subs      []SubagentInfo
	specs     []SlashCommandSpec
	tools     []ToolInfo
	approvals []ApprovalLog

	nextAgent Agent
	switchErr error
	ruleErr   error

	// What the PermissionController mutators recorded, so a test can
	// prove the rule reached the host and not just the transcript.
	allowed []string
	denied  []string
	bundles []string

	// invokeCtx is the context InvokeSlash was handed, kept so a test
	// can ask whether it carries a deadline — the half of issue #137's
	// slash-dispatch fix that isn't about blocking.
	invokeCtx context.Context

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

func (a *slowAgent) SubagentEvents(context.Context, string, int64) (SubagentEventPage, error) {
	a.sleep()
	return SubagentEventPage{}, nil
}

func (a *slowAgent) SlashCommands() []SlashCommandSpec {
	a.sleep()
	return a.specs
}

func (a *slowAgent) InvokeSlash(ctx context.Context, name, _ string) (SlashResult, error) {
	a.invokeCtx = ctx
	a.sleep()
	return SlashResult{SystemMessage: "/" + name + " answered"}, nil
}

// The /cmd-path capabilities issue #137 moved off the loop. Same
// contract as the ones above: sleep, then answer.

func (a *slowAgent) Tools() []ToolInfo {
	a.sleep()
	return a.tools
}

func (a *slowAgent) SessionApprovals() []ApprovalLog {
	a.sleep()
	return a.approvals
}

func (a *slowAgent) AddAllowPatterns(p []string) error {
	a.sleep()
	a.allowed = append(a.allowed, p...)
	return a.ruleErr
}

func (a *slowAgent) AddDenyPatterns(p []string) error {
	a.sleep()
	a.denied = append(a.denied, p...)
	return a.ruleErr
}

func (a *slowAgent) AddBuiltinAllowExtra(bundle string) error {
	a.sleep()
	a.bundles = append(a.bundles, bundle)
	return a.ruleErr
}

func (a *slowAgent) Refresh(context.Context) (string, error) {
	a.sleep()
	return "prices refreshed", nil
}

func (a *slowAgent) Set(id string, in, out float64) (string, error) {
	a.sleep()
	return fmt.Sprintf("/pricing set: %s = $%.2f in / $%.2f out", id, in, out), nil
}

// readyModelPicker asks the model picker with the host list already
// snapshotted — the test-side shorthand for ask + the modelsLoadedMsg
// round trip, for tests about what happens AFTER the list lands. It
// opens the picker, as /model does; askModelPicker is the unloaded
// half.
func readyModelPicker(m *Model) *modelPickerQuestion {
	sw, wired := m.opts.Agent.(ModelSwapper)
	q := askModelPicker(m, wired)
	if wired {
		q.applyModels(sw.AvailableModels(), m.displayModelName())
	}
	return q
}

// readySessionPicker is readyModelPicker's session-list twin, and asks
// the same way: on the stack, with the host list already snapshotted.
func readySessionPicker(m *Model) *sessionPickerQuestion {
	sw, wired := m.opts.Agent.(SessionSwitcher)
	q := askSessionPicker(m, wired)
	if wired {
		q.applySessions(sw.Sessions())
	}
	return q
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

// drainBatch runs cmd and flattens whatever it produced into the
// individual msgs, recursing into tea.BatchMsg. Several of the
// off-loop rewrites pair a host call with a second Cmd — the theme
// picker emits ThemeChangedMsg alongside its persist call — so a test
// that only ran the outer Cmd would see a BatchMsg and nothing else.
func drainBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, drainBatch(t, c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
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

	askModelPicker(&m, true)
	mustBeFast(t, "View with a loading model picker", func() { _ = m.View() })
	m.overlayStack.Close(modelPickerDialogID)

	askSessionPicker(&m, true)
	mustBeFast(t, "View with a loading session picker", func() { _ = m.View() })
	m.overlayStack.Close(sessionPickerDialogID)

	if got := agent.calls.Load(); got != before {
		t.Errorf("View() made %d host call(s); the contract is zero", got-before)
	}

	// Snapshot installed: still zero host calls, now with real rows.
	// The AvailableModels() pull readyModelPicker makes is the
	// open-time one and happens before the counter is re-read.
	readyModelPicker(&m)
	before = agent.calls.Load()
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
	q := modelPickerOn(&m.overlayStack)
	if q.loaded {
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
	q = modelPickerOn(&m.overlayStack)
	if !q.loaded || len(q.rows()) != 2 {
		t.Fatalf("snapshot not installed: loaded=%v rows=%d", q.loaded, len(q.rows()))
	}
	if got := ansi.Strip(m.overlayStack.Render(80, &m)); !strings.Contains(got, "m1") {
		t.Errorf("picker render missing the snapshot rows:\n%s", got)
	}
}

// TestModelPicker_EnterSwitchesOffLoop — neither Enter nor the
// request it schedules may touch the host on the Update goroutine; the
// picker stays open showing progress, and the attach happens when
// modelSwitchedMsg lands.
//
// Two hops rather than one since #164 stage 3: Enter names the model
// and the request arm makes the call, because the live ModelSwapper
// and the live generation are the two things a question is not allowed
// to hold. The non-blocking claim has to hold at both.
func TestModelPicker_EnterSwitchesOffLoop(t *testing.T) {
	next := &bareAgent{id: "next"}
	agent := &slowAgent{
		id:        "slow",
		models:    []ModelInfo{{ID: "m1"}, {ID: "m2"}},
		nextAgent: next,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	q := readyModelPicker(&m)
	q.idx = 1

	var cmd tea.Cmd
	mustBeFast(t, "enter on the model picker", func() {
		m, cmd = pressKey(m, tea.Key{Code: tea.KeyEnter})
	})
	if !m.overlayStack.HasID(modelPickerDialogID) {
		t.Error("enter closed the picker; the switch is still in flight")
	}
	if q.switching != "m2" {
		t.Errorf("switching = %q, want m2", q.switching)
	}
	if m.opts.Agent != Agent(agent) {
		t.Errorf("Agent swapped before the reply landed")
	}
	if got := ansi.Strip(m.overlayStack.Render(80, &m)); !strings.Contains(got, "switching to m2") {
		t.Errorf("in-flight render missing the progress line:\n%s", got)
	}

	req, ok := cmd().(modelSwitchRequestedMsg)
	if !ok {
		t.Fatalf("enter scheduled a %T, want modelSwitchRequestedMsg", cmd())
	}
	mustBeFast(t, "the switch request", func() {
		out, follow := m.Update(req)
		m, cmd = out.(Model), follow
	})
	if cmd == nil {
		t.Fatalf("the request scheduled no SwitchModel call")
	}

	msg := cmd().(modelSwitchedMsg)
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
	askModelPicker(&m, false)

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

	picker := askModelPicker(&m, true)
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

	q := readySessionPicker(&m)
	q.idx = 1

	var (
		ans answer
		cmd tea.Cmd
	)
	mustBeFast(t, "enter on the session picker", func() {
		ans, cmd = q.Key(keyMsgFromStroke("enter"))
	})
	if ans != nil || cmd == nil {
		t.Fatalf("enter = (%#v, %v), want a Cmd and no answer yet", ans, cmd)
	}
	if q.switching != "other" {
		t.Errorf("switching = %q, want other", q.switching)
	}

	// Enter only NAMES the session; Update is what reaches the host,
	// and it has to hand that off rather than dial inline too.
	var follow tea.Cmd
	mustBeFast(t, "the switch request", func() {
		out, c := m.Update(cmd().(sessionSwitchRequestedMsg))
		m, follow = out.(Model), c
	})
	if follow == nil {
		t.Fatal("the request produced no Cmd — the host call would never happen")
	}
	for _, msg := range drainBatch(t, follow) {
		out, _ := m.Update(msg)
		m = out.(Model)
	}
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
		t.Fatalf("a wired SubagentReporter must start the snapshot cycle")
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

// ---- issue #137: the /cmd path and the Options.* callbacks ----

// slowPermissionMode is an Options.PermissionMode wiring whose Set and
// Persist both sleep — Persist because on a real host it writes the
// mode to a config file. Shift+Tab reached both from inside Update.
type slowPermissionMode struct {
	calls   atomic.Int64
	set     []PermissionMode
	persist []PermissionMode
	setErr  error
}

func (p *slowPermissionMode) wiring() PermissionModeWiring {
	return PermissionModeWiring{
		Set: func(mode PermissionMode) error {
			p.calls.Add(1)
			time.Sleep(slowHostDelay)
			if p.setErr != nil {
				return p.setErr
			}
			p.set = append(p.set, mode)
			return nil
		},
		Persist: func(mode PermissionMode) error {
			p.calls.Add(1)
			time.Sleep(slowHostDelay)
			p.persist = append(p.persist, mode)
			return nil
		},
	}
}

// TestShiftTab_PermissionModeRunsOffLoop — the highest-priority call
// site in issue #137. Shift+Tab is a BARE KEYSTROKE that reaches two
// host callbacks, one of which writes to disk. The chip must flip on
// the keystroke and the callbacks must ride the Cmd.
func TestShiftTab_PermissionModeRunsOffLoop(t *testing.T) {
	host := &slowPermissionMode{}
	m := NewModel(Options{Agent: &bareAgent{id: "a"}, PermissionMode: host.wiring()})
	m.viewport.SetWidth(80)

	var cmd tea.Cmd
	mustBeFast(t, "shift+tab", func() {
		m, cmd = pressKey(m, tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	})
	if m.permMode != PermissionModeAcceptEdits {
		t.Fatalf("chip = %s, want acceptEdits — it must track the key, not the host", m.permMode)
	}
	if got := host.calls.Load(); got != 0 {
		t.Fatalf("%d host callback(s) ran on the Update goroutine", got)
	}
	if cmd == nil {
		t.Fatal("shift+tab returned no Cmd — the host would never hear about the mode")
	}

	msg, ok := cmd().(permissionModeAppliedMsg)
	if !ok {
		t.Fatalf("shift+tab Cmd produced %T, want permissionModeAppliedMsg", msg)
	}
	if len(host.set) != 1 || host.set[0] != PermissionModeAcceptEdits {
		t.Errorf("Set got %v, want [acceptEdits]", host.set)
	}
	if len(host.persist) != 1 || host.persist[0] != PermissionModeAcceptEdits {
		t.Errorf("Persist got %v, want [acceptEdits]", host.persist)
	}
	if msg.prev != PermissionModeDefault {
		t.Errorf("reply carried prev = %s, want default", msg.prev)
	}

	out, _ := m.Update(msg)
	m = out.(Model)
	if m.permMode != PermissionModeAcceptEdits {
		t.Errorf("chip moved on a clean reply: %s", m.permMode)
	}
	for _, row := range m.history.Snapshot() {
		if row.Role == RoleError {
			t.Errorf("a successful mode change wrote an error row: %q", row.Text)
		}
	}
}

// TestPermissionModeApplied_SetFailureRollsBackTheChip — a chip that
// reads "bypassPermissions" while the gate refused to enter it is a
// safety claim core-tui can't back, so a failed Set rewinds it. The
// error also stops being swallowed.
func TestPermissionModeApplied_SetFailureRollsBackTheChip(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)
	m.permMode = PermissionModeBypass

	out, _ := m.Update(permissionModeAppliedMsg{
		gen:  m.sessionGen,
		prev: PermissionModePlan,
		mode: PermissionModeBypass,
		err:  errors.New("gate refuses bypass"),
	})
	m = out.(Model)
	if m.permMode != PermissionModePlan {
		t.Fatalf("chip = %s, want the rollback to plan", m.permMode)
	}
	if last := lastText(m); !strings.Contains(last, "gate refuses bypass") {
		t.Errorf("Set failure was swallowed, last row = %q", last)
	}

	// A second Shift+Tab that landed while the first was in flight owns
	// the chip now — the late failure must not rewind past it.
	m.permMode = PermissionModeDefault
	out, _ = m.Update(permissionModeAppliedMsg{
		gen:  m.sessionGen,
		prev: PermissionModePlan,
		mode: PermissionModeBypass,
		err:  errors.New("late refusal"),
	})
	m = out.(Model)
	if m.permMode != PermissionModeDefault {
		t.Errorf("a superseded failure rewound the chip to %s", m.permMode)
	}
}

// TestPermissionModeApplied_PersistFailureKeepsTheMode — Set worked,
// only the config write failed: the session IS in the new mode, it
// just won't survive a restart. Say so, keep the chip.
func TestPermissionModeApplied_PersistFailureKeepsTheMode(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)
	m.permMode = PermissionModePlan

	out, _ := m.Update(permissionModeAppliedMsg{
		gen:        m.sessionGen,
		prev:       PermissionModeAcceptEdits,
		mode:       PermissionModePlan,
		persistErr: errors.New("read-only config"),
	})
	m = out.(Model)
	if m.permMode != PermissionModePlan {
		t.Errorf("chip = %s, want plan — Set succeeded", m.permMode)
	}
	if last := lastText(m); !strings.Contains(last, "persist failed") {
		t.Errorf("persist failure was swallowed, last row = %q", last)
	}
}

// TestSlashCommandsRunOffLoop — every /cmd in issue #137's inventory
// must return from Update before its host method has been entered,
// with a Cmd that carries the call and a row that says work started.
func TestSlashCommandsRunOffLoop(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		args    string
		ack     string // substring of the immediate acknowledgement row
		want    string // substring of the row the reply produces
		wantMsg any
	}{
		{"tools", "tools", "", "reading the tool catalog", "shell", toolsListedMsg{}},
		{"permissions", "permissions", "", "reading the session approval log", "bash", approvalsListedMsg{}},
		{"subagents", "subagents", "", "reading the roster", "probe", subagentRosterMsg{}},
		{"allow", "allow", "bash:git *", "adding bash:git *", "added bash:git *", permissionRuleAddedMsg{}},
		{"deny", "deny", "bash:rm *", "adding bash:rm *", "added bash:rm *", permissionRuleAddedMsg{}},
		{"allow bundle", "allow", "bundle:dev_tools", "enabling bundle dev_tools", "enabled bundle dev_tools", permissionRuleAddedMsg{}},
		{"pricing set", "pricing", "set m1 1.25 5", "applying m1", "$1.25 in", pricingSetMsg{}},
		{"help", "help", "", "Built-in commands", "host command", helpCommandsMsg{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent := &slowAgent{
				id:        "slow",
				tools:     []ToolInfo{{Name: "shell", Description: "run a command"}},
				approvals: []ApprovalLog{{Tool: "bash", Key: "git status", Decision: "allow-session"}},
				subs:      []SubagentInfo{{Name: "probe", Status: "running"}},
				specs:     []SlashCommandSpec{{Name: "btw", Description: "host command"}},
			}
			m := NewModel(Options{Agent: agent})
			m.viewport.SetWidth(80)

			var (
				handled bool
				out     tea.Model
				cmd     tea.Cmd
			)
			mustBeFast(t, "/"+tc.cmd+" "+tc.args, func() {
				handled, out, cmd = m.dispatchBuiltinSlash(tc.cmd, tc.args)
			})
			if !handled {
				t.Fatalf("/%s not handled", tc.cmd)
			}
			m = out.(Model)
			if got := agent.calls.Load(); got != 0 {
				t.Fatalf("%d host call(s) ran on the Update goroutine", got)
			}
			if last := lastText(m); !strings.Contains(last, tc.ack) {
				t.Errorf("acknowledgement row = %q, want it to contain %q", last, tc.ack)
			}
			if cmd == nil {
				t.Fatalf("/%s returned no Cmd — the host would never be asked", tc.cmd)
			}

			msg := cmd()
			if reflect.TypeOf(msg) != reflect.TypeOf(tc.wantMsg) {
				t.Fatalf("Cmd produced %T, want %T", msg, tc.wantMsg)
			}
			if got := agent.calls.Load(); got != 1 {
				t.Errorf("Cmd made %d host call(s), want 1", got)
			}
			out, _ = m.Update(msg)
			m = out.(Model)
			if last := lastText(m); !strings.Contains(last, tc.want) {
				t.Errorf("reply row = %q, want it to contain %q", last, tc.want)
			}
			mustBeFast(t, "View after /"+tc.cmd, func() { _ = m.View() })
		})
	}
}

// TestPermissionRules_ReachTheHost — the /allow and /deny rewrites
// still hand the pattern to the right PermissionController mutator.
func TestPermissionRules_ReachTheHost(t *testing.T) {
	agent := &slowAgent{id: "slow"}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	for _, step := range []struct{ cmd, args string }{
		{"allow", "bash:git *"},
		{"deny", "bash:rm *"},
		{"allow", "bundle:dev_tools"},
	} {
		_, out, cmd := m.dispatchBuiltinSlash(step.cmd, step.args)
		m = out.(Model)
		if cmd == nil {
			t.Fatalf("/%s %s returned no Cmd", step.cmd, step.args)
		}
		out, _ = m.Update(cmd())
		m = out.(Model)
	}
	if len(agent.allowed) != 1 || agent.allowed[0] != "bash:git *" {
		t.Errorf("AddAllowPatterns got %v", agent.allowed)
	}
	if len(agent.denied) != 1 || agent.denied[0] != "bash:rm *" {
		t.Errorf("AddDenyPatterns got %v", agent.denied)
	}
	if len(agent.bundles) != 1 || agent.bundles[0] != "dev_tools" {
		t.Errorf("AddBuiltinAllowExtra got %v", agent.bundles)
	}
}

// TestPermissionRuleAdded_FailureRendersAsAnError — the failure
// wording the inline version produced survives the move, on the error
// row rather than the system row.
func TestPermissionRuleAdded_FailureRendersAsAnError(t *testing.T) {
	m := NewModel(Options{Agent: &slowAgent{id: "slow", ruleErr: errors.New("gate is read-only")}})
	m.viewport.SetWidth(80)

	_, out, cmd := m.dispatchBuiltinSlash("deny", "bash:rm *")
	m = out.(Model)
	out, _ = m.Update(cmd())
	m = out.(Model)

	snap := m.history.Snapshot()
	last := snap[len(snap)-1]
	if last.Role != RoleError {
		t.Errorf("failure row role = %v, want RoleError", last.Role)
	}
	if !strings.Contains(last.Text, "/deny: gate is read-only") {
		t.Errorf("failure row = %q", last.Text)
	}
}

// TestHelpCommands_HostSectionArrivesSeparately — /help paints the
// built-ins on the keystroke and only the "Agent commands" block waits
// on the host. A host with no commands produces no second row.
func TestHelpCommands_HostSectionArrivesSeparately(t *testing.T) {
	m := NewModel(Options{Agent: &slowAgent{id: "slow"}}) // no specs
	m.viewport.SetWidth(80)

	_, out, cmd := m.dispatchBuiltinSlash("help", "")
	m = out.(Model)
	before := len(m.history.Snapshot())
	if !strings.Contains(lastText(m), "/pricing refresh|set") {
		t.Errorf("built-in help did not render on the keystroke: %q", lastText(m))
	}
	out, _ = m.Update(cmd())
	m = out.(Model)
	if got := len(m.history.Snapshot()); got != before {
		t.Errorf("an empty SlashCommands() still appended a row (%d → %d)", before, got)
	}
}

// TestSubagentDetail_ResolvesFromTheSnapshot — `/subagents <name>`
// resolves against the off-loop hostSnapshot roster instead of calling
// Subagents() from Update.
func TestSubagentDetail_ResolvesFromTheSnapshot(t *testing.T) {
	agent := &slowAgent{
		id:   "slow",
		subs: []SubagentInfo{{Name: "auditor", Status: "running"}},
	}
	m := NewModel(Options{Agent: agent})
	m.width, m.height = 120, 40
	m.viewport.SetWidth(80)
	m.resize()

	// Seed the snapshot the way the periodic refresh does.
	out, _ := m.Update(m.refreshHostSnapshotCmd()())
	m = out.(Model)
	before := agent.calls.Load()

	var (
		text string
		cmd  tea.Cmd
	)
	mustBeFast(t, "/subagents auditor", func() {
		text, cmd = m.openSubagentDetail("audit")
	})
	if text != "" || cmd == nil {
		t.Fatalf("openSubagentDetail = (%q, %v), want the overlay + a fetch", text, cmd)
	}
	if got := agent.calls.Load(); got != before {
		t.Errorf("name resolution made %d host call(s) from Update", got-before)
	}
	d, ok := m.overlayStack.Get(subagentDialogID).(*subagentDialog)
	if !ok {
		t.Fatal("no subagent overlay opened")
	}
	if d.name != "auditor" {
		t.Errorf("overlay targets %q, want the snapshot's auditor", d.name)
	}
}

// TestIssue137Msgs_StaleGenDropped — every message type this change
// introduced carries gen and is guarded, so a reply that outlived its
// session leaves no trace in the transcript.
func TestIssue137Msgs_StaleGenDropped(t *testing.T) {
	msgs := []struct {
		name  string
		msg   tea.Msg
		probe string // text that must NOT appear
	}{
		{"persistDone", persistDoneMsg{gen: 1, what: "/model", err: errors.New("ghost-persist")}, "ghost-persist"},
		{"toolsListed", toolsListedMsg{gen: 1, tools: []ToolInfo{{Name: "ghost-tool"}}}, "ghost-tool"},
		{"approvalsListed", approvalsListedMsg{gen: 1, logs: []ApprovalLog{{Tool: "ghost-approval"}}}, "ghost-approval"},
		{"permissionRuleAdded", permissionRuleAddedMsg{gen: 1, op: permissionRuleAllow, arg: "ghost-rule"}, "ghost-rule"},
		{"subagentRoster", subagentRosterMsg{gen: 1, subs: []SubagentInfo{{Name: "ghost-sub"}}}, "ghost-sub"},
		{"pricingSet", pricingSetMsg{gen: 1, summary: "ghost-price"}, "ghost-price"},
		{"helpCommands", helpCommandsMsg{gen: 1, specs: []SlashCommandSpec{{Name: "ghost-cmd"}}}, "ghost-cmd"},
	}
	for _, tc := range msgs {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(Options{Agent: &bareAgent{id: "a"}})
			m.viewport.SetWidth(80)
			m.sessionGen = 2 // the msg left under gen 1

			out, _ := m.Update(tc.msg)
			m = out.(Model)
			for _, row := range m.history.Snapshot() {
				if strings.Contains(row.Text, tc.probe) {
					t.Fatalf("stale %s leaked a transcript row: %q", tc.name, row.Text)
				}
			}
		})
	}

	// permissionModeApplied is guarded the same way, but its damage
	// would be to the chip rather than the transcript.
	m := NewModel(Options{Agent: &bareAgent{id: "a"}})
	m.viewport.SetWidth(80)
	m.sessionGen = 2
	m.permMode = PermissionModeBypass
	out, _ := m.Update(permissionModeAppliedMsg{
		gen: 1, prev: PermissionModeDefault, mode: PermissionModeBypass,
		err: errors.New("stale refusal"),
	})
	if got := out.(Model).permMode; got != PermissionModeBypass {
		t.Errorf("stale permissionModeAppliedMsg rewound the chip to %s", got)
	}
}

// TestPersistCallbacks_RunOffLoop — the three Options persistence
// callbacks are host code that writes to the host's config, and all
// three were reached inline. Ctrl+B is the bare-keystroke one.
func TestPersistCallbacks_RunOffLoop(t *testing.T) {
	var layouts []StatusLayout
	m := NewModel(Options{
		Agent: &bareAgent{id: "a"},
		PersistStatusLayout: func(l StatusLayout) error {
			time.Sleep(slowHostDelay)
			layouts = append(layouts, l)
			return nil
		},
	})
	m.width, m.height = 120, 40
	m.viewport.SetWidth(80)
	m.resize()

	var cmd tea.Cmd
	mustBeFast(t, "ctrl+b", func() {
		m, cmd = pressKey(m, tea.Key{Code: 'b', Mod: tea.ModCtrl})
	})
	if len(layouts) != 0 {
		t.Fatalf("PersistStatusLayout ran on the Update goroutine: %v", layouts)
	}
	if cmd == nil {
		t.Fatal("ctrl+b returned no Cmd — the layout would never persist")
	}
	if msg, ok := cmd().(persistDoneMsg); !ok || msg.what != "status layout" {
		t.Fatalf("ctrl+b Cmd produced %#v, want a persistDoneMsg for the layout", msg)
	}
	if len(layouts) != 1 || layouts[0] != StatusSidebar {
		t.Errorf("PersistStatusLayout got %v, want [sidebar]", layouts)
	}

	// A failure now surfaces instead of vanishing into `_`.
	out, _ := m.Update(persistDoneMsg{
		gen: m.sessionGen, what: "status layout", err: errors.New("disk full"),
	})
	if last := lastText(out.(Model)); !strings.Contains(last, "status layout: persist failed: disk full") {
		t.Errorf("persist failure row = %q", last)
	}
}

// TestModelSwitch_PersistsOffLoop — PersistModelChoice was the other
// Options callback in issue #137's list; applyModelSwitch now hands it
// back as a Cmd.
func TestModelSwitch_PersistsOffLoop(t *testing.T) {
	var persisted []string
	m := NewModel(Options{
		Agent: &bareAgent{id: "cur"},
		PersistModelChoice: func(id string) error {
			time.Sleep(slowHostDelay)
			persisted = append(persisted, id)
			return nil
		},
	})
	m.viewport.SetWidth(80)

	var cmd tea.Cmd
	mustBeFast(t, "modelSwitchedMsg", func() {
		out, c := m.Update(modelSwitchedMsg{gen: m.sessionGen, id: "m2", agent: &bareAgent{id: "m2"}})
		m, cmd = out.(Model), c
	})
	if len(persisted) != 0 {
		t.Fatalf("PersistModelChoice ran on the Update goroutine: %v", persisted)
	}
	if cmd == nil {
		t.Fatal("no persist Cmd returned")
	}
	if msg, ok := cmd().(persistDoneMsg); !ok || msg.what != "/model" {
		t.Fatalf("Cmd produced %#v, want a persistDoneMsg for /model", msg)
	}
	if len(persisted) != 1 || persisted[0] != "m2" {
		t.Errorf("PersistModelChoice got %v, want [m2]", persisted)
	}
}

// ---- the two call sites be1817d deferred (issue #137) ----

// TestSwitchWithID_IsTwoStagesOffLoop — `/switch <id>` was two host
// calls in a row on the event loop: Sessions() to find out whether the
// id names an action row, then SwitchToSession. Neither may run from
// Update, and the second must not leave until the first has come back
// and been accepted.
func TestSwitchWithID_IsTwoStagesOffLoop(t *testing.T) {
	agent := &slowAgent{id: "slow", sessions: []SessionInfo{{ID: "b", Display: "other"}}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	var (
		out tea.Model
		cmd tea.Cmd
	)
	mustBeFast(t, "/switch b", func() { out, cmd = m.dispatchSlash("/switch b") })
	m = out.(Model)
	if got := agent.calls.Load(); got != 0 {
		t.Fatalf("%d host call(s) ran on the Update goroutine", got)
	}
	if last := lastText(m); !strings.Contains(last, "looking up b") {
		t.Errorf("acknowledgement row = %q", last)
	}
	if cmd == nil {
		t.Fatal("/switch b returned no Cmd — the enumerate would never happen")
	}

	// Stage one: the enumerate, and only the enumerate.
	lookup, ok := cmd().(switchLookupMsg)
	if !ok {
		t.Fatalf("stage one produced %T, want switchLookupMsg", lookup)
	}
	if lookup.row != nil {
		t.Errorf("a plain session id resolved to an action row: %+v", lookup.row)
	}
	if got := agent.calls.Load(); got != 1 {
		t.Fatalf("stage one made %d host call(s), want 1 (Sessions)", got)
	}

	var stage2 tea.Cmd
	mustBeFast(t, "switchLookupMsg", func() { out, stage2 = m.Update(lookup) })
	m = out.(Model)
	if got := agent.calls.Load(); got != 1 {
		t.Fatalf("SwitchToSession ran on the Update goroutine (%d calls)", got)
	}
	if m.opts.Agent != Agent(agent) {
		t.Errorf("Agent swapped before stage two had answered")
	}
	if stage2 == nil {
		t.Fatal("stage one produced no stage two")
	}

	// Stage two lands as the ordinary sessionSwitchedMsg, so the
	// picker's Enter path and this one share a handler.
	switched, ok := stage2().(sessionSwitchedMsg)
	if !ok {
		t.Fatalf("stage two produced %T, want sessionSwitchedMsg", switched)
	}
	out, _ = m.Update(switched)
	m = out.(Model)
	if m.opts.Agent == Agent(agent) {
		t.Fatalf("the switch never landed")
	}
	if last := lastText(m); !strings.Contains(last, "Attached to b") {
		t.Errorf("post-switch row = %q, want the target's note", last)
	}
}

// TestSwitchWithID_ActionRowStillOpensItsDialog — the issue #56 path
// through the same two stages: an id naming an action row opens that
// row's text input and never reaches SwitchToSession.
func TestSwitchWithID_ActionRowStillOpensItsDialog(t *testing.T) {
	agent := &slowAgent{id: "slow", sessions: []SessionInfo{
		{ID: "+attach", Display: "Attach to endpoint", Input: &SessionInput{Prompt: "Daemon URL:"}},
	}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/switch +attach")
	m = out.(Model)
	lookup := cmd().(switchLookupMsg)
	if lookup.row == nil || lookup.row.ID != "+attach" {
		t.Fatalf("enumerate did not resolve the action row: %+v", lookup.row)
	}
	out, cmd = m.Update(lookup)
	m = out.(Model)
	if cmd != nil {
		t.Errorf("an action row produced a stage-two Cmd: %T", cmd)
	}
	if !m.overlayStack.HasID(sessionInputDialogID) {
		t.Error("action row did not open its text input")
	}
	if got := agent.calls.Load(); got != 1 {
		t.Errorf("%d host call(s), want 1 — SwitchToSession must not be reached", got)
	}
}

// TestSwitchLookup_SupersededEnumerateNeverSwitches — the second risk
// in a two-stage flow. An enumerate the operator has already moved
// past must not be allowed to drive a switch, and neither must one
// that outlived its session.
func TestSwitchLookup_SupersededEnumerateNeverSwitches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stale func(m *Model)
	}{
		{"another /cmd was typed", func(m *Model) {
			out, _ := m.dispatchSlash("/keys")
			*m = out.(Model)
		}},
		{"the session turned over", func(m *Model) { m.sessionGen++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := &slowAgent{id: "slow", sessions: []SessionInfo{{ID: "b"}}}
			m := NewModel(Options{Agent: agent})
			m.viewport.SetWidth(80)

			out, cmd := m.dispatchSlash("/switch b")
			m = out.(Model)
			lookup := cmd().(switchLookupMsg)

			tc.stale(&m)
			before := agent.calls.Load()

			out, cmd = m.Update(lookup)
			m = out.(Model)
			if cmd != nil {
				t.Errorf("superseded enumerate returned a Cmd (%T) — stage two would run", cmd)
			}
			if got := agent.calls.Load(); got != before {
				t.Errorf("superseded enumerate made %d more host call(s)", got-before)
			}
			if m.opts.Agent != Agent(agent) {
				t.Error("superseded enumerate switched the session")
			}
		})
	}
}

// TestDispatchSlash_MatchAndInvokeRunOffLoop — the host half of a
// /cmd. The SlashCommands() match and the InvokeSlash it gates were
// both inline, and InvokeSlash was handed context.Background() despite
// taking a ctx precisely to say it might be slow.
func TestDispatchSlash_MatchAndInvokeRunOffLoop(t *testing.T) {
	agent := &slowAgent{id: "slow", specs: []SlashCommandSpec{{Name: "btw"}}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	var (
		out tea.Model
		cmd tea.Cmd
	)
	mustBeFast(t, "/btw hello", func() { out, cmd = m.dispatchSlash("/btw hello") })
	m = out.(Model)
	if got := agent.calls.Load(); got != 0 {
		t.Fatalf("%d host call(s) ran on the Update goroutine", got)
	}
	if cmd == nil {
		t.Fatal("/btw returned no Cmd — the host would never be asked")
	}

	msg, ok := cmd().(slashDispatchedMsg)
	if !ok {
		t.Fatalf("Cmd produced %T, want slashDispatchedMsg", msg)
	}
	if !msg.matched || !msg.invoked {
		t.Fatalf("matched=%v invoked=%v, want both — a plain provider resolves in one hop", msg.matched, msg.invoked)
	}
	if got := agent.calls.Load(); got != 2 {
		t.Errorf("Cmd made %d host call(s), want 2 (SlashCommands + InvokeSlash)", got)
	}
	if agent.invokeCtx == nil {
		t.Fatal("InvokeSlash was never reached")
	}
	if _, ok := agent.invokeCtx.Deadline(); !ok {
		t.Error("InvokeSlash was handed a context with no deadline — the contract inverted")
	}

	mustBeFast(t, "slashDispatchedMsg", func() { out, _ = m.Update(msg) })
	m = out.(Model)
	if last := lastText(m); !strings.Contains(last, "/btw answered") {
		t.Errorf("reply row = %q, want the host's SystemMessage", last)
	}
}

// TestUnknownCommand_CannotLandUnderTheNextCommand — the ordering
// hazard the move creates. "unknown command /foo" used to be written
// in the dispatching frame and so could not be out of order; with the
// name match off-loop it can arrive after whatever the operator typed
// next, and a row blaming /foo under /help's output reads as /help
// being the command that doesn't exist.
func TestUnknownCommand_CannotLandUnderTheNextCommand(t *testing.T) {
	agent := &slowAgent{id: "slow", specs: []SlashCommandSpec{{Name: "btw"}}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	// Control: nothing else happens, and the operator is told.
	out, cmd := m.dispatchSlash("/foo")
	m = out.(Model)
	verdict := cmd().(slashDispatchedMsg)
	if verdict.matched {
		t.Fatal("setup: /foo should not have matched")
	}
	out, _ = m.Update(verdict)
	if last := lastText(out.(Model)); !strings.Contains(last, "unknown command /foo") {
		t.Fatalf("the verdict never reached the operator: %q", last)
	}

	// The real thing: the operator doesn't wait, and types /help
	// while the match for /foo is still out with the host.
	m = NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	out, cmd = m.dispatchSlash("/foo")
	m = out.(Model)
	out, _ = m.dispatchSlash("/help")
	m = out.(Model)
	helpRows := m.history.Len()

	out, _ = m.Update(cmd().(slashDispatchedMsg))
	m = out.(Model)
	if m.history.Len() != helpRows {
		t.Errorf("the stale verdict appended a row: %q", lastText(m))
	}
	for _, row := range m.history.Snapshot() {
		if strings.Contains(row.Text, "unknown command") {
			t.Errorf("stale unknown-command row landed under /help: %q", row.Text)
		}
	}
}

// TestSlashDispatched_StaleGenDropped — the session-scoped half of the
// same guard, in the shape the rest of #137's replies use.
func TestSlashDispatched_StaleGenDropped(t *testing.T) {
	m := NewModel(Options{Agent: &slowAgent{id: "slow", specs: []SlashCommandSpec{{Name: "btw"}}}})
	m.viewport.SetWidth(80)
	m.sessionGen = 2

	out, _ := m.Update(slashDispatchedMsg{gen: 1, name: "ghost-cmd"})
	m = out.(Model)
	out, _ = m.Update(slashDispatchedMsg{
		gen: 1, name: "btw", matched: true, invoked: true,
		res: SlashResult{SystemMessage: "ghost-answer"},
	})
	m = out.(Model)
	for _, row := range m.history.Snapshot() {
		if strings.Contains(row.Text, "ghost-") {
			t.Errorf("stale slashDispatchedMsg leaked a transcript row: %q", row.Text)
		}
	}
}

// ---- the call site the #183 sweep left standing (issue #194) ----

// slowAttachRow builds the row that motivated issue #194: the
// "+ Attach to endpoint…" action row, whose Submit closure dials.
// Slowly, and through the agent's own counter, so a Submit still
// running inline shows up in exactly the instrument every other host
// call in this file is measured with.
func slowAttachRow(a *slowAgent, next Agent) SessionInfo {
	return SessionInfo{
		ID:      "+attach",
		Display: "+ Attach to endpoint…",
		Input: &SessionInput{
			Title:  "Attach to Endpoint",
			Prompt: "Daemon URL:",
			Submit: func(v string) (SwitchTarget, error) {
				a.sleep()
				return SwitchTarget{Agent: next, Note: "Attached to " + v}, nil
			},
		},
	}
}

// attachRowEntryPoints are the two ways an operator reaches the action
// row's text input. Both landed in the same inline closure, so both
// have to be proven off-loop. Each leaves the dialog frontmost with
// the host counter zeroed, so what a test measures afterwards is the
// commit and nothing that led up to it.
var attachRowEntryPoints = []struct {
	name string
	open func(t *testing.T, m *Model, a *slowAgent)
}{
	{"enter on the picker's action row", func(t *testing.T, m *Model, a *slowAgent) {
		t.Helper()
		q := readySessionPicker(m)
		q.idx = len(q.rows()) - 1 // the action row, appended last
		ans, cmd := q.Key(keyMsgFromStroke("enter"))
		if ans != nil || cmd == nil {
			t.Fatalf("enter on the action row = (%#v, %v), want a Cmd and no answer", ans, cmd)
		}
		// The Open is Update's, not the widget's: Overlay pops the
		// front dialog after Key returns, so a question that pushed
		// one would watch it be popped again.
		for _, msg := range drainBatch(t, cmd) {
			out, _ := m.Update(msg)
			*m = out.(Model)
		}
		if !m.overlayStack.HasID(sessionInputDialogID) {
			t.Fatal("the action row did not open its text input")
		}
		a.calls.Store(0)
	}},
	{"/switch <action-row-id>", func(t *testing.T, m *Model, a *slowAgent) {
		t.Helper()
		out, cmd := m.dispatchSlash("/switch +attach")
		*m = out.(Model)
		if cmd == nil {
			t.Fatal("/switch +attach returned no Cmd — the enumerate would never happen")
		}
		lookup, ok := cmd().(switchLookupMsg)
		if !ok {
			t.Fatalf("/switch +attach produced %T, want switchLookupMsg", lookup)
		}
		out, _ = m.Update(lookup)
		*m = out.(Model)
		a.calls.Store(0)
	}},
}

// openAttachDialog wires a slow host whose session list ends in the
// action row, opens the row's text input through entry, and types
// value into it — leaving the model one Enter short of committing.
func openAttachDialog(t *testing.T, open func(*testing.T, *Model, *slowAgent), value string) (Model, *slowAgent, Agent) {
	t.Helper()
	next := Agent(&bareAgent{id: "attached"})
	agent := &slowAgent{id: "slow"}
	agent.sessions = []SessionInfo{{ID: "b", Display: "other"}, slowAttachRow(agent, next)}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	open(t, &m, agent)
	front := m.overlayStack.Front()
	if front == nil || front.ID() != sessionInputDialogID {
		t.Fatalf("entry point did not leave the action row's text input frontmost: %v", front)
	}
	typeInto(t, front, &m, value)
	return m, agent, next
}

// TestSessionInputSubmit_RunsOffLoop — the defect. Enter on the action
// row's text input called SessionInput.Submit inline, so a wedged
// endpoint froze the Update goroutine for the whole dial: no repaint,
// no keys, no Ctrl+C. Enter must now cost nothing but a Cmd, and the
// attach must land only once that Cmd's reply is applied.
func TestSessionInputSubmit_RunsOffLoop(t *testing.T) {
	for _, tc := range attachRowEntryPoints {
		t.Run(tc.name, func(t *testing.T) {
			m, agent, next := openAttachDialog(t, tc.open, "http://wedged:7778")

			// Timed by hand rather than through mustBeFast so the
			// order of the two assertions can be chosen. The COUNTER
			// is the exact instrument — Submit either ran in this
			// goroutine or it did not — and it is the one that should
			// speak first; the clock is a backstop for a block that
			// somehow never reached a counted method, and it is the
			// assertion a loaded CI runner can make lie.
			start := time.Now()
			consumed, cmd := pressEnter(&m)
			elapsed := time.Since(start)

			if got := agent.calls.Load(); got != 0 {
				t.Fatalf("%d host call(s) ran on the Update goroutine — SessionInput.Submit is still inline", got)
			}
			if !consumed {
				t.Fatal("enter on the text input was not consumed")
			}
			if cmd == nil {
				t.Fatal("enter returned no Cmd — Submit would never run at all")
			}
			if elapsed > nonBlockingBudget {
				t.Errorf("enter took %s, want under %s — something blocked outside the counted host calls",
					elapsed, nonBlockingBudget)
			}

			// The dialog stays up and says what it is waiting for.
			// That acknowledgement is the whole reason it stays: a
			// modal that closed on Enter would leave the operator
			// watching an unchanged chat screen with an attach
			// happening invisibly behind it.
			if !m.overlayStack.HasID(sessionInputDialogID) {
				t.Fatal("the dialog closed before its answer arrived")
			}
			frame := renderPlain(m.overlayStack.Front(), &m)
			if got := agent.calls.Load(); got != 0 {
				t.Fatalf("the in-flight render made %d host call(s) — View must never touch the host", got)
			}
			if !strings.Contains(frame, "attaching to http://wedged:7778") {
				t.Errorf("in-flight body does not name the endpoint it is waiting on:\n%s", frame)
			}

			msg, ok := cmd().(sessionInputSubmittedMsg)
			if !ok {
				t.Fatalf("Cmd produced %T, want sessionInputSubmittedMsg", msg)
			}
			if got := agent.calls.Load(); got != 1 {
				t.Fatalf("the Cmd made %d host call(s), want exactly 1 (Submit)", got)
			}

			var out tea.Model
			mustBeFast(t, "sessionInputSubmittedMsg", func() { out, _ = m.Update(msg) })
			m = out.(Model)
			if m.opts.Agent != next {
				t.Fatalf("the attach never landed: agent = %v", m.opts.Agent)
			}
			if m.overlayStack.HasDialogs() {
				t.Error("a completed attach should close the text input and the picker under it")
			}
			if last := lastText(m); !strings.Contains(last, "Attached to http://wedged:7778") {
				t.Errorf("post-attach row = %q, want the target's note", last)
			}
		})
	}
}

// TestSessionInputSubmit_SecondEnterDoesNotStackADial — the hazard a
// non-blocking modal creates. The dialog is still on screen while the
// first dial is out, so the keyboard is live and an impatient operator
// will press Enter again. Keys are swallowed while the call is out —
// the picker does the same under an in-flight SwitchToSession —
// because otherwise an unresponsive endpoint collects one connection
// per keypress. Esc is the deliberate exception: the defect being
// fixed here is a UI that cannot be escaped.
func TestSessionInputSubmit_SecondEnterDoesNotStackADial(t *testing.T) {
	m, agent, _ := openAttachDialog(t, attachRowEntryPoints[0].open, "http://wedged:7778")

	_, cmd := pressEnter(&m)
	if cmd == nil {
		t.Fatal("setup: the first enter produced no Cmd")
	}

	for _, stroke := range []string{"enter", "x", "backspace"} {
		consumed, extra := m.overlayStack.HandleKeyMsg(keyMsgFromStroke(stroke), &m)
		if !consumed {
			t.Errorf("%q leaked out of an in-flight dialog — it would land in the chat textarea behind it", stroke)
		}
		if extra != nil {
			t.Errorf("%q produced a Cmd (%T) while a dial was already out", stroke, extra)
		}
	}
	if got := agent.calls.Load(); got != 0 {
		t.Errorf("keys pressed during the dial made %d host call(s), want 0", got)
	}
	if !m.overlayStack.HasID(sessionInputDialogID) {
		t.Fatal("a swallowed key closed the dialog")
	}

	consumed, _ := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("esc"), &m)
	if !consumed || m.overlayStack.HasID(sessionInputDialogID) {
		t.Error("esc must still close an in-flight dialog — a wedged endpoint may not take the keyboard with it")
	}
	if !m.overlayStack.HasID(sessionPickerDialogID) {
		t.Error("esc should pop back to the picker, as it does on an idle dialog")
	}
}

// TestSessionInputSubmit_ReplyIsDiscardedWhenTheOperatorMovedOn —
// the price of answering later. Three ways the answer can arrive
// somewhere it no longer belongs; all three must attach nothing, say
// nothing, and return no Cmd. Silence is the point on the dismissed
// case in particular: esc is an explicit cancel, and swapping the
// operator's whole session seconds later, with no keystroke to blame
// it on, is worse than dropping a connection the host opened.
func TestSessionInputSubmit_ReplyIsDiscardedWhenTheOperatorMovedOn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		moveOn func(t *testing.T, m *Model)
	}{
		{"esc dismissed the dialog", func(t *testing.T, m *Model) {
			t.Helper()
			m.overlayStack.HandleKeyMsg(keyMsgFromStroke("esc"), m)
			if m.overlayStack.HasID(sessionInputDialogID) {
				t.Fatal("setup: esc did not close the in-flight dialog")
			}
		}},
		{"another /cmd was typed", func(t *testing.T, m *Model) {
			t.Helper()
			out, _ := m.dispatchSlash("/keys")
			*m = out.(Model)
		}},
		{"the session turned over", func(t *testing.T, m *Model) { m.sessionGen++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := openAttachDialog(t, attachRowEntryPoints[0].open, "http://wedged:7778")
			before := m.opts.Agent

			_, cmd := pressEnter(&m)
			if cmd == nil {
				t.Fatal("setup: enter produced no Cmd")
			}
			// The host answers, eventually — after the operator has
			// already gone elsewhere.
			msg := cmd().(sessionInputSubmittedMsg)
			tc.moveOn(t, &m)
			rows := m.history.Len()

			out, after := m.Update(msg)
			m = out.(Model)
			if after != nil {
				t.Errorf("a discarded reply returned a Cmd (%T) — the attach was still dispatched", after)
			}
			if m.opts.Agent != before {
				t.Errorf("a discarded reply attached %v", m.opts.Agent)
			}
			if m.history.Len() != rows {
				t.Errorf("a discarded reply appended a transcript row: %q", lastText(m))
			}
		})
	}
}

// TestSessionInputSubmit_ReplyDoesNotLandOnAReplacementDialog — the
// case gen and seq cannot see. Reaching the action row through the
// PICKER bumps neither counter (Ctrl+G is not a slash dispatch and
// nothing switched), so an operator who gives up on one endpoint,
// escapes, and immediately tries another has two replies in flight
// wearing identical stamps. The first answer must not commit the
// second dialog's question.
func TestSessionInputSubmit_ReplyDoesNotLandOnAReplacementDialog(t *testing.T) {
	m, agent, next := openAttachDialog(t, attachRowEntryPoints[0].open, "http://wedged:7778")
	before := m.opts.Agent

	_, cmd := pressEnter(&m)
	if cmd == nil {
		t.Fatal("setup: enter produced no Cmd")
	}
	stale := cmd().(sessionInputSubmittedMsg)

	// Give up on that endpoint and type a different one. The picker
	// is still underneath, with the cursor still on the action row.
	m.overlayStack.HandleKeyMsg(keyMsgFromStroke("esc"), &m)
	picker := sessionPickerOn(&m.overlayStack)
	if picker == nil {
		t.Fatalf("setup: esc left %T frontmost, want the picker", m.overlayStack.Front())
	}
	if want := len(picker.rows()) - 1; picker.idx != want {
		t.Fatalf("setup: cursor is at %d, want it still on the action row %d", picker.idx, want)
	}
	_, reopen := m.overlayStack.HandleKeyMsg(keyMsgFromStroke("enter"), &m)
	for _, msg := range drainBatch(t, reopen) {
		out, _ := m.Update(msg)
		m = out.(Model)
	}
	typeInto(t, m.overlayStack.Front(), &m, "http://elsewhere:7778")
	agent.calls.Store(0)
	_, second := pressEnter(&m)
	if second == nil {
		t.Fatal("setup: the replacement dialog produced no Cmd")
	}

	out, after := m.Update(stale)
	m = out.(Model)
	if after != nil {
		t.Errorf("the abandoned reply returned a Cmd (%T)", after)
	}
	if m.opts.Agent != before {
		t.Errorf("the abandoned reply attached %v", m.opts.Agent)
	}
	if !m.overlayStack.HasID(sessionInputDialogID) {
		t.Fatal("the abandoned reply closed the replacement dialog")
	}
	if frame := renderPlain(m.overlayStack.Front(), &m); !strings.Contains(frame, "attaching to http://elsewhere:7778") {
		t.Errorf("the replacement dialog stopped waiting for its own answer:\n%s", frame)
	}

	// And the replacement's own reply still commits normally.
	out, _ = m.Update(second().(sessionInputSubmittedMsg))
	m = out.(Model)
	if m.opts.Agent != next {
		t.Fatalf("the replacement never attached: agent = %v", m.opts.Agent)
	}
	if last := lastText(m); !strings.Contains(last, "Attached to http://elsewhere:7778") {
		t.Errorf("post-attach row = %q, want the second endpoint's note", last)
	}
}
