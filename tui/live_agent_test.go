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

// liveAgentStub implements LiveAgent for tests. The Events channel
// is exposed so tests can push events / errors and close cleanly.
// Run is the existing Pattern-A entry point; the stub also
// implements it so we can verify LiveAgent precedence (Run must be
// silently skipped when LiveAgent is satisfied).
type liveAgentStub struct {
	events    chan eventOrErr
	runCalled bool
}

type eventOrErr struct {
	ev  Event
	err error
}

func (a *liveAgentStub) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	a.runCalled = true
	return func(_ func(Event, error) bool) {}
}

func (a *liveAgentStub) Events(ctx context.Context) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		for {
			select {
			case <-ctx.Done():
				return
			case pair, ok := <-a.events:
				if !ok {
					return // iterator end
				}
				if !yield(pair.ev, pair.err) {
					return
				}
			}
		}
	}
}

// injectableLiveAgentStub adds InjectableAgent on top of LiveAgent.
// The injects channel is on the wrapper so tests can read what
// was injected without reaching through the embedded type.
type injectableLiveAgentStub struct {
	*liveAgentStub
	injectsOut chan string
}

func (a *injectableLiveAgentStub) Inject(msg string) error {
	a.injectsOut <- msg
	return nil
}

func newLiveAgentStub() *liveAgentStub {
	return &liveAgentStub{events: make(chan eventOrErr, 16)}
}

func TestNewModel_DetectsLiveAgent(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	if !m.liveMode {
		t.Errorf("expected liveMode=true when Agent satisfies LiveAgent")
	}
}

func TestNewModel_NoLiveAgentLeavesLiveModeOff(t *testing.T) {
	m := NewModel(Options{Agent: &slashAgent{}}) // Run-only agent
	if m.liveMode {
		t.Errorf("expected liveMode=false for plain Agent")
	}
}

func TestLiveStream_DrainsEventsIntoChannel(t *testing.T) {
	agent := newLiveAgentStub()
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	cancel := m.startLiveStream(agent)
	defer cancel()

	// Push two events; expect both to surface via m.eventCh as the
	// usual streamChunkMsg / toolCallMsg etc.
	agent.events <- eventOrErr{ev: Event{Text: "hello", Partial: true}}
	agent.events <- eventOrErr{ev: Event{Text: "world", Partial: false}}

	got1 := drainOne(t, m.eventCh)
	got2 := drainOne(t, m.eventCh)

	if c, ok := got1.(streamChunkMsg); !ok || c.text != "hello" || !c.partial {
		t.Errorf("first msg = %+v, want streamChunkMsg{text=hello, partial=true}", got1)
	}
	if c, ok := got2.(streamChunkMsg); !ok || c.text != "world" || c.partial {
		t.Errorf("second msg = %+v, want streamChunkMsg{text=world, partial=false}", got2)
	}
}

func TestLiveStream_SurfacesErrorAndKeepsDraining(t *testing.T) {
	agent := newLiveAgentStub()
	m := NewModel(Options{Agent: agent})

	cancel := m.startLiveStream(agent)
	defer cancel()

	agent.events <- eventOrErr{err: errors.New("boom")}
	agent.events <- eventOrErr{ev: Event{Text: "still here", Partial: false}}

	gotErr := drainOne(t, m.eventCh)
	gotEv := drainOne(t, m.eventCh)

	if e, ok := gotErr.(liveStreamErrMsg); !ok || e.err == nil || e.err.Error() != "boom" {
		t.Errorf("expected liveStreamErrMsg{err=boom}, got %+v", gotErr)
	}
	if c, ok := gotEv.(streamChunkMsg); !ok || c.text != "still here" {
		t.Errorf("expected drain to continue after error, got %+v", gotEv)
	}
}

func TestLiveStream_EndedSignalOnIteratorClose(t *testing.T) {
	agent := newLiveAgentStub()
	m := NewModel(Options{Agent: agent})

	cancel := m.startLiveStream(agent)
	defer cancel()

	close(agent.events)
	got := drainOne(t, m.eventCh)
	if _, ok := got.(liveStreamEndedMsg); !ok {
		t.Errorf("expected liveStreamEndedMsg after iterator close, got %T", got)
	}
}

func TestLiveStream_CtxCancelStopsWithoutFinalError(t *testing.T) {
	agent := newLiveAgentStub()
	m := NewModel(Options{Agent: agent})

	cancel := m.startLiveStream(agent)
	cancel() // stop the stream

	// Push an event AFTER cancel — should not surface.
	agent.events <- eventOrErr{ev: Event{Text: "stale", Partial: false}}

	select {
	case got := <-m.eventCh:
		// If something arrives quickly, must NOT be the stale event.
		if _, ok := got.(liveStreamEndedMsg); ok {
			return // acceptable — clean iterator-return after cancel
		}
		if c, ok := got.(streamChunkMsg); ok && c.text == "stale" {
			t.Errorf("expected no event after cancel, got stale chunk")
		}
	case <-time.After(50 * time.Millisecond):
		// No message — also acceptable; cancel stopped the goroutine quietly.
	}
}

