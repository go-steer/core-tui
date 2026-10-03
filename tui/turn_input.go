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

import "context"

// TurnInput is what the operator supplied for the turn an Agent.Run
// call serves. core-tui stamps it on Run's context; a host reads it
// with TurnInputFrom (#359).
//
// The prompt Run receives is not always what the operator wrote: an
// @-reference is expanded into the referenced file's content, and an
// auto-continue turn's prompt is the drained inbox formatted by
// Options.AutoContinueFormatter. A host that must tell the operator's
// own words from text core-tui composed — core-agent's auto-mode
// approver judges calls only against what a person wrote, and file
// content is where an injection would sit — reads it here instead of
// parsing the prompt.
type TurnInput struct {
	// Typed is the text the operator submitted for this turn, before
	// any @-reference was expanded: what they typed at the prompt, a
	// queued prompt they typed earlier, a steer sent on resume, or the
	// host's Options.InitialPrompt. "" on an auto-continue turn.
	Typed string
	// AutoContinue is set on the turn core-tui builds from
	// InboxDrainer.DrainInbox. Its prompt is Options.AutoContinueFormatter
	// applied to Drained, followed by the files that @-references in the
	// drained texts this TUI queued itself point at (a relayed text's
	// references are never expanded, #364); so a host cannot rebuild or
	// bound the prompt from Drained alone.
	AutoContinue bool
	// Drained is the inbox texts an auto-continue turn was built from,
	// in DrainInbox order, without the blank entries core-tui drops; nil
	// otherwise. A copy: the host may reuse the slice it returned.
	Drained []string
}

type turnInputKey struct{}

// TurnInputFrom returns the TurnInput core-tui stamped on the context
// of an Agent.Run call, and whether there was one. A Run that core-tui
// did not start has none, and neither does a LiveAgent host's own turn,
// which core-tui feeds through Inject; treat absent as "nothing typed".
//
// The stamp lives in this process. An Agent adapter that forwards turns
// elsewhere (core-agent's attach client, for one) must carry Typed
// across itself, or the far side sees only the expanded prompt.
func TurnInputFrom(ctx context.Context) (TurnInput, bool) {
	in, ok := ctx.Value(turnInputKey{}).(TurnInput)
	return in, ok
}

func withTurnInput(ctx context.Context, in TurnInput) context.Context {
	return context.WithValue(ctx, turnInputKey{}, in)
}
