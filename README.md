# core-tui

[![CI](https://github.com/go-steer/core-tui/actions/workflows/ci.yml/badge.svg)](https://github.com/go-steer/core-tui/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-steer/core-tui.svg)](https://pkg.go.dev/github.com/go-steer/core-tui)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

A reusable [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI
for agentic assistants. core-tui owns the operator-facing surface — chat
loop, slash commands, command palette, `@file` expansion, permissions
modal, MCP elicitation, transcript persistence — and integrates with any
agent via a small Go interface.

It is intentionally agent-framework agnostic: nothing in `package tui`
imports an LLM SDK, an MCP SDK, or an agent runtime. Hosts adapt their
own agent types to the interface in
[`docs/design.md`](./docs/design.md) §3 and call `tui.Run(ctx, opts)`.

## Status

Latest release: [v0.9.1](https://github.com/go-steer/core-tui/releases/latest).
Pre-1.0 — public API is largely stable but minor breaking changes can
ship in 0.X minor bumps; see the
[release notes](https://github.com/go-steer/core-tui/releases) for
what's in each version.

The reference host is
[`go-steer/core-agent`](https://github.com/go-steer/core-agent), which
tracks the latest release and exercises the full capability surface:
theme picker, `Notifier` side channel, SSE push-mode subscriber, and
the v2 transcript schema.

## Documentation

- **[Requirements](./docs/requirements.md)** — user-visible behavior
  the library must deliver.
- **[Design](./docs/design.md)** — module layout, the plug-in surface,
  lifecycle, test strategy.
- **[Style](./docs/style.md)** — visual house style (palette, glyphs,
  spacing, modal composition).
- **[Decisions](./docs/decisions.md)** — every load-bearing choice
  with rationale + rejected alternatives.
- **[UI references](./docs/ui-references.md)** — observations from
  other agentic TUIs (Antigravity, Claude Code, Codex, Crush, Cursor).
- **[Migration guide](./MIGRATION.md)** — adapter contract + per-host
  capability mapping (cogo, core-agent local + attach) + sketches.
- **Site** — https://go-steer.github.io/core-tui/ (published once the
  first commit lands).

## Install

```bash
go get github.com/go-steer/core-tui
```

Requires Go ≥ 1.23.

## Quick start

A host implements the `tui.Agent` interface and calls `tui.Run`:

```go
package main

import (
    "context"

    "github.com/go-steer/core-tui/tui"
)

func main() {
    ctx := context.Background()
    if err := tui.Run(ctx, tui.Options{
        Agent: myAgent{},          // implements tui.Agent
        // Optional: any capability interfaces the host wants to light up
        // — ModelSwapper, Reloader, PermissionController, SlashProvider, …
    }); err != nil {
        panic(err)
    }
}
```

See [`docs/design.md`](./docs/design.md) §3 for the full interface set
and §6 for the host-adapter recipe. Worked examples live under
[`examples/`](./examples/):

- [`examples/local`](./examples/local) — visual-preview binary running
  against a scripted test agent; exercises every renderer path (chat,
  tool calls, slash palette, modal forms, theme picker, status surfaces).
- [`examples/notifier-smoke`](./examples/notifier-smoke) — standalone
  harness for the [`Notifier`](./tui/notifier.go) capability: a
  producer goroutine pushes realistic notices on a rotation (plus a
  periodic burst to demonstrate the drop-with-coalescence backpressure
  marker).

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the development workflow,
DCO sign-off requirement, and the no-`Co-Authored-By` policy. Local CI
is one command:

```bash
dev/tools/ci
```

## License

[Apache License 2.0](./LICENSE).
