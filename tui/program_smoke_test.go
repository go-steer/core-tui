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

// Headless tea.Program smoke tests — the third layer of
// docs/design.md §7 (issue #81).
//
// Every other test in this package calls Update(msg) directly. That
// is fast and precise, and it is structurally blind to the program
// lifecycle: Init's Cmd batch, the event loop, the View composition
// path, and teardown. Issue #69 — the View() wedge and the SIGQUIT
// recovery hatch — is the class of bug that lives in exactly that
// gap. These tests run a real tea.Program over an in-memory pipe and
// an in-memory output buffer and assert on the bytes it emits.
//
// The file deliberately sits in `tui_test` rather than `tui`. Partly
// it has to: tui/testagent imports tui, so only the external test
// package can drive the library against the shared fixture the issue
// asks for. Mostly it is the point — every scenario here is written
// against the exported surface a host actually has, which is worth
// having on the eve of a v1.0 API freeze.
//
// Rules for anything added here, learned the expensive way in other
// projects: a smoke test that hangs is worse than no smoke test. So
//
//   - every wait is bounded and fails with the captured output
//     rather than leaning on `go test -timeout`;
//   - synchronisation is by polling the output for the content we
//     are waiting on, never by sleeping a guessed interval. The one
//     sleep in the file waits out a production debounce constant
//     (modalInputGrace) and is re-exported from the package rather
//     than copied, see program_smoke_export_test.go;
//   - the output buffer is written by the program's goroutine and
//     read by the test's, so it is mutex-guarded (`-race` is on in
//     CI on both ubuntu and macOS).
package tui_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/go-steer/core-tui/tui"
	"github.com/go-steer/core-tui/tui/testagent"
)

// Geometry of the fake terminal. Wide enough that none of the tokens
// the assertions look for get word-wrapped mid-token, tall enough
// that the chat, the inline permission block and the composer all
// fit without the viewport scrolling content off before it is
// painted.
const (
	smokeCols = 100
	smokeRows = 30
)

// Time budgets. All four are ceilings on a failure path, not
// expected durations — a healthy run of the whole file finishes well
// inside a second per scenario. They exist so that a wedged program
// produces a named failure with the captured output attached instead
// of a package-level timeout panic ten minutes later.
const (
	// smokeProgramBudget bounds the whole tea.Program via
	// tea.WithContext, so even a test that somehow escapes its own
	// assertions cannot outlive it.
	smokeProgramBudget = 60 * time.Second
	// smokeWaitBudget bounds a single waitFor.
	smokeWaitBudget = 20 * time.Second
	// smokeExitBudget bounds shutdown after the quit keystroke.
	smokeExitBudget = 20 * time.Second
	// smokeWriteBudget bounds a single keystroke write. io.Pipe
	// writes block until the reader consumes them, so a program
	// that has stopped reading stdin would otherwise wedge the test
	// goroutine itself.
	smokeWriteBudget = 10 * time.Second
	// smokePollInterval is how often waitFor re-reads the output.
	smokePollInterval = 2 * time.Millisecond
)

// smokeEnv pins the program's environment. Only TERM is needed for
// key decoding; the point of passing an explicit slice is that the
// color profile is then a function of the harness rather than of
// whoever's CI is running it. The output is an in-memory buffer and
// never a term.File, so colorprofile.Detect resolves to NoTTY and
// the frame lands in the buffer as plain text — which is what makes
// the content assertions below readable. A stray CLICOLOR_FORCE=1 in
// the ambient environment would otherwise upgrade that to ANSI and
// interleave SGR sequences through every token.
var smokeEnv = []string{"TERM=xterm-256color"}

// ---------------------------------------------------------------
// Scenario 1 — startup, prompt, streamed response, /quit, shutdown.
// docs/requirements.md §7 acceptance criterion 2.
// ---------------------------------------------------------------

