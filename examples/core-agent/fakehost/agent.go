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

// Package fakehost stands in for the parts of
// github.com/go-steer/core-agent that a host adapter touches: the
// in-process agent type (`pkg/agent.Agent`, whose Run yields ADK
// `*session.Event` values), the attach API's HTTP+SSE client
// (`internal/attachclient.Client`), and a toy daemon to point that
// client at.
//
// It is deliberately a fake rather than a dependency on the real
// module, for two reasons:
//
//   - core-agent depends on core-tui. Importing core-agent here would
//     close an import cycle at the ecosystem level.
//   - it would make this repo's CI depend on another repo's release
//     cadence, which defeats the point of the canary in
//     docs/design.md §7: the adapter next door has to fail to compile
//     the moment core-tui's plug-in surface shifts, not the next time
//     core-agent cuts a tag.
//
// Nothing here is a faithful reimplementation. The shapes are what
// matter — method names, signatures, and the event structure the
// adapter has to translate — because those are what the adapter is
// written against. Behavior is canned: one scripted turn, replayed
// on every submission, like tui/testagent.
package fakehost

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"
)

// Event mirrors the ADK `*session.Event` that core-agent's
// `Agent.Run` yields. The nesting (Event → Content → []Part →
// FunctionCall / FunctionResponse / Text) is the part that matters:
// it is the shape the adapter's Run translator has to flatten into
// a single tui.Event.
type Event struct {
	Author        string         `json:"author,omitempty"`
	Partial       bool           `json:"partial,omitempty"`
	Content       *Content       `json:"content,omitempty"`
	UsageMetadata *UsageMetadata `json:"usage_metadata,omitempty"`
	TurnComplete  bool           `json:"turn_complete,omitempty"`
}

// Content is one event's payload: an ordered list of parts.
type Content struct {
	Parts []Part `json:"parts,omitempty"`
}

// Part is a single fragment — prose, a tool call, or a tool result.
// At most one field is set in practice.
type Part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *FunctionCall     `json:"function_call,omitempty"`
	FunctionResponse *FunctionResponse `json:"function_response,omitempty"`
}

// FunctionCall is a model-issued tool invocation.
type FunctionCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// FunctionResponse is the completion for a FunctionCall with the
// matching ID. core-agent packs failures into the response map under
// an "error" key rather than a separate field, and the adapter is
// what splits the two apart — see splitFunctionResponse in local.go.
type FunctionResponse struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response,omitempty"`
}

// UsageMetadata is the per-event token accounting. Real ADK reports
// this cumulatively within a turn, which is why core-agent runs it
// through a "TurnTap" before handing anything to the TUI; the fake
// reports it once, on the final event.
type UsageMetadata struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Tool is one entry in the host's tool registry.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Gate        string `json:"gate"`
}

// Subagent is one background agent the host is managing.
type Subagent struct {
	Name    string    `json:"name"`
	State   string    `json:"state"`
	Report  string    `json:"report"`
	Started time.Time `json:"started"`
}

// SubagentTurn is one recorded turn inside a background agent —
// what it said and what it called, which is what the /subagents
// drill-down renders.
type SubagentTurn struct {
	Seq     int64              `json:"seq"`
	At      time.Time          `json:"at"`
	Author  string             `json:"author"`
	Text    string             `json:"text"`
	Calls   []FunctionCall     `json:"calls,omitempty"`
	Results []FunctionResponse `json:"results,omitempty"`
}

// UnknownSubagentError is the host's "no such background agent"
// error. It carries the names that WOULD resolve so the caller can
// say so instead of rendering a convincing empty log for a typo.
type UnknownSubagentError struct {
	Name      string
	Available []string
}

func (e *UnknownSubagentError) Error() string {
	return fmt.Sprintf("unknown subagent %q (have: %s)", e.Name, strings.Join(e.Available, ", "))
}

// Approval is one row from the permission gate's session log.
type Approval struct {
	Tool     string `json:"tool"`
	Key      string `json:"key"`
	Decision string `json:"decision"`
}

// TurnUsage is the host's own per-turn accounting, the thing
// core-agent's `internal/usage.Tracker` owns. The TUI reads a
// projection of it through tui.UsageTracker.
type TurnUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// Models is the catalog the host offers for /model. core-agent
// hardcodes an equivalent list rather than querying the provider.
func Models() []string {
	return []string{
		"gemini-3.1-pro",
		"gemini-3.1-flash",
		"claude-opus-4-7",
		"claude-sonnet-4-6",
	}
}

