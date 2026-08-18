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

// The agent's own question to the operator (R-PROMPT-1, issue #255 —
// stage 5 of docs/design-question-dialogs.md §12, designed in §10.3).
//
// The gap this closes is narrow and had been open since v0.19.0. The
// reference host ships an ask_user tool whose production implementation
// reads the process's stdin, and under the TUI that contends with
// Bubble Tea for the terminal — so a host with an agent-driven question
// path could not put a question to the operator in the one arrangement
// where an operator is demonstrably sitting there. R-PROMPT-1 has
// carried a "specified, not shipped" mark for three releases on that
// basis.
//
// It mirrors PermissionPrompter (prompter.go) and Elicitor
// (elicitor.go) rather than inventing a third shape: the TUI
// implements the interface, the host wires the value into its tool,
// the call blocks on a modal, and the answer comes back over a
// per-flow channel. Everything below the exported surface is the
// question seam stage 3 landed — see question_ask.go.
//
// The one thing this file does NOT do is seal the answer.
// design-question-dialogs.md §10.3 argues that at length: the sealed
// `answer` is for inside the package, where the compiler and
// gochecksumtype can reach; across the module boundary an enum plus a
// payload is what ElicitResult and PermissionDecision already
// established, and the reference host's translation layers all end in
// a deliberate `default:` because it upgrades on its own schedule.

package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Asker is the interface the TUI implements and the host wires into
// its agent's ask-the-user tool. The agent calls Ask when it needs an
// answer from the operator mid-turn; the call blocks on the TUI's
// modal until they answer, decline, or ctx cancels.
//
// A non-nil error means no operator was consulted: either ctx
// cancelled, or the request is one this TUI cannot draw and was
// refused before any modal opened (ErrAskUnsupported). Both pair the
// error with AskCancelled, which is a placeholder and not an answer —
// a host relaying the result to its agent should check err first. That
// rule is Elicitor's too, and it is there because the alternative
// shipped once and had to be fixed (issue #209).
//
// Distinct from Elicitor, which is MCP-server-initiated and
// form-shaped. This is the agent itself asking one discrete question.
//
// See R-PROMPT-1 in requirements.md and design.md §3.5.
type Asker interface {
	Ask(ctx context.Context, req AskRequest) (AskResult, error)
}

// AskKind picks the modal shape. The TUI declines a kind it cannot
// render rather than opening a broken modal (cf. supportedElicit).
type AskKind int

const (
	AskChoice      AskKind = iota // single-select over Choices
	AskMultiChoice                // multi-select over Choices
	AskConfirm                    // yes / no
	AskText                       // one free-text value
	AskLongText                   // free text, edited in $EDITOR
)

// AskOption is one row of a choice list.
//
// design-question-dialogs.md §10.3 spells this AskChoice, which does
// not compile: AskChoice is also the name of the first AskKind
// constant, and Go has one package scope for both. The kind constants
// keep their names because they are the vocabulary the whole design
// speaks in, and the struct takes the name §7.2 already gives the
// in-package equivalent (`option`).
type AskOption struct {
	ID          string
	Label       string
	Description string
}

// AskRequest carries everything the modal needs. Kind picks the
// rendering shape; the other fields populate per kind.
type AskRequest struct {
	Kind AskKind

	// Title shown in the modal header. Empty renders a default naming
	// the surface rather than leaving the bar blank.
	Title string

	// Prompt is the question itself, rendered above the answer widget.
	Prompt string

	// Choices are the rows for AskChoice / AskMultiChoice. Ignored by
	// the other three kinds; a chooser with none of them is refused as
	// unrenderable rather than opened empty.
	Choices []AskOption

	// Placeholder and Initial seed AskText / AskLongText. Initial is
	// the starting buffer — for AskLongText it is what the editor
	// opens on, which is how an agent proposes a draft.
	Placeholder string
	Initial     string

	// Source is the originating sub-agent name, empty for the
	// foreground agent. Same field, same meaning, as
	// PermissionRequest.Source.
	Source string
}

// AskAction is the operator's top-level answer. Every value reports
// something they actually did; the TUI never synthesizes one on their
// behalf, which is why an unrenderable request comes back as an error
// instead of as a decline.
//
// Declined and Cancelled are different and an agent may act on the
// difference: Declined is "I read this and I am not answering",
// Cancelled is "I dismissed it". A "no" to an AskConfirm is neither —
// it is AskAnswered with ChoiceIDs of {"no"}, because the question was
// answered.
type AskAction int

const (
	AskAnswered  AskAction = iota // enter, on a filled-in question
	AskDeclined                   // ctrl+d
	AskCancelled                  // esc, a session switch, or shutdown
)

// AskResult is what the operator's answer produces. Action picks
// between answered / declined / cancelled; the payload fields carry
// the answer when Action == AskAnswered and are zero otherwise.
type AskResult struct {
	Action AskAction

	// ChoiceIDs holds the AskOption.ID values picked: exactly one for
	// AskChoice, zero or more for AskMultiChoice, and for AskConfirm
	// the literal "yes" or "no".
	ChoiceIDs []string

	// Text is the AskText / AskLongText value, already trimmed of
	// surrounding whitespace.
	Text string
}

