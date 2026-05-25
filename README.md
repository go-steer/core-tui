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

Pre-v0.1. The repo currently ships only the design docs — implementation
is in progress. The library is consolidating the duplicated TUI code
from [`go-steer/cogo`](https://github.com/go-steer/cogo) and
[`go-steer/core-agent`](https://github.com/go-steer/core-agent); both
will migrate to depend on it.

## Documentation

- **[Requirements](./docs/requirements.md)** — user-visible behavior
  the library must deliver.
- **[Design](./docs/design.md)** — module layout, the plug-in surface,
  lifecycle, test strategy.
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
`examples/` (`local`, `permissions`, `cogo`, `core-agent`) once the
implementation lands.

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the development workflow,
DCO sign-off requirement, and the no-`Co-Authored-By` policy. Local CI
is one command:

```bash
dev/tools/ci
```

## License

[Apache License 2.0](./LICENSE).
