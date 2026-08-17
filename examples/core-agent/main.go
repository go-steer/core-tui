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

// Command core-agent is the reference-host adapter sketch (issue
// #82): what a core-agent-shaped host writes to satisfy core-tui's
// plug-in surface, in the two flavors requirements.md I-CORE-AGENT
// names — the local in-process agent, and the `attachclient` flavor
// talking to a remote agent over HTTP.
//
// # Why it exists
//
// This is the compile-time canary docs/design.md §7 asks for. The
// adapters here implement 18 of core-tui's plug-in interfaces, so a
// rename, a signature change, or an interface split in package tui
// fails `go build ./...` in this repo's own CI — before core-agent
// finds out by upgrading. #80's apidiff gate catches surface
// CHANGES; this catches the usability changes that still typecheck
// in isolation but leave a host unable to express what it means.
//
// The adapter code is local.go (in-process), attach.go (remote) and
// translate.go (the event mapping both share). Read those; main.go
// is just enough wiring to make them run.
//
// # It is a sketch, and honest about it
//
//   - There is no dependency on github.com/go-steer/core-agent. That
//     module depends on core-tui, so importing it would close an
//     ecosystem-level import cycle and chain this repo's CI to
//     another repo's release cadence. The host lives in
//     ./fakehost — a local stand-in with the same SHAPES (method
//     names, signatures, the ADK-style nested event tree) as
//     core-agent's `pkg/agent.Agent` and
//     `internal/attachclient.Client`.
//   - There is no model behind it. Every submission replays one
//     canned turn, the way tui/testagent does.
//   - The attach flavor's "remote" daemon is real HTTP on loopback,
//     started in-process by this binary. The socket, the SSE frames
//     and the resume cursor are genuine; the agent behind them is
//     the same fake.
//
// # Running it
//
//	go run ./examples/core-agent                     # local flavor
//	go run ./examples/core-agent -flavor attach      # remote, per-turn
//	go run ./examples/core-agent -flavor attach -observer
//
// The third form is observer mode: core-tui drives tui.LiveAgent
// instead of Agent.Run and paints whatever the daemon is doing,
// typed at or not.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/go-steer/core-tui/examples/core-agent/fakehost"
	"github.com/go-steer/core-tui/tui"
)

func main() {
	flavor := flag.String("flavor", "local",
		"which adapter to run: `local` (in-process agent) or `attach` (remote agent over HTTP)")
	observer := flag.Bool("observer", false,
		"attach flavor only: drive tui.LiveAgent instead of Agent.Run")
	flag.Parse()

	if err := run(context.Background(), *flavor, *observer); err != nil {
		fmt.Fprintln(os.Stderr, "core-agent example:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, flavor string, observer bool) error {
	switch flavor {
	case "local":
		return runLocal(ctx)
	case "attach":
		return runAttach(ctx, observer)
	default:
		return fmt.Errorf("unknown -flavor %q (want local or attach)", flavor)
	}
}

// runLocal is the shape of core-agent's own
// `cmd/core-agent/coretui_enabled.go`: build the agent, wrap it,
// hand the TUI's prompter + elicitor to the host's gate and MCP
// layer BEFORE the first turn, fill in Options, call tui.Run.
func runLocal(ctx context.Context) error {
	agent := fakehost.NewAgent("")
	adapter := &localAdapter{inner: agent}

	// Step 3 of the adapter contract (design.md §6.0): core-tui
	// SUPPLIES these two, and the host wires them into the places
	// that need an operator decision. A real host passes the
	// prompter to its permission gate and the elicitor to its MCP
	// client; there is nothing to wire them into here.
	prompter := tui.NewPrompter()
	elicitor := tui.NewElicitor()
	notifier := tui.NewNotifier()

	opts := tui.Options{
		Agent:        adapter,
		UsageTracker: &localUsage{inner: agent},
		Prompter:     prompter,
		Elicitor:     elicitor,
		Notifier:     notifier,
		Branding: tui.Branding{
			Wordmark:      "core-agent",
			AgentIdentity: "local",
		},
		StatusLayout: tui.StatusHeader,
		Memory:       memoryFeed(),
		MCPServers:   mcpFeed(),
		Skills:       skillFeed(),
		// Inject + DrainInbox + PendingInboxCount are all wired, so
		// core-tui can run the whole auto-continue loop: type during
		// a turn, and the notes fire as a synthetic follow-up when
		// the turn ends.
		MidTurnInjectionMode: tui.AutoContinueFromInbox,
		PermissionMode: tui.PermissionModeWiring{
			Initial: tui.PermissionModeDefault,
			Set:     func(tui.PermissionMode) error { return nil },
		},
		SeedHistory: []tui.Message{{
			Role: tui.RoleSystem,
			Text: "core-agent adapter sketch — LOCAL flavor. The agent is a fake with one " +
				"canned turn; the point is the adapter in local.go. Capabilities wired: " +
				"/model /reload /permissions /pricing /tools /subagents /btw /stats, plus " +
				"mid-turn inject with auto-continue and the wake toast (/wake). Run with " +
				"-flavor attach for the attachclient flavor.",
		}},
	}
	return tui.Run(ctx, opts)
}

// runAttach starts the toy daemon, points a client at it, and hands
// core-tui the remote adapter — the shape of core-agent's
// `cmd/core-agent-tui/main.go`, minus the URL parsing and the
// pre-attach session picker.
func runAttach(ctx context.Context, observer bool) error {
	daemon, err := fakehost.StartDaemon(fakehost.NewAgent(""))
	if err != nil {
		return err
	}
	defer func() { _ = daemon.Close() }()

	adapter := newAttachAdapter(fakehost.NewClient(daemon.URL()), fakehost.SessionPath)

	// The choice that has to be explicit: hand core-tui the bare
	// adapter and it drives Agent.Run per submission; hand it the
	// observer wrapper and tui.LiveAgent takes over the whole
	// session, with typed input routed through InjectableAgent.
	var agent tui.Agent = adapter
	mode := "per-turn"
	if observer {
		agent = attachObserver{attachAdapter: adapter}
		mode = "observer (LiveAgent)"
	}

	// Attach mode has no local permission gate — approvals happen on
	// the daemon, and a real core-agent-tui subscribes to a remote
	// prompt stream and bridges it into this prompter. The fake
	// daemon has no such stream, so the prompter sits idle.
	prompter := tui.NewPrompter()

	opts := tui.Options{
		Agent:        agent,
		UsageTracker: adapter,
		Prompter:     prompter,
		Branding: tui.Branding{
			Wordmark:      "core-agent-tui",
			AgentIdentity: "demo-session",
		},
		StatusLayout: tui.StatusHeader,
		SeedHistory: []tui.Message{{
			Role: tui.RoleSystem,
			Text: "core-agent adapter sketch — ATTACH flavor (" + mode + "), talking to a " +
				"throwaway daemon at " + daemon.URL() + " over real HTTP + SSE. Capabilities " +
				"wired: /tools /subagents /switch /interrupt /btw /stats. /model /reload " +
				"/permissions /pricing are deliberately absent — the attach API has no RPCs " +
				"behind them, so they degrade to \"not available\", which is the graceful " +
				"path the capability design promises.",
		}},
	}
	return tui.Run(ctx, opts)
}