func TestUpdate_LiveStreamStartedMsg_LogsAttachedNote(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	out, _ := m.Update(liveStreamStartedMsg{cancel: func() {}})
	got := out.(Model)
	if got.cancelLiveStream == nil {
		t.Error("expected cancelLiveStream stashed from message")
	}
	if got.history.Len() == 0 {
		t.Fatal("expected attached-as-observer system row")
	}
	last := got.history.Snapshot()[got.history.Len()-1]
	if !strings.Contains(last.Text, "observer") {
		t.Errorf("expected 'observer' in attached-note text, got: %q", last.Text)
	}
}

// Issue #50: hosts that implement InjectableAgent alongside LiveAgent
// have operator typing feed the running stream — the "observer" framing
// is wrong for them. The banner text must branch on the capability.
func TestUpdate_LiveStreamStartedMsg_InjectableAgent_DropsObserverFraming(t *testing.T) {
	agent := &injectableLiveAgentStub{liveAgentStub: newLiveAgentStub(), injectsOut: make(chan string, 1)}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	out, _ := m.Update(liveStreamStartedMsg{cancel: func() {}})
	got := out.(Model)
	if got.history.Len() == 0 {
		t.Fatal("expected banner system row")
	}
	last := got.history.Snapshot()[got.history.Len()-1]
	if strings.Contains(last.Text, "observer") {
		t.Errorf("InjectableAgent host should not see 'observer' framing, got: %q", last.Text)
	}
	if !strings.Contains(last.Text, "Live session") {
		t.Errorf("expected 'Live session' framing for InjectableAgent host, got: %q", last.Text)
	}
}

func TestUpdate_LiveStreamEndedMsg_FlipsDisconnectedAndLogs(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	out, _ := m.Update(liveStreamEndedMsg{})
	got := out.(Model)
	if !got.liveDisconnected {
		t.Error("expected liveDisconnected=true after end signal")
	}
	last := got.history.Snapshot()[got.history.Len()-1]
	if !strings.Contains(last.Text, "Disconnected") {
		t.Errorf("expected 'Disconnected' system row, got: %q", last.Text)
	}
}

func TestUpdate_LiveStreamErrMsg_RendersErrorRow(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	out, cmd := m.Update(liveStreamErrMsg{err: errors.New("boom")})
	got := out.(Model)
	last := got.history.Snapshot()[got.history.Len()-1]
	if last.Role != RoleError {
		t.Errorf("expected RoleError, got %v", last.Role)
	}
	if !strings.Contains(last.Text, "boom") {
		t.Errorf("expected 'boom' in error row, got: %q", last.Text)
	}
	if !strings.Contains(last.Text, "waiting to reconnect") {
		t.Errorf("transient error should carry reconnect hint, got: %q", last.Text)
	}
	if got.liveDisconnected {
		t.Error("transient error must not flip liveDisconnected")
	}
	if cmd == nil {
		t.Error("transient error should keep draining (cmd != nil)")
	}
}

// Issue #51: HTTP 404 / 401 / 403 are permanent conditions — the TUI
// must NOT loop forever hitting the reconnect path. Verify that the
// handler flips the disconnected bit, surfaces a distinct row, and
// returns no drain cmd for each permanent status marker.
func TestUpdate_LiveStreamErrMsg_PermanentStatuses_StopRetrying(t *testing.T) {
	permanent := []struct {
		name string
		err  error
	}{
		{"404", errors.New("attach: status 404: session not found")},
		{"401", errors.New("attach: status 401: token revoked")},
		{"403", errors.New("attach: status 403: acl revoked")},
	}
	for _, tc := range permanent {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(Options{Agent: newLiveAgentStub()})
			m.viewport.SetWidth(80)

			out, cmd := m.Update(liveStreamErrMsg{err: tc.err})
			got := out.(Model)
			last := got.history.Snapshot()[got.history.Len()-1]
			if last.Role != RoleError {
				t.Errorf("expected RoleError, got %v", last.Role)
			}
			if !strings.Contains(last.Text, "session unavailable") {
				t.Errorf("permanent error should read 'session unavailable', got: %q", last.Text)
			}
			if strings.Contains(last.Text, "waiting to reconnect") {
				t.Errorf("permanent error must not offer reconnect hint, got: %q", last.Text)
			}
			if !got.liveDisconnected {
				t.Error("expected liveDisconnected=true after permanent error")
			}
			if cmd != nil {
				t.Error("expected nil cmd (stop retrying) on permanent error")
			}
		})
	}
}

// Verify the PermanentStreamError interface path — adapters that wrap
// their own typed errors bypass the substring heuristic.
type permErr struct{ msg string }

func (e *permErr) Error() string            { return e.msg }
func (e *permErr) PermanentStreamErr() bool { return true }

