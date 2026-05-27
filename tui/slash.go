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
	"time"
)

// SlashProvider is an optional Agent capability: hosts that implement
// it on their Agent type can advertise additional slash commands the
// TUI merges into /help and the palette. Invocations dispatch back
// via InvokeSlash. Built-in command names always win on collision; a
// system warning is logged at startup when the agent's spec list
// shadows a built-in.
//
// See R-CMD-4 in requirements.md and design.md §3.3.
type SlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlash(ctx context.Context, name, args string) (SlashResult, error)
}

// SlashCommandSpec is one entry in the agent's command catalog.
// Name is the bare identifier (no leading "/"). Aliases are
// alternative invocations (e.g. {"by-the-way"} for /btw). Description
// renders in /help and as the dim subtitle in the palette.
type SlashCommandSpec struct {
	Name        string
	Aliases     []string
	Description string
}

// SlashResult is what InvokeSlash returns. Exactly one of the fields
// should typically be non-zero:
//
//   - SystemMessage — a one-line confirmation that renders as a dim
//     italic system row in the chat history.
//   - ModalAnswer — a richer Q+A overlay rendered as a dismissable
//     Glamour-formatted modal. Used by /btw-style side questions
//     whose answer shouldn't pollute the persistent chat history.
//
// Both may be empty (the call ran but had nothing visible to say).
// When both are non-zero, the modal renders first; the system
// message lands behind it.
type SlashResult struct {
	SystemMessage string
	ModalAnswer   *SideAnswer
}

// SideAnswer carries the operator's question + the agent's response
// for modal-style rendering. Used for /btw and similar side-channel
// Q&A flows that should display once and disappear (not lodge in
// chat history). When Err is non-nil the modal renders an error
// state instead of the Glamour-rendered answer body.
//
// See R-CMD-5 in requirements.md.
type SideAnswer struct {
	Question string
	Answer   string
	Err      error
}

// AsyncSlashProvider is the non-blocking variant of SlashProvider
// (issue #10). Hosts whose slash commands do network or file I/O
// implement this so the dispatch runs off the Update goroutine
// and the TUI stays responsive — every keystroke, render tick,
// and toast continues processing while the host's call is in
// flight.
//
// Implementation contract:
//   - InvokeSlashAsync returns a receive-only channel; core-tui
//     reads exactly one value and closes its tea.Cmd. Hosts must
//     send exactly one SlashResultOrErr and then close (or just
//     send + abandon — core-tui doesn't re-read).
//   - The supplied ctx is cancellable; when the operator hits
//     Ctrl+C / Esc, core-tui cancels it and the host should bail
//     as fast as the underlying work allows. The eventual sent
//     value is discarded.
//   - A host satisfying BOTH SlashProvider and AsyncSlashProvider
//     prefers the async path. Built-in slash commands are not
//     routed here — they're synchronous-and-fast by design.
type AsyncSlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlashAsync(ctx context.Context, name, args string) <-chan SlashResultOrErr
}

// SlashResultOrErr bundles the SlashResult + error pair that
// InvokeSlashAsync's channel carries. Exactly one of Res / Err is
// meaningful per send.
type SlashResultOrErr struct {
	Res SlashResult
	Err error
}

// slashFlight tracks one pending AsyncSlashProvider call (issue #13).
// Name carries the slash identifier so the toast + status-line
// indicator can render "/<name> running…"; startedAt isn't read by
// any indicator today but lets future "running 8s…" / progress
// affordances ride the same struct without another model field.
//
// Lifecycle: created in dispatchSlash's async branch, cleared in
// the slashResultMsg handler (success, error, OR cancel — every
// path lands a slashResultMsg one way or another).
type slashFlight struct {
	name      string
	startedAt time.Time
}