func TestProgramSmokeStartupStreamQuit(t *testing.T) {
	// Two chunks rather than one: a single-shot reply would not
	// distinguish "the stream rendered" from "the final message
	// rendered". Each chunk ends its own line so the cell-diffing
	// renderer emits it whole rather than as a patch spliced into a
	// line it already painted.
	script := []testagent.Step{
		{Event: tui.Event{Text: "smoke-reply-alpha\n", Partial: true}},
		{Wait: 5 * time.Millisecond, Event: tui.Event{Text: "\nsmoke-reply-omega\n", Partial: true}},
		{Wait: 5 * time.Millisecond, Event: tui.Event{Usage: &tui.Usage{InputTokens: 12, OutputTokens: 34}}},
	}

	r := startProgram(t, tui.Options{Agent: testagent.NewScripted(script)})

	r.waitFor("the startup frame", "core-tui", "Ask me anything to get started.")

	r.submit("smoke-prompt")
	r.waitFor("the submission to render as a user row", "❯ smoke-prompt")

	r.waitFor("the first streamed chunk", "smoke-reply-alpha")
	r.waitFor("the second streamed chunk", "smoke-reply-omega")
	// The per-turn footer only renders once the Usage event — the
	// last step in the script — has been applied, which makes it the
	// deterministic "the turn is over" signal. It doubles as proof
	// that a non-text Event survived the round trip.
	r.waitFor("the per-turn usage footer", "12 in", "34 out")

	r.quitViaSlash()

	if err := r.waitExit(); err != nil {
		t.Fatalf("tea.Program.Run returned %v; want a clean exit.\n%s", err, r.dump())
	}
	r.assertTerminalRestored(true)
}

// ---------------------------------------------------------------
// Scenario 2 — teardown restores the terminal.
// ---------------------------------------------------------------

// TestProgramSmokeTeardownRestoresTerminal is the assertion no
// Update()-level test can make: after the program returns, the byte
// stream must leave the terminal the way it found it. The failure it
// guards against is the one operators actually hit — a TUI that
// exits with the alt screen still up, or with mouse reporting still
// on so every click spews escape codes into their shell.
func TestProgramSmokeTeardownRestoresTerminal(t *testing.T) {
	mouseOff := false

	for _, tc := range []struct {
		name        string
		mouse       *bool
		wantCapture bool
	}{
		{name: "default mouse capture", mouse: nil, wantCapture: true},
		{name: "mouse capture disabled", mouse: &mouseOff, wantCapture: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := startProgram(t, tui.Options{Agent: testagent.New(), Mouse: tc.mouse})
			r.waitFor("the startup frame", "Ask me anything to get started.")
			r.quitViaSlash()

			if err := r.waitExit(); err != nil {
				t.Fatalf("tea.Program.Run returned %v; want a clean exit.\n%s", err, r.dump())
			}
			r.assertTerminalRestored(tc.wantCapture)
		})
	}
}

// ---------------------------------------------------------------
// Scenario 3 — the permission round trip.
// docs/requirements.md §7 acceptance criterion 3.
// ---------------------------------------------------------------

