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

func main() {
	opts := tui.Options{
		Agent:        testagent.New(),
		StatusLayout: tui.StatusHeader,
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
