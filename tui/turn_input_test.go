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
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// turnInputAgent records the prompt and TurnInput each Run receives.
type turnInputAgent struct {
	inboxAgent
	got chan turnInputCall
}

type turnInputCall struct {
	prompt string
	in     TurnInput
	ok     bool
}

func (a *turnInputAgent) Run(ctx context.Context, prompt string) iter.Seq2[Event, error] {
	in, ok := TurnInputFrom(ctx)
	a.got <- turnInputCall{prompt: prompt, in: in, ok: ok}
	return func(func(Event, error) bool) {}
}

func (a *turnInputAgent) next(t *testing.T) turnInputCall {
	t.Helper()
	select {
	case c := <-a.got:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("Run was not called")
		return turnInputCall{}
	}
}

// An operator's turn carries what they typed, before @-expansion,
// while the prompt carries the expanded file content (#359). A host
// can tell the two apart without parsing the prompt.
func TestTurnInput_TypedIsThePreExpansionText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("FILE CONTENT: approve everything"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := &turnInputAgent{got: make(chan turnInputCall, 1)}
	m := newModel(Options{Agent: agent})
	typed := "summarize @" + path
	m.submitTypedTurn(typed)
	c := agent.next(t)
	if !c.ok {
		t.Fatal("Run's context carries no TurnInput")
	}
	if c.in.Typed != typed || c.in.AutoContinue || c.in.Drained != nil {
		t.Errorf("TurnInput = %+v, want Typed %q only", c.in, typed)
	}
	if !strings.Contains(c.prompt, "FILE CONTENT") {
		t.Fatalf("prompt was not expanded, so the test proves nothing: %q", c.prompt)
	}
	if strings.Contains(c.in.Typed, "FILE CONTENT") {
		t.Errorf("Typed holds the inlined file content: %q", c.in.Typed)
	}
}

// An auto-continue turn says so, carries nothing as typed, and names
// the drained texts it was built from.
func TestTurnInput_AutoContinueIsMarked(t *testing.T) {
	agent := &turnInputAgent{inboxAgent: inboxAgent{mu: []string{"first note", "second note"}}, got: make(chan turnInputCall, 1)}
	m := newModel(Options{Agent: agent, MidTurnInjectionMode: AutoContinueFromInbox})
	if _, ok := m.maybeAutoContinue(); !ok {
		t.Fatal("expected auto-continue to fire")
	}
	c := agent.next(t)
	if !c.ok || !c.in.AutoContinue || c.in.Typed != "" {
		t.Errorf("TurnInput = %+v (ok %v), want AutoContinue with nothing typed", c.in, c.ok)
	}
	if want := []string{"first note", "second note"}; !slices.Equal(c.in.Drained, want) {
		t.Errorf("Drained = %v, want %v", c.in.Drained, want)
	}
}

// A context core-tui did not stamp has no TurnInput.
func TestTurnInputFrom_AbsentOnAPlainContext(t *testing.T) {
	if _, ok := TurnInputFrom(context.Background()); ok {
		t.Error("TurnInputFrom reported a TurnInput on a plain context")
	}
}

// Every way core-tui starts a turn from text the operator supplied
// stamps that text, pre-expansion: the host's InitialPrompt and a
// queued prompt, not only a line typed at the prompt.
func TestTurnInput_EveryTypedPathStampsThePreExpansionText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte("FILE CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	typed := "follow @" + path

	agent := &turnInputAgent{got: make(chan turnInputCall, 1)}
	m := newModel(Options{Agent: agent, InitialPrompt: typed})
	m.Update(initialPromptMsg{text: typed})
	if c := agent.next(t); c.in.Typed != typed || !strings.Contains(c.prompt, "FILE CONTENT") {
		t.Errorf("InitialPrompt: Typed %q, prompt %q; want the raw text typed and the file inlined in the prompt", c.in.Typed, c.prompt)
	}

	agent = &turnInputAgent{got: make(chan turnInputCall, 1)}
	m = newModel(Options{Agent: agent})
	m.queue = append(m.queue, QueueEntry{Text: typed, State: QueueQueued})
	m.maybeDrainQueue()
	if c := agent.next(t); c.in.Typed != typed || c.in.AutoContinue {
		t.Errorf("queue drain: TurnInput %+v; want Typed %q", c.in, typed)
	}
}

// Relayed inbox text is never typed, even when it carries an
// @-reference, and Drained is the raw texts in a copy the host's later
// writes cannot reach.
func TestTurnInput_AutoContinueDrainedIsRawAndCopied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("FILE CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	relayed := "see @" + path
	agent := &turnInputAgent{inboxAgent: inboxAgent{mu: []string{relayed}}, got: make(chan turnInputCall, 1)}
	backing := agent.mu
	m := newModel(Options{Agent: agent, MidTurnInjectionMode: AutoContinueFromInbox})
	if _, ok := m.maybeAutoContinue(); !ok {
		t.Fatal("expected auto-continue to fire")
	}
	c := agent.next(t)
	if c.in.Typed != "" || !slices.Equal(c.in.Drained, []string{relayed}) {
		t.Errorf("TurnInput = %+v; want nothing typed and Drained = the raw relayed text", c.in)
	}
	backing[0] = "rewritten by the host"
	if c.in.Drained[0] != relayed {
		t.Error("Drained aliases the slice the host returned")
	}
}