// TestProgramSmokePermissionRoundTrip drives the full gate: a
// scripted tool call inside a streaming turn blocks on
// Prompter.AskApproval, the TUI paints the prompt, the operator's
// keystroke unblocks the agent goroutine, and the turn resumes with
// the outcome the decision implies.
//
// Three of the six R-PERM-2 decisions are covered — allow-once, deny
// and allow-always, the three the issue names. They are the three
// with distinct observable consequences: the other three
// (allow-session, -verb, -tool) differ only in the persistence scope
// the HOST derives from them, and the TUI's own behaviour for all of
// them is byte-identical to allow-once. Both permission layouts run
// every case, because the layouts are two entirely separate render
// paths (renderPermissionInline vs renderPermissionModal) reached
// from the same keystroke dispatch.
func TestProgramSmokePermissionRoundTrip(t *testing.T) {
	layouts := []struct {
		name   string
		layout tui.PermissionLayout
	}{
		{"inline", tui.PermissionInline},
		{"overlay", tui.PermissionOverlay},
	}
	decisions := []struct {
		name     string
		key      string
		wantEcho string
		wantTurn string
		// wantPersist is true for the decision that has to reach the
		// host's AlwaysAllow callback.
		wantPersist bool
	}{
		{name: "allow once", key: "y", wantEcho: "Permission allow-once", wantTurn: "GATE-ALLOWED"},
		{name: "deny", key: "n", wantEcho: "Permission deny", wantTurn: "GATE-DENIED"},
		{name: "allow always", key: "a", wantEcho: "Permission allow-always", wantTurn: "GATE-ALLOWED", wantPersist: true},
	}

	for _, lay := range layouts {
		for _, dec := range decisions {
			t.Run(lay.name+"/"+dec.name, func(t *testing.T) {
				prompter := tui.NewPrompter()
				persisted := make(chan tui.PermissionRequest, 1)

				r := startProgram(t, tui.Options{
					Agent:            newGatedAgent(prompter),
					Prompter:         prompter,
					PermissionLayout: lay.layout,
					AlwaysAllow: func(req tui.PermissionRequest) error {
						persisted <- req
						return nil
					},
				})

				r.waitFor("the startup frame", "Ask me anything to get started.")
				r.submit("gate-me")

				// The gate trips from inside the streaming turn, on
				// the agent's own goroutine, so this wait is the
				// synchronisation point for the whole cross-goroutine
				// handshake.
				r.waitFor("the permission prompt to render",
					"Permission required", gateToolName, gateCommand)

				// The one sleep in this file. modalInputGrace makes
				// the decision keys inert for a fixed window after
				// the prompt appears, so that terminal input already
				// buffered when the agent raised it cannot be read as
				// the answer (issue #95). There is nothing to poll
				// for — the window is a timestamp comparison with no
				// rendered consequence — so the harness waits it out.
				// The wait starts from the moment we OBSERVED the
				// prompt, which is strictly after the model stamped
				// permissionShownAt, so it can only ever be too long.
				time.Sleep(tui.ModalInputGraceForTest + 100*time.Millisecond)

				r.send(dec.key)

				// If the keystroke had been swallowed these two waits
				// would fail on their own budget with the frame
				// attached; there is deliberately no retry.
				r.waitFor("the decision echo in the transcript", dec.wantEcho)
				r.waitFor("the turn to resume past the gate", dec.wantTurn)

				if dec.wantPersist {
					select {
					case req := <-persisted:
						if req.ToolName != gateToolName {
							t.Errorf("AlwaysAllow got ToolName %q, want %q", req.ToolName, gateToolName)
						}
						if req.PersistKey != gatePersistKey {
							t.Errorf("AlwaysAllow got PersistKey %q, want %q", req.PersistKey, gatePersistKey)
						}
					case <-time.After(smokeWaitBudget):
						t.Fatalf("allow-always never reached Options.AlwaysAllow.\n%s", r.dump())
					}
				} else {
					select {
					case req := <-persisted:
						t.Errorf("Options.AlwaysAllow was called for the %s decision with %+v; want no call", dec.name, req)
					default:
					}
				}

				r.quitViaSlash()
				if err := r.waitExit(); err != nil {
					t.Fatalf("tea.Program.Run returned %v; want a clean exit.\n%s", err, r.dump())
				}
				r.assertTerminalRestored(true)
			})
		}
	}
}

// ---------------------------------------------------------------
// Scenario 4 — the listener goroutines do not outlive the program.
// Issue #202.
// ---------------------------------------------------------------

