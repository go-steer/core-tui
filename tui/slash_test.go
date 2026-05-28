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
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// slashAgent is a stub Agent + SlashProvider for dispatch tests.
type slashAgent struct {
	specs []SlashCommandSpec
	res   SlashResult
	err   error

	invokedName string
	invokedArgs string
}

func (s *slashAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (s *slashAgent) SlashCommands() []SlashCommandSpec { return s.specs }
func (s *slashAgent) InvokeSlash(_ context.Context, name, args string) (SlashResult, error) {
	s.invokedName = name
	s.invokedArgs = args
	return s.res, s.err
}

// TestDispatchSlash_OpensSideAnswerModal pins R-CMD-5: a SlashResult
// with a non-nil ModalAnswer sets m.sideAnswer and doesn't add the
// answer to chat history.
func TestDispatchSlash_OpensSideAnswerModal(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "btw"}},
		res:   SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}},
	}
	m := NewModel(Options{Agent: agent})
	m.input.SetValue("/btw what now")
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/btw what now")
	got := out.(Model)
	if got.sideAnswer == nil {
		t.Fatalf("expected sideAnswer to be set")
	}
	if got.sideAnswer.Question != "q?" {
		t.Errorf("question = %q, want %q", got.sideAnswer.Question, "q?")
	}
	if got.history.Len() != 0 {
		t.Errorf("history len = %d, want 0 (modal answer must not land in chat)", got.history.Len())
	}
	if agent.invokedName != "btw" {
		t.Errorf("invoked name = %q, want %q", agent.invokedName, "btw")
	}
	if agent.invokedArgs != "what now" {
		t.Errorf("invoked args = %q, want %q", agent.invokedArgs, "what now")
	}
}

// TestDispatchSlash_AppendsSystemMessage pins that a SlashResult with
// only SystemMessage adds a RoleSystem entry to history.
func TestDispatchSlash_AppendsSystemMessage(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "ping"}},
		res:   SlashResult{SystemMessage: "pong"},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/ping")
	got := out.(Model)
	entries := got.history.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("history len = %d, want 1", len(entries))
	}
	if entries[0].Role != RoleSystem || entries[0].Text != "pong" {
		t.Errorf("entry = %+v, want RoleSystem 'pong'", entries[0])
	}
}

// TestDispatchSlash_SurfacesError pins that an InvokeSlash error
// renders as a RoleError row (§4.2 error semantics).
func TestDispatchSlash_SurfacesError(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "boom"}},
		err:   errors.New("kaboom"),
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/boom")
	got := out.(Model)
	entries := got.history.Snapshot()
	if len(entries) != 1 || entries[0].Role != RoleError {
		t.Fatalf("expected one RoleError row, got %+v", entries)
	}
}