func TestUpdate_LiveStreamErrMsg_PermanentStreamErrorInterface(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	out, cmd := m.Update(liveStreamErrMsg{err: &permErr{msg: "adapter-specific: session evicted"}})
	got := out.(Model)
	last := got.history.Snapshot()[got.history.Len()-1]
	if !got.liveDisconnected {
		t.Error("PermanentStreamError should flip liveDisconnected")
	}
	if cmd != nil {
		t.Error("PermanentStreamError should stop draining")
	}
	if !strings.Contains(last.Text, "session unavailable") {
		t.Errorf("expected 'session unavailable' prefix, got: %q", last.Text)
	}
}

// isPermanentStreamErr unit table — covers nil, no-match, each marker,
// and the interface path.
func TestIsPermanentStreamErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transient", errors.New("connection refused"), false},
		{"transient-5xx", errors.New("server error: status 502"), false},
		{"404-marker", errors.New("attach: status 404: session not found"), true},
		{"401-marker", errors.New("status 401 unauthorized"), true},
		{"403-marker", errors.New("some prefix status 403 forbidden"), true},
		{"interface", &permErr{msg: "adapter-permanent"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanentStreamErr(tc.err); got != tc.want {
				t.Errorf("isPermanentStreamErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestApplyStreamChunk_LiveMode_PartialFlipsSpinnerAndStamps(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	m.applyStreamChunk(streamChunkMsg{text: "tok", partial: true})
	if !m.spinnerActive {
		t.Error("expected spinnerActive=true after partial in liveMode")
	}
	if m.liveLastPartialAt.IsZero() {
		t.Error("expected liveLastPartialAt stamped")
	}
}

func TestApplyStreamChunk_LiveMode_NonPartialCommitsAndStopsSpinner(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	// Build up some partial text first.
	m.applyStreamChunk(streamChunkMsg{text: "hello ", partial: true})
	m.applyStreamChunk(streamChunkMsg{text: "world", partial: true})
	if !m.spinnerActive {
		t.Fatal("setup: spinner should be active mid-stream")
	}
	// Commit: non-partial flushes inProgressText into history as
	// a finalized assistant row and stops the spinner.
	m.applyStreamChunk(streamChunkMsg{text: "hello world", partial: false})
	if m.spinnerActive {
		t.Error("expected spinnerActive=false after non-partial commit")
	}
	if m.inProgressText != "" {
		t.Errorf("expected inProgressText cleared after commit, got %q", m.inProgressText)
	}
	if m.history.Len() == 0 {
		t.Fatal("expected committed assistant row in history")
	}
	last := m.history.Snapshot()[m.history.Len()-1]
	if last.Role != RoleAssistant || last.Text != "hello world" {
		t.Errorf("expected RoleAssistant row with committed text, got %+v", last)
	}
}

func TestUpdate_SubmitInLiveMode_NoInjectLogsReadOnlyNoteOnce(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	// First submit logs the note.
	m.input.SetValue("hello")
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out, _ := m.Update(enter)
	m = out.(Model)
	if !m.liveReadOnlyNoted {
		t.Fatal("expected liveReadOnlyNoted=true after first submit")
	}
	first := m.history.Snapshot()
	noteCount := 0
	for _, e := range first {
		if e.Role == RoleSystem && strings.Contains(e.Text, "Read-only view") {
			noteCount++
		}
	}
	if noteCount != 1 {
		t.Errorf("expected exactly 1 read-only system row, got %d", noteCount)
	}

	// Second submit must NOT log a duplicate note.
	m.input.SetValue("again")
	out, _ = m.Update(enter)
	m = out.(Model)
	second := m.history.Snapshot()
	noteCount = 0
	for _, e := range second {
		if e.Role == RoleSystem && strings.Contains(e.Text, "Read-only view") {
			noteCount++
		}
	}
	if noteCount != 1 {
		t.Errorf("expected still exactly 1 read-only system row after second submit, got %d", noteCount)
	}
}

func TestUpdate_SubmitInLiveMode_InjectableHostCallsInjectAndAppendsUserRow(t *testing.T) {
	inner := newLiveAgentStub()
	agent := &injectableLiveAgentStub{liveAgentStub: inner, injectsOut: make(chan string, 1)}
	m := NewModel(Options{Agent: agent})
	m.viewport.SetWidth(80)

	m.input.SetValue("hello there")
	enter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	out, _ := m.Update(enter)
	got := out.(Model)

	select {
	case v := <-agent.injectsOut:
		if v != "hello there" {
			t.Errorf("Inject called with %q, want %q", v, "hello there")
		}
	default:
		t.Fatal("expected Inject called")
	}
	// User row should be rendered so operator sees what they sent.
	last := got.history.Snapshot()[got.history.Len()-1]
	if last.Role != RoleUser || last.Text != "hello there" {
		t.Errorf("expected RoleUser row with typed text, got %+v", last)
	}
}

// drainOne pulls one tea.Msg off ch with a short timeout. Fails
// the test if nothing arrives — keeps the suite tight when a
// behavior regresses.
func drainOne(t *testing.T, ch chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}
