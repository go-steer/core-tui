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

// Operator hold — the pause gate (R-HOLD-1..5, issue #260).
//
// A paused session is one where no new turn starts until someone
// resumes. That is a different thing from "no turn is running": an
// idle agent will pick up the next queued prompt on its own, a paused
// one will not. The distinction is the whole point of the feature —
// it is what makes ESC mean "stop and wait for me" rather than "cancel
// this one turn and let the scheduler start another".
//
// Wire vocabulary mirrors the host side (core-agent's pkg/attach,
// protocol v1.5.0, docs/sse-event-stream-protocol.md §2.8) so an
// adapter is a field-for-field copy rather than a translation layer.

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Pauser is an optional capability: hosts that can park their agent
// loop implement it so ESC arms a hold, the TUI can render a paused
// state, and /continue, /abandon, and /pause do something.
//
// Without this capability everything degrades the usual way — ESC
// falls through to the local cancelTurn (or to RemoteInterrupter,
// which parks server-side on hosts new enough to hold), and the
// pause slashes report "not available in this host". The paused
// banner never renders because the TUI never learns of a pause.
//
// Pause and Resume MAY block briefly on network I/O; the TUI calls
// both off the Update-loop path (same contract as RemoteInterrupter)
// and a short deadline via ctx is appropriate. Errors surface as an
// inline RoleError row.
//
// PauseState is the polled fallback and must NOT block: the TUI
// refreshes it from the same background tick as StatusReporter.Status
// (host_snapshot.go), so return cached/last-known state rather than
// doing I/O inline. Hosts that push a PauseEvent through
// Event.Pause still need PauseState — it is what makes a TUI attaching
// to an already-paused session render the banner without waiting for
// a transition that already happened.
//
// Which host shape this sits on changes what a steer means, and it is
// worth knowing before implementing Resume. A LiveAgent host owns the
// loop, so ResumeModeSteer is a real instruction: the host makes the
// operator's text the next turn and the standing Events stream shows
// it. A per-turn host does not — Agent.Run opens a subscription for
// the turn it starts and closes it again — so the TUI never sends
// ResumeModeSteer there. It opens the gate with ResumeModeAbandon and
// runs the text through Run itself, which is the only way the turn's
// events reach the operator who asked for it (R-HOLD-4).
type Pauser interface {
	Pause(ctx context.Context, reason string) error
	Resume(ctx context.Context, req ResumeRequest) error
	PauseState() PauseInfo
}

// PauseInfo is the current gate state, as returned by
// Pauser.PauseState. Mirrors attach.PauseInfo.
//
// Interrupted distinguishes the two ways into a pause: true when a
// turn was actually cancelled on the way in, false for a plain /pause
// or an interrupt that landed while the agent was idle. "Your work was
// killed" and "the loop just won't start" are different situations and
// the banner says which.
type PauseInfo struct {
	Paused      bool
	Since       time.Time
	Reason      string
	Interrupted bool
}

// ResumeRequest is the disposition an operator picks when reopening
// the gate. Mirrors attach.ResumeRequest.
//
// Steer carries the new instruction for ResumeModeSteer and is
// ignored by the other two modes.
type ResumeRequest struct {
	Mode  string
	Steer string
}

// Resume modes carried on ResumeRequest.Mode and echoed back on
// PauseEvent.Mode. Plain strings with a named-constant vocabulary,
// matching TurnError.Kind and InboxEvent.State rather than
// introducing a named string type.
const (
	// ResumeModeSteer resumes with the operator's new instruction
	// injected under interrupt framing — the Enter-with-text path on a
	// host that drives its own loop. Sent only to a LiveAgent host: it
	// asks the HOST to run the instruction, which a per-turn client
	// would have no stream to watch (see Pauser).
	ResumeModeSteer = "steer"
	// ResumeModeContinue resumes with "carry on where you left off"
	// and no new instruction — /continue.
	ResumeModeContinue = "continue"
	// ResumeModeAbandon opens the gate without injecting anything and
	// without waking the loop: the interrupted work is dropped and the
	// agent goes quiet until something else drives it — /abandon.
	ResumeModeAbandon = "abandon"
)

// pauseSettleWindow is how long an applied pause transition is held
// against contradiction by the PauseState poll.
//
// Two sources feed the model's pause state: the PauseEvent push (fast,
// authoritative at the instant it fires) and the once-a-second
// PauseState poll (slow, authoritative eventually). A poll already in
// flight when a resume lands returns the pre-resume answer and would
// flip the banner back on for a tick. Beyond the window the host's
// answer wins — it is the truth, and a TUI that ignored it would stay
// wrong forever after a missed event.
const pauseSettleWindow = 2 * time.Second