// smokeListenerFrames names the drain-loop Cmds in agentcmd.go that
// park on a channel waiting for the next request / notice / signal /
// event. Each one is a goroutine bubbletea spawned and, by its own
// documented design, will never wait for or cancel: handleCommands
// runs every Cmd in a bare `go func()` and leaks it if it has not
// returned by the time Run does. So whether these goroutines ever end
// is entirely a question of whether the TUI gives them a way out.
//
// Matching on the function name rather than counting goroutines is
// what makes the failure message worth reading: a runtime.NumGoroutine
// delta says a number went up; this says which listener is still
// parked and prints the stack it is parked on.
//
// The entries are substrings rather than whole symbols because the
// compiler decorates the closure's name with wherever it inlined the
// constructor. The same listener shows up as
// `tui.model.wakeListener.func1` when it wasn't inlined and as
// `tui.model.Init.model.eventListener.func2` when it was, so the
// receiver-qualified method name is the longest part that is stable
// across an inlining decision.
var smokeListenerFrames = []string{
	".model.eventListener",
	".model.wakeListener",
	".model.promptListener",
	".model.elicitListener",
	".model.notifyListener",
}

// TestProgramSmokeListenersReleasedOnQuit is the assertion the issue
// asks for, and the reason it says only this layer can see the bug:
// nothing reachable from Update can observe a goroutine that Update
// is not itself running on. It wires every listener the TUI has —
// four opt-in capabilities plus the always-on event drain — runs a
// real program, quits it the way an operator does, and requires that
// all five goroutines have returned.
//
// Before the fix all five park forever: three blocked on
// context.Background(), one on a Notifier channel that only tui.Run
// closes (and only after tea.Program.Run has already returned, which
// this test, like any embedding host, never calls), and one on an
// eventCh that nothing closes at all.
func TestProgramSmokeListenersReleasedOnQuit(t *testing.T) {
	// Attribute goroutines to THIS program. Package tests run
	// sequentially, but runtime.Stack is process-wide and the
	// scenarios above leave their own programs winding down, so the
	// assertion is "every listener this test started has gone" rather
	// than "no listener exists anywhere in the process".
	baseline := parkedListeners()

	agent := &wakingSmokeAgent{wake: make(chan struct{})}
	r := startProgram(t, tui.Options{
		Agent:    agent,
		Prompter: tui.NewPrompter(),
		Elicitor: tui.NewElicitor(),
		Notifier: tui.NewNotifier(),
	})
	r.waitFor("the startup frame", "Ask me anything to get started.")

	// Init arms the listeners concurrently with the first paint, so
	// poll rather than sampling once. This also keeps the scenario
	// from passing vacuously: a listener that was never armed — a
	// capability that stopped being wired, a renamed method — would
	// trivially satisfy the post-quit assertion, so require it to be
	// running before quitting.
	armed := waitForArmedListeners(t, baseline)
	t.Logf("armed listener goroutines: %v", sortedFrames(armed))

	r.quitViaSlash()
	if err := r.waitExit(); err != nil {
		t.Fatalf("tea.Program.Run returned %v; want a clean exit.\n%s", err, r.dump())
	}

	// The goroutines are released by a cancellation that happened on
	// the event loop, so they are already runnable by the time Run
	// returns — but "runnable" is the scheduler's business, not ours,
	// so this is a bounded poll rather than a bare sample. A healthy
	// run clears on the first or second iteration.
	deadline := time.Now().Add(smokeExitBudget)
	for {
		leaked := newListeners(baseline)
		if len(leaked) == 0 {
			return
		}
		if time.Now().After(deadline) {
			var b strings.Builder
			fmt.Fprintf(&b, "%d listener goroutine(s) still parked %s after tea.Program.Run returned; "+
				"bubbletea does not reap them, so they are leaked for the life of the process.\n",
				len(leaked), smokeExitBudget)
			for _, id := range sortedKeys(leaked) {
				fmt.Fprintf(&b, "\n%s\n", leaked[id])
			}
			t.Fatal(b.String())
		}
		time.Sleep(smokePollInterval)
	}
}