// PauseInfo is the gate's state, the fake of core-agent's
// `attach.PauseInfo`. Interrupted is the field an operator reads
// first: it separates "your turn was killed and the loop is parked"
// from "the loop is parked and nothing was lost".
type PauseInfo struct {
	Paused      bool      `json:"paused"`
	Since       time.Time `json:"since,omitzero"`
	Reason      string    `json:"reason,omitempty"`
	Interrupted bool      `json:"interrupted,omitempty"`
}

// The dispositions POST {session}/resume accepts. Plain strings with
// a named-constant vocabulary, matching how the rest of this fake
// carries wire enums.
const (
	ResumeModeSteer    = "steer"
	ResumeModeContinue = "continue"
	ResumeModeAbandon  = "abandon"
)

// Agent is the fake of core-agent's `pkg/agent.Agent`. Every method
// below exists on the real type with the same signature; the
// adapters in this example are written against these and nothing
// else.
type Agent struct {
	mu        sync.Mutex
	model     string
	inbox     []string
	approvals []Approval
	allow     []string
	deny      []string
	prices    map[string]TurnUsage
	turns     int
	session   TurnUsage
	last      TurnUsage
	started   time.Time
	wake      chan struct{}

	// Turns in flight, keyed by an id beginTurn hands out, valued by
	// whether that turn has been interrupted. A single running /
	// interrupted pair could not tell two turns apart, and beginTurn
	// cleared the flag — so a turn starting while another's interrupt
	// was still pending swallowed it, and the interrupted turn ran to
	// completion. More than one turn at a time is ordinary here: the
	// TUI's per-turn subscription and an injected steer each start
	// one. This is the only end-to-end check on the interrupt path, so
	// it getting the semantics wrong costs more than the toy is worth.
	turnSeq  int
	inFlight map[int]bool

	// The pause gate. hold is non-nil and open exactly while the
	// agent is held; Resume closes it, which is what releases
	// awaitResume. A channel rather than a sync.Cond because the
	// waiter has to be able to select on ctx.Done as well.
	pause       PauseInfo
	hold        chan struct{}
	waiting     int
	resumeMode  string
	resumeSteer string
}

// NewAgent builds a fake agent on the given model.
func NewAgent(model string) *Agent {
	if model == "" {
		model = Models()[0]
	}
	return &Agent{
		model:    model,
		prices:   map[string]TurnUsage{},
		started:  time.Now(),
		wake:     make(chan struct{}, 4),
		inFlight: map[int]bool{},
		approvals: []Approval{
			{Tool: "bash", Key: "go test ./...", Decision: "allow-session"},
			{Tool: "edit", Key: "internal/auth/session.go", Decision: "allow-once"},
			{Tool: "fetch", Key: "https://internal.example/admin", Decision: "deny"},
		},
	}
}

// ModelName reports the model the next turn will run on.
func (a *Agent) ModelName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model
}

// WithModel returns a new agent bound to modelID, the way
// core-agent rebuilds its runner around a swapped model rather than
// mutating the live one. Session usage carries over.
func (a *Agent) WithModel(modelID string) (*Agent, error) {
	for _, m := range Models() {
		if m == modelID {
			a.mu.Lock()
			defer a.mu.Unlock()
			next := NewAgent(modelID)
			next.session = a.session
			next.last = a.last
			next.turns = a.turns
			next.started = a.started
			next.approvals = a.approvals
			return next, nil
		}
	}
	return nil, fmt.Errorf("unknown model %q", modelID)
}

// State reports what the agent is doing right now.
func (a *Agent) State() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.inFlight) > 0 {
		return "running"
	}
	return "idle"
}

// Provider reports the configured provider for the current model.
func (a *Agent) Provider() string {
	if strings.HasPrefix(a.ModelName(), "claude") {
		return "anthropic"
	}
	return "gemini"
}

// Interrupt cancels every turn in flight, reporting whether there was
// one. Note the signature: it is NOT tui.RemoteInterrupter's
// `Interrupt(ctx) error` — see the comment on localAdapter.
//
// It marks the turns that exist now and no others: a turn started
// afterwards is new work the operator has not asked to stop.
func (a *Agent) Interrupt() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.inFlight) == 0 {
		return false
	}
	for id := range a.inFlight {
		a.inFlight[id] = true
	}
	return true
}

