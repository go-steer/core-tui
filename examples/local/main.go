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

// Command local is the visual-preview binary for core-tui. It boots
// the TUI against an idle test agent with a hardcoded multi-turn
// conversation pre-seeded into the chat history so the operator can
// judge layout, colors, glyphs, spacing, sidebar/header, and modal
// composition without needing a real model.
//
// Key bindings exposed by this slice:
//
//	ctrl+c, ctrl+d  quit
//	ctrl+b          toggle StatusHeader <-> StatusSidebar
//	shift+tab       cycle the permission mode chip
//	ctrl+p          open the (sample) command palette
//	ctrl+g          open the (sample) model picker
//	ctrl+y          open the (sample) permission modal
//	ctrl+e          open the (sample) MCP elicitation form
//	esc             close any open modal
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-steer/core-tui/tui"
	"github.com/go-steer/core-tui/tui/testagent"
)

// demoAgent wraps the scripted testagent and adds capability
// implementations so the visual preview exercises real end-to-end
// flows: /btw opens a SideAnswer modal via SlashProvider, and
// (when Options.MidTurnInjectionMode == InjectIntoCurrent) typing
// during streaming routes through InjectableAgent.Inject. A real
// host's agent exposes these on its own type; this composition is
// for the visual harness only.
type demoAgent struct{ tui.Agent }

// Inject implements tui.InjectableAgent. The demo doesn't actually
// feed the message into the scripted playback — it just returns nil
// so the queue panel can render an injected entry. A real host
// would push the message onto an inbox / context-augmentation channel.
func (demoAgent) Inject(_ string) error { return nil }

// wakeCh and demoWaker drive the WakeRequester demo (R-WAKE-1). A
// goroutine fires a wake every 25 seconds so the toast banner
// surfaces a few times across a session.
var wakeCh = makeDemoWakeChannel()

func makeDemoWakeChannel() chan struct{} {
	ch := make(chan struct{}, 4)
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
	return ch
}

// WakeRequested implements tui.WakeRequester. Returns the shared
// demo channel; real hosts return their agent's wake channel.
func (demoAgent) WakeRequested() <-chan struct{} { return wakeCh }

// demoPermissionAfter fires a synthetic permission prompt after
// delay so the visual preview can demo the modal. The decision
// returned by the operator is just printed to stderr — the demo
// agent doesn't actually need approval.
func demoPermissionAfter(p tui.PermissionPrompter, delay time.Duration) {
	time.Sleep(delay)
	_, _ = p.AskApproval(context.Background(), tui.PermissionRequest{
		Kind:        tui.PermissionKindEdit,
		ToolName:    "Write",
		Detail:      "- if user.Email == \"\" {\n+ if user.Email == \"\" || !strings.Contains(user.Email, \"@\") {",
		DetailKind:  tui.DetailDiff,
		Verb:        "edit",
		PersistTool: "edit",
		PersistKey:  "internal/auth/session.go",
	})
}

// demoElicitAfter fires a synthetic MCP elicit request after delay
// so the visual preview demos the form modal end-to-end.
func demoElicitAfter(e tui.Elicitor, delay time.Duration) {
	time.Sleep(delay)
	_, _ = e.Elicit(context.Background(), "github", tui.ElicitRequest{
		Mode:        tui.ElicitFormMode,
		Title:       "repository access",
		Description: "the github MCP server needs a repo + branch to push to",
		Fields: []tui.ElicitField{
			{Name: "repo", Type: tui.ElicitFieldString, Required: true, Default: "go-steer/core-tui"},
			{Name: "branch", Type: tui.ElicitFieldString, Required: true, Default: "main"},
			{Name: "force", Type: tui.ElicitFieldBoolean, Description: "force push", Default: false},
			{Name: "visibility", Type: tui.ElicitFieldEnum, EnumChoices: []string{"public", "private", "internal"}, Default: "private"},
		},
	})
}

func (demoAgent) SlashCommands() []tui.SlashCommandSpec {
	return []tui.SlashCommandSpec{
		{
			Name:        "btw",
			Aliases:     []string{"by-the-way"},
			Description: "ask a side question (modal, doesn't land in chat history)",
		},
	}
}