// waitForArmedListeners blocks until every listener in
// smokeListenerFrames has a goroutine of its own that was not in the
// baseline, and returns the set of frames it saw. Fails the test
// rather than returning a partial set — a listener that never arms
// makes the scenario's real assertion meaningless.
func waitForArmedListeners(t *testing.T, baseline map[string]string) map[string]bool {
	t.Helper()

	seen := map[string]bool{}
	deadline := time.Now().Add(smokeWaitBudget)
	for {
		for _, block := range newListeners(baseline) {
			for _, frame := range smokeListenerFrames {
				if strings.Contains(block, frame) {
					seen[frame] = true
				}
			}
		}
		if len(seen) == len(smokeListenerFrames) {
			return seen
		}
		if time.Now().After(deadline) {
			var missing []string
			for _, frame := range smokeListenerFrames {
				if !seen[frame] {
					missing = append(missing, frame)
				}
			}
			t.Fatalf("timed out after %s waiting for the listener Cmds to arm; never saw %v running. "+
				"Either a capability stopped being wired from Init or a listener stopped blocking, and "+
				"either way the leak assertion this scenario exists for would be vacuous.",
				smokeWaitBudget, missing)
		}
		time.Sleep(smokePollInterval)
	}
}

// newListeners returns the parked listener goroutines that are not in
// the given baseline, keyed by goroutine id.
func newListeners(baseline map[string]string) map[string]string {
	out := map[string]string{}
	for id, block := range parkedListeners() {
		if _, ok := baseline[id]; !ok {
			out[id] = block
		}
	}
	return out
}

// parkedListeners dumps every goroutine in the process and returns
// the ones sitting inside one of the listener Cmds, keyed by
// goroutine id with the whole stack as the value.
//
// Keying by id rather than by frame is what lets the caller subtract
// a baseline and be sure it is talking about the same goroutine
// rather than a same-named one belonging to a program some earlier
// scenario started.
func parkedListeners() map[string]string {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}

	out := map[string]string{}
	for _, block := range strings.Split(string(buf), "\n\n") {
		if !strings.HasPrefix(block, "goroutine ") {
			continue
		}
		if !containsAny(block, smokeListenerFrames) {
			continue
		}
		id, _, ok := strings.Cut(strings.TrimPrefix(block, "goroutine "), " ")
		if !ok {
			continue
		}
		out[id] = block
	}
	return out
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedFrames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// wakingSmokeAgent is an idle Agent that also claims the
// WakeRequester capability, so that Init arms wakeListener. Its
// channel is deliberately one nobody ever writes to or closes —
// which is the realistic state of a host-owned wake channel at
// shutdown, and the reason that listener needs a second way out.
type wakingSmokeAgent struct {
	wake chan struct{}
}

func (a *wakingSmokeAgent) Run(_ context.Context, _ string) iter.Seq2[tui.Event, error] {
	return func(_ func(tui.Event, error) bool) {}
}

func (a *wakingSmokeAgent) WakeRequested() <-chan struct{} { return a.wake }

// ---------------------------------------------------------------
// The gated agent fixture.
// ---------------------------------------------------------------

const (
	gateToolName   = "SmokeGate"
	gateCommand    = "smoke-gate --apply"
	gatePersistKey = "smoke-gate:apply"
)

// gatedAgent is a scripted turn with a permission gate wired into
// the middle of it, the way a host wires its own: the turn streams
// some text, announces a tool call, and then blocks the agent
// goroutine on PermissionPrompter.AskApproval before it will produce
// a result. What comes out the far side depends on the operator's
// decision, so the assertions can tell "the decision unblocked the
// turn" from "the modal merely closed".
//
// tui/testagent has no equivalent because a gated agent needs a
// handle on the TUI's Prompter, which is a per-run object rather
// than a static script; keeping the fixture here also keeps this
// change to _test.go files.
type gatedAgent struct {
	prompter tui.PermissionPrompter
}

