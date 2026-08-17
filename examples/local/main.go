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
//	tab             move the keyboard between the composer and the
//	                transcript (core-tui #151 / #155); tab, enter or
//	                esc gives it back
//	ctrl+b          toggle StatusHeader <-> StatusSidebar
//	shift+tab       cycle the permission mode chip
//	ctrl+p          open the (sample) command palette
//	ctrl+g, /model  open the (sample) model picker; the pick moves
//	                the header's model + provider and survives a
//	                /switch
//	ctrl+x          open the tool-call detail overlay (core-tui #52)
//	ctrl+y          open the (sample) permission modal
//	ctrl+e          open the (sample) MCP elicitation form
//	/switch         open the session picker; its "+ Attach to
//	                endpoint…" row demos the text-input dialog
//	                (core-tui #56)
//	esc             close any open modal
//
// With the transcript holding the keyboard, the last seeded turn — a
// wide, tall one, put there on purpose — is what the rest of the
// keymap is worth trying on:
//
//	up/down, k/j    move the cursor an item at a time (core-tui #152)
//	space           fold the selected item to its first three lines,
//	                and unfold it again
//	y / c           copy the item, or just the code in it, to the
//	                clipboard via OSC 52 (core-tui #153) — the
//	                footer says which mechanism it went out by, and
//	                a terminal that declines the escape is why it
//	                bothers (core-tui #175)
//	shift+up/down   scroll a line inside an item taller than the
//	                window
//	shift+left/     pan sideways over content too wide to fit — the
//	  shift+right   footer says how far (core-tui #154)
//	g / G           jump to the first / last item; G also re-arms
//	                following the stream
//
// Flags:
//
//	-verbose-tools  append full args + response detail under every
//	                tool row (core-tui #52 tier 2)
//	-clipboard-file also write y / c copies to this path (core-tui
//	                #175). Unset, the harness uses
//	                tui.SystemClipboardWriter(), which finds nothing
//	                on a box with no clipboard of its own and leaves
//	                OSC 52 as the only write. Point this at a file
//	                open in a locally-rendered editor and a copy
//	                made over SSH becomes reachable from the desktop
//	                the operator is actually sitting at.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
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
type demoAgent struct {
	tui.Agent

	// model is the /model pick this instance was built for. It is
	// per-instance and never mutated, which is what makes Status()
	// safe to call from the host-snapshot goroutine while a
	// SwitchModel is in flight on another: the switch produces a NEW
	// demoAgent rather than writing to this one.
	model string
}

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

// demoModels is the /model catalog. The entries are deliberately
// provider-qualified and the descriptions deliberately long: the
// picker lays each row out as an ID column plus a dim subtitle, so
// short placeholder names would leave the column arithmetic and the
// modal's right edge untested — which is the one thing a visual
// harness is for. The last row is longer than the picker is wide on
// purpose, so truncation shows up too.
var demoModels = []tui.ModelInfo{
	{ID: "anthropic/claude-opus-5", Display: "claude-opus-5",
		Description: "frontier tier — deepest reasoning, slowest, priciest"},
	{ID: "anthropic/claude-sonnet-5", Display: "claude-sonnet-5",
		Description: "balanced tier — the default for interactive coding"},
	{ID: "anthropic/claude-haiku-4-5", Display: "claude-haiku-4-5",
		Description: "fast tier — subtasks, classification, cheap fan-out"},
	{ID: "google/gemini-3.1-pro", Display: "gemini-3.1-pro",
		Description: "long context, strong multimodal"},
	{ID: "openai/gpt-5.1", Display: "gpt-5.1",
		Description: "alternate vendor, for A/B-ing a prompt"},
	{ID: "local/qwen3-coder-30b-a3b-instruct",
		Description: "runs on the box — no network, no spend, and no subtitle shorter than this row is wide"},
}

// AvailableModels implements the read half of tui.ModelSwapper
// (/model, also bound to ctrl+g). Returning a package-level slice is
// fine here because nothing mutates it; a real host reads its own
// config or asks its provider registry.
func (demoAgent) AvailableModels() []tui.ModelInfo { return demoModels }

// SwitchModel implements the write half of tui.ModelSwapper. Note it
// returns a tui.Agent rather than an error-or-nothing: the host hands
// back the agent to talk to from now on, so even a demo that changes
// nothing but a label has to build one. The scripted playback is
// carried across unchanged, which is why every model answers with the
// same turn.
//
// An unknown ID is a real error and demos the RoleError path, the
// same way SwitchToSession does.
func (a demoAgent) SwitchModel(modelID string) (tui.Agent, error) {
	for _, mi := range demoModels {
		if mi.ID == modelID {
			return demoAgent{Agent: a.Agent, model: modelID}, nil
		}
	}
	return nil, fmt.Errorf("unknown model %q", modelID)
}