func (demoAgent) InvokeSlash(_ context.Context, name, args string) (tui.SlashResult, error) {
	if name != "btw" && name != "by-the-way" {
		return tui.SlashResult{}, fmt.Errorf("unknown slash: %s", name)
	}
	q := args
	if q == "" {
		q = "what's on the agenda?"
	}
	answer := "**Side-question answer** rendered through *Glamour* in a transient modal.\n\n" +
		"This is what `/btw " + q + "` would surface from the agent's `AskSideQuestion`.\n\n" +
		"- Question came from `args` after the slash.\n" +
		"- Answer renders as Markdown.\n" +
		"- Dismiss with `Esc`, `Enter`, or `Space`.\n" +
		"- Nothing lands in chat history."
	return tui.SlashResult{ModalAnswer: &tui.SideAnswer{Question: q, Answer: answer}}, nil
}

func main() {
	prompter := tui.NewPrompter()
	elicitor := tui.NewElicitor()

	// Fire a fake permission prompt + elicit request a few seconds
	// after launch so the visual preview demos both modals end-to-
	// end. A real host wires these into its permission gate +
	// MCP servers.
	go demoPermissionAfter(prompter, 8*time.Second)
	go demoElicitAfter(elicitor, 18*time.Second)

	opts := tui.Options{
		// Scripted agent plays a believable coding-task turn on every
		// submit so the operator can see streaming + spinner + Glamour
		// + per-turn footer end-to-end. Same script regardless of
		// prompt — it's a visual harness, not a real agent.
		Agent:        demoAgent{Agent: testagent.NewScripted(testagent.CodingDemo())},
		Prompter:     prompter,
		Elicitor:     elicitor,
		StatusLayout: tui.StatusHeader,
		// QueueForNext (default) demos R-CHAT-10 — type-ahead entries
		// buffer as ○ queued and drain on turn-end. Flip to
		// InjectIntoCurrent to demo R-CHAT-11 — entries land as
		// ✓ Done (injected) immediately, no auto-drain.
		MidTurnInjectionMode: tui.QueueForNext,
		PermissionMode: tui.PermissionModeWiring{
			Initial: tui.PermissionModeDefault,
			Set:     func(m tui.PermissionMode) error { return nil },
		},
		SeedHistory: seededConversation(),
	}
	if err := tui.Run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, "core-tui:", err)
		os.Exit(1)
	}
}

// seededConversation hardcodes a multi-turn agent-coding session that
// exercises every renderer path: user prompt, multi-paragraph
// assistant reply, tool calls (Read + Bash), a system info line, and
// an error line. Edit freely while iterating on the visual style.
func seededConversation() []tui.Message {
	return []tui.Message{
		{
			Role: tui.RoleSystem,
			Text: "Visual preview — type ? for the full keymap. Try: / for slash palette · " +
				"@ for file palette · ctrl+g model · ctrl+y permission · ctrl+e elicit · " +
				"ctrl+b toggle layout · shift+tab cycle perm-mode · /btw <q> for a side-answer modal. " +
				"Press enter to start a streaming turn; type ahead and press enter again to " +
				"queue follow-up prompts — they auto-fire as each turn ends.",
		},
		{
			Role: tui.RoleUser,
			Text: "Add a NOT NULL constraint to users.email and write the migration.",
		},
		{
			Role: tui.RoleAssistant,
			Text: "Got it. I'll start by reading the existing schema to confirm the current column definition, then write the migration.",
		},
		{
			Role:     tui.RoleTool,
			ToolName: "Read",
			ToolArgs: "db/schema/users.sql",
		},
		{
			Role: tui.RoleAssistant,
			Text: "The email column is currently `VARCHAR(255)` with no constraint. " +
				"I'll add a single migration that backfills NULLs to empty strings " +
				"(so the constraint can be added safely) and then adds NOT NULL.",
		},
		{
			Role:     tui.RoleTool,
			ToolName: "Write",
			ToolArgs: "db/migrations/0042_users_email_not_null.sql",
		},
		{
			Role:     tui.RoleTool,
			ToolName: "Bash",
			ToolArgs: "psql -f db/migrations/0042_users_email_not_null.sql",
		},
		{
			Role: tui.RoleSystem,
			Text: "Migration applied to the dev database (0 rows changed).",
		},
		{
			Role: tui.RoleAssistant,
			Text: "Done. The migration is at `db/migrations/0042_users_email_not_null.sql` " +
				"and verifies cleanly against dev. Want me to also write the matching " +
				"down-migration?",
			Model:   "Claude Sonnet 4.6",
			Usage:   &tui.Usage{InputTokens: 8421, OutputTokens: 2103},
			CostUSD: 0.0124,
			Elapsed: 4*time.Second + 200*time.Millisecond,
		},
		{
			Role: tui.RoleError,
			Text: "Sample error row for visual reference (renderer path: RoleError).",
		},
	}
}