func newGatedAgent(p tui.PermissionPrompter) tui.Agent { return gatedAgent{prompter: p} }

func (g gatedAgent) Run(ctx context.Context, _ string) iter.Seq2[tui.Event, error] {
	return func(yield func(tui.Event, error) bool) {
		if !yield(tui.Event{Text: "Reaching for the gate.\n", Partial: true}, nil) {
			return
		}
		call := tui.ToolCall{ID: "gate-1", Name: gateToolName, Args: map[string]any{"command": gateCommand}}
		if !yield(tui.Event{ToolCalls: []tui.ToolCall{call}}, nil) {
			return
		}

		decision, err := g.prompter.AskApproval(ctx, tui.PermissionRequest{
			Kind:        tui.PermissionKindBash,
			ToolName:    gateToolName,
			Detail:      gateCommand,
			DetailKind:  tui.DetailShell,
			PersistTool: gateToolName,
			PersistKey:  gatePersistKey,
		})
		if err != nil {
			yield(tui.Event{}, err)
			return
		}

		if decision == tui.DecisionDeny {
			yield(tui.Event{Text: "\nGATE-DENIED\n", Partial: true}, nil)
			return
		}
		if !yield(tui.Event{ToolResults: []tui.ToolResult{{
			ID:        call.ID,
			Name:      gateToolName,
			LatencyMs: 7,
			Response:  map[string]any{"stdout": "ok\n", "exit_code": 0},
		}}}, nil) {
			return
		}
		yield(tui.Event{Text: "\nGATE-ALLOWED\n", Partial: true}, nil)
	}
}

// ---------------------------------------------------------------
// Harness.
// ---------------------------------------------------------------

// syncBuffer is an io.Writer the program's render goroutine writes
// and the test goroutine reads. Everything else about it is
// bytes.Buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// program is one headless tea.Program plus the plumbing needed to
// type at it and read what it painted.
type program struct {
	t   *testing.T
	p   *tea.Program
	out *syncBuffer

	// Keystrokes go in through an io.Pipe rather than a
	// bytes.Buffer. A buffer is fine when every key is known up
	// front, but it hits EOF the moment it drains and the input
	// reader shuts down for the rest of the run — which rules out
	// reacting to what the program painted, and this file's whole
	// method is reacting to what the program painted.
	in     *io.PipeWriter
	inRead *io.PipeReader

	cancel   context.CancelFunc
	finished chan struct{}
	runErr   error // written before finished closes; read after
}

func startProgram(t *testing.T, opts tui.Options) *program {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), smokeProgramBudget)
	pr, pw := io.Pipe()

	r := &program{
		t:        t,
		out:      &syncBuffer{},
		in:       pw,
		inRead:   pr,
		cancel:   cancel,
		finished: make(chan struct{}),
	}
	r.p = tea.NewProgram(tui.NewModel(opts),
		tea.WithContext(ctx),
		tea.WithInput(pr),
		tea.WithOutput(r.out),
		tea.WithWindowSize(smokeCols, smokeRows),
		// The harness owns process lifetime; a program-installed
		// SIGINT handler would be a global side effect shared with
		// every other test in the binary.
		tea.WithoutSignalHandler(),
		tea.WithEnvironment(smokeEnv),
	)

	go func() {
		_, err := r.p.Run()
		r.runErr = err
		close(r.finished)
	}()

	t.Cleanup(r.stop)
	return r
}

// stop tears the program down at the end of a test, whether it
// exited on its own or not. Idempotent-safe: on the happy path
// everything here is already true and it returns immediately.
func (r *program) stop() {
	r.cancel()
	_ = r.in.Close()
	_ = r.inRead.Close()

	select {
	case <-r.finished:
		return
	case <-time.After(smokeExitBudget):
	}

	r.p.Kill()
	select {
	case <-r.finished:
		r.t.Errorf("tea.Program only exited after Kill; cancelling its context was not enough.\n%s", r.dump())
	case <-time.After(smokeExitBudget):
		r.t.Errorf("tea.Program.Run never returned, even after Kill; leaking its goroutine.\n%s", r.dump())
	}
}