// Pause shuts the gate. Idempotent: a second Pause while held keeps
// the original Since and reason, so a double esc does not restart
// the clock on the banner.
func (a *Agent) Pause(reason string, interrupted bool) PauseInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.pause.Paused {
		a.pause = PauseInfo{
			Paused:      true,
			Since:       time.Now(),
			Reason:      reason,
			Interrupted: interrupted,
		}
		a.hold = make(chan struct{})
	}
	return a.pause
}

// Resume opens the gate with a disposition, releasing whoever is
// blocked in awaitResume. Resuming an agent that is not held is an
// error rather than a no-op — it means the caller's view of the
// session is stale, and swallowing it would hide that.
func (a *Agent) Resume(mode, steer string) error {
	switch mode {
	case ResumeModeSteer, ResumeModeContinue, ResumeModeAbandon:
	default:
		return fmt.Errorf("unknown resume mode %q", mode)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.pause.Paused {
		return fmt.Errorf("agent is not held")
	}
	a.resumeMode, a.resumeSteer = mode, steer
	a.pause = PauseInfo{}
	close(a.hold)
	a.hold = nil
	return nil
}

// PauseState reports the gate's state without blocking. The adapter
// serves tui.Pauser.PauseState off a cached poll of this.
func (a *Agent) PauseState() PauseInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pause
}

// awaitResume blocks while the gate is shut and reports the
// disposition it was opened with. This is what "paused" MEANS: not
// "the current turn stops" but "no new turn starts until someone
// resumes". ok is false only when ctx died first.
func (a *Agent) awaitResume(ctx context.Context) (mode, steer string, ok bool) {
	a.mu.Lock()
	hold := a.hold
	if hold == nil {
		a.mu.Unlock()
		return ResumeModeContinue, "", true
	}
	a.waiting++
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.waiting--
		a.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return "", "", false
	case <-hold:
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resumeMode, a.resumeSteer, true
}

// HeldWaiters counts the turns currently blocked on the gate. The
// daemon reads it to decide whether a steer needs a turn started for
// it or whether there is already one parked that will adopt it —
// without that, resuming a held session runs the work twice.
func (a *Agent) HeldWaiters() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.waiting
}

// Inject feeds a message into the currently-running turn's context.
// Distinct from queueing a prompt for the next turn.
func (a *Agent) Inject(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("empty message")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inbox = append(a.inbox, message)
	return nil
}

// DrainInbox returns everything injected since the last drain and
// empties the inbox in the same call.
func (a *Agent) DrainInbox() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.inbox
	a.inbox = nil
	return out
}

// PendingInboxCount peeks at the inbox depth without draining.
func (a *Agent) PendingInboxCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.inbox)
}

// WakeRequested is the "a background agent wants the operator"
// channel.
func (a *Agent) WakeRequested() <-chan struct{} { return a.wake }

// RequestWake pushes a wake signal. Best-effort: a full channel
// drops rather than blocking the caller.
func (a *Agent) RequestWake() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// Tools lists the host's registered tools.
func (a *Agent) Tools() []Tool {
	return []Tool{
		{Name: "read_file", Description: "read a file from the workspace", Source: "builtin", Gate: "allowed"},
		{Name: "write_file", Description: "write a file in the workspace", Source: "builtin", Gate: "ask"},
		{Name: "bash", Description: "run a shell command", Source: "builtin", Gate: "ask"},
		{Name: "kubectl_get", Description: "read Kubernetes objects", Source: "mcp:k8s", Gate: "allowed"},
		{Name: "pagerduty_ack", Description: "acknowledge an incident", Source: "mcp:pagerduty", Gate: "denied"},
	}
}

// Subagents lists the background agents the host is managing.
func (a *Agent) Subagents() []Subagent {
	now := time.Now()
	return []Subagent{
		{Name: "security-review", State: "done", Report: "no findings above medium in the diff", Started: now.Add(-9 * time.Minute)},
		{Name: "flake-hunter", State: "running", Report: "re-running TestResizeDebounce (attempt 3)", Started: now.Add(-2 * time.Minute)},
	}
}

