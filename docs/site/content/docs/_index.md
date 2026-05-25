---
title: Documentation
linkTitle: Documentation
weight: 1
menu:
  main:
    weight: 10
---

You're in the `core-tui` reference docs. The site root has the marketing pitch; this section is the reference.

## Status

`core-tui` is pre-v0.1. The two normative documents are the source of truth while implementation is in progress:

- **[Requirements](https://github.com/go-steer/core-tui/blob/main/docs/requirements.md)** — every user-visible behavior the library must deliver, indexed by stable IDs (`R-CHAT-1`, `R-PERM-2`, …).
- **[Design](https://github.com/go-steer/core-tui/blob/main/docs/design.md)** — module layout, the plug-in surface (the `Agent` interface, capability interfaces, `Options`, `Prompter`/`Elicitor`), lifecycle, and the host-adapter recipe.

Both live in the repo so they version with the code. Once `package tui` lands, the long-form reference pages — host adapter walkthrough, capability matrix, slash-command catalog, modal contracts — will appear here.

## What this library is

`core-tui` is the operator-facing surface for any Go agent that wants a polished terminal UI without writing one. It owns the chat loop, slash commands, command palette, `@file` expansion, permissions modal, MCP elicitation, transcript persistence, and Glamour-rendered markdown. It does **not** define an agent loop, pick LLM providers, drive MCP servers, or open files — those are the host's concern.

The integration seam is a small Go interface set: hosts implement `tui.Agent` (one method, `Run`), optionally implement any subset of the capability interfaces, and call `tui.Run(ctx, opts)`. See [`docs/design.md` §3](https://github.com/go-steer/core-tui/blob/main/docs/design.md#3-the-plug-in-surface) for the full shape.

## Help and community

- **Source code** → [github.com/go-steer/core-tui](https://github.com/go-steer/core-tui)
- **Issues** → [github.com/go-steer/core-tui/issues](https://github.com/go-steer/core-tui/issues) — bug reports, feature requests
- **Discussions** → [github.com/go-steer/core-tui/discussions](https://github.com/go-steer/core-tui/discussions) — questions, what-are-you-building threads
- **Releases & changelog** → [latest releases](https://github.com/go-steer/core-tui/releases)