// send types keys at the program. Bounded, because an io.Pipe write
// blocks until the reader takes the bytes and a program that has
// stopped reading stdin would otherwise wedge the test goroutine
// where no budget can see it.
func (r *program) send(keys string) {
	r.t.Helper()

	written := make(chan error, 1)
	go func() {
		_, err := r.in.Write([]byte(keys))
		written <- err
	}()

	select {
	case err := <-written:
		if err != nil {
			r.t.Fatalf("writing %q to the program's stdin: %v\n%s", keys, err, r.dump())
		}
	case <-time.After(smokeWriteBudget):
		r.t.Fatalf("timed out after %s writing %q to the program's stdin — its input reader is not draining.\n%s",
			smokeWriteBudget, keys, r.dump())
	}
}

// submit types a prompt and presses Enter in a single write.
//
// It is one write rather than "type, assert the echo, press Enter"
// on purpose. The renderer paints diffs, so a word typed into a line
// that already had content on it reaches the output buffer split
// across however many repaints the keystrokes happened to straddle
// ("…gat" in one frame, "e-me" in the next) and never appears as a
// contiguous run of bytes. Assertions therefore have to target text
// the renderer emits in one piece — a line that was previously blank
// — which the submitted user row is and the composer is not.
func (r *program) submit(prompt string) {
	r.t.Helper()
	r.send(prompt + "\r")
}

// waitFor polls the captured output until every want is present, and
// fails with the frame attached if that does not happen inside
// smokeWaitBudget or if the program exits first.
//
// It reads the whole byte stream, not a reconstruction of the
// current screen, so content that was painted and has since
// scrolled away still counts. That is the right semantics here: the
// question these tests ask is "did the program ever render this",
// and it removes a class of race where an assertion loses to a
// repaint. The cost is the fragmentation caveat on submit: a want
// has to be something the renderer writes in one go.
func (r *program) waitFor(what string, want ...string) {
	r.t.Helper()

	deadline := time.Now().Add(smokeWaitBudget)
	for {
		if missing := r.missing(want); len(missing) == 0 {
			return
		}
		if r.exited() {
			// One last look: the content may have landed in the
			// same instant the program quit.
			if missing := r.missing(want); len(missing) == 0 {
				return
			}
			r.t.Fatalf("program exited (err=%v) before %s appeared; still missing %q.\n%s",
				r.runErr, what, r.missing(want), r.dump())
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out after %s waiting for %s; still missing %q.\n%s",
				smokeWaitBudget, what, r.missing(want), r.dump())
		}
		time.Sleep(smokePollInterval)
	}
}

// quitViaSlash types `/quit` and submits it, which is the path an
// operator takes: the leading slash opens the command palette, and
// Enter runs the selected entry rather than the raw text.
//
// No intermediate wait, and it does not need one. Keystrokes are
// handled in order by the event loop and the palette re-filters
// synchronously inside the same Update that forwards a character to
// the composer, so by the time Enter is dispatched the selection is
// already the one the full "/quit" filters down to. Waiting on a
// painted frame in between would add a dependency on the renderer
// without adding any determinism.
func (r *program) quitViaSlash() {
	r.t.Helper()
	r.send("/quit\r")
}

// waitExit blocks until Run returns and reports its error, or fails
// the test if shutdown takes longer than smokeExitBudget.
func (r *program) waitExit() error {
	r.t.Helper()
	select {
	case <-r.finished:
		return r.runErr
	case <-time.After(smokeExitBudget):
		r.t.Fatalf("tea.Program did not shut down within %s of the quit keystroke.\n%s", smokeExitBudget, r.dump())
		return nil
	}
}