// Status implements tui.StatusReporter. Without it the harness has no
// way to say which model is live, so the header reads "(model not
// set)" and the picker marks nothing as (current) — both of which
// make the /model demo hard to read. Provider is the ID's leading
// segment, which is also what Options.AutoProviderTheme keys off.
func (a demoAgent) Status() tui.Status {
	provider, _, _ := strings.Cut(a.model, "/")
	return tui.Status{ModelName: a.model, Provider: provider, State: "idle"}
}

// Sessions implements tui.SessionSwitcher. Two fake sessions plus
// the action row from core-tui #56: a row carrying SessionInput is
// rendered with a ▸ chevron and, on Enter, opens a single-line
// text-input dialog stacked on the picker instead of switching. The
// typed value goes to SessionInput.Submit, which returns the
// SwitchTarget — no magic IDs round-tripping through
// SwitchToSession. A real multi-daemon host dials the URL here.
func (a demoAgent) Sessions() []tui.SessionInfo {
	return []tui.SessionInfo{
		{ID: "local", Display: "local", Description: "in-process scripted agent", Current: true},
		{ID: "staging", Display: "staging", Description: "fake remote daemon"},
		{
			ID:      "attach",
			Display: "+ Attach to endpoint…",
			Input: &tui.SessionInput{
				Title:       "Attach to Endpoint",
				Prompt:      "Daemon URL:",
				Placeholder: "http://otherhost:7778",
				Validate: func(v string) string {
					if v == "" {
						return "endpoint is required"
					}
					if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
						return "endpoint must start with http:// or https://"
					}
					return ""
				},
				Submit: func(v string) (tui.SwitchTarget, error) {
					return tui.SwitchTarget{
						Agent: demoAgent{Agent: testagent.NewScripted(testagent.CodingDemo()), model: a.model},
						Note:  "Attached to " + v + " (demo — nothing was actually dialed)",
					}, nil
				},
			},
		},
	}
}