// pauseInfo is the model's view of the gate: the host-facing PauseInfo
// plus the bookkeeping the reconciliation and the banner need.
type pauseInfo struct {
	PauseInfo

	// appliedAt stamps the last transition the model applied, for
	// pauseSettleWindow. Zero means "nothing applied yet", which lets
	// the first poll through unopposed.
	appliedAt time.Time

	// dismissed suppresses the banner without resuming: ESC while
	// paused clears the screen, not the gate. Reset on the next
	// transition so a subsequent pause renders again.
	dismissed bool

	// lastResumeMode is the disposition of the last Resume the host
	// acked, kept so the poll can name it. PauseEvent echoes the mode
	// back; PauseState has no such field, so on a host the TUI learns
	// about through the poll this is the only way "Abandoned the held
	// work" is distinguishable from a bare "Resumed." Empty when the
	// host reopened the gate on its own, which is the honest answer:
	// nobody here chose a disposition.
	lastResumeMode string
}

// paused reports whether the gate is closed. Independent of
// m.state — a session can be paused while a turn it let start is
// still streaming, and is routinely paused while stateIdle.
func (p pauseInfo) paused() bool { return p.Paused }

// showBanner reports whether the paused banner should render.
func (p pauseInfo) showBanner() bool { return p.Paused && !p.dismissed }

// applyEvent folds a PauseEvent into the model's pause state and
// reports whether anything changed. now is passed in rather than read
// from the clock so tests drive the settle window deterministically.
func (p pauseInfo) applyEvent(ev PauseEvent, now time.Time) (pauseInfo, bool) {
	next := p
	switch ev.State {
	case PauseStatePaused:
		next.Paused = true
		next.Reason = ev.Reason
		next.Interrupted = ev.Interrupted
		next.Since = ev.At
	case PauseStateResumed:
		next.Paused = false
		next.Reason = ""
		next.Interrupted = false
		next.Since = time.Time{}
	default:
		// Unknown state: tolerate per spec §2.8, change nothing.
		return p, false
	}
	if next.PauseInfo == p.PauseInfo {
		return p, false
	}
	next.appliedAt = now
	next.dismissed = false
	return next, true
}

// applyPoll folds a PauseState() reading into the model's pause state.
// Within pauseSettleWindow of the last applied PUSH the poll is
// ignored: it may have been sampled before that transition landed.
//
// Only a push arms that window, and deliberately. One refresh plus one
// pending tick are ever in flight (host_snapshot.go), so there is no
// poll-against-poll race to protect anything from — and arming it here
// would mean a poll-driven host, where the poll is the ONLY source,
// silently dropped every transition that landed within two seconds of
// the last one. At a one-second cadence that is most of them.
func (p pauseInfo) applyPoll(info PauseInfo, now time.Time) (pauseInfo, bool) {
	if info == p.PauseInfo {
		return p, false
	}
	if !p.appliedAt.IsZero() && now.Sub(p.appliedAt) < pauseSettleWindow {
		return p, false
	}
	next := p
	next.PauseInfo = info
	next.dismissed = false
	return next, true
}

// narratePauseTransition appends the transcript row for a gate
// transition the model has just applied. wasPaused is the state before
// it; mode names the disposition on the way out and may be empty.
//
// Exactly one source narrates, and which one depends on the host shape
// — the same split Enter-while-held makes (see resumeThenSubmitCmd).
//
// A LiveAgent host has a standing Events stream, so the push frame is
// both immediate and the richest description of what happened: it
// carries the host's reason, the interrupted bit, and the resume mode.
// The poll stays silent there.
//
// A per-turn host has no standing stream. Agent.Run opens a
// subscription for the turn it starts and closes it again, so a
// transition between turns reaches no listener at all — and the
// backlog it accumulates is replayed, all at once and long stale, into
// whichever subscription opens next. Narrating that replay puts the
// whole history of the hold on screen underneath the prompt that
// happened to reopen the stream. So there the poll owns the gate
// outright, within its one-second cadence, and the replay is dropped.
func (m *model) narratePauseTransition(wasPaused bool, next pauseInfo, mode string) {
	switch {
	case next.Paused:
		m.history.Append(Message{Role: RoleSystem, Text: pausedSystemText(next.PauseInfo)})
		// A hold that arrived by cancelling a turn also ends the
		// stretch this spinner was animating; on the live path
		// nothing else closes it (same reasoning as
		// remoteInterruptDoneMsg).
		if next.Interrupted {
			m.endLiveStretch()
		}
	case wasPaused:
		m.history.Append(Message{Role: RoleSystem, Text: resumedSystemText(mode)})
	}
}

// pauseTimeout bounds a Pause / Resume round trip. Same reasoning as
// remoteInterruptCmd's: a healthy attach endpoint answers in tens of
// milliseconds, and a hung one is better surfaced as an error row than
// left blocking with the operator unsure whether their input landed.
const pauseTimeout = 5 * time.Second

// pauseCmd calls Pause off the Update loop and posts the outcome back.
func pauseCmd(p Pauser, reason string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pauseTimeout)
		defer cancel()
		return pauseDoneMsg{err: p.Pause(ctx, reason)}
	}
}