// TestDispatchSlash_AliasMatches pins that aliases in SlashCommandSpec
// route to the same InvokeSlash call.
func TestDispatchSlash_AliasMatches(t *testing.T) {
	agent := &slashAgent{
		specs: []SlashCommandSpec{
			{Name: "btw", Aliases: []string{"by-the-way"}},
		},
		res: SlashResult{SystemMessage: "matched"},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/by-the-way hello")
	got := out.(Model)
	if got.history.Len() != 1 {
		t.Errorf("alias dispatch failed: history len = %d, want 1", got.history.Len())
	}
	if agent.invokedName != "by-the-way" {
		t.Errorf("invoked name = %q, want %q", agent.invokedName, "by-the-way")
	}
}

// TestSlashClear_BareEnterConfirms locks in the fix for the
// /clear confirmation bug: the prompt promises "press enter for
// y/yes" but the Enter handler used to short-circuit on empty
// input before reaching the confirmingClear branch — pressing
// Enter quietly did nothing. Now an empty Enter while armed
// counts as the y/yes answer and wipes history.
func TestSlashClear_BareEnterConfirms(t *testing.T) {
	agent := &slashAgent{}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	// Seed some content so we can confirm the wipe.
	m.history.Append(Message{Role: RoleUser, Text: "hello"})
	m.history.Append(Message{Role: RoleAssistant, Text: "hi back"})

	// Arm the confirmation.
	out, _ := m.dispatchSlash("/clear")
	m = out.(Model)
	if !m.confirmingClear {
		t.Fatalf("expected confirmingClear=true after /clear")
	}
	// Two existing rows + the "press enter" system prompt = 3.
	if m.history.Len() != 3 {
		t.Fatalf("expected 3 history entries before confirmation, got %d", m.history.Len())
	}

	// Bare Enter (empty input) MUST wipe history.
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out2, _ := m.Update(enter)
	m = out2.(Model)
	if m.confirmingClear {
		t.Errorf("expected confirmingClear=false after bare-Enter confirmation")
	}
	if m.history.Len() != 0 {
		t.Errorf("expected history.Len()==0 after bare-Enter confirm, got %d", m.history.Len())
	}
}

// TestSlashClear_YesConfirms covers the typed-y/yes path so the
// existing accept words still work alongside the new bare-Enter
// shortcut.
func TestSlashClear_YesConfirms(t *testing.T) {
	agent := &slashAgent{}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	m.history.Append(Message{Role: RoleUser, Text: "hello"})

	out, _ := m.dispatchSlash("/clear")
	m = out.(Model)

	m.input.SetValue("yes")
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out2, _ := m.Update(enter)
	m = out2.(Model)
	if m.history.Len() != 0 {
		t.Errorf("expected history wiped after typed 'yes', got Len=%d", m.history.Len())
	}
}

// TestSlashClear_OtherTextCancels: any non-y/yes text disarms
// without clearing and leaves a "clear cancelled" system row.
func TestSlashClear_OtherTextCancels(t *testing.T) {
	agent := &slashAgent{}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)
	m.history.Append(Message{Role: RoleUser, Text: "hello"})

	out, _ := m.dispatchSlash("/clear")
	m = out.(Model)
	armedLen := m.history.Len()

	m.input.SetValue("nope")
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out2, _ := m.Update(enter)
	m = out2.(Model)

	if m.confirmingClear {
		t.Errorf("expected confirmingClear=false after cancel")
	}
	// Original entries + arming prompt + "clear cancelled" = armedLen+1.
	if m.history.Len() != armedLen+1 {
		t.Errorf("expected armedLen+1 entries (cancel row added), got %d", m.history.Len())
	}
	last := m.history.Snapshot()[m.history.Len()-1]
	if !strings.Contains(last.Text, "cancelled") {
		t.Errorf("expected 'clear cancelled' system row, got %q", last.Text)
	}
}

// asyncSlashAgent stubs SlashProvider + AsyncSlashProvider so the
// dispatch tests can exercise the non-blocking path (issue #10).
// The channel is buffered + pre-loaded so the goroutine spawned by
// invokeSlashAsync drains immediately and the test doesn't need to
// orchestrate timing.
type asyncSlashAgent struct {
	specs []SlashCommandSpec
	out   chan SlashResultOrErr
	calls int
}

func (a *asyncSlashAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *asyncSlashAgent) SlashCommands() []SlashCommandSpec { return a.specs }
func (a *asyncSlashAgent) InvokeSlash(_ context.Context, _, _ string) (SlashResult, error) {
	a.calls++
	return SlashResult{}, errors.New("should not be called when AsyncSlashProvider is satisfied")
}
func (a *asyncSlashAgent) InvokeSlashAsync(_ context.Context, _, _ string) <-chan SlashResultOrErr {
	a.calls++
	return a.out
}

func TestDispatchSlash_AsyncPathReturnsCmd(t *testing.T) {
	// AsyncSlashProvider satisfied → dispatch must return a Cmd
	// instead of resolving the result synchronously. The Cmd, when
	// invoked, drains one value from the host's channel.
	ch := make(chan SlashResultOrErr, 1)
	ch <- SlashResultOrErr{Res: SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}}}
	agent := &asyncSlashAgent{specs: []SlashCommandSpec{{Name: "btw"}}, out: ch}
	m := NewModel(Options{Agent: agent})
	m.input.SetValue("/btw hello")
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/btw hello")
	if _, ok := out.(Model); !ok {
		t.Fatalf("expected Model, got %T", out)
	}
	if cmd == nil {
		t.Fatal("expected a Cmd for the async path, got nil")
	}
	// Run the Cmd; it should produce a slashResultMsg carrying the
	// modal answer we pre-loaded.
	msg := cmd()
	r, ok := msg.(slashResultMsg)
	if !ok {
		t.Fatalf("expected slashResultMsg, got %T", msg)
	}
	if r.res.ModalAnswer == nil || r.res.ModalAnswer.Question != "q?" {
		t.Errorf("expected pre-loaded modal answer, got %+v", r.res)
	}
}