// ErrAskUnsupported is returned by Ask, wrapped, when the request is
// one this TUI has no way to draw — see supportedAsk for the screen.
// The result carries AskCancelled, but the error is the real answer
// and the Action is only there because the return type has one.
//
// It is the same sentinel shape as ErrElicitUnsupported, and it is
// here on the same reasoning, which #255's own issue text does not
// quite reach: the issue says an unrenderable kind is "declined", and
// a decline is precisely what issue #209 removed from the elicit path
// two weeks ago. "I could not draw this" and "the operator read it and
// said no" are different facts, and the second one puts words in the
// operator's mouth. Shipping a new host surface with the bug the old
// one was just cured of is not a saving.
//
// Hosts match it with errors.Is. The wrapped message names which part
// of the request could not be drawn, and is the same reason the
// operator sees in the transcript.
var ErrAskUnsupported = errors.New("ask request is not renderable by this TUI")

// askFlow couples one AskRequest with the response channel. Same
// pattern as permissionFlow (prompter.go) and elicitFlow.
type askFlow struct {
	req      AskRequest
	response chan askResponse
}

type askResponse struct {
	result AskResult
	err    error
}

// asker is the TUI-side Asker implementation. The host obtains one via
// tui.NewAsker() and wires it into its ask-the-user tool. The Bubble
// Tea loop drains the request channel via a listener Cmd; each request
// becomes an askRequestMsg that Update routes to the question seam.
//
// Concurrency model matches the Prompter and the Elicitor (design.md
// §4.1): requests channel buffered 1, response channel per-flow
// buffered 1, ctx cancellation drains in the background.
type asker struct {
	requests chan askFlow

	mu      sync.Mutex
	pending *askFlow
}

// NewAsker constructs an Asker ready to be wired into the host's
// ask-the-user tool and the TUI's Options. Returns the interface so
// callers can swap impls in tests without referring to the unexported
// concrete type.
func NewAsker() Asker { return &asker{requests: make(chan askFlow, 1)} }

// Ask blocks until the operator answers, declines or dismisses the
// modal, or until ctx cancels.
func (a *asker) Ask(ctx context.Context, req AskRequest) (AskResult, error) {
	response := make(chan askResponse, 1)
	flow := askFlow{req: req, response: response}

	select {
	case a.requests <- flow:
	case <-ctx.Done():
		return AskResult{Action: AskCancelled}, ctx.Err()
	}

	select {
	case r := <-response:
		return r.result, r.err
	case <-ctx.Done():
		go func() { <-response }()
		return AskResult{Action: AskCancelled}, ctx.Err()
	}
}

// supportedAsk screens out requests the TUI cannot put to an operator,
// so they are refused rather than opened as an empty or inert modal.
//
// The screen runs on the Bubble Tea loop, in the askRequestMsg
// handler, and not here in Ask — for the reason spelled out at
// supportedElicit: the refusal owes the operator a transcript row as
// well as the agent an error, and only the loop can append to the
// transcript.
//
// AskConfirm and AskText need nothing from the request: a yes/no and
// an empty input box are drawable with a blank prompt, and a question
// with nothing to read is the agent's problem to have written, not an
// unrenderable one.
func supportedAsk(req AskRequest) bool {
	switch req.Kind {
	case AskChoice, AskMultiChoice:
		return len(req.Choices) > 0
	case AskConfirm, AskText:
		return true
	case AskLongText:
		return len(editorArgv()) > 0
	default:
		return false
	}
}

// askUnsupportedReason names the part of the request the TUI could not
// draw, in one clause. It mirrors supportedAsk's own arms rather than
// re-reading the request, so the screen and the explanation cannot
// describe different failures — both the operator's transcript row and
// the agent's error are built from this one function.
func askUnsupportedReason(req AskRequest) string {
	switch req.Kind {
	case AskChoice, AskMultiChoice:
		return "the question has no choices"
	case AskConfirm, AskText:
		// Unreachable while supportedAsk says these are always
		// drawable, and named anyway: a reason that silently became a
		// default arm is how the two functions would drift.
		return "the question could not be drawn"
	case AskLongText:
		return "no editor is configured ($VISUAL / $EDITOR are unset)"
	default:
		return "the question kind is not one this TUI renders"
	}
}

// askUnsupportedError is what the agent gets: ErrAskUnsupported so a
// host can match it with errors.Is, wrapping the same reason the
// operator reads.
func askUnsupportedError(req AskRequest) error {
	return fmt.Errorf("%w: %s", ErrAskUnsupported, askUnsupportedReason(req))
}

// askUnsupportedNotice is the operator's half of a refusal: what was
// refused, which agent asked, and which part of it could not be drawn.
//
// It says "without asking you" for the same reason the elicit notice
// does — a row reading "declined" would leave the operator believing a
// refusal had gone out over their name.
func askUnsupportedNotice(req AskRequest) string {
	from := ""
	if req.Source != "" {
		from = " from " + req.Source
	}
	return "Agent question" + from + ": " + askUnsupportedReason(req) +
		" — refused without asking you"
}

// nextRequest is the Bubble Tea side's blocking read; mirrors the
// Prompter's and the Elicitor's namesakes.
func (a *asker) nextRequest(ctx context.Context) (askFlow, bool) {
	select {
	case flow := <-a.requests:
		a.mu.Lock()
		a.pending = &flow
		a.mu.Unlock()
		return flow, true
	case <-ctx.Done():
		return askFlow{}, false
	}
}

// dispatchResult writes the operator's answer to the pending flow.
// No-op when no flow is pending.
//
// err is non-nil only when the TUI is answering on its own account
// rather than relaying an operator — today that is the
// ErrAskUnsupported refusal, which never opens a modal at all.
func (a *asker) dispatchResult(r AskResult, err error) {
	a.mu.Lock()
	flow := a.pending
	a.pending = nil
	a.mu.Unlock()
	if flow == nil {
		return
	}
	flow.response <- askResponse{result: r, err: err}
}