// assertTerminalRestored checks that every terminal mode the program
// turned on it also turned back off, in that order, before Run
// returned. wantMouseCapture says whether mouse reporting was
// expected to be enabled at all.
func (r *program) assertTerminalRestored(wantMouseCapture bool) {
	r.t.Helper()
	raw := r.out.String()

	// Alt screen. Required to have been entered, so the assertion
	// cannot pass vacuously on a program that painted nothing.
	touched, stillSet := lastPrivateMode(raw, "1049")
	if !touched {
		r.t.Errorf("program never entered the alternate screen; the teardown assertion would be vacuous.\n%s", r.dump())
	} else if stillSet {
		r.t.Errorf("program exited with the alternate screen still up (no trailing ESC[?1049l).\n%s", r.dump())
	}

	// Mouse reporting. 1002 is cell-motion tracking, 1003 any-motion,
	// 1006 the SGR extended encoding.
	anyMouse := false
	for _, mode := range []string{"1002", "1003", "1006"} {
		touched, stillSet := lastPrivateMode(raw, mode)
		anyMouse = anyMouse || touched
		if stillSet {
			r.t.Errorf("program exited with mouse mode ?%s still enabled.\n%s", mode, r.dump())
		}
	}
	if anyMouse != wantMouseCapture {
		r.t.Errorf("mouse reporting touched = %v, want %v", anyMouse, wantMouseCapture)
	}

	// Bracketed paste — same failure mode, quieter symptom: the
	// operator's next paste into the shell arrives wrapped in
	// ESC[200~ markers.
	if _, stillSet := lastPrivateMode(raw, "2004"); stillSet {
		r.t.Errorf("program exited with bracketed paste (?2004) still enabled.\n%s", r.dump())
	}
}

// missing returns the wants that are not in the captured output.
func (r *program) missing(want []string) []string {
	screen := r.screen()
	var out []string
	for _, w := range want {
		if !strings.Contains(screen, w) {
			out = append(out, w)
		}
	}
	return out
}

// screen is the captured output with the escape sequences removed
// and runs of whitespace collapsed, so that an assertion token
// survives the line breaks and cursor jumps the renderer scatters
// through a repaint.
func (r *program) screen() string {
	return strings.Join(strings.Fields(ansi.Strip(r.out.String())), " ")
}

func (r *program) exited() bool {
	select {
	case <-r.finished:
		return true
	default:
		return false
	}
}

// dump renders the captured output for a failure message: escapes
// stripped but line structure kept, tail-truncated so a wedged
// render loop cannot bury the assertion under a megabyte of frames.
func (r *program) dump() string {
	const maxDump = 8000
	plain := ansi.Strip(r.out.String())
	elided := ""
	if len(plain) > maxDump {
		elided = fmt.Sprintf("[… %d earlier bytes elided …]\n", len(plain)-maxDump)
		plain = plain[len(plain)-maxDump:]
	}
	return "--- captured output (ANSI stripped, tail) ---\n" + elided + plain +
		"\n--- end captured output ---"
}

// lastPrivateMode reports whether the stream ever set or reset the
// given DEC private mode, and if so whether the LAST thing it did
// was set it. Scanning for the final state rather than counting
// pairs keeps the assertion honest about ordering: a program that
// emitted reset-then-set would balance but would still leave the
// terminal wrong.
func lastPrivateMode(raw, mode string) (touched, set bool) {
	setSeq := "\x1b[?" + mode + "h"
	resetSeq := "\x1b[?" + mode + "l"
	for {
		s := strings.Index(raw, setSeq)
		e := strings.Index(raw, resetSeq)
		switch {
		case s < 0 && e < 0:
			return touched, set
		case e < 0 || (s >= 0 && s < e):
			touched, set = true, true
			raw = raw[s+len(setSeq):]
		default:
			touched, set = true, false
			raw = raw[e+len(resetSeq):]
		}
	}
}