// SwitchToSession implements tui.SessionSwitcher for the plain
// (non-action) rows. Hands back a fresh scripted agent so the demo
// shows the history wipe + re-paint. Action-row IDs never land here
// — that's the point of SessionInput.Submit — so an unknown ID is a
// real error and demos the RoleError path.
//
// The model pick rides across the switch. A session change replaces
// the agent, so anything the operator chose that the new agent
// doesn't carry forward is silently lost — here that would show up
// as the header reverting to a model nobody selected.
func (a demoAgent) SwitchToSession(id string) (tui.SwitchTarget, error) {
	if id != "local" && id != "staging" {
		return tui.SwitchTarget{}, fmt.Errorf("unknown session %q", id)
	}
	return tui.SwitchTarget{
		Agent: demoAgent{Agent: testagent.NewScripted(testagent.CodingDemo()), model: a.model},
		Note:  "Attached to session " + id,
	}, nil
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
	// The library itself has no CLI surface — Options is the seam
	// (docs/design.md §3). This example binary layers a couple of
	// flags on top so operators can toggle demo-relevant knobs
	// without editing + rebuilding.
	verboseTools := flag.Bool("verbose-tools", false,
		"append full args + response detail under every tool row (core-tui #52 tier 2)")
	clipboardFile := flag.String("clipboard-file", "",
		"also write y / c copies to this file (core-tui #175) — the way to reach a "+
			"local clipboard from a remote box: keep the file open in a locally-rendered "+
			"editor and copy from there")
	flag.Parse()

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
		// Boots on the middle tier so /model has somewhere to move
		// from in both directions.
		Agent:        demoAgent{Agent: testagent.NewScripted(testagent.CodingDemo()), model: "anthropic/claude-sonnet-5"},
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
		SeedHistory:       seededConversation(),
		ToolDetailVerbose: *verboseTools,
		// What a host on a desktop wires so y / c reach the system
		// clipboard as well as the terminal's (issue #175). Returns
		// nil — and is therefore a no-op — on a machine with no
		// clipboard to write to, which is why it needs no guard.
		ClipboardWriter: tui.SystemClipboardWriter(),
	}
	if *clipboardFile != "" {
		// The other kind of host writer, and the only one that works
		// on a box with no clipboard of its own: a sink the operator
		// can reach from the machine they are sitting at. An editor
		// showing this file renders LOCALLY even when the process is
		// remote, so copying out of it uses the real desktop
		// clipboard — the thing OSC 52 was trying and failing to
		// reach.
		opts.ClipboardWriter = func(text string) error {
			return os.WriteFile(*clipboardFile, []byte(text), 0o600)
		}
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
				"@ for file palette · tab focus the transcript (then ↑↓ / k j select an item, " +
				"space fold it, y / c copy it, shift+↑↓ scroll a line, shift+←→ pan sideways, " +
				"g / G first / last, tab or esc back) · ctrl+g model · ctrl+y permission · ctrl+e elicit · " +
				"ctrl+b toggle layout · shift+tab cycle perm-mode · /btw <q> for a side-answer modal · " +
				"/switch for the session picker (its last row types in an endpoint). " +
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
			// Structured args + response mirror the compact display
			// string so the Ctrl+X detail overlay (core-tui #52
			// tier 1) shows real content on seeded rows, not just
			// on rows freshly emitted by a scripted turn.
			// ToolLatencyMs demos the tier-3 `[Ns]` badge + dialog
			// chip (core-tui #60 / SSE spec v1.2.0) inline.
			ToolArgsMap: map[string]any{"path": "db/schema/users.sql"},
			ToolResponseMap: map[string]any{
				"content": "CREATE TABLE users (\n  id BIGSERIAL PRIMARY KEY,\n  email VARCHAR(255),\n  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()\n);\n",
			},
			ToolLatencyMs: 320,
		},
		{
			Role: tui.RoleAssistant,
			Text: "The email column is currently `VARCHAR(255)` with no constraint. " +
				"I'll add a single migration that backfills NULLs to empty strings " +
				"(so the constraint can be added safely) and then adds NOT NULL.",
		},
		{
			Role:        tui.RoleTool,
			ToolName:    "Write",
			ToolArgs:    "db/migrations/0042_users_email_not_null.sql",
			ToolArgsMap: map[string]any{"path": "db/migrations/0042_users_email_not_null.sql"},
			ToolResponseMap: map[string]any{
				"path":          "db/migrations/0042_users_email_not_null.sql",
				"bytes_written": 512,
				"lines_written": 12,
			},
			ToolLatencyMs: 180,
		},
		{
			Role:        tui.RoleTool,
			ToolName:    "Bash",
			ToolArgs:    "psql -f db/migrations/0042_users_email_not_null.sql",
			ToolArgsMap: map[string]any{"command": "psql -f db/migrations/0042_users_email_not_null.sql"},
			ToolResponseMap: map[string]any{
				"stdout":    "BEGIN\nUPDATE 0\nALTER TABLE\nCOMMIT\n",
				"exit_code": 0,
			},
			ToolLatencyMs: 2400,
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
			Role: tui.RoleUser,
			Text: "Show me the table as it stands now, and the lookup that motivated the constraint.",
		},
		{
			Role: tui.RoleAssistant,
			// Deliberately wide AND tall. Preformatted blocks do not
			// wrap, so this is the turn with something for shift+←→
			// to pan over (core-tui #154) and enough lines for space
			// to fold (core-tui #152); two fenced blocks, so `c`
			// reports copying both (core-tui #153).
			Text: "Here is the table after the migration, and the query the composite index is there for.\n\n" +
				"```\n" +
				"                                            Table \"public.users\"\n" +
				"   Column    |           Type           | Collation | Nullable |              Default              | Storage  | Description\n" +
				"-------------+--------------------------+-----------+----------+-----------------------------------+----------+--------------------------------------\n" +
				" id          | bigint                   |           | not null | nextval('users_id_seq'::regclass) | plain    |\n" +
				" email       | character varying(255)   |           | not null |                                   | extended | login identity, unique per tenant\n" +
				" created_at  | timestamp with time zone |           | not null | now()                             | plain    |\n" +
				" updated_at  | timestamp with time zone |           |          |                                   | plain    | touched by the audit trigger\n" +
				" tenant_id   | bigint                   |           | not null |                                   | plain    | FK -> tenants(id), ON DELETE CASCADE\n" +
				"Indexes:\n" +
				"    \"users_pkey\" PRIMARY KEY, btree (id)\n" +
				"    \"users_email_tenant_key\" UNIQUE CONSTRAINT, btree (email, tenant_id)\n" +
				"Foreign-key constraints:\n" +
				"    \"users_tenant_id_fkey\" FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE\n" +
				"```\n\n" +
				"The plan confirms the index is doing the work rather than a sequential scan:\n\n" +
				"```sh\n" +
				"psql -c 'EXPLAIN (ANALYZE, BUFFERS) SELECT id FROM users WHERE email = $1 AND tenant_id = $2'\n" +
				"```\n",
		},
		{
			Role: tui.RoleError,
			Text: "Sample error row for visual reference (renderer path: RoleError).",
		},
	}
}
