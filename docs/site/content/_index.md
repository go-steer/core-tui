---
title: core-tui
---

{{< blocks/cover title="core-tui" image_anchor="top" height="med" >}}

<p class="lead mt-5">
A reusable Bubble Tea TUI for agentic assistants. Drop it into your Go program, supply a small agent interface, ship.
</p>

<a class="btn btn-lg btn-primary me-3 mb-4" href="docs/">Read the docs <i class="fa-solid fa-arrow-right ms-2"></i></a>
<a class="btn btn-lg btn-secondary me-3 mb-4" href="https://github.com/go-steer/core-tui">Source on GitHub <i class="fa-brands fa-github ms-2"></i></a>

{{< /blocks/cover >}}

{{% blocks/lead color="primary" %}}

`core-tui` consolidates the duplicated terminal UI from [`go-steer/cogo`](https://github.com/go-steer/cogo) and [`go-steer/core-agent`](https://github.com/go-steer/core-agent) into one library. It owns the operator-facing surface — chat loop, slash commands, command palette, `@file` expansion, permissions modal, MCP elicitation, transcript persistence — and integrates with any agent via a small, documented Go interface set.

{{% /blocks/lead %}}

{{% blocks/section color="dark" type="row" %}}

{{% blocks/feature icon="fa-solid fa-comments" title="Streaming chat" %}}
Multi-line input, role-tagged history, mouse-wheel scrolling, Glamour-rendered markdown that updates live while the model streams.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-solid fa-shield-halved" title="Permissions modal" %}}
Six-decision approval flow (once / session / verb / tool / always / deny) with host-supplied persistence, plus `/permissions` review and `/allow` / `/deny` live editing.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-solid fa-puzzle-piece" title="Capability composition" %}}
Every advanced feature — model swap, reload, pricing, sub-agents, tool listing, host-defined commands — is an opt-in capability the TUI feature-detects. Missing ones degrade to a "not available" hint.
{{% /blocks/feature %}}

{{% /blocks/section %}}

{{% blocks/section %}}

## Install

```bash
go get github.com/go-steer/core-tui
```

Requires Go ≥ 1.23. The library is pre-v0.1 — see the [docs](docs/) for the current shape and progress.

{{% /blocks/section %}}