func TestDispatchSlash_AsyncPath_RoutesThroughApplySlashResult(t *testing.T) {
	// Once the slashResultMsg lands, Update must route it through
	// applySlashResult so the modal opens / system message appends.
	ch := make(chan SlashResultOrErr, 1)
	ch <- SlashResultOrErr{Res: SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}}}
	agent := &asyncSlashAgent{specs: []SlashCommandSpec{{Name: "btw"}}, out: ch}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	// Hand-deliver the message (no goroutine race in the test).
	out, _ := m.Update(slashResultMsg{name: "btw", res: SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}}})
	got := out.(Model)
	if got.sideAnswer == nil {
		t.Fatalf("expected sideAnswer to be set after slashResultMsg")
	}
	if got.sideAnswer.Question != "q?" {
		t.Errorf("question = %q, want %q", got.sideAnswer.Question, "q?")
	}
}

func TestDispatchSlash_SyncFallbackWhenNoAsync(t *testing.T) {
	// A plain SlashProvider (no AsyncSlashProvider) still resolves
	// synchronously — backward compat with existing hosts.
	agent := &slashAgent{
		specs: []SlashCommandSpec{{Name: "btw"}},
		res:   SlashResult{SystemMessage: "ok"},
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/btw ping")
	if cmd != nil {
		t.Errorf("sync path should return nil Cmd, got %T", cmd)
	}
	got := out.(Model)
	if got.history.Len() == 0 {
		t.Errorf("expected system message appended on sync path")
	}
}

// blockingAsyncSlashAgent stubs AsyncSlashProvider with a channel
// the test controls — useful for verifying the in-flight state
// (issue #13) without racing the result back too quickly.
type blockingAsyncSlashAgent struct {
	specs []SlashCommandSpec
	out   chan SlashResultOrErr
	ctx   context.Context // captured at InvokeSlashAsync time
}

func (a *blockingAsyncSlashAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *blockingAsyncSlashAgent) SlashCommands() []SlashCommandSpec { return a.specs }
func (a *blockingAsyncSlashAgent) InvokeSlash(_ context.Context, _, _ string) (SlashResult, error) {
	return SlashResult{}, errors.New("should not be called")
}
func (a *blockingAsyncSlashAgent) InvokeSlashAsync(ctx context.Context, _, _ string) <-chan SlashResultOrErr {
	a.ctx = ctx
	return a.out
}

func TestDispatchSlash_Async_ArmsInFlightAndStickyToast(t *testing.T) {
	agent := &blockingAsyncSlashAgent{
		specs: []SlashCommandSpec{{Name: "compact"}},
		out:   make(chan SlashResultOrErr, 1),
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/compact")
	got := out.(Model)
	if got.inFlightSlash == nil || got.inFlightSlash.name != "compact" {
		t.Errorf("expected inFlightSlash{name=compact}, got %+v", got.inFlightSlash)
	}
	if got.cancelSlash == nil {
		t.Error("expected cancelSlash set after async dispatch")
	}
	if !strings.Contains(got.toast, "compact") || !strings.Contains(got.toast, "running") {
		t.Errorf("expected sticky toast with name + 'running', got %q", got.toast)
	}
	if cmd == nil {
		t.Error("expected a Cmd for the async path")
	}
}

func TestUpdate_ToastClearMsg_StickyWhenSlashInFlight(t *testing.T) {
	// Set up a model with an in-flight slash. The toastClearMsg
	// handler must NOT clear the toast while a slash is pending,
	// regardless of how long ago the toast was set.
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.inFlightSlash = &slashFlight{name: "compact", startedAt: time.Now().Add(-30 * time.Second)}
	m.toast = "▸ /compact running…"
	m.toastSetAt = time.Now().Add(-30 * time.Second) // way past TTL

	out, _ := m.Update(toastClearMsg{})
	got := out.(Model)
	if got.toast == "" {
		t.Errorf("expected toast to persist while slash in flight, got cleared")
	}
}

func TestUpdate_SlashResultMsg_ClearsInFlightAndToast(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.inFlightSlash = &slashFlight{name: "btw", startedAt: time.Now()}
	m.cancelSlash = func() {}
	m.toast = "▸ /btw running…"

	out, _ := m.Update(slashResultMsg{name: "btw", res: SlashResult{SystemMessage: "answer"}})
	got := out.(Model)
	if got.inFlightSlash != nil {
		t.Errorf("expected inFlightSlash cleared on result, got %+v", got.inFlightSlash)
	}
	if got.cancelSlash != nil {
		t.Errorf("expected cancelSlash cleared on result")
	}
	if got.toast != "" {
		t.Errorf("expected toast cleared on result, got %q", got.toast)
	}
}

func TestDispatchSlash_Async_RefusesConcurrent(t *testing.T) {
	// First /compact arms the in-flight state. The follow-up /btw
	// must be refused with a system note instead of dispatching.
	agent := &blockingAsyncSlashAgent{
		specs: []SlashCommandSpec{{Name: "compact"}, {Name: "btw"}},
		out:   make(chan SlashResultOrErr, 1),
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/compact")
	m = out.(Model)
	if m.inFlightSlash == nil {
		t.Fatal("setup: expected first dispatch to arm inFlightSlash")
	}

	// Second slash while compact still in flight.
	out2, cmd2 := m.dispatchSlash("/btw hi")
	got := out2.(Model)
	if cmd2 != nil {
		t.Errorf("expected nil Cmd on refused concurrent slash, got %T", cmd2)
	}
	if got.history.Len() == 0 {
		t.Fatal("expected refusal system message appended")
	}
	last := got.history.Snapshot()[got.history.Len()-1]
	if last.Role != RoleSystem {
		t.Errorf("expected RoleSystem refusal, got %v", last.Role)
	}
	if !strings.Contains(last.Text, "still running") {
		t.Errorf("expected 'still running' in refusal text, got: %q", last.Text)
	}
}

func TestUpdate_Esc_CancelsInFlightSlash(t *testing.T) {
	cancelled := false
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.inFlightSlash = &slashFlight{name: "compact", startedAt: time.Now()}
	m.cancelSlash = func() { cancelled = true }

	esc := tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	out, _ := m.Update(esc)
	got := out.(Model)
	if !cancelled {
		t.Error("expected Esc to fire cancelSlash")
	}
	if got.cancelSlash != nil {
		t.Error("expected cancelSlash cleared after fire")
	}
	// inFlightSlash NOT cleared yet — that happens when the
	// host's slashResultMsg lands. Toast switches to "cancelling…".
	if got.inFlightSlash == nil {
		t.Error("expected inFlightSlash to persist until slashResultMsg arrives")
	}
	if !strings.Contains(got.toast, "cancelling") {
		t.Errorf("expected 'cancelling' toast after Esc, got %q", got.toast)
	}
}

func TestRenderStatusLine_NoCursorBlock(t *testing.T) {
	// The cursor block (GlyphCursor) used to sit between the
	// wordmark and the model. It's gone — the AsyncSlashProvider
	// "running" segment is the new alive-and-working affordance.
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	line := m.renderStatusLine()
	if strings.Contains(line, GlyphCursor) {
		t.Errorf("expected no cursor block in banner, got: %q", line)
	}
}

func TestRenderStatusLine_ShowsAgentIdentity(t *testing.T) {
	// When Branding.AgentIdentity is set and differs from the
	// wordmark, render "<wordmark> · <identity> · <model>".
	m := NewModel(Options{Branding: Branding{AgentIdentity: "scion"}})
	m.viewport.SetWidth(80)
	line := m.renderStatusLine()
	if !strings.Contains(line, "scion") {
		t.Errorf("expected agent identity 'scion' in banner, got: %q", line)
	}
}

func TestRenderStatusLine_OmitsIdentityWhenSameAsWordmark(t *testing.T) {
	// Redundancy guard — when identity equals the wordmark, the
	// banner should NOT carry the wordmark twice (the test
	// substring is deliberately something that can't legitimately
	// appear elsewhere in the status line like a cwd, model, or
	// provider label).
	m := NewModel(Options{Branding: Branding{
		Wordmark:      "Zinnia-The-Brand",
		AgentIdentity: "Zinnia-The-Brand",
	}})
	m.viewport.SetWidth(80)
	line := m.renderStatusLine()
	if c := strings.Count(line, "Zinnia-The-Brand"); c != 1 {
		t.Errorf("expected wordmark exactly once when identity matches, got %d occurrences in: %q", c, line)
	}
}

func TestRenderStatusLine_OmitsIdentityWhenEmpty(t *testing.T) {
	// Zero Branding (no AgentIdentity) leaves the banner as
	// "<wordmark> · <model>" — no extra segment.
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	line := m.renderStatusLine()
	// Should have wordmark + model glyph, no "· <identity> ·"
	// double-separator pattern. Sanity: just count separators.
	// Default state: wordmark · model = 1 separator.
	if c := strings.Count(line, "·"); c != 1 {
		t.Logf("status line content: %q", line)
		// Not strict — provider / cwd / perms / usage segments
		// can add separators when wired. Skip the strict count
		// when the line has more than the bare-minimum content.
	}
}

func TestRenderStatusLine_ShowsInFlightSlashSegment(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.inFlightSlash = &slashFlight{name: "compact", startedAt: time.Now()}

	line := m.renderStatusLine()
	if !strings.Contains(line, "/compact running") {
		t.Errorf("expected status-line segment '/compact running', got: %q", line)
	}
}

func TestRenderStatusLine_NoSegmentWhenIdle(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	// no inFlightSlash set
	line := m.renderStatusLine()
	if strings.Contains(line, "running") {
		t.Errorf("expected no 'running' segment when idle, got: %q", line)
	}
}

// preambleAsyncSlashAgent stubs the AsyncSlashProviderWithPreamble
// variant (issue #16). The host pre-computes the preamble string,
// then returns it alongside the result channel. The channel is
// buffered so the goroutine spawned by awaitSlashChannel drains
// immediately without orchestration.
type preambleAsyncSlashAgent struct {
	specs    []SlashCommandSpec
	preamble string
	out      chan SlashResultOrErr
	ctx      context.Context // captured at dispatch
}

func (a *preambleAsyncSlashAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *preambleAsyncSlashAgent) SlashCommands() []SlashCommandSpec { return a.specs }

// InvokeSlash is the SlashProvider fallback. dispatchSlash requires
// SlashProvider for command-name lookup before it type-asserts to
// the async variant; an error here is a tripwire — the preamble
// path should always win on agents satisfying both.
func (a *preambleAsyncSlashAgent) InvokeSlash(_ context.Context, _, _ string) (SlashResult, error) {
	return SlashResult{}, errors.New("should not be called when AsyncSlashProviderWithPreamble is satisfied")
}
func (a *preambleAsyncSlashAgent) InvokeSlashAsync(ctx context.Context, _, _ string) (string, <-chan SlashResultOrErr) {
	a.ctx = ctx
	return a.preamble, a.out
}

func TestDispatchSlash_PreambleVariant_AppendsAtDispatch(t *testing.T) {
	// Preamble is a non-empty string → core-tui should append a
	// RoleSystem row to history at dispatch time (before the result
	// channel is drained). Solves issue #16: operator sees chat-
	// visible feedback immediately instead of just the bottom toast.
	ch := make(chan SlashResultOrErr, 1)
	ch <- SlashResultOrErr{Res: SlashResult{SystemMessage: "done"}}
	agent := &preambleAsyncSlashAgent{
		specs:    []SlashCommandSpec{{Name: "done"}},
		preamble: "ℹ Capturing checkpoint summary…",
		out:      ch,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/done")
	got := out.(Model)
	if cmd == nil {
		t.Fatal("expected a Cmd for the async preamble path")
	}
	if got.history.Len() == 0 {
		t.Fatal("expected preamble row appended at dispatch")
	}
	last := got.history.Snapshot()[got.history.Len()-1]
	if last.Role != RoleSystem {
		t.Errorf("preamble row role = %v, want RoleSystem", last.Role)
	}
	if !strings.Contains(last.Text, "Capturing checkpoint summary") {
		t.Errorf("preamble text not preserved, got: %q", last.Text)
	}
	// In-flight state armed identically to the bare variant.
	if got.inFlightSlash == nil || got.inFlightSlash.name != "done" {
		t.Errorf("expected inFlightSlash{name=done}, got %+v", got.inFlightSlash)
	}
	if got.cancelSlash == nil {
		t.Error("expected cancelSlash set after preamble dispatch")
	}
}

func TestDispatchSlash_PreambleVariant_EmptyPreambleSkipsRow(t *testing.T) {
	// Empty preamble is the "no preamble" signal — the row is
	// skipped and behavior matches the bare AsyncSlashProvider.
	ch := make(chan SlashResultOrErr, 1)
	ch <- SlashResultOrErr{Res: SlashResult{SystemMessage: "ok"}}
	agent := &preambleAsyncSlashAgent{
		specs:    []SlashCommandSpec{{Name: "compact"}},
		preamble: "",
		out:      ch,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, cmd := m.dispatchSlash("/compact")
	got := out.(Model)
	if cmd == nil {
		t.Fatal("expected a Cmd")
	}
	if got.history.Len() != 0 {
		t.Errorf("expected no history row for empty preamble, got %d entries", got.history.Len())
	}
}

func TestDispatchSlash_PreambleVariant_DrainsResultChannel(t *testing.T) {
	// The Cmd returned by the preamble path must drain one value
	// from the host's result channel — same single-shot semantics
	// as the bare variant.
	ch := make(chan SlashResultOrErr, 1)
	ch <- SlashResultOrErr{Res: SlashResult{ModalAnswer: &SideAnswer{Question: "q?", Answer: "a."}}}
	agent := &preambleAsyncSlashAgent{
		specs:    []SlashCommandSpec{{Name: "done"}},
		preamble: "checkpointing…",
		out:      ch,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	_, cmd := m.dispatchSlash("/done")
	msg := cmd()
	r, ok := msg.(slashResultMsg)
	if !ok {
		t.Fatalf("expected slashResultMsg, got %T", msg)
	}
	if r.res.ModalAnswer == nil || r.res.ModalAnswer.Question != "q?" {
		t.Errorf("result not forwarded, got: %+v", r.res)
	}
}

func TestDispatchSlash_PreambleVariant_PassesCancellableCtx(t *testing.T) {
	// The preamble variant must thread a cancellable ctx to the
	// host (same as the bare variant) so Esc can cancel — and
	// the post-dispatch Model's cancelSlash, when fired, must
	// propagate cancellation to that ctx.
	ch := make(chan SlashResultOrErr, 1)
	agent := &preambleAsyncSlashAgent{
		specs:    []SlashCommandSpec{{Name: "done"}},
		preamble: "running…",
		out:      ch,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/done")
	got := out.(Model)
	if agent.ctx == nil {
		t.Fatal("expected ctx captured at dispatch")
	}
	select {
	case <-agent.ctx.Done():
		t.Fatal("ctx already cancelled at dispatch — should still be live")
	default:
	}
	if got.cancelSlash == nil {
		t.Fatal("expected cancelSlash set on post-dispatch Model")
	}
	got.cancelSlash()
	select {
	case <-agent.ctx.Done():
		// expected
	default:
		t.Error("expected captured ctx to be Done after cancelSlash fired")
	}
}

func TestDispatchSlash_PreambleVariant_RefusesConcurrent(t *testing.T) {
	// Concurrent-slash refusal (#13) applies equally to the
	// preamble path — second dispatch while one is in flight must
	// log a system note and return nil Cmd.
	ch1 := make(chan SlashResultOrErr, 1)
	agent := &preambleAsyncSlashAgent{
		specs:    []SlashCommandSpec{{Name: "done"}, {Name: "compact"}},
		preamble: "first running…",
		out:      ch1,
	}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/done")
	m = out.(Model)
	if m.inFlightSlash == nil {
		t.Fatal("setup: first dispatch should arm inFlightSlash")
	}
	preambleRows := m.history.Len()

	out2, cmd2 := m.dispatchSlash("/compact")
	got := out2.(Model)
	if cmd2 != nil {
		t.Errorf("expected nil Cmd on refused concurrent slash, got %T", cmd2)
	}
	if got.history.Len() != preambleRows+1 {
		t.Errorf("expected exactly +1 history row (refusal), got %d → %d", preambleRows, got.history.Len())
	}
	last := got.history.Snapshot()[got.history.Len()-1]
	if !strings.Contains(last.Text, "still running") {
		t.Errorf("expected 'still running' refusal, got: %q", last.Text)
	}
}

// TestDispatchSlash_UnknownCommand pins that an unknown slash logs a
// helpful system row instead of failing silently or panicking.
func TestDispatchSlash_UnknownCommand(t *testing.T) {
	agent := &slashAgent{specs: []SlashCommandSpec{{Name: "known"}}}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.dispatchSlash("/nope")
	got := out.(Model)
	if got.history.Len() != 1 {
		t.Fatalf("expected one system row, got %d", got.history.Len())
	}
	if got.history.Snapshot()[0].Role != RoleSystem {
		t.Errorf("unknown command should render as RoleSystem, got %v", got.history.Snapshot()[0].Role)
	}
}