// holdCmd is the command behind every operator hold — esc and
// /interrupt both end here — against a host that implements Pauser.
//
// It picks the shape of the hold from what is actually happening. With
// work in flight against a host that can also stop it, the hold cancels
// and then parks; otherwise it just parks.
//
// Nothing in flight means nothing to interrupt, and the distinction is
// load-bearing rather than an optimisation: PauseInfo.Interrupted is
// what makes the banner read "Interrupted —" instead of "Agent held",
// which is the first thing an operator wants to know. Esc between turns
// is getting ahead of the next one, not killing this one, and the
// banner has to keep saying so.
func (m model) holdCmd(p Pauser, reason string) tea.Cmd {
	if ri, ok := m.opts.Agent.(RemoteInterrupter); ok && m.turnInFlight() {
		return interruptThenPauseCmd(ri, p, reason)
	}
	return pauseCmd(p, reason)
}

// interruptThenPauseCmd is the operator hold against a host that can
// both stop work and park: cancel the turn, then shut the gate, in that
// order and off the Update loop.
//
// The two calls are halves of one gesture rather than alternatives.
// Pause means "start no new turn" — it says nothing about the turn
// already running, and neither core-agent's endpoint nor the example's
// interrupts on the operator's behalf. Nor does the local cancel cover
// the gap: a host with a gate runs its loop on the far side of a wire,
// so cancelTurn ends this client's subscription while the host's own
// context, which we do not hold, carries the turn through to the end.
// Treating the two as an either/or left the agent working under a
// banner saying it had stopped (issue #280).
//
// Both run even if the first fails, and both errors are reported: a
// failed cancel is exactly when shutting the gate matters most, because
// the scheduler is otherwise free to start another turn on top of the
// one that would not die.
//
// One deadline covers the pair. A hold is one operator gesture and
// should have one budget; a host that burns all of it on the cancel has
// already earned an error row.
func interruptThenPauseCmd(ri RemoteInterrupter, p Pauser, reason string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pauseTimeout)
		defer cancel()
		var errs []error
		if err := ri.Interrupt(ctx); err != nil {
			errs = append(errs, fmt.Errorf("interrupt: %w", err))
		}
		if err := p.Pause(ctx, reason); err != nil {
			errs = append(errs, fmt.Errorf("pause: %w", err))
		}
		return pauseDoneMsg{err: errors.Join(errs...)}
	}
}

// resumeCmd calls Resume off the Update loop and posts the outcome
// back, tagged with the mode so a failure row can say what was tried.
func resumeCmd(p Pauser, req ResumeRequest) tea.Cmd {
	return resumeThenSubmitCmd(p, req, "")
}

// resumeThenSubmitCmd is resumeCmd for a per-turn host: it opens the
// gate and carries the operator's text back on the message so Update
// can run it as an ordinary turn once the resume lands.
//
// Which mode gets sent is the whole point of the split. On a LiveAgent
// host the steer goes out as ResumeModeSteer and the host's loop makes
// it the next turn, which the standing Events stream shows. A per-turn
// host has no such stream: Agent.Run opens a subscription for the turn
// it starts and closes it again, so a turn the HOST starts on a steer
// streams to nobody and the operator watches their prompt disappear.
// There the client owns the turn, so the gate is opened with
// ResumeModeAbandon — drop whatever was held — and the text is run
// through Run like any other prompt.
func resumeThenSubmitCmd(p Pauser, req ResumeRequest, submit string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pauseTimeout)
		defer cancel()
		return resumeDoneMsg{mode: req.Mode, submit: submit, err: p.Resume(ctx, req)}
	}
}

// dispatchResumeSlash is the shared body of /continue and /abandon.
// Both are "open the gate with this disposition"; only the mode and
// the not-held message differ.
func (m model) dispatchResumeSlash(mode string) (bool, tea.Model, tea.Cmd) {
	p, ok := m.opts.Agent.(Pauser)
	if !ok {
		m.history.Append(Message{Role: RoleSystem, Text: "/" + mode + ": agent doesn't implement Pauser"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil
	}
	if !m.pause.paused() {
		m.history.Append(Message{Role: RoleSystem, Text: "/" + mode + ": agent isn't held"})
		m.input.Reset()
		m.refreshAndScroll()
		return true, m, nil
	}
	m.input.Reset()
	return true, m, resumeCmd(p, ResumeRequest{Mode: mode})
}

// pausedSystemText is the transcript row for a pause landing. Reads
// off Interrupted first because that is the question an operator has
// when the banner appears: was my work killed, or merely not started.
func pausedSystemText(info PauseInfo) string {
	var b strings.Builder
	if info.Interrupted {
		b.WriteString("Interrupted — agent held")
	} else {
		b.WriteString("Agent held")
	}
	if r := strings.TrimSpace(info.Reason); r != "" {
		b.WriteString(" (")
		b.WriteString(r)
		b.WriteString(")")
	}
	b.WriteString(". Type to steer, /continue to carry on, /abandon to drop it.")
	return b.String()
}

// resumedSystemText is the transcript row for the gate reopening. mode
// is whatever the host echoed; an unrecognised or absent one still
// gets a row, since the operator needs to know the hold is over.
func resumedSystemText(mode string) string {
	switch mode {
	case ResumeModeSteer:
		return "Resumed with your instruction."
	case ResumeModeContinue:
		return "Resumed — carrying on."
	case ResumeModeAbandon:
		return "Abandoned the held work. Agent is idle."
	default:
		return "Resumed."
	}
}