// SubagentTurns returns the turns recorded for the named background
// agent whose Seq is greater than since. An unresolvable name is an
// *UnknownSubagentError, never an empty page.
func (a *Agent) SubagentTurns(name string, since int64) ([]SubagentTurn, error) {
	log := map[string][]SubagentTurn{
		"security-review": {
			{Seq: 1, At: time.Now().Add(-9 * time.Minute), Author: "agent", Text: "Scanning the diff for injected secrets and shell interpolation."},
			{Seq: 2, At: time.Now().Add(-8 * time.Minute), Author: "grep", Calls: []FunctionCall{{
				ID: "sa-1", Name: "grep", Args: map[string]any{"pattern": "exec\\.Command"},
			}}},
			{Seq: 3, At: time.Now().Add(-8 * time.Minute), Author: "grep", Results: []FunctionResponse{{
				ID: "sa-1", Name: "grep", Response: map[string]any{"matches": 0},
			}}},
			{Seq: 4, At: time.Now().Add(-7 * time.Minute), Author: "agent", Text: "No findings above medium."},
		},
		"flake-hunter": {
			{Seq: 1, At: time.Now().Add(-2 * time.Minute), Author: "agent", Text: "Re-running TestResizeDebounce (attempt 3)."},
		},
	}
	turns, ok := log[name]
	if !ok {
		available := make([]string, 0, len(log))
		for _, s := range a.Subagents() {
			available = append(available, s.Name)
		}
		return nil, &UnknownSubagentError{Name: name, Available: available}
	}
	var out []SubagentTurn
	for _, t := range turns {
		if t.Seq > since {
			out = append(out, t)
		}
	}
	return out, nil
}

// Reload re-reads the host's configuration, memory files, skills and
// MCP servers. The real one returns fresh views of each; the fake
// just reports that it ran.
func (a *Agent) Reload(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "reloaded 2 memory files, 3 MCP servers, 1 skill", nil
}

// RefreshPricing re-pulls the provider price table.
func (a *Agent) RefreshPricing(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "pricing refreshed for " + a.ModelName(), nil
}

// SetPricing installs a manual per-MTok price override.
func (a *Agent) SetPricing(modelID string, inputPerMTok, outputPerMTok float64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prices[modelID] = TurnUsage{CostUSD: inputPerMTok + outputPerMTok}
	return nil
}

// Approvals is the permission gate's session log.
func (a *Agent) Approvals() []Approval {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Approval(nil), a.approvals...)
}

// AllowPatterns / DenyPatterns install session-scoped gate rules.
func (a *Agent) AllowPatterns(patterns []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allow = append(a.allow, patterns...)
	return nil
}

// DenyPatterns installs session-scoped deny rules.
func (a *Agent) DenyPatterns(patterns []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deny = append(a.deny, patterns...)
	return nil
}

// AllowBundle turns on one of the host's named allow bundles
// (its "builtin allow extra" sets).
func (a *Agent) AllowBundle(name string) error {
	switch name {
	case "read-only", "git", "build":
		return a.AllowPatterns([]string{name + ":*"})
	default:
		return fmt.Errorf("no allow bundle named %q", name)
	}
}

// AskSideQuestion answers a question out of band, without it
// landing in the main conversation. This is core-agent's /btw.
func (a *Agent) AskSideQuestion(ctx context.Context, question string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(400 * time.Millisecond):
	}
	return "**" + question + "**\n\nAnswered out of band by `" + a.ModelName() +
		"`, so it never enters the turn context.\n\n" +
		"- The host owns the side-channel call.\n" +
		"- core-tui only renders what comes back.\n", nil
}

// SessionUsage / LastTurnUsage / Turns / Uptime are the host's usage
// accounting, projected to the TUI through tui.UsageTracker.
func (a *Agent) SessionUsage() TurnUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session
}

// LastTurnUsage reports the most recent turn's tokens and cost.
func (a *Agent) LastTurnUsage() TurnUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

// Turns counts completed turns this session.
func (a *Agent) Turns() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.turns
}

// Uptime is how long this session has been attached.
func (a *Agent) Uptime() time.Duration { return time.Since(a.started) }

// ContextWindow reports the model's window size and how much of it
// the conversation is currently occupying.
func (a *Agent) ContextWindow() (size, used int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return 1_000_000, a.session.InputTokens + a.session.OutputTokens
}

// Run executes one turn and yields the host's native events. The
// signature — iter.Seq2 over the host's OWN event type — is the
// thing the adapter exists to bridge.
func (a *Agent) Run(ctx context.Context, prompt string) iter.Seq2[*Event, error] {
	return func(yield func(*Event, error) bool) {
		// The gate, before anything else — core-agent's Agent.Run
		// calls awaitResume as its first step for the same reason. A
		// turn that checked the gate later would already have spent
		// tokens the operator was trying to stop.
		mode, steer, ok := a.awaitResume(ctx)
		if !ok {
			return
		}
		switch mode {
		case ResumeModeAbandon:
			// The held work is dropped: no turn, no usage, no events.
			return
		case ResumeModeSteer:
			if steer != "" {
				prompt = steer
			}
		}
		id := a.beginTurn()
		defer a.endTurn(id)
		script := a.script(prompt)
		for _, ev := range script {
			select {
			case <-ctx.Done():
				return
			case <-time.After(90 * time.Millisecond):
			}
			if a.takeInterrupt(id) {
				yield(nil, fmt.Errorf("turn interrupted by operator"))
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// AppendUsage folds one turn's tokens into the session accounting
// and returns the priced turn. The CALLER drives this — core-agent's
// adapter does the same, because the host's usage tracker sits
// beside the agent rather than inside it, and the adapter is what
// holds both.
func (a *Agent) AppendUsage(u UsageMetadata) TurnUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	// A stand-in price table: $3/MTok in, $15/MTok out.
	turn := TurnUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CostUSD:      float64(u.InputTokens)*3e-6 + float64(u.OutputTokens)*15e-6,
	}
	a.turns++
	a.last = turn
	a.session.InputTokens += turn.InputTokens
	a.session.OutputTokens += turn.OutputTokens
	a.session.CostUSD += turn.CostUSD
	return turn
}

// beginTurn registers a turn and returns the id its caller identifies
// itself by for the rest of its life.
func (a *Agent) beginTurn() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inFlight == nil {
		a.inFlight = map[int]bool{}
	}
	a.turnSeq++
	a.inFlight[a.turnSeq] = false
	return a.turnSeq
}

// takeInterrupt reports whether this turn in particular has been
// interrupted.
func (a *Agent) takeInterrupt(id int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inFlight[id]
}

func (a *Agent) endTurn(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inFlight, id)
}

// script is the canned turn: streamed prose, a tool call, its
// result, a committed close, and the usage event. Every submission
// replays it — the point of this example is that the adapter
// compiles and translates, not that the agent is smart.
func (a *Agent) script(prompt string) []*Event {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "the workspace"
	}
	if len(prompt) > 60 {
		prompt = prompt[:60] + "…"
	}
	partial := func(s string) *Event {
		return &Event{Author: "agent", Partial: true, Content: &Content{Parts: []Part{{Text: s}}}}
	}
	return []*Event{
		partial("Looking at "),
		partial("`" + prompt + "`"),
		partial(" — reading the deployment first.\n"),
		{Author: "agent", Content: &Content{Parts: []Part{{FunctionCall: &FunctionCall{
			ID:   "call-1",
			Name: "read_file",
			Args: map[string]any{"path": "deploy/api.yaml"},
		}}}}},
		{Author: "read_file", Content: &Content{Parts: []Part{{FunctionResponse: &FunctionResponse{
			ID:   "call-1",
			Name: "read_file",
			Response: map[string]any{
				"content":    "kind: Deployment\nspec:\n  replicas: 3\n",
				"latency_ms": int64(240),
			},
		}}}}},
		{Author: "agent", Content: &Content{Parts: []Part{{FunctionCall: &FunctionCall{
			ID:   "call-2",
			Name: "kubectl_get",
			Args: map[string]any{"resource": "deploy/api", "namespace": "prod"},
		}}}}},
		{Author: "kubectl_get", Content: &Content{Parts: []Part{{FunctionResponse: &FunctionResponse{
			ID:       "call-2",
			Name:     "kubectl_get",
			Response: map[string]any{"error": "the server could not find the requested resource"},
		}}}}},
		{
			Author: "agent",
			Content: &Content{Parts: []Part{{Text: "Looking at `" + prompt + "` — reading the deployment first.\n\n" +
				"The manifest asks for **3 replicas**, but the live object is gone — " +
				"`kubectl_get` came back with a 404, so the deployment was never applied " +
				"to `prod` (or something reaped it).\n\n" +
				"Next step would be `kubectl apply -f deploy/api.yaml`, which needs your " +
				"approval — that is the permission modal this example wires up.\n"}}},
			UsageMetadata: &UsageMetadata{InputTokens: 8_412, OutputTokens: 1_104},
			TurnComplete:  true,
		},
	}
}
