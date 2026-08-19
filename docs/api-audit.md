<!-- Generated audit. See "How this document was produced". -->

# Exported API audit — `package tui`

**Audit date:** 2026-08-14 · **HEAD:** [`558e3db`](https://github.com/go-steer/core-tui/commit/558e3dbd179dd72f4e8acb8ac262f260aa2f7bca) · **Latest release:** v0.19.0 · **Reference host:** `core-agent` @ core-tui v0.18.0

Decision material for the three breaking-change issues that must land before the 1.0 freeze: [#77](https://github.com/go-steer/core-tui/issues/77) (capability consolidation), [#78](https://github.com/go-steer/core-tui/issues/78) (exported-surface audit), [#79](https://github.com/go-steer/core-tui/issues/79) (vestigial-path removal). All three turn on the same two questions — what is structurally reachable from the documented contract, and what the only real host actually uses — so they are answered here in one pass.

---

## Contents

- **1** [How this document was produced](#1-how-this-document-was-produced)
- **2** [The surface at a glance](#2-the-surface-at-a-glance)
    - **2.1** [Contract closure vs. host usage](#21-contract-closure-vs-host-usage)
- **3** [Capability implementation matrix](#3-capability-implementation-matrix)
    - **3.1** [Two findings that fall out of the matrix](#31-two-findings-that-fall-out-of-the-matrix)
- **4** [Decision — #79, vestigial paths](#4-decision--79-vestigial-paths)
    - **4.1** [The overlay enum: confirmed dead, delete](#41-the-overlay-enum-confirmed-dead-delete)
    - **4.2** [The `usageMsg` item: false positive, keep](#42-the-usagemsg-item-false-positive-keep)
- **5** [Decision — #77, capability consolidation](#5-decision--77-capability-consolidation)
    - **5.1** [Reject the mitigation as written](#51-reject-the-mitigation-as-written)
    - **5.2** [Delete the three with zero implementations](#52-delete-the-three-with-zero-implementations)
    - **5.3** [Merge the redundant pair](#53-merge-the-redundant-pair)
    - **5.4** [The judgment call: whether to go further](#54-the-judgment-call-whether-to-go-further)
- **6** [Decision — #78, the exported surface](#6-decision--78-the-exported-surface)
    - **6.1** [The real finding: untyped vocabularies](#61-the-real-finding-untyped-vocabularies)
    - **6.2** [The 91 unexported candidates split three ways](#62-the-91-unexported-candidates-split-three-ways)
- **7** [Sequencing](#7-sequencing)
    - **7.1** [Open questions this document does not settle](#71-open-questions-this-document-does-not-settle)
    - **7.2** [Downstream follow-ups for core-agent](#72-downstream-follow-ups-for-core-agent)
- **8** [Full symbol inventory](#8-full-symbol-inventory)
    - **8.1** [Entry point and configuration](#81-entry-point-and-configuration)
    - **8.2** [The Agent contract](#82-the-agent-contract)
    - **8.3** [Optional agent capabilities](#83-optional-agent-capabilities)
    - **8.4** [Permission, elicitation, notification](#84-permission-elicitation-notification)
    - **8.5** [Chat data model](#85-chat-data-model)
    - **8.6** [Render extension points](#86-render-extension-points)
    - **8.7** [Theming and styling](#87-theming-and-styling)
    - **8.8** [Transcripts](#88-transcripts)
    - **8.9** [Terminal capabilities](#89-terminal-capabilities)
- **9** [Appendices](#9-appendices)
    - **9.1** [Appendix A — every declaration that changed after introduction](#91-appendix-a--every-declaration-that-changed-after-introduction)
    - **9.2** [Appendix B — outside the contract closure and unused by core-agent](#92-appendix-b--outside-the-contract-closure-and-unused-by-core-agent)
    - **9.3** [Appendix C — inside the closure but unused by core-agent](#93-appendix-c--inside-the-closure-but-unused-by-core-agent)
    - **9.4** [Appendix D — how the surface grew, by release](#94-appendix-d--how-the-surface-grew-by-release)

## 1. How this document was produced

Four datasets, all machine-generated from the tree rather than read off by eye.

**1. The surface.** `go/doc` + `go/ast` over the 51 non-test files of
`package tui`, yielding every exported top-level symbol and every exported
member with its declaration, doc comment, and `file:line`. Cross-checked
against an independent `go/types` enumeration — both agree on exactly 223
symbols.

**2. Contract closure.** A `go/types` reachability walk rooted at what
[`docs/design.md`](./design.md) §3 calls the plug-in surface: `Run`,
`Options`, `Agent`, `Event`, the TUI-implemented `PermissionPrompter` /
`Elicitor` pair, and all 20 optional capability interfaces. The walk follows
struct fields, interface and concrete method signatures, parameters, results,
slice/map/chan/pointer element types, and named-type constants. A symbol is
**inside the closure** if a host can reach it without naming anything the
contract does not already name. This is the machine-checkable version of the
distinction `design.md` §8 currently draws in prose.

**3. Provenance.** The package was re-parsed at each of the 106 commits that
touched `tui/*.go`, oldest to newest, and each snapshot's symbol set diffed
against the previous one. First appearance of a symbol or member is therefore
exact, not inferred from a `git log -S` pickaxe, and so is every subsequent
change to its declaration. Each commit is mapped to the earliest release tag
that contains it. All 661 keys (223 symbols + 438 members) resolved to a
commit and a release; none were orphaned.

**4. Host usage.** Every `coretui.*` reference in `core-agent` at HEAD, with
counts and referencing files, plus a `types.Implements` pass over
core-agent's full type-checked package graph (68 packages, zero type errors)
to determine which types satisfy which capability interface. Type-checking
matters here: `AsyncSlashProvider` and `AsyncSlashProviderWithPreamble` share
a method *name* and differ only in signature, so any grep-based answer about
them is wrong.

> **Data-quality note.** `core-agent` has nine stale checkouts under
> `.claude/worktrees/`. They inflate every naive `grep -r` count by 9× and
> represent old revisions, not the host. They are excluded throughout; the
> `types.Implements` pass excludes them too.

Everything below is reproducible from the tree at the audited commit. Numbers
in this document are as of **2026-08-14**; re-run before acting on them if the
tree has moved.

---

## 2. The surface at a glance

| | count |
|---|---|
| non-test files | 51 |
| non-test lines | 16,951 |
| exported types | 107 |
| exported constants | 83 |
| exported functions | 28 |
| exported package vars | 5 |
| **exported top-level symbols** | **223** |
| exported struct fields | 344 |
| interface methods | 52 |
| concrete methods | 39 |
| embedded members | 3 |
| **exported members** | **438** |

Issue #78's headline figure — *174 symbols* — is 107 types + 28 functions +
39 concrete methods. It has not drifted; it just omits constants, fields, and
interface methods, which is why the real promise at 1.0 is 661 things, not
174.

### 2.1 Contract closure vs. host usage

Two independent cuts through the same 223 symbols. **Closure** answers "can a
host reach this without naming something outside the documented contract?"
**Usage** answers "does the only real host touch it?" Crossing them is what
makes the three issues decidable:

| | in closure | outside closure | total |
|---|---|---|---|
| **used by core-agent** | 92 (D) | 4 (B) | 96 |
| **unused by core-agent** | 36 (C) | 91 (A) | 127 |
| **total** | 128 | 95 | 223 |

- **D (92) — frozen contract.** Reachable from the `design.md` §3 roots
  *and* load-bearing for a real host. Not up for debate; this is what 1.0
  promises.
- **C (36) — contract by design, dark in practice.** Alternate branches of
  things core-agent uses one side of: `PermissionOverlay` (core-agent uses
  `PermissionInline`), `StatusSidebar` (uses `StatusHeader`), the `Role*`
  values only `SeedHistory` needs, the elicit modes. Keep — a second host
  will use the other branch.
- **B (4) — contract that the closure cannot see.** The interesting set; see
  §6 below.
- **A (91) — outside the contract and untouched by the only host.** The
  unexport question.

core-agent references **97** distinct `coretui.*` names across **21** files.
Ninety-six exist; the ninety-seventh is `coretui.Interruptible` in a comment
at `cmd/core-agent/coretui_enabled.go:591`, naming an interface that has never
existed in `package tui`. Every symbol core-agent's tests use is also used in
its non-test code, so there is no test-only usage to discount.

---

## 3. Capability implementation matrix

The evidence for #77. Computed with `types.Implements` against core-tui
v0.18.0 — the version core-agent compiles against — over its whole package
graph, so "no" means the type genuinely does not satisfy the interface, not
that a grep missed it.

| capability | methods | local (`coreAgentAdapter`) | remote (`coretuiremote.Adapter`) | introduced |
|---|---|---|---|---|
| `Agent` (base) | 1 | ✅ | ✅ | v0.1.0 |
| `Reloader` | 1 | ✅ | ✅ | v0.1.0 |
| `PermissionController` | 4 | ✅ | ✅ | v0.1.0 |
| `PricingController` | 2 | ✅ | ✅ | v0.1.0 |
| `ToolLister` | 1 | ✅ | ✅ | v0.1.0 |
| `SubagentLister` | 1 | ✅ | ✅ | v0.1.0 |
| `SubagentEventReader` | 1 | ✅ | ✅ | v0.18.0 |
| `StatusReporter` | 1 | ✅ | ✅ | v0.1.0 |
| `SlashProvider` | 2 | ✅ | ✅ | v0.1.0 |
| `AsyncSlashProviderWithPreamble` | 2 | ✅ | ✅ | v0.6.3 |
| `InjectableAgent` | 1 | ✅ | ✅ | v0.1.0 |
| `UsageTracker` | 7 | ✅ (via `coreUsageBridge`) | ✅ | v0.1.0 |
| `ModelSwapper` | 2 | ✅ | ❌ | v0.1.0 |
| `InboxDrainer` | 2 | ✅ | ❌ | v0.6.0 |
| `WakeRequester` | 1 | ✅ | ❌ (see below) | v0.1.0 |
| `SessionSwitcher` | 2 | ❌ | ✅ | v0.10.0 |
| `LiveAgent` | 1 | ❌ | ✅ | v0.6.6 |
| `RemoteInterrupter` | 1 | ❌ | ✅ | v0.15.0 |
| `AsyncSlashProvider` | 2 | ❌ | ❌ | v0.6.0 |
| `ContentRunner` | 1 | ❌ | ❌ | v0.1.0 |
| `SessionByModelTracker` | 1 | ❌ | ❌ | v0.6.4 |

Twelve of twenty are implemented by both adapters, five by exactly one, and
three by neither.

### 3.1 Two findings that fall out of the matrix

**A latent bug in core-agent's attach mode.** `coretuiremote/adapter.go:705`
is commented *"RequestWake satisfies `coretui.WakeRequester`"*, but the
interface method is `WakeRequested() <-chan struct{}` and the adapter's method
is `RequestWake()` — different name, different signature. It satisfies
core-agent's own `attach.Registrant`, not the core-tui capability. The
consequence is silent, because capabilities are feature-detected: **R-WAKE-1
wake toasts never fire in attach mode**, and nothing errors to say so. This is
a core-agent fix, not a core-tui one, but it is direct evidence for the
argument in §6.1 below that type-assertion feature detection needs a
compile-time canary — which is exactly what [#82](https://github.com/go-steer/core-tui/issues/82)'s
adapter example is for.

**One-method interfaces are structurally weak.** `InjectableAgent` is
satisfied by seven distinct core-agent types — `coreAgentAdapter`,
`coretuiremote.Adapter`, `autonomous.Handle`, `agent.Agent`,
`attach.OperatorView`, `attach.Registrant`, `attachadapter.Adapter` — most of
which have nothing to do with the TUI. A single common method name is enough
to satisfy a capability by accident. This does not break anything today (the
TUI only asserts against the object the host hands it), but it is worth
knowing that the narrower an interface is, the less the assertion actually
proves.

---

## 4. Decision — #79, vestigial paths

### 4.1 The overlay enum: confirmed dead, delete

Write-set analysis settles this without a compatibility check. Across all of
`tui/*.go` **and** the test suite, the `overlay` field is only ever compared
or reset:

- `overlayModelPicker` appears at `tui/model.go:62` (declaration),
  `tui/view.go:831`, `tui/view.go:866`, `tui/update.go:1003` — all reads.
- `m.overlay = overlayNone` at `tui/update.go:885`, `1006`, `1011`, `1026` —
  all resets.
- No assignment of `overlayModelPicker`, `overlayPermission`, or
  `overlayElicit` exists anywhere, including tests.

So `m.overlay` is provably always `overlayNone` and every consumer of a
non-`overlayNone` value is unreachable. **Delete** the enum, the `overlay`
field on `Model`, `renderOverlay`'s vestigial body, the
`case m.overlay != overlayNone` arm at `tui/view.go:209`, and the legacy model
picker at `tui/update.go:1000`. Real picker state has lived in the Dialog
stack since v0.1.0 (`6f29721`, tier B).

### 4.2 The `usageMsg` item: false positive, keep

The issue asks whether any host still emits the legacy `usageMsg` shape before
removing it. The answer is that it is not legacy at all.
`tui/agentcmd.go:390` feeds `usageMsg` from `Event.Usage`, and
`tui/agentcmd.go:397` feeds it from `Event.Model` on usage-less events.
`turnSummaryMsg` is an *additional* push-mode path added in v0.9.0
(`7c862e5`, SSE protocol v1.1.0), not a replacement for it.

core-agent actively emits both: `internal/coretuiremote/typed_events.go:74`
and `internal/coretuiremote/adapter.go:778` populate `Event.Usage`. Deleting
the `usageMsg` path would silently break per-turn usage rendering for every
pull-mode host.

**Decision: keep `usageMsg`; strike that bullet from #79.**

---

## 5. Decision — #77, capability consolidation

**Implemented in v0.21.0.** All four decisions below shipped: 5.1 as a
rewrite of `design.md` §10 risk 1, 5.2 and 5.3 as deletions and a merge in
`package tui`, 5.4 as a stop. The capability count is 16. The rest of this
section is the reasoning as it stood at audit time, and the symbol
inventory in §8 is a snapshot of the pre-consolidation surface — neither is
maintained against head. `docs/api-surface.md` is.

### 5.1 Reject the mitigation as written

`docs/design.md` §10 risk 1 says: *when a capability is required for "most"
hosts, fold it into the base `Agent` interface (and accept the breaking change
before v1.0)*. That was written before any of this shipped, and the matrix
says not to do it.

Folding the twelve both-adapter capabilities into base `Agent` would turn a
one-method interface into roughly a twenty-five-method one. Every host would
have to implement all of it to get any of it; `examples/local` and
`tui/testagent` would need two dozen stub methods each; and the
"not available in this host" degradation that `design.md` §3 treats as
load-bearing would stop existing for those twelve. That is a strictly worse version of the
boilerplate problem `design.md` §10 was trying to prevent.

The evidence is also thin for "most hosts": there is one host. Two adapters
inside one host share that host's architecture and are not two independent
data points. Making something mandatory on n=1 is exactly the decision that
1.0 makes permanent.

**Decision: do not fold anything into base `Agent`. Rewrite `design.md` §10
risk 1** to say that the capability count is managed by deleting unused
capabilities and merging redundant ones, not by promotion to the base interface.

### 5.2 Delete the three with zero implementations

| capability | verdict |
|---|---|
| `AsyncSlashProvider` | **Delete; rename `AsyncSlashProviderWithPreamble` into its place.** The two share the method name `InvokeSlashAsync` and differ only in return signature, so no single Go type can satisfy both — the doc comment says as much. The preamble variant is a strict superset: an empty preamble is documented as behaving identically to the bare variant. Both core-agent adapters chose the preamble variant; nothing anywhere implements the bare one. Three slash interfaces become two, with no capability lost and no host change. |
| `ContentRunner` | **Delete, and drop R-CHAT-12 with it.** `RunWithContents` is never called anywhere in `tui/`; the only non-declaration references are in `content_test.go`, which asserts that the type assertion compiles. R-CHAT-12 itself says it is "invoked only by host-supplied affordances" — meaning core-tui is declaring an interface purely so hosts can call each other through it. `Content` and `ContentPart` go with it. Shipped in v0.1.0 (`2b09061`) and unused since. |
| `SessionByModelTracker` | **Delete.** Zero implementations since v0.6.4 (`df18400`), so the per-model `/stats` breakdown it backs has been dark for its entire life. Folding its one method into `UsageTracker` instead would be a breaking change for the one host that implements all seven existing methods, in exchange for a feature nobody has asked for. Re-add additively when a host wants it — that direction is not breaking. |

### 5.3 Merge the redundant pair

**`SubagentLister` + `SubagentEventReader` → one interface.** Both are one
method, both are about subagents, and both are implemented by both adapters —
there is no host that has one and not the other, and no plausible one. The
usual objection to merging (it destroys the "not available in this host"
signal) does not apply: a host with no subagents already expresses that by
returning an empty list.

**Net: 20 → 16 optional capabilities**, with exactly one host-visible change
(the merged subagent pair), which core-agent absorbs by renaming an interface
in two `var _` assertions.

### 5.4 The judgment call: whether to go further

Getting near `design.md` §10's ~10 threshold requires grouping capabilities
that are *correlated in practice but independent in principle*. The obvious candidate
is an `Introspector` — `Tools` + `Subagents` + `SubagentEvents` + `Status`,
all four implemented by both adapters — which would take 16 → 13.

The cost is real and worth stating plainly: a host that can list tools but has
no subagent concept would have to stub `Subagents()` to return nil, and the
TUI could no longer tell "this host has no subagents" from "this host does not
do subagents". `design.md` §3 explicitly values that distinction, and the
`WakeRequester` bug in §3.1 above is a reminder that silent degradation is
already the failure mode here.

**Recommendation: stop at 16.** The count was never the actual problem —
adapter boilerplate is a function of total method count, which grouping does
not change. Grouping trades a real diagnostic property for a smaller number.

---

## 6. Decision — #78, the exported surface

### 6.1 The real finding: untyped vocabularies

The four symbols in quadrant B — outside the structural closure but used by
the host — are not an oddity. They are the visible edge of a pattern:

| symbol(s) | reached how | why the closure misses it |
|---|---|---|
| `ThemeAuto`, `ThemeDark`, `ThemeLight` | `Options.ForceTheme` | field is a plain `string` |
| `SubagentNotFoundError` | `SubagentEventReader` error return | returned as `error`, matched with `errors.As` |

And the same pattern appears in three more places the host has not needed yet:

| vocabulary | consumed by | declared as |
|---|---|---|
| `TurnErrorConfig` … `TurnErrorUnknown` | `TurnError.Kind` | untyped `string` consts |
| `TurnStateIdle` … `TurnStateAwaitingElicit` | spec §2.2 turn state | untyped `string` consts |
| `SavingsPathPassthrough` … `SavingsPathAgentic` | `ToolSavings.Path` | untyped `string` consts |

There is also a collision worth fixing while we are here: `Status.State` is
documented as `"idle" / "running" / "deferred"` while `TurnState*` defines
`"idle" / "streaming" / "awaiting_permission" / "awaiting_elicit"`. Two
overlapping state vocabularies, neither typed, both frozen at 1.0 if nothing
changes.

**Decision: give each vocabulary a named string type** — `ThemeMode`,
`TurnErrorKind`, `TurnState`, `SavingsPath` — and reconcile the two state
vocabularies into one. This is:

- **breaking**, so it belongs in this pass and not after the freeze;
- **JSON-transparent**, since a named string type marshals identically, so the
  wire protocol is untouched;
- cheap for hosts — an explicit conversion at the assignment site;
- and precisely what #78 asks for when it says the contract should be
  *structural rather than conventional*. It is also what makes
  [#80](https://github.com/go-steer/core-tui/issues/80)'s apidiff able to
  police the vocabulary at all: today, adding a value to any of these is
  invisible to a type-level diff.

`SubagentNotFoundError` needs the opposite treatment — it cannot be made
structural, so it should be **named explicitly in `design.md` §3** as part of
the frozen contract, with the `errors.As` pattern documented.

### 6.2 The 91 unexported candidates split three ways

Quadrant A is not one decision. It is three, and they should not be made
together:

1. **Incidental — unexport.** Exported with no host-facing purpose:
   the `Glyph*` constants (16), the `Brand*` color vars (5), `Model` and
   `NewModel`, `Overlay`, `Scrollbar`, `History`, `RenderContext`,
   `LipglossFormatter`, `TerminalCapabilities`, `DetectCapabilities`. Nothing
   outside the package needs these; several exist only because a test in the
   same package referenced them, which does not require export.

2. **Theming — decide deliberately.** The twelve named theme constructors
   (`AnthropicTheme`, `GopherTheme`, `MatrixTheme`, …), `Theme`, `Styles`,
   `NewStyles`, `NewStylesWithTheme`, `BuiltinTheme`, `BuiltinThemes`,
   `ThemeByName`, `ThemeForProvider`, `ThemeChangedMsg`. `Options` already
   references this vocabulary informally: `InitialThemeName` is documented as
   "resolved case-insensitively against `tui.BuiltinThemes`", and
   `ThemeChangedMsg` is documented as an observable the host may watch. So
   part of this set is already contract in prose. Either promote that part
   explicitly or narrow `Options` to not mention it.

3. **Render extension points — the flat-package question.** `Dialog`,
   `DialogAction`, `KeyMsgDialog`, `ScrollDialog`, `Item`, `RawRenderable`,
   `Focusable`, `ToolRenderer`, `TextInputConfig`, `NewTextInputDialog`.
   These are a coherent extension surface that no host uses yet. #78 asks
   whether they want a subpackage so the contract becomes structural. They
   are also the set most likely to be wanted by a second host. **Suggest:
   keep exported, move to `tui/render` (or similar), and let the package
   boundary carry the promise** — but this is the one item here that is a
   genuine design change rather than a cleanup, and it can be deferred past
   1.0 only if the symbols are unexported now, since moving them later is
   breaking either way.

Also in quadrant A and worth a separate call: the transcript surface
(`Transcript`, `TranscriptInfo`, `TranscriptMsg`, `TranscriptUsage`,
`TranscriptSchemaVersion`, `ListTranscripts`, `LoadTranscript`). It is a
coherent, documented, on-disk-format-bearing API that core-agent does not use
because core-tui writes transcripts itself via `Options.AgentsDir`. Freezing
an on-disk schema version constant at 1.0 deserves an explicit yes, not an
inherited one.

---

## 7. Sequencing

The three issues are separable and should stay separate PRs, in this order:

1. **#79 first.** Pure deletion, no exported-surface change, nothing for a
   host to absorb. Shrinks the tree the other two have to audit.
2. **#77 second.** Three deletions plus one merge. One host-visible change;
   core-agent absorbs it in two lines.
3. **#78 last.** Named vocabularies plus the unexport sweep — the largest
   diff, and the one that benefits most from the other two having already
   removed code.

Each is independently revertible, and core-agent can take them one release at
a time rather than absorbing one large breaking bump.

### 7.1 Open questions this document does not settle

- Whether to group capabilities past 16 (§5.4) — recommendation is no, but it
  is a product call.
- Which of the three quadrant-A groups to unexport versus promote (§6.2) —
  group 1 is clear, groups 2 and 3 need a decision.
- Whether the render extension points move to a subpackage, which is the last
  live piece of the `design.md` §2.1 "why one flat package" decision.
- Whether the transcript API is part of the 1.0 promise.

### 7.2 Downstream follow-ups for core-agent

- `coretuiremote.Adapter.RequestWake()` does not satisfy `WakeRequester`;
  attach-mode wake toasts silently never fire (§3.1). Filed as
  go-steer/core-agent#802.
- `cmd/core-agent/coretui_enabled.go:708` refers to `coretui.Interruptible`,
  which has never existed. **It satisfies nothing** — `RemoteInterrupter` is
  `Interrupt(ctx context.Context) error` and this method is `Interrupt() bool`,
  so local-mode interrupt is unreachable from the TUI. (An earlier revision of
  this line said the interface it satisfies is `RemoteInterrupter`; that was
  wrong, and recording the defect as benign is why it survived two version
  bumps.) Filed as go-steer/core-agent#803.
- Both of the above are instances of one class: capabilities are feature-detected
  by type assertion, so a near-miss is indistinguishable from a host declining
  the capability. core-agent carries four `var _ coretui.X` guards across
  roughly 27 implemented surfaces, all four added by the v0.21.0 migration —
  so the gap sits where nothing has broken yet, which is where both of the
  above live. Filed as go-steer/core-agent#804, tracked
  together in go-steer/core-agent#805.
- Nine stale worktrees under `.claude/worktrees/` (§1).

---

## 8. Full symbol inventory

Every exported symbol in `package tui`, grouped by declaring file. For each: kind, location, contract-closure membership, core-agent usage, the commit and release that introduced it, any later declaration changes, and the doc comment exactly as written in the source. Struct fields and interface methods are in the collapsible member lists, each with its own location, doc, and — where it differs from its parent — its own introduction release.

### 8.1 Entry point and configuration

_Files: `tui/options.go`, `tui/program.go`_

#### `Branding`

`type` · `tui/options.go:282` · contract closure: **inside** · core-agent: **10 refs (`cmd/core-agent-tui/main.go`, `cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter.go`, +2 more)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_<br>declaration changed since: **v0.6.2** ([`0086682`](https://github.com/go-steer/core-tui/commit/008668259ba74f8485f2d31d1edacb4c8e6f703b), _feat(banner): drop cursor block; add Branding.AgentIdentity segment_)

Branding overrides the brand-line and chrome strings. Empty fields
fall back to the house defaults (style.md §1.1 + §8).


```go
type Branding struct {
	Wordmark string
	// AgentIdentity is the operator's per-deployment label for the
	// running agent — typically `cfg.Agent.DisplayName` from the
	// host's config. When set AND not equal to Wordmark, the
	// status-line banner renders "<wordmark> · <identity> · …" so
	// the operator can tell which agent they're talking to in
	// multi-window setups (parity with core-agent's internal/tui).
	// Empty falls back to the bare wordmark.
	AgentIdentity    string
	AccentColor      string
	SecondaryColor   string
	CursorColor      string
	EmptyStateHint   string
	FooterHint       string
	InputPlaceholder string
}
```

<details>
<summary><b>8 exported members</b></summary>


**`Wordmark`** — field · `tui/options.go:283`

```go
Wordmark string
```

_No doc comment._


**`AgentIdentity`** — field · `tui/options.go:291` · added in **v0.6.2** ([`0086682`](https://github.com/go-steer/core-tui/commit/008668259ba74f8485f2d31d1edacb4c8e6f703b))

```go
AgentIdentity string
```

AgentIdentity is the operator's per-deployment label for the
running agent — typically `cfg.Agent.DisplayName` from the
host's config. When set AND not equal to Wordmark, the
status-line banner renders "<wordmark> · <identity> · …" so
the operator can tell which agent they're talking to in
multi-window setups (parity with core-agent's internal/tui).
Empty falls back to the bare wordmark.


**`AccentColor`** — field · `tui/options.go:292`

```go
AccentColor string
```

_No doc comment._


**`SecondaryColor`** — field · `tui/options.go:293`

```go
SecondaryColor string
```

_No doc comment._


**`CursorColor`** — field · `tui/options.go:294`

```go
CursorColor string
```

_No doc comment._


**`EmptyStateHint`** — field · `tui/options.go:295`

```go
EmptyStateHint string
```

_No doc comment._


**`FooterHint`** — field · `tui/options.go:296`

```go
FooterHint string
```

_No doc comment._


**`InputPlaceholder`** — field · `tui/options.go:297`

```go
InputPlaceholder string
```

_No doc comment._


</details>

---

#### `MidTurnInjectionMode`

`type` · `tui/options.go:245` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5), 2026-05-25, _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_

MidTurnInjectionMode controls operator-typed-during-streaming
routing (R-CHAT-11).


```go
type MidTurnInjectionMode int
```

---

#### `Options`

`type` · `tui/options.go:28` · contract closure: **inside** · core-agent: **7 refs (`cmd/core-agent-tui/main.go`, `cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`, +1 more)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_<br>declaration changed since: **v0.1.0** ([`6bbee7e`](https://github.com/go-steer/core-tui/commit/6bbee7e14b4e16a7ef355b81eaa7f7281cac2ecb), _feat(tui): persist user's status-layout choice across sessions_); **v0.1.0** ([`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5), _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_); **v0.1.0** ([`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_); **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), _feat(tui): land remaining §3.3 capability surface (PR 7)_); **v0.1.0** ([`41e2e51`](https://github.com/go-steer/core-tui/commit/41e2e51d11e791a0309d96336131a72b1f29b7bd), _feat(tui): add Options.PersistModelChoice (R-MOD-3)_); **v0.1.0** ([`2193696`](https://github.com/go-steer/core-tui/commit/21936966b0c07480dac078a97c6a9c356badeab0), _fix(tui-facelift): per-provider theming is now opt-in (default = brand palette)_); **v0.1.0** ([`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5), _feat(tui-facelift): configurable permission layout (inline default, overlay opt-in)_); **v0.5.0** ([`1b9845c`](https://github.com/go-steer/core-tui/commit/1b9845ca2dedee13181dd536fe944779f191c93e), _feat(options): expose ForceTheme + Mouse hooks; /mouse becomes a real toggle_); **v0.6.0** ([`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f), _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_); **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_); **v0.8.0** ([`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b), _feat(notifier): out-of-band Notify() side channel for host-initiated chat rows (#30)_); **v0.12.0** ([`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc), _feat(tui): expandable tool-call detail overlay + verbose flag (tiers 1+2 of #52) (#61)_); **v0.13.0** ([`926e722`](https://github.com/go-steer/core-tui/commit/926e72244cf84edd2dce1a4100e95261487e9638), _feat(tui): add Options.InitialPrompt for seeded interactive sessions (#63)_)

Options configures tui.Run.


```go
type Options struct {
	// ... 30 exported members, documented below
}
```

<details>
<summary><b>30 exported members</b></summary>


**`Agent`** — field · `tui/options.go:30`

```go
Agent Agent
```

Agent is required.


**`Branding`** — field · `tui/options.go:34`

```go
Branding Branding
```

Branding overrides the default house style on the axes listed in
R-BRAND-1. Zero value uses defaults.


**`AutoProviderTheme`** — field · `tui/options.go:42` · added in **v0.1.0** ([`2193696`](https://github.com/go-steer/core-tui/commit/21936966b0c07480dac078a97c6a9c356badeab0))

```go
AutoProviderTheme bool
```

AutoProviderTheme opts the host in to per-provider palette
tinting (Anthropic clay / Gemini blue / OpenAI green) based
on StatusReporter.Status().Provider. Defaults to false so
the brand palette stays consistent across model swaps; hosts
that prefer the per-provider identity flip it on. Branding
overrides still apply on top of whichever theme is picked.


**`ForceTheme`** — field · `tui/options.go:51` · added in **v0.5.0** ([`1b9845c`](https://github.com/go-steer/core-tui/commit/1b9845ca2dedee13181dd536fe944779f191c93e))

```go
ForceTheme string
```

ForceTheme overrides core-tui's terminal-background auto-
detection (OSC-11 → BackgroundColorMsg → m.styles.Dark). One
of "" (auto, the default — query the terminal), "dark", or
"light". Useful on terminals where the OSC-11 query is
unreliable (some SSH stacks, tmux passthrough quirks). When
set, BackgroundColorMsg is ignored so a stale or wrong
response can't override the operator's explicit choice.


**`InitialThemeName`** — field · `tui/options.go:59` · added in **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501))

```go
InitialThemeName string
```

InitialThemeName seeds the named theme at startup. Resolved
case-insensitively against tui.BuiltinThemes; unknown names
fall through to DefaultTheme. Set this when the host has
persisted a previous /theme pick (observed via
ThemeChangedMsg). Empty leaves the theme on the auto /
per-provider path (see AutoProviderTheme).


**`Mouse`** — field · `tui/options.go:68` · added in **v0.5.0** ([`1b9845c`](https://github.com/go-steer/core-tui/commit/1b9845ca2dedee13181dd536fe944779f191c93e))

```go
Mouse *bool
```

Mouse toggles terminal mouse capture. nil (the default) keeps
MouseModeCellMotion on so the wheel scrolls the viewport;
*false disables capture entirely (MouseModeNone), restoring
the terminal's native click-drag text-select. Operators on
terminals that handle wheel scrolling natively, or who prefer
text-select-without-Shift, flip this off. The /mouse slash
flips it at runtime; this option is the startup default.


**`PermissionLayout`** — field · `tui/options.go:77` · added in **v0.1.0** ([`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5))

```go
PermissionLayout PermissionLayout
```

PermissionLayout picks how the permission prompt is rendered
when the gate asks for approval (R-PERM-1). Zero value =
PermissionInline: the prompt renders as a block inside the
chat viewport flow, right under the tool call that triggered
it, preserving the assistant context. PermissionOverlay
renders a centered modal that dims the chat — more
attention-grabbing, less context.


**`StatusLayout`** — field · `tui/options.go:82`

```go
StatusLayout StatusLayout
```

StatusLayout picks the status surface (R-USE-2). The initial
value is whatever the host sets here; the user can flip it at
runtime via Ctrl+B.


**`PersistStatusLayout`** — field · `tui/options.go:89` · added in **v0.1.0** ([`6bbee7e`](https://github.com/go-steer/core-tui/commit/6bbee7e14b4e16a7ef355b81eaa7f7281cac2ecb))

```go
PersistStatusLayout func(StatusLayout) error
```

PersistStatusLayout is called when the user toggles the status
layout at runtime so the host can write the choice to a
settings file. Hosts that read it back into StatusLayout on
the next launch give users a layout preference that survives
restarts. Nil means the toggle stays session-local.


**`PersistModelChoice`** — field · `tui/options.go:96` · added in **v0.1.0** ([`41e2e51`](https://github.com/go-steer/core-tui/commit/41e2e51d11e791a0309d96336131a72b1f29b7bd))

```go
PersistModelChoice func(modelID string) error
```

PersistModelChoice is called when the operator picks a new
model in the /model picker (R-MOD-3). Hosts persist the
choice to their config + read it back on next launch so the
preference survives restarts. Nil means the choice stays
session-local.


**`PersistThemeChoice`** — field · `tui/options.go:107` · added in **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501))

```go
PersistThemeChoice func(name string) error
```

PersistThemeChoice is called when the operator picks a new
theme via the /theme picker (or `/theme <name>` with a
known name). Mirrors PersistModelChoice: hosts persist the
name to their config + seed it back via InitialThemeName on
next launch. Nil means the theme stays session-local. Hosts
can ALSO observe ThemeChangedMsg in their Update loop for
the same notification — pick whichever pattern fits the
host's architecture (callback = less code; msg = no
Options field needed).


**`PermissionMode`** — field · `tui/options.go:111`

```go
PermissionMode PermissionModeWiring
```

PermissionMode wires the permission-mode chip (R-PERM-6 / R-PERM-7).
Zero value hides the chip and disables Shift+Tab cycling.


**`ThinkingPhrases`** — field · `tui/options.go:115`

```go
ThinkingPhrases []string
```

ThinkingPhrases / WorkingPhrases override the spinner verb pools
(R-CHAT-3). Nil uses built-in defaults.


**`WorkingPhrases`** — field · `tui/options.go:116`

```go
WorkingPhrases []string
```

_No doc comment._


**`SeedHistory`** — field · `tui/options.go:121`

```go
SeedHistory []Message
```

SeedHistory pre-populates the chat with example messages. Used by
the examples/local visual-preview binary; production hosts leave
this nil.


**`Prompter`** — field · `tui/options.go:129` · added in **v0.1.0** ([`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda))

```go
Prompter PermissionPrompter
```

Prompter is the TUI-provided PermissionPrompter that the host
wires into its permission gate before the first turn (R-PERM-1).
Hosts construct one via tui.NewPrompter() and pass it both
into the gate (`gate.SetPrompter(prompter)`) AND here. The TUI
drains the prompter's request channel and renders a modal
for each inbound request.


**`Elicitor`** — field · `tui/options.go:134` · added in **v0.1.0** ([`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda))

```go
Elicitor Elicitor
```

Elicitor is the TUI-provided Elicitor that the host wires
into each MCP server's elicit callback before MCP connect
(R-ELIC-1). Construct via tui.NewElicitor().


**`Notifier`** — field · `tui/options.go:147` · added in **v0.8.0** ([`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b))

```go
Notifier *Notifier
```

Notifier is the host-facing side channel for chat rows
that don't belong to the agent event stream (issue #30):
reconnect notices, host-shutdown warnings, multi-attach
signals, version-mismatch errors, etc. Construct via
tui.NewNotifier(); call Notifier.Notify(text) from any
goroutine. The TUI drains the channel and renders each
notice as a RoleNotice row (◇ glyph + muted color) —
visually distinct from RoleSystem so operators can tell
"framework speaking" from "agent system response". Nil
(the default) disables the side channel; existing
"yield-through-agent-stream" workarounds keep working.


**`AlwaysAllow`** — field · `tui/options.go:153` · added in **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a))

```go
AlwaysAllow func(req PermissionRequest) error
```

AlwaysAllow is invoked when the operator picks
DecisionAllowAlways in the permission modal (R-PERM-3). The
host persists the entry to its allowlist; on nil callback the
TUI falls back to allow-session and logs a system message.


**`UsageTracker`** — field · `tui/options.go:160` · added in **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a))

```go
UsageTracker UsageTracker
```

UsageTracker provides per-turn + session totals for the status
surface (R-USE-2) and /stats (R-USE-1). Optional — when nil
the per-turn footer renders only the Usage / Model / Elapsed
fields the agent populates directly on the Message and the
session-total slot in the status surface stays empty.


**`AgentsDir`** — field · `tui/options.go:164` · added in **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a))

```go
AgentsDir string
```

AgentsDir is the path the TUI writes the on-exit transcript
to (R-TR-1) when non-empty.


**`Memory`** — field · `tui/options.go:170` · added in **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a))

```go
Memory []MemoryFile
```

Memory / MCPServers / Skills feed the display-only slash
commands (/memory, /mcp, /skills). Optional — when nil the
corresponding slash renders an empty list with a hint about
configuring the host.


**`MCPServers`** — field · `tui/options.go:171` · added in **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a))

```go
MCPServers []MCPServerInfo
```

_No doc comment._


**`Skills`** — field · `tui/options.go:172` · added in **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a))

```go
Skills []SkillInfo
```

_No doc comment._


**`PathScope`** — field · `tui/options.go:176` · added in **v0.1.0** ([`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a))

```go
PathScope PathScope
```

PathScope is the list of roots the @file palette filters
against (R-SCOPE-1). Empty means no scope filtering.


**`MidTurnInjectionMode`** — field · `tui/options.go:186` · added in **v0.1.0** ([`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5))

```go
MidTurnInjectionMode MidTurnInjectionMode
```

MidTurnInjectionMode picks what happens when the operator
submits a prompt while a turn is in flight (R-CHAT-11). Zero
value (`QueueForNext`) preserves the R-CHAT-10 default:
buffer the entry as Queued, auto-drain on turn-end.
`InjectIntoCurrent` routes the entry through
`InjectableAgent.Inject` instead so it lands in the running
turn's context — falls back to `QueueForNext` when the agent
doesn't satisfy `InjectableAgent`.


**`AutoContinueFormatter`** — field · `tui/options.go:199` · added in **v0.6.0** ([`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f))

```go
AutoContinueFormatter func([]string) string
```

AutoContinueFormatter wraps the slice of drained inbox
messages into a single prompt string for the synthetic
auto-continue turn. Only consulted when MidTurnInjectionMode
== AutoContinueFromInbox AND the agent satisfies
InboxDrainer. Nil falls back to defaultAutoContinueFormatter
(a bulleted "[Operator notes added during the previous task]"
frame followed by a "Continue." instruction).

Receives the same []string DrainInbox returned, in order,
after the TUI has already removed empty strings. Return
value becomes the prompt of a fresh turn.


**`ToolDetailVerbose`** — field · `tui/options.go:215` · added in **v0.12.0** ([`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc))

```go
ToolDetailVerbose bool
```

ToolDetailVerbose opts the operator in to the "show me
everything" tool-call rendering (core-tui #52 tier 2). When
true, every tool row's compact preview is followed by a full
pretty-printed args + response dump — the same content the
Ctrl+X detail overlay shows on demand, but inline in the
transcript. Off by default so the transcript stays readable;
hosts flip it on for CI / debug runs where operators want
every byte in the log stream instead of hunting through the
SSE events.

Composes with core-tui #52 tier 1 (expand-single overlay,
Ctrl+X) — the overlay works regardless of this flag; verbose
mode just skips the "open a modal" step by rendering the
same content inline for every call.


**`AutoContinueCap`** — field · `tui/options.go:226` · added in **v0.6.0** ([`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f))

```go
AutoContinueCap int
```

AutoContinueCap is the soft limit on chained auto-continues
between operator-initiated turns. After this many consecutive
auto-continue turns without the operator typing a fresh
prompt, the loop pauses and a system note tells the operator
the remaining drained messages will land on their next
submission. 0 (zero value) uses DefaultAutoContinueCap.
Negative disables the cap entirely (use with care — a typo-
fast operator can pile messages faster than the model
answers).


**`InitialPrompt`** — field · `tui/options.go:240` · added in **v0.13.0** ([`926e722`](https://github.com/go-steer/core-tui/commit/926e72244cf84edd2dce1a4100e95261487e9638))

```go
InitialPrompt string
```

InitialPrompt seeds the first turn on startup. When non-empty,
Init() emits a one-shot message that Update routes through the
same submitTurn path as an operator-typed submission — so the
prompt renders as a normal RoleUser row, the assistant response
streams into the chat scroll, and the operator lands on the
input line when the turn completes. Empty (the default) keeps
the pre-seed behavior (blank chat on startup).

Intended for the host CLI's "seed then stay interactive" flag
(core-agent's -i / --interactive-prompt); library callers
wanting non-interactive one-shot behavior should keep using
their headless entrypoint instead.


</details>

---

#### `PermissionLayout`

`type` · `tui/options.go:311` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5), 2026-05-26, _feat(tui-facelift): configurable permission layout (inline default, overlay opt-in)_

PermissionLayout picks how permission prompts render (R-PERM-1).


```go
type PermissionLayout int
```

---

#### `PermissionMode`

`type` · `tui/options.go:334` · contract closure: **inside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

PermissionMode is the agent-wide approval policy.


```go
type PermissionMode int
```

<details>
<summary><b>2 exported members</b></summary>


**`Next`** — method · `tui/options.go:358`

```go
func (m PermissionMode) Next() PermissionMode
```

Next returns the next mode in the Shift+Tab cycle.


**`String`** — method · `tui/options.go:344`

```go
func (m PermissionMode) String() string
```

String returns the canonical name of the mode.


</details>

---

#### `PermissionModeWiring`

`type` · `tui/options.go:327` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

PermissionModeWiring backs the permission-mode chip (R-PERM-6 /
R-PERM-7). When Set is nil the chip is hidden.


```go
type PermissionModeWiring struct {
	Initial PermissionMode
	Set     func(PermissionMode) error
	Persist func(PermissionMode) error
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Initial`** — field · `tui/options.go:328`

```go
Initial PermissionMode
```

_No doc comment._


**`Set`** — field · `tui/options.go:329`

```go
Set func(PermissionMode) error
```

_No doc comment._


**`Persist`** — field · `tui/options.go:330`

```go
Persist func(PermissionMode) error
```

_No doc comment._


</details>

---

#### `StatusLayout`

`type` · `tui/options.go:301` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

StatusLayout picks the persistent status surface (R-USE-2).


```go
type StatusLayout int
```

---

#### `AutoContinueFromInbox`

`const` · `tui/options.go:270` · block `QueueForNext` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.6.0** — [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f), 2026-05-27, _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_

AutoContinueFromInbox is the "opaque-runner" mode (issue #9):
operator-typed-during-streaming entries call Inject AND stay
Queued in the panel. On turn end, the TUI calls
InboxDrainer.DrainInbox to pull all queued operator messages,
formats them via Options.AutoContinueFormatter (or a default
framing), and submits as a synthetic auto-continue turn —
the resulting user-row renders with the ↻ glyph + muted
style so the operator can tell which turns they typed and
which came from the auto-continue. Matching queue entries
flip Queued → Done.

Falls back to QueueForNext when the agent doesn't satisfy
InboxDrainer (no runtime error). Soft cap on consecutive
auto-continues (Options.AutoContinueCap, default
DefaultAutoContinueCap) prevents runaway loops.


```go
// AutoContinueFromInbox is the "opaque-runner" mode (issue #9):
// operator-typed-during-streaming entries call Inject AND stay
// Queued in the panel. On turn end, the TUI calls
// InboxDrainer.DrainInbox to pull all queued operator messages,
// formats them via Options.AutoContinueFormatter (or a default
// framing), and submits as a synthetic auto-continue turn —
// the resulting user-row renders with the ↻ glyph + muted
// style so the operator can tell which turns they typed and
// which came from the auto-continue. Matching queue entries
// flip Queued → Done.
//
// Falls back to QueueForNext when the agent doesn't satisfy
// InboxDrainer (no runtime error). Soft cap on consecutive
// auto-continues (Options.AutoContinueCap, default
// DefaultAutoContinueCap) prevents runaway loops.
AutoContinueFromInbox
```

---

#### `DefaultAutoContinueCap`

`const` · `tui/options.go:278` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.6.0** — [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f), 2026-05-27, _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_

DefaultAutoContinueCap is the fallback consecutive-auto-continue
limit when Options.AutoContinueCap is unset. After this many
chained auto-continues without an operator-initiated turn in
between, the TUI logs a system note and stops — the next batch
of inbox messages will land on the operator's next prompt.


```go
DefaultAutoContinueCap = 10
```

---

#### `InjectIntoCurrent`

`const` · `tui/options.go:254` · block `QueueForNext` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5), 2026-05-25, _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_

InjectIntoCurrent calls InjectableAgent.Inject so the entry
lands in the running turn's context. The queue entry renders
immediately as Done with an "injected" suffix.


```go
// InjectIntoCurrent calls InjectableAgent.Inject so the entry
// lands in the running turn's context. The queue entry renders
// immediately as Done with an "injected" suffix.
InjectIntoCurrent
```

---

#### `PermissionInline`

`const` · `tui/options.go:318` · block `PermissionInline` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5), 2026-05-26, _feat(tui-facelift): configurable permission layout (inline default, overlay opt-in)_

PermissionInline (default) renders the prompt as a block
inside the chat viewport flow — under the tool call that
triggered it. Preserves context; the decision is part of
the natural conversation scroll.


```go
// PermissionInline (default) renders the prompt as a block
// inside the chat viewport flow — under the tool call that
// triggered it. Preserves context; the decision is part of
// the natural conversation scroll.
PermissionInline PermissionLayout = iota
```

---

#### `PermissionModeAcceptEdits`

`const` · `tui/options.go:338` · block `PermissionModeDefault` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

file-edit tools auto-allow


```go
PermissionModeAcceptEdits // file-edit tools auto-allow
```

---

#### `PermissionModeBypass`

`const` · `tui/options.go:340` · block `PermissionModeDefault` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

every tool call auto-allows


```go
PermissionModeBypass // every tool call auto-allows
```

---

#### `PermissionModeDefault`

`const` · `tui/options.go:337` · block `PermissionModeDefault` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

every tool call asks


```go
PermissionModeDefault PermissionMode = iota // every tool call asks
```

---

#### `PermissionModePlan`

`const` · `tui/options.go:339` · block `PermissionModeDefault` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

no tool calls execute


```go
PermissionModePlan // no tool calls execute
```

---

#### `PermissionOverlay`

`const` · `tui/options.go:322` · block `PermissionInline` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5), 2026-05-26, _feat(tui-facelift): configurable permission layout (inline default, overlay opt-in)_

PermissionOverlay renders a centered modal that dims the
chat. Most attention-grabbing; covers the surrounding
context until the operator decides.


```go
// PermissionOverlay renders a centered modal that dims the
// chat. Most attention-grabbing; covers the surrounding
// context until the operator decides.
PermissionOverlay
```

---

#### `QueueForNext`

`const` · `tui/options.go:250` · block `QueueForNext` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5), 2026-05-25, _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_

QueueForNext (default) buffers the entry as a Queued queue
row; drains on the next turn-end (R-CHAT-10).


```go
// QueueForNext (default) buffers the entry as a Queued queue
// row; drains on the next turn-end (R-CHAT-10).
QueueForNext MidTurnInjectionMode = iota
```

---

#### `StatusHeader`

`const` · `tui/options.go:305` · block `StatusHeader` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

StatusHeader places a single status line above the chat (default).


```go
// StatusHeader places a single status line above the chat (default).
StatusHeader StatusLayout = iota
```

---

#### `StatusSidebar`

`const` · `tui/options.go:307` · block `StatusHeader` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

StatusSidebar places a fixed-width right-hand panel.


```go
// StatusSidebar places a fixed-width right-hand panel.
StatusSidebar
```

---

#### `ThemeAuto`

`const` · `tui/options.go:22` · block `ThemeAuto` · contract closure: **outside** · core-agent: **6 refs (`cmd/core-agent/coretui_enabled.go`, `cmd/core-agent/coretui_ui_test.go`)**  
introduced in **v0.5.0** — [`1b9845c`](https://github.com/go-steer/core-tui/commit/1b9845ca2dedee13181dd536fe944779f191c93e), 2026-05-27, _feat(options): expose ForceTheme + Mouse hooks; /mouse becomes a real toggle_

ForceTheme values for Options.ForceTheme. "" (the zero value)
means "auto" — let core-tui's OSC-11 query decide. The strings
match what core-agent's UIConfig writes to JSON so hosts can
pass cfg.UI.Theme through unchanged.


```go
ThemeAuto = ""
```

---

#### `ThemeDark`

`const` · `tui/options.go:23` · block `ThemeAuto` · contract closure: **outside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`, `cmd/core-agent/coretui_ui_test.go`)**  
introduced in **v0.5.0** — [`1b9845c`](https://github.com/go-steer/core-tui/commit/1b9845ca2dedee13181dd536fe944779f191c93e), 2026-05-27, _feat(options): expose ForceTheme + Mouse hooks; /mouse becomes a real toggle_

ForceTheme values for Options.ForceTheme. "" (the zero value)
means "auto" — let core-tui's OSC-11 query decide. The strings
match what core-agent's UIConfig writes to JSON so hosts can
pass cfg.UI.Theme through unchanged.


```go
ThemeDark = "dark"
```

---

#### `ThemeLight`

`const` · `tui/options.go:24` · block `ThemeAuto` · contract closure: **outside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`, `cmd/core-agent/coretui_ui_test.go`)**  
introduced in **v0.5.0** — [`1b9845c`](https://github.com/go-steer/core-tui/commit/1b9845ca2dedee13181dd536fe944779f191c93e), 2026-05-27, _feat(options): expose ForceTheme + Mouse hooks; /mouse becomes a real toggle_

ForceTheme values for Options.ForceTheme. "" (the zero value)
means "auto" — let core-tui's OSC-11 query decide. The strings
match what core-agent's UIConfig writes to JSON so hosts can
pass cfg.UI.Theme through unchanged.


```go
ThemeLight = "light"
```

---

#### `Run`

`func` · `tui/program.go:39` · contract closure: **inside** · core-agent: **9 refs (`cmd/core-agent-tui/main.go`, `cmd/core-agent-tui/styles.go`, `cmd/core-agent/coretui_enabled.go`, +2 more)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Run constructs the Model and runs the Bubble Tea program until exit.
Blocks until the user quits or ctx is cancelled. Returns the first
error encountered by tea.Program.Run, if any.

On clean exit (when Options.AgentsDir is non-empty) writes a
JSON transcript of the session to
<AgentsDir>/sessions/<RFC3339-timestamp>.json. Failures are
non-fatal — surfaced to stderr after the alt-screen tears down
so the operator can see them.

Mouse cell-motion is enabled so the wheel scrolls the viewport;
operators who want native terminal text-select hold Shift to
bypass capture. Hosts can rely on this default rather than
passing tea.WithMouseCellMotion themselves.


```go
func Run(ctx context.Context, opts Options) error
```

---

### 8.2 The Agent contract

_Files: `tui/agent.go`, `tui/remote_events.go`_

#### `Agent`

`type` · `tui/agent.go:28` · contract closure: **inside** · core-agent: **4 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Agent is the minimum interface a host must supply. Run executes one
turn against prompt and returns an iterator of Events the TUI drains
in a goroutine. Cancel the context to abort mid-turn. Multi-turn
state is the agent's concern; the TUI calls Run once per submission.


```go
type Agent interface {
	Run(ctx context.Context, prompt string) iter.Seq2[Event, error]
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Run`** — method · `tui/agent.go:29`

```go
Run(ctx context.Context, prompt string) iter.Seq2[Event, error]
```

_No doc comment._


</details>

---

#### `Content`

`type` · `tui/agent.go:301` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`2b09061`](https://github.com/go-steer/core-tui/commit/2b09061e223672bd9c42f42d067761de887765dd), 2026-05-25, _feat(tui): ContentRunner capability + Content type (R-CHAT-12, PR 5/5)_

Content is a neutral structured-prompt fragment for ContentRunner
(R-CHAT-12). Adapters translate their host's native content
representation (ADK Content, anthropic Message, etc.) into / out
of this shape so the TUI stays framework-agnostic.

Role is one of "user" / "assistant" / "system" / "tool". Text is
the primary payload — structured parts (tool calls, function
responses, image refs) ride alongside in Parts. Both fields may
be set; a renderer or downstream agent decides precedence.


```go
type Content struct {
	Role  string
	Text  string
	Parts []ContentPart
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Role`** — field · `tui/agent.go:302`

```go
Role string
```

_No doc comment._


**`Text`** — field · `tui/agent.go:303`

```go
Text string
```

_No doc comment._


**`Parts`** — field · `tui/agent.go:304`

```go
Parts []ContentPart
```

_No doc comment._


</details>

---

#### `ContentPart`

`type` · `tui/agent.go:312` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`2b09061`](https://github.com/go-steer/core-tui/commit/2b09061e223672bd9c42f42d067761de887765dd), 2026-05-25, _feat(tui): ContentRunner capability + Content type (R-CHAT-12, PR 5/5)_

ContentPart is one named-kind fragment within a Content (tool
call, tool response, image, etc.). Kind is a host-defined string
— adapters agree with their backend on the vocabulary. Data is
the raw payload, typed as `any` so adapters can pass through
structured values without forcing a serialization here.


```go
type ContentPart struct {
	Kind string
	Data any
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Kind`** — field · `tui/agent.go:313`

```go
Kind string
```

_No doc comment._


**`Data`** — field · `tui/agent.go:314`

```go
Data any
```

_No doc comment._


</details>

---

#### `ContentRunner`

`type` · `tui/agent.go:328` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`2b09061`](https://github.com/go-steer/core-tui/commit/2b09061e223672bd9c42f42d067761de887765dd), 2026-05-25, _feat(tui): ContentRunner capability + Content type (R-CHAT-12, PR 5/5)_

ContentRunner is an optional Agent capability: when implemented,
adapters can drive turns from a structured `[]Content` slice
instead of a single prompt string. Used by retry / replay flows
where the host has already constructed the conversation context
programmatically.

Detected via type assertion; the TUI's default submit flow still
uses Agent.Run(ctx, prompt) until a host wires a UI affordance
that invokes RunWithContents.

See R-CHAT-12 in requirements.md and design.md §3.3.


```go
type ContentRunner interface {
	RunWithContents(ctx context.Context, contents []Content) iter.Seq2[Event, error]
}
```

<details>
<summary><b>1 exported members</b></summary>


**`RunWithContents`** — method · `tui/agent.go:329`

```go
RunWithContents(ctx context.Context, contents []Content) iter.Seq2[Event, error]
```

_No doc comment._


</details>

---

#### `Event`

`type` · `tui/agent.go:35` · contract closure: **inside** · core-agent: **48 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter.go`, `internal/coretuiremote/adapter_test.go`, +4 more)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_<br>declaration changed since: **v0.1.0** ([`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd), _feat(tui): real /mcp /tools /skills + cost + scrollable palette_); **v0.3.0** ([`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc), _feat(tool-results): wire ToolResult events through the agent → TUI flow_); **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_)

Event is the neutral representation of one agent event. A single
Event typically carries ONE of: streamed text, a tool call, or a
usage update.


```go
type Event struct {
	// ... 12 exported members, documented below
}
```

<details>
<summary><b>12 exported members</b></summary>


**`Text`** — field · `tui/agent.go:40`

```go
Text string
```

Text is the chunk produced by the model when Partial=true, or
the committed full text when Partial=false. The TUI accumulates
partials into the in-progress assistant message and Glamour-
renders the accumulated text on every update.


**`Partial`** — field · `tui/agent.go:41`

```go
Partial bool
```

_No doc comment._


**`ToolCalls`** — field · `tui/agent.go:46`

```go
ToolCalls []ToolCall
```

ToolCalls lists tool invocations the model issued in this event.
ID is the stable function-call ID used for deduping across
partial + committed echoes.


**`ToolResults`** — field · `tui/agent.go:55` · added in **v0.3.0** ([`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc))

```go
ToolResults []ToolResult
```

ToolResults lists tool completions delivered alongside or after
the corresponding ToolCalls. ID matches the call's ID so the
TUI can attach the result to the right tool row. A populated
Error string indicates failure; a populated Response carries
the structured payload (per-tool shape — `content` for
read_file, `stdout`/`stderr` for bash, `bytes_written` for
write_file, etc.). The renderer picks the relevant keys.


**`Usage`** — field · `tui/agent.go:59`

```go
Usage *Usage
```

Usage carries token counts. The TUI snapshots the most recent
non-nil value and reports it at turn end.


**`CostUSD`** — field · `tui/agent.go:66` · added in **v0.1.0** ([`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd))

```go
CostUSD float64
```

CostUSD is the dollar cost for THIS event's usage (typically
the final per-turn cost when the agent emits its usage event).
0 suppresses the per-turn footer's "$X" segment. The TUI also
snapshots the most recent positive value and reports it at
turn end alongside Usage / Model.


**`Model`** — field · `tui/agent.go:72` · added in **v0.1.0** ([`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd))

```go
Model string
```

Model is the resolved model identifier for THIS event. Adapters
populate it on the usage event so the per-turn footer ("◇ X
· in · out · $X · 4s") and status sidebar can reflect the live
agent. Empty events leave m.currentModel unchanged.


**`StatusUpdate`** — field · `tui/agent.go:86` · added in **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751))

```go
StatusUpdate *StatusUpdate
```

Push-mode fields (issue #40, SSE event-stream spec v1.1.0 at
docs/sse-event-stream-protocol.md). Host adapters that
consume push events from a server (currently core-agent's
SSE /events stream) populate exactly one of these per Event
to carry the corresponding spec payload through to the TUI's
Update loop. All optional — legacy hosts that don't consume
push events leave them nil and the per-turn-inferred state
(via Usage / Model on this struct) keeps working unchanged.

At most one of these is non-nil per Event in normal usage
(one SSE wire event → one Event), though multi-population
is tolerated — handlers fire independently.


**`UsageUpdate`** — field · `tui/agent.go:87` · added in **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751))

```go
UsageUpdate *UsageUpdate
```

_No doc comment._


**`Inbox`** — field · `tui/agent.go:88` · added in **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751))

```go
Inbox *InboxEvent
```

_No doc comment._


**`TurnComplete`** — field · `tui/agent.go:89` · added in **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751))

```go
TurnComplete *TurnSummary
```

_No doc comment._


**`TurnError`** — field · `tui/agent.go:90` · added in **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751))

```go
TurnError *TurnError
```

_No doc comment._


</details>

---

#### `InboxDrainer`

`type` · `tui/agent.go:242` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.6.0** — [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f), 2026-05-27, _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_

InboxDrainer is an optional capability for hosts whose agent
queues operator-injected messages in an internal inbox that's
distinct from the per-turn prompt. Combined with InjectableAgent
it gives core-tui the ability to drive an auto-continue loop on
hosts whose runner is opaque (ADK, anywhere the iterator-shaped
runner owns its own loop and doesn't expose mid-turn hooks).

DrainInbox returns the currently queued messages AND removes
them from the inbox in one call — semantics matter, since the
TUI then formats + submits them as a synthetic turn. A nil /
empty return is the signal "nothing to auto-continue, idle."

PendingInboxCount is a non-destructive peek used for sizing /
UI hints; it may return a coarse upper bound if the host can't
precisely count without mutating state.

See issue #9 and Options.MidTurnInjectionMode ==
AutoContinueFromInbox.


```go
type InboxDrainer interface {
	DrainInbox() []string
	PendingInboxCount() int
}
```

<details>
<summary><b>2 exported members</b></summary>


**`DrainInbox`** — method · `tui/agent.go:243`

```go
DrainInbox() []string
```

_No doc comment._


**`PendingInboxCount`** — method · `tui/agent.go:244`

```go
PendingInboxCount() int
```

_No doc comment._


</details>

---

#### `InjectableAgent`

`type` · `tui/agent.go:161` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter.go`)**  
introduced in **v0.1.0** — [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5), 2026-05-25, _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_

InjectableAgent is an optional capability: hosts whose agent
supports mid-turn message injection (feeding a message INTO the
currently-streaming turn's context, distinct from queueing for
the next turn) implement it on their Agent type. The TUI checks
the capability with a type assertion when
Options.MidTurnInjectionMode == InjectIntoCurrent — without the
capability, the mode silently falls back to QueueForNext (no
runtime error).

See R-CHAT-11 in requirements.md and design.md §3.3.


```go
type InjectableAgent interface {
	Inject(message string) error
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Inject`** — method · `tui/agent.go:162`

```go
Inject(message string) error
```

_No doc comment._


</details>

---

#### `LiveAgent`

`type` · `tui/agent.go:203` · contract closure: **inside** · core-agent: **3 refs (`internal/coretuiremote/adapter.go`, `internal/coretuiremote/live_agent_test.go`)**  
introduced in **v0.6.6** — [`e277ddc`](https://github.com/go-steer/core-tui/commit/e277ddc5f712fe712c0d5bb9a513520456f8affd), 2026-05-31, _feat(agent): LiveAgent capability — observer-mode hosts (#22)_

LiveAgent is an optional capability for hosts whose agent isn't
driven by per-turn Run calls — remote-attached daemons running
autonomously, observer-mode TUIs watching MCP-server-triggered
activity, etc. (issue #22). When implemented, core-tui spawns a
single long-lived goroutine at startup that ranges over
Events(ctx) and feeds the chat view from every event,
regardless of whether the operator typed.

Precedence: LiveAgent WINS over the per-turn Run path. Hosts
satisfying both interfaces have Run silently skipped — operator
submissions flow through InjectableAgent.Inject when available,
otherwise the TUI logs a one-time "read-only view" system note
and discards the typed text.

Semantics (locked during PR review):
  - ctx cancellation mid-iter: implementations stop yielding;
    no final (zero, ctx.Err()) yield is required.
  - Transient errors (non-nil err): core-tui surfaces them in
    the chat as a RoleError row and KEEPS draining. The
    iterator decides whether to keep yielding events.
  - Iterator end (Events returns / stops yielding): core-tui
    renders a "Disconnected — Ctrl+C to quit" system row and
    keeps the program alive so the operator can read scrollback.
  - Reconnect: implementation-internal. core-tui calls Events
    exactly once at startup and trusts the iterator to handle
    its own reconnection / replay semantics.
  - Turn-end commit: Event{Text: ..., Partial: false} commits
    the accumulated in-progress assistant text (matches the
    existing Run-path convention). Hosts that forget to flush
    a non-partial close cause a slightly-laggy commit — never
    corruption.
  - Spinner: active whenever the most recent partial Text
    arrived AFTER the most recent commit Text (i.e. tokens are
    in flight). Idle when committed and idle, even though the
    event stream itself is "always live".

See docs/remote-tui-observer-mode.md (in the core-agent repo)
for the architectural motivation + adapter sketch.


```go
type LiveAgent interface {
	Events(ctx context.Context) iter.Seq2[Event, error]
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Events`** — method · `tui/agent.go:204`

```go
Events(ctx context.Context) iter.Seq2[Event, error]
```

_No doc comment._


</details>

---

#### `PermanentStreamError`

`type` · `tui/agent.go:219` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.11.0** — [`691bd82`](https://github.com/go-steer/core-tui/commit/691bd826439623942506cc8106e0b2deeb7e7d40), 2026-07-16, _fix(live-stream): quality pass — observer banner, permanent-error classification, SystemMessage wrap test (#59)_

PermanentStreamError, when implemented by an error returned from
LiveAgent.Events, signals a condition the TUI can't recover from by
retrying (session gone, auth revoked). Adapters wrap upstream
HTTP 404 / 401 / 403 errors — or any locally-detected permanent
condition — with this interface so the TUI can flip to a terminal
"session unavailable" row instead of looping forever on the
reconnect path (issue #51).

If the interface isn't implemented, the TUI falls back to a small
substring heuristic ("status 404" / "status 401" / "status 403") so
existing adapters that already stringify the HTTP status keep the
same behavior without needing an immediate update.


```go
type PermanentStreamError interface {
	error
	PermanentStreamErr() bool
}
```

<details>
<summary><b>2 exported members</b></summary>


**`error`** — embedded · `tui/agent.go:220`

```go

```

_No doc comment._


**`PermanentStreamErr`** — method · `tui/agent.go:221`

```go
PermanentStreamErr() bool
```

_No doc comment._


</details>

---

#### `RemoteInterrupter`

`type` · `tui/agent.go:288` · contract closure: **inside** · core-agent: **3 refs (`internal/coretuiremote/adapter.go`)**  
introduced in **v0.15.0** — [`6ea9c1e`](https://github.com/go-steer/core-tui/commit/6ea9c1e07a7d45b185e9a07198704c38bef862cf), 2026-07-17, _feat(interrupt): RemoteInterrupter capability for observer-mode (#65)_

RemoteInterrupter is an optional capability: hosts whose agent runs
remotely (LiveAgent observer mode against a daemon) implement it so
the /interrupt slash can cancel an in-flight turn even when the TUI
has no local per-turn context to cancel.

Without this capability, /interrupt short-circuits with "no turn in
flight" on remote sessions — the local Run-path gate keys off
`m.cancelTurn`, which is only set for operator-initiated turns
through the per-turn iterator. Autonomous turns driven by the daemon
(k8s-event-watcher injects, runaway tool loops, etc.) stream
through LiveAgent but never populate cancelTurn, leaving the
operator without a way to stop them from the TUI even when the
daemon exposes a cancel endpoint.

Implementations MAY block briefly on network I/O; the TUI calls
Interrupt off the Update-loop path so it doesn't stall the UI. A
short deadline via ctx is appropriate — a hung interrupt is worse
than a failed one because it leaves the operator uncertain whether
their input landed. Errors surface as an inline RoleError row.

Same optional-capability pattern as LiveAgent / InboxDrainer /
WakeRequester — the TUI type-asserts at slash-fire time and falls
back to the existing "no turn in flight" message when the interface
isn't implemented.


```go
type RemoteInterrupter interface {
	Interrupt(ctx context.Context) error
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Interrupt`** — method · `tui/agent.go:289`

```go
Interrupt(ctx context.Context) error
```

_No doc comment._


</details>

---

#### `ToolCall`

`type` · `tui/agent.go:94` · contract closure: **inside** · core-agent: **5 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuievent/coretuievent.go`, `internal/coretuiremote/adapter.go`)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

ToolCall describes a single tool invocation.


```go
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}
```

<details>
<summary><b>3 exported members</b></summary>


**`ID`** — field · `tui/agent.go:95`

```go
ID string
```

_No doc comment._


**`Name`** — field · `tui/agent.go:96`

```go
Name string
```

_No doc comment._


**`Args`** — field · `tui/agent.go:97`

```go
Args map[string]any
```

_No doc comment._


</details>

---

#### `ToolResult`

`type` · `tui/agent.go:106` · contract closure: **inside** · core-agent: **5 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuievent/coretuievent.go`, `internal/coretuiremote/adapter.go`, +1 more)**  
introduced in **v0.3.0** — [`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc), 2026-05-26, _feat(tool-results): wire ToolResult events through the agent → TUI flow_<br>declaration changed since: **v0.12.0** ([`6622f3a`](https://github.com/go-steer/core-tui/commit/6622f3a2fc366a529732a6df986b04ecbd1449a4), _feat(tui): consume per-tool-call latency_ms + spec bump v1.2.0 (#62)_); **v0.14.0** ([`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924), _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_)

ToolResult describes a single tool completion. ID correlates
with the originating ToolCall.ID. Error is non-empty iff the
tool failed; Response carries the per-tool structured payload
when the call succeeded. The TUI uses Name + Response in the
per-tool result renderer (renderToolResult) — adapters should
preserve the tool's native shape rather than pre-flattening.


```go
type ToolResult struct {
	// ... 6 exported members, documented below
}
```

<details>
<summary><b>6 exported members</b></summary>


**`ID`** — field · `tui/agent.go:107`

```go
ID string
```

_No doc comment._


**`Name`** — field · `tui/agent.go:108`

```go
Name string
```

_No doc comment._


**`Response`** — field · `tui/agent.go:109`

```go
Response map[string]any
```

_No doc comment._


**`Error`** — field · `tui/agent.go:110`

```go
Error string
```

_No doc comment._


**`LatencyMs`** — field · `tui/agent.go:125` · added in **v0.12.0** ([`6622f3a`](https://github.com/go-steer/core-tui/commit/6622f3a2fc366a529732a6df986b04ecbd1449a4))

```go
LatencyMs int64
```

LatencyMs is the wall-clock time (in milliseconds) the tool
call took, measured from dispatch to result received. Optional
— 0 suppresses the inline `[2.4s]` badge and dialog chip.

Adapters MAY populate this field directly; core-tui also
auto-plucks the value from Response["latency_ms"] when this
field is 0, because core-agent's PR #278 emits it inside the
response map (ADK's Tool.Run has no write access to the
enclosing session.Event's CustomMetadata, so the map itself is
the only sidecar channel). Either surface works — hosts pick
whichever fits their pipeline.

Consumer side of core-tui #60 / SSE spec v1.2.0.


**`Savings`** — field · `tui/agent.go:142` · added in **v0.14.0** ([`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924))

```go
Savings *ToolSavings
```

Savings is the digest wrap's per-call reduction — original vs.
digested byte / token counts, plus the router's dispatch
decision (structural pruner, LLM subagent, or bypassed
passthrough). Nil when the host didn't dispatch through a
digest wrap (or the response arrived pre-v1.3.0 without the
sidecar). Renderers show a compact inline chip on the tool row
and a full block in the tool-call detail overlay.

Same auto-pluck pattern as LatencyMs: adapters MAY populate
this field directly; core-tui also plucks it from
Response["savings"] when this field is nil, because
core-agent's PR #290 emits the map inside the response
payload (same ADK constraint that shipped latency_ms there).

Consumer side of SSE spec v1.3.0 / core-agent #223 Phase 4.


</details>

---

#### `Usage`

`type` · `tui/agent.go:146` · contract closure: **inside** · core-agent: **24 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter.go`, `internal/coretuiremote/adapter_test.go`, +2 more)**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Usage carries token counts for a turn.


```go
type Usage struct {
	InputTokens  int
	OutputTokens int
}
```

<details>
<summary><b>2 exported members</b></summary>


**`InputTokens`** — field · `tui/agent.go:147`

```go
InputTokens int
```

_No doc comment._


**`OutputTokens`** — field · `tui/agent.go:148`

```go
OutputTokens int
```

_No doc comment._


</details>

---

#### `WakeRequester`

`type` · `tui/agent.go:260` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter.go`)**  
introduced in **v0.1.0** — [`33055c6`](https://github.com/go-steer/core-tui/commit/33055c6e6a3097e8b782eb780ffcdb48a057635d), 2026-05-25, _feat(tui): WakeRequester capability + toast banner (R-WAKE-1, PR 4/5)_

WakeRequester is an optional capability: hosts whose agent
emits "I need the operator's attention" signals (typically from
background sub-agents reporting completion or asking for input)
implement it. WakeRequested returns a receive-only channel; each
receive triggers a transient toast banner in the TUI.

The TUI subscribes once at startup via a goroutine that ranges
over the channel; the host owns channel lifecycle (closing the
channel is fine — the goroutine exits cleanly). The interface
makes no promise about coalescing: rapid back-to-back wakes will
render multiple toasts in sequence.

See R-WAKE-1 in requirements.md and design.md §3.3.


```go
type WakeRequester interface {
	WakeRequested() <-chan struct{}
}
```

<details>
<summary><b>1 exported members</b></summary>


**`WakeRequested`** — method · `tui/agent.go:261`

```go
WakeRequested() <-chan struct{}
```

_No doc comment._


</details>

---

#### `InboxEvent`

`type` · `tui/remote_events.go:111` · contract closure: **inside** · core-agent: **2 refs (`internal/coretuiremote/typed_events.go`)**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

InboxEvent matches the spec §2.4 inbox payload — operator-typed
prompt transitioning between inbox states. The PromptID
correlates queued/dequeued pairs and threads through to the
matching TurnSummary / TurnError for the same prompt.


```go
type InboxEvent struct {
	State    string    `json:"state"`
	PromptID string    `json:"prompt_id"`
	QueuedAt time.Time `json:"queued_at,omitempty"`
}
```

<details>
<summary><b>3 exported members</b></summary>


**`State`** — field · `tui/remote_events.go:112`

```go
State string `json:"state"`
```

_No doc comment._


**`PromptID`** — field · `tui/remote_events.go:113`

```go
PromptID string `json:"prompt_id"`
```

_No doc comment._


**`QueuedAt`** — field · `tui/remote_events.go:114`

```go
QueuedAt time.Time `json:"queued_at,omitempty"`
```

_No doc comment._


</details>

---

#### `StatusUpdate`

`type` · `tui/remote_events.go:42` · contract closure: **inside** · core-agent: **2 refs (`internal/coretuiremote/typed_events.go`)**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

StatusUpdate matches the spec §2.2 status-update payload. Used
for session-level state changes — turn boundaries, model swap,
permission mode change, provider tag change.

Merge semantics: when a host populates Event.StatusUpdate, the
consumer applies fields field-by-field — absent / zero-valued
optional fields leave the existing state unchanged. TurnState is
always present on every emission per spec. Optional fields use
pointer types where the zero value would conflict with a
meaningful empty / zero state (e.g. ContextPct = 0 means
"fresh context", not "unknown").


```go
type StatusUpdate struct {
	Model      string `json:"model,omitempty"`
	Provider   string `json:"provider,omitempty"`
	PermMode   string `json:"perm_mode,omitempty"`
	TurnState  string `json:"turn_state"`
	ContextPct *int   `json:"context_pct,omitempty"`
}
```

<details>
<summary><b>5 exported members</b></summary>


**`Model`** — field · `tui/remote_events.go:43`

```go
Model string `json:"model,omitempty"`
```

_No doc comment._


**`Provider`** — field · `tui/remote_events.go:44`

```go
Provider string `json:"provider,omitempty"`
```

_No doc comment._


**`PermMode`** — field · `tui/remote_events.go:45`

```go
PermMode string `json:"perm_mode,omitempty"`
```

_No doc comment._


**`TurnState`** — field · `tui/remote_events.go:46`

```go
TurnState string `json:"turn_state"`
```

_No doc comment._


**`ContextPct`** — field · `tui/remote_events.go:47`

```go
ContextPct *int `json:"context_pct,omitempty"`
```

_No doc comment._


</details>

---

#### `TurnError`

`type` · `tui/remote_events.go:145` · contract closure: **inside** · core-agent: **2 refs (`internal/coretuiremote/typed_events.go`)**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnError matches the spec §2.6 turn-error payload — structured
error info that should be surfaced inline in the chat. Kind
drives client rendering decisions (e.g. retry affordance only
when Retryable=true). Consumers tolerate unknown Kind values by
treating them as TurnErrorUnknown.


```go
type TurnError struct {
	Kind      string `json:"kind"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Hint      string `json:"hint,omitempty"`
}
```

<details>
<summary><b>5 exported members</b></summary>


**`Kind`** — field · `tui/remote_events.go:146`

```go
Kind string `json:"kind"`
```

_No doc comment._


**`Code`** — field · `tui/remote_events.go:147`

```go
Code string `json:"code,omitempty"`
```

_No doc comment._


**`Message`** — field · `tui/remote_events.go:148`

```go
Message string `json:"message"`
```

_No doc comment._


**`Retryable`** — field · `tui/remote_events.go:149`

```go
Retryable bool `json:"retryable"`
```

_No doc comment._


**`Hint`** — field · `tui/remote_events.go:150`

```go
Hint string `json:"hint,omitempty"`
```

_No doc comment._


</details>

---

#### `TurnSummary`

`type` · `tui/remote_events.go:131` · contract closure: **inside** · core-agent: **4 refs (`internal/coretuiremote/typed_events.go`, `internal/coretuiremote/typed_events_test.go`)**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnSummary matches the spec §2.5 turn-complete payload —
per-turn tokens + cost + latency + model. CostUSD is OPTIONAL
in spec v1.1.0: servers that compute cost out-of-band (e.g.
core-agent's pkg/agent doesn't know about internal/pricing)
emit 0 here and rely on the immediately-following UsageUpdate
to carry authoritative cost. Consumers correlate via PromptID.


```go
type TurnSummary struct {
	PromptID  string  `json:"prompt_id"`
	Model     string  `json:"model"`
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	LatencyMs int64   `json:"latency_ms"`
}
```

<details>
<summary><b>6 exported members</b></summary>


**`PromptID`** — field · `tui/remote_events.go:132`

```go
PromptID string `json:"prompt_id"`
```

_No doc comment._


**`Model`** — field · `tui/remote_events.go:133`

```go
Model string `json:"model"`
```

_No doc comment._


**`TokensIn`** — field · `tui/remote_events.go:134`

```go
TokensIn int `json:"tokens_in"`
```

_No doc comment._


**`TokensOut`** — field · `tui/remote_events.go:135`

```go
TokensOut int `json:"tokens_out"`
```

_No doc comment._


**`CostUSD`** — field · `tui/remote_events.go:136`

```go
CostUSD float64 `json:"cost_usd,omitempty"`
```

_No doc comment._


**`LatencyMs`** — field · `tui/remote_events.go:137`

```go
LatencyMs int64 `json:"latency_ms"`
```

_No doc comment._


</details>

---

#### `UsageByModel`

`type` · `tui/remote_events.go:100` · contract closure: **inside** · core-agent: **2 refs (`internal/coretuiremote/typed_events.go`)**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

UsageByModel is one entry in UsageUpdate.ByModel — per-model
token counts, cost, and turn count for the cost-routing pitch
of --agentic-tools (primary vs small model).


```go
type UsageByModel struct {
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
	Turns     int     `json:"turns"`
}
```

<details>
<summary><b>4 exported members</b></summary>


**`TokensIn`** — field · `tui/remote_events.go:101`

```go
TokensIn int `json:"tokens_in"`
```

_No doc comment._


**`TokensOut`** — field · `tui/remote_events.go:102`

```go
TokensOut int `json:"tokens_out"`
```

_No doc comment._


**`CostUSD`** — field · `tui/remote_events.go:103`

```go
CostUSD float64 `json:"cost_usd"`
```

_No doc comment._


**`Turns`** — field · `tui/remote_events.go:104`

```go
Turns int `json:"turns"`
```

_No doc comment._


</details>

---

#### `UsageLastTurn`

`type` · `tui/remote_events.go:89` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.10.2** — [`f1162dd`](https://github.com/go-steer/core-tui/commit/f1162ddd0176ad293c45fb02fe39de2fbafd9a13), 2026-07-15, _fix(observer): stamp per-turn footer on tail assistant Message so LiveAgent mode renders it (#58)_

UsageLastTurn is the per-turn payload attached to UsageUpdate.
Cost is authoritative (server-side pricing layer, includes
cache-discount + operator overrides). TokensInCached is optional —
servers with cache-attribution wired (core-agent post-#248)
populate it; older servers omit and consumers ignore.

Issue #57 / spec v1.1.1.


```go
type UsageLastTurn struct {
	TokensIn       int     `json:"tokens_in"`
	TokensInCached int     `json:"tokens_in_cached,omitempty"`
	TokensOut      int     `json:"tokens_out"`
	CostUSD        float64 `json:"cost_usd"`
	Model          string  `json:"model,omitempty"`
}
```

<details>
<summary><b>5 exported members</b></summary>


**`TokensIn`** — field · `tui/remote_events.go:90`

```go
TokensIn int `json:"tokens_in"`
```

_No doc comment._


**`TokensInCached`** — field · `tui/remote_events.go:91`

```go
TokensInCached int `json:"tokens_in_cached,omitempty"`
```

_No doc comment._


**`TokensOut`** — field · `tui/remote_events.go:92`

```go
TokensOut int `json:"tokens_out"`
```

_No doc comment._


**`CostUSD`** — field · `tui/remote_events.go:93`

```go
CostUSD float64 `json:"cost_usd"`
```

_No doc comment._


**`Model`** — field · `tui/remote_events.go:94`

```go
Model string `json:"model,omitempty"`
```

_No doc comment._


</details>

---

#### `UsageUpdate`

`type` · `tui/remote_events.go:73` · contract closure: **inside** · core-agent: **2 refs (`internal/coretuiremote/typed_events.go`)**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_<br>declaration changed since: **v0.10.2** ([`f1162dd`](https://github.com/go-steer/core-tui/commit/f1162ddd0176ad293c45fb02fe39de2fbafd9a13), _fix(observer): stamp per-turn footer on tail assistant Message so LiveAgent mode renders it (#58)_)

UsageUpdate matches the spec §2.3 usage-update payload — the
cumulative session totals plus optional per-model breakdown. The
per-model breakdown is the data side of #38 (the rendering side
in /stats reads from a parallel local field that this update
snapshots into).

LastTurn (spec v1.1.1 addition, issue #57) carries authoritative
per-turn tokens + cost for the just-completed turn. Optional —
pre-v1.1.1 servers omit it; consumers back-annotate the tail
assistant Message's footer when present so observer-mode
(LiveAgent) sessions render the per-turn footer without needing
finalizeTurn (which only fires on turnDoneMsg from the per-turn
Run path).


```go
type UsageUpdate struct {
	TokensInTotal  int                     `json:"tokens_in_total"`
	TokensOutTotal int                     `json:"tokens_out_total"`
	CostUSDTotal   float64                 `json:"cost_usd_total"`
	TurnsTotal     int                     `json:"turns_total"`
	ByModel        map[string]UsageByModel `json:"by_model,omitempty"`
	LastTurn       *UsageLastTurn          `json:"last_turn,omitempty"`
}
```

<details>
<summary><b>6 exported members</b></summary>


**`TokensInTotal`** — field · `tui/remote_events.go:74`

```go
TokensInTotal int `json:"tokens_in_total"`
```

_No doc comment._


**`TokensOutTotal`** — field · `tui/remote_events.go:75`

```go
TokensOutTotal int `json:"tokens_out_total"`
```

_No doc comment._


**`CostUSDTotal`** — field · `tui/remote_events.go:76`

```go
CostUSDTotal float64 `json:"cost_usd_total"`
```

_No doc comment._


**`TurnsTotal`** — field · `tui/remote_events.go:77`

```go
TurnsTotal int `json:"turns_total"`
```

_No doc comment._


**`ByModel`** — field · `tui/remote_events.go:78`

```go
ByModel map[string]UsageByModel `json:"by_model,omitempty"`
```

_No doc comment._


**`LastTurn`** — field · `tui/remote_events.go:79` · added in **v0.10.2** ([`f1162dd`](https://github.com/go-steer/core-tui/commit/f1162ddd0176ad293c45fb02fe39de2fbafd9a13))

```go
LastTurn *UsageLastTurn `json:"last_turn,omitempty"`
```

_No doc comment._


</details>

---

#### `InboxStateDequeued`

`const` · `tui/remote_events.go:122` · block `InboxStateQueued` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

Inbox state values from spec §2.4. Servers MAY emit unknown
values for future states (e.g. "injected"); consumers MUST
tolerate them (treat as no-op).


```go
InboxStateDequeued = "dequeued"
```

---

#### `InboxStateQueued`

`const` · `tui/remote_events.go:121` · block `InboxStateQueued` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

Inbox state values from spec §2.4. Servers MAY emit unknown
values for future states (e.g. "injected"); consumers MUST
tolerate them (treat as no-op).


```go
InboxStateQueued = "queued"
```

---

#### `TurnErrorAuth`

`const` · `tui/remote_events.go:157` · block `TurnErrorConfig` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnError kind constants from spec §2.6. Hosts MAY emit unknown
values; consumers MUST treat unknown as TurnErrorUnknown.


```go
TurnErrorAuth = "auth_error"
```

---

#### `TurnErrorConfig`

`const` · `tui/remote_events.go:156` · block `TurnErrorConfig` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnError kind constants from spec §2.6. Hosts MAY emit unknown
values; consumers MUST treat unknown as TurnErrorUnknown.


```go
TurnErrorConfig = "config_error"
```

---

#### `TurnErrorModelNotFound`

`const` · `tui/remote_events.go:158` · block `TurnErrorConfig` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnError kind constants from spec §2.6. Hosts MAY emit unknown
values; consumers MUST treat unknown as TurnErrorUnknown.


```go
TurnErrorModelNotFound = "model_not_found"
```

---

#### `TurnErrorRateLimited`

`const` · `tui/remote_events.go:159` · block `TurnErrorConfig` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnError kind constants from spec §2.6. Hosts MAY emit unknown
values; consumers MUST treat unknown as TurnErrorUnknown.


```go
TurnErrorRateLimited = "rate_limited"
```

---

#### `TurnErrorTransientNet`

`const` · `tui/remote_events.go:160` · block `TurnErrorConfig` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnError kind constants from spec §2.6. Hosts MAY emit unknown
values; consumers MUST treat unknown as TurnErrorUnknown.


```go
TurnErrorTransientNet = "transient_network"
```

---

#### `TurnErrorUnknown`

`const` · `tui/remote_events.go:161` · block `TurnErrorConfig` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

TurnError kind constants from spec §2.6. Hosts MAY emit unknown
values; consumers MUST treat unknown as TurnErrorUnknown.


```go
TurnErrorUnknown = "unknown"
```

---

#### `TurnStateAwaitingElicit`

`const` · `tui/remote_events.go:57` · block `TurnStateIdle` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

Turn-state values from spec §2.2. Hosts MAY emit unknown values
(forward-compat); consumers tolerate them by treating as the
no-op idle state.


```go
TurnStateAwaitingElicit = "awaiting_elicit"
```

---

#### `TurnStateAwaitingPermission`

`const` · `tui/remote_events.go:56` · block `TurnStateIdle` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

Turn-state values from spec §2.2. Hosts MAY emit unknown values
(forward-compat); consumers tolerate them by treating as the
no-op idle state.


```go
TurnStateAwaitingPermission = "awaiting_permission"
```

---

#### `TurnStateIdle`

`const` · `tui/remote_events.go:54` · block `TurnStateIdle` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

Turn-state values from spec §2.2. Hosts MAY emit unknown values
(forward-compat); consumers tolerate them by treating as the
no-op idle state.


```go
TurnStateIdle = "idle"
```

---

#### `TurnStateStreaming`

`const` · `tui/remote_events.go:55` · block `TurnStateIdle` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.9.0** — [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), 2026-06-09, _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_

Turn-state values from spec §2.2. Hosts MAY emit unknown values
(forward-compat); consumers tolerate them by treating as the
no-op idle state.


```go
TurnStateStreaming = "streaming"
```

---

### 8.3 Optional agent capabilities

_Files: `tui/capabilities.go`, `tui/slash.go`_

#### `ApprovalLog`

`type` · `tui/capabilities.go:188` · contract closure: **inside** · core-agent: **7 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

ApprovalLog is one row in the /permissions review picker — the
gate's recollection of every approval-shaped decision the
operator made this session.


```go
type ApprovalLog struct {
	Tool     string
	Key      string
	Decision string // "allow-once" / "allow-session" / "deny" / etc.
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Tool`** — field · `tui/capabilities.go:189`

```go
Tool string
```

_No doc comment._


**`Key`** — field · `tui/capabilities.go:190`

```go
Key string
```

_No doc comment._


**`Decision`** — field · `tui/capabilities.go:191`

```go
Decision string
```

"allow-once" / "allow-session" / "deny" / etc.


</details>

---

#### `MCPServerInfo`

`type` · `tui/capabilities.go:150` · contract closure: **inside** · core-agent: **6 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_<br>declaration changed since: **v0.1.0** ([`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd), _feat(tui): real /mcp /tools /skills + cost + scrollable palette_)

MCPServerInfo is one entry in the /mcp display. Tools carries the
per-server tool catalog (name + description) so /mcp can render a
nested view; an empty slice falls back to the ToolCount summary.


```go
type MCPServerInfo struct {
	Name      string
	Transport string // "stdio" / "http" / "sse" / "websocket"
	URL       string // empty for stdio
	Connected bool
	ToolCount int
	Tools     []MCPToolInfo
}
```

<details>
<summary><b>6 exported members</b></summary>


**`Name`** — field · `tui/capabilities.go:151`

```go
Name string
```

_No doc comment._


**`Transport`** — field · `tui/capabilities.go:152`

```go
Transport string
```

"stdio" / "http" / "sse" / "websocket"


**`URL`** — field · `tui/capabilities.go:153`

```go
URL string
```

empty for stdio


**`Connected`** — field · `tui/capabilities.go:154`

```go
Connected bool
```

_No doc comment._


**`ToolCount`** — field · `tui/capabilities.go:155`

```go
ToolCount int
```

_No doc comment._


**`Tools`** — field · `tui/capabilities.go:156` · added in **v0.1.0** ([`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd))

```go
Tools []MCPToolInfo
```

_No doc comment._


</details>

---

#### `MCPToolInfo`

`type` · `tui/capabilities.go:162` · contract closure: **inside** · core-agent: **6 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd), 2026-05-25, _feat(tui): real /mcp /tools /skills + cost + scrollable palette_

MCPToolInfo is one tool exposed by an MCP server, for the /mcp
nested rendering. Name is required; Description is optional and
rendered indented under the name when present.


```go
type MCPToolInfo struct {
	Name        string
	Description string
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Name`** — field · `tui/capabilities.go:163`

```go
Name string
```

_No doc comment._


**`Description`** — field · `tui/capabilities.go:164`

```go
Description string
```

_No doc comment._


</details>

---

#### `MemoryFile`

`type` · `tui/capabilities.go:140` · contract closure: **inside** · core-agent: **6 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_<br>declaration changed since: **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3), _feat(tui-parity): tier 3 — polish (cursor + cwd + provider, ctrl-l/u, dynamic thinking, drill-in, /stats turns+duration, /memory bytes, perm echo, aliases, indent, elicit desc)_)

MemoryFile is one entry in the /memory display.


```go
type MemoryFile struct {
	Path      string
	Excerpt   string // optional first few lines for the display
	Bytes     int64  // optional file size; 0 = not tracked
	Truncated bool   // host reads only first N bytes when true
}
```

<details>
<summary><b>4 exported members</b></summary>


**`Path`** — field · `tui/capabilities.go:141`

```go
Path string
```

_No doc comment._


**`Excerpt`** — field · `tui/capabilities.go:142`

```go
Excerpt string
```

optional first few lines for the display


**`Bytes`** — field · `tui/capabilities.go:143` · added in **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3))

```go
Bytes int64
```

optional file size; 0 = not tracked


**`Truncated`** — field · `tui/capabilities.go:144` · added in **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3))

```go
Truncated bool
```

host reads only first N bytes when true


</details>

---

#### `ModelInfo`

`type` · `tui/capabilities.go:42` · contract closure: **inside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

ModelInfo is one entry in the /model picker.


```go
type ModelInfo struct {
	ID          string
	Display     string // optional; defaults to ID when empty
	Description string // optional dim subtitle
}
```

<details>
<summary><b>3 exported members</b></summary>


**`ID`** — field · `tui/capabilities.go:43`

```go
ID string
```

_No doc comment._


**`Display`** — field · `tui/capabilities.go:44`

```go
Display string
```

optional; defaults to ID when empty


**`Description`** — field · `tui/capabilities.go:45`

```go
Description string
```

optional dim subtitle


</details>

---

#### `ModelSwapper`

`type` · `tui/capabilities.go:36` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

ModelSwapper backs /model (R-MOD-1 / R-MOD-2).


```go
type ModelSwapper interface {
	AvailableModels() []ModelInfo
	SwitchModel(modelID string) (Agent, error)
}
```

<details>
<summary><b>2 exported members</b></summary>


**`AvailableModels`** — method · `tui/capabilities.go:37`

```go
AvailableModels() []ModelInfo
```

_No doc comment._


**`SwitchModel`** — method · `tui/capabilities.go:38`

```go
SwitchModel(modelID string) (Agent, error)
```

_No doc comment._


</details>

---

#### `ModelTotals`

`type` · `tui/capabilities.go:402` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.6.4** — [`df18400`](https://github.com/go-steer/core-tui/commit/df184001a93752715dcbdd60b1f348b5ccc335f4), 2026-05-28, _feat(usage): SessionByModelTracker — /stats per-model cost breakdown (#18)_

ModelTotals is the per-model usage row surfaced by the optional
SessionByModelTracker capability (issue #18). One entry per
distinct model the session has routed work to — useful when the
host routes subtasks to a cheaper tier (e.g. parent on
gemini-3.1-pro, subtasks on gemini-2.5-flash) and the operator
wants to see the cost-efficiency split in /stats.


```go
type ModelTotals struct {
	Turns        int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}
```

<details>
<summary><b>4 exported members</b></summary>


**`Turns`** — field · `tui/capabilities.go:403`

```go
Turns int
```

_No doc comment._


**`InputTokens`** — field · `tui/capabilities.go:404`

```go
InputTokens int
```

_No doc comment._


**`OutputTokens`** — field · `tui/capabilities.go:405`

```go
OutputTokens int
```

_No doc comment._


**`CostUSD`** — field · `tui/capabilities.go:406`

```go
CostUSD float64
```

_No doc comment._


</details>

---

#### `PathScope`

`type` · `tui/capabilities.go:428` · contract closure: **inside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

PathScope is the list of roots that bound `@file` palette lookups
(R-SCOPE-1 / R-SCOPE-2). Empty list = no scope filtering.


```go
type PathScope struct {
	Roots []string
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Roots`** — field · `tui/capabilities.go:429`

```go
Roots []string
```

_No doc comment._


**`Allows`** — method · `tui/capabilities.go:433`

```go
func (p PathScope) Allows(path string) bool
```

Allows reports whether path is inside any of the roots.


</details>

---

#### `PermissionController`

`type` · `tui/capabilities.go:178` · contract closure: **inside** · core-agent: **9 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

PermissionController backs /permissions, /allow, /deny, and the
persistence side of the permission-modal's allow-always decision
(R-PERM-3 / R-PERM-4 / R-PERM-5).


```go
type PermissionController interface {
	SessionApprovals() []ApprovalLog
	AddAllowPatterns(patterns []string) error
	AddDenyPatterns(patterns []string) error
	AddBuiltinAllowExtra(bundleName string) error
}
```

<details>
<summary><b>4 exported members</b></summary>


**`SessionApprovals`** — method · `tui/capabilities.go:179`

```go
SessionApprovals() []ApprovalLog
```

_No doc comment._


**`AddAllowPatterns`** — method · `tui/capabilities.go:180`

```go
AddAllowPatterns(patterns []string) error
```

_No doc comment._


**`AddDenyPatterns`** — method · `tui/capabilities.go:181`

```go
AddDenyPatterns(patterns []string) error
```

_No doc comment._


**`AddBuiltinAllowExtra`** — method · `tui/capabilities.go:182`

```go
AddBuiltinAllowExtra(bundleName string) error
```

_No doc comment._


</details>

---

#### `PricingController`

`type` · `tui/capabilities.go:196` · contract closure: **inside** · core-agent: **5 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

PricingController backs /pricing refresh + /pricing set
(R-PRICE-1).


```go
type PricingController interface {
	Refresh(ctx context.Context) (summary string, err error)
	Set(modelID string, inputPerMTok, outputPerMTok float64) (summary string, err error)
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Refresh`** — method · `tui/capabilities.go:197`

```go
Refresh(ctx context.Context) (summary string, err error)
```

_No doc comment._


**`Set`** — method · `tui/capabilities.go:198`

```go
Set(modelID string, inputPerMTok, outputPerMTok float64) (summary string, err error)
```

_No doc comment._


</details>

---

#### `ReloadResult`

`type` · `tui/capabilities.go:131` · contract closure: **inside** · core-agent: **12 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

ReloadResult is what Reload returns on success — the host
constructs fresh views of every reload-able piece of state and
the TUI atomically swaps to them.


```go
type ReloadResult struct {
	Agent      Agent           // replaces the live agent
	Memory     []MemoryFile    // for /memory
	MCPServers []MCPServerInfo // for /mcp
	Skills     []SkillInfo     // for /skills
	Note       string          // optional one-line system-message confirmation
}
```

<details>
<summary><b>5 exported members</b></summary>


**`Agent`** — field · `tui/capabilities.go:132`

```go
Agent Agent
```

replaces the live agent


**`Memory`** — field · `tui/capabilities.go:133`

```go
Memory []MemoryFile
```

for /memory


**`MCPServers`** — field · `tui/capabilities.go:134`

```go
MCPServers []MCPServerInfo
```

for /mcp


**`Skills`** — field · `tui/capabilities.go:135`

```go
Skills []SkillInfo
```

for /skills


**`Note`** — field · `tui/capabilities.go:136`

```go
Note string
```

optional one-line system-message confirmation


</details>

---

#### `Reloader`

`type` · `tui/capabilities.go:124` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

Reloader backs /reload (R-RELOAD-1 / R-RELOAD-2).


```go
type Reloader interface {
	Reload(ctx context.Context) (ReloadResult, error)
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Reload`** — method · `tui/capabilities.go:125`

```go
Reload(ctx context.Context) (ReloadResult, error)
```

_No doc comment._


</details>

---

#### `SessionByModelTracker`

`type` · `tui/capabilities.go:422` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.6.4** — [`df18400`](https://github.com/go-steer/core-tui/commit/df184001a93752715dcbdd60b1f348b5ccc335f4), 2026-05-28, _feat(usage): SessionByModelTracker — /stats per-model cost breakdown (#18)_

SessionByModelTracker is an optional capability on UsageTracker:
hosts that track usage per-model can satisfy it so /stats
surfaces a per-model breakdown under the existing aggregate
rows. core-tui does a duck-typed assertion at render time, so
trackers that don't implement it keep working unchanged.

Contract:
  - Map key is the model name (host-defined; should match what
    StatusReporter.Status().ModelName / displayModelName render).
  - Empty map OR single entry → /stats skips the breakdown row
    (single entry would just restate SessionTotals).
  - Hosts can include zero-cost entries; /stats decides what to
    show. Sorting / formatting lives entirely on the TUI side.


```go
type SessionByModelTracker interface {
	SessionByModel() map[string]ModelTotals
}
```

<details>
<summary><b>1 exported members</b></summary>


**`SessionByModel`** — method · `tui/capabilities.go:423`

```go
SessionByModel() map[string]ModelTotals
```

_No doc comment._


</details>

---

#### `SessionInfo`

`type` · `tui/capabilities.go:67` · contract closure: **inside** · core-agent: **5 refs (`internal/coretuiremote/capabilities.go`)**  
introduced in **v0.10.0** — [`b08dac6`](https://github.com/go-steer/core-tui/commit/b08dac6868e2fe588014d4d0c12fadc5aba908f1), 2026-07-14, _feat(switch): mid-session Agent swap via SlashResult.SwitchTo + /switch built-in (#54)_<br>declaration changed since: **v0.18.0** ([`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9), _feat(tui): text-input Dialog primitive + session-picker action rows (#56) (#72)_)

SessionInfo is one row in the /switch picker.


```go
type SessionInfo struct {
	ID          string
	Display     string // optional; defaults to ID when empty
	Description string // optional dim subtitle
	Current     bool   // marks the currently-attached row

	// Input, when non-nil, turns this row into an ACTION row
	// rather than a session row (issue #56): Enter opens a
	// single-line text-input dialog on top of the picker instead
	// of calling SwitchToSession. The canonical use is a
	// "+ Attach to endpoint…" row on a multi-daemon host where
	// the target isn't enumerable — the operator types it.
	//
	// Action rows are never marked "(current)" and their ID is
	// never passed to SwitchToSession; the row's own Submit
	// closure produces the SwitchTarget. `/switch <id>` naming an
	// action row opens the same dialog.
	Input *SessionInput
}
```

<details>
<summary><b>5 exported members</b></summary>


**`ID`** — field · `tui/capabilities.go:68`

```go
ID string
```

_No doc comment._


**`Display`** — field · `tui/capabilities.go:69`

```go
Display string
```

optional; defaults to ID when empty


**`Description`** — field · `tui/capabilities.go:70`

```go
Description string
```

optional dim subtitle


**`Current`** — field · `tui/capabilities.go:71`

```go
Current bool
```

marks the currently-attached row


**`Input`** — field · `tui/capabilities.go:84` · added in **v0.18.0** ([`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9))

```go
Input *SessionInput
```

Input, when non-nil, turns this row into an ACTION row
rather than a session row (issue #56): Enter opens a
single-line text-input dialog on top of the picker instead
of calling SwitchToSession. The canonical use is a
"+ Attach to endpoint…" row on a multi-daemon host where
the target isn't enumerable — the operator types it.

Action rows are never marked "(current)" and their ID is
never passed to SwitchToSession; the row's own Submit
closure produces the SwitchTarget. `/switch <id>` naming an
action row opens the same dialog.


</details>

---

#### `SessionInput`

`type` · `tui/capabilities.go:95` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.18.0** — [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9), 2026-08-13, _feat(tui): text-input Dialog primitive + session-picker action rows (#56) (#72)_

SessionInput describes the text-input dialog a SessionInfo action
row opens on Enter, plus the closure that turns the typed value
into a SwitchTarget. Submit is the only required field.

Keeping Submit on the row (rather than routing back through
SwitchToSession with a magic ID) means the host writes the
"what does this row do" logic in exactly one place, and core-tui
never has to guess which IDs are real sessions.


```go
type SessionInput struct {
	// Title is the dialog title bar. Empty defaults to the row's
	// display name.
	Title string

	// Prompt is the question line above the input box, e.g.
	// "Daemon URL:".
	Prompt string

	// Placeholder is the dim hint shown while the box is empty,
	// e.g. "http://host:7778".
	Placeholder string

	// Initial pre-fills the box.
	Initial string

	// Validate is called with the trimmed value on Enter. A
	// non-empty return renders inline under the box and keeps the
	// dialog open. Nil accepts anything (including "").
	Validate func(value string) string

	// Submit turns the trimmed value into a SwitchTarget. A
	// non-nil error surfaces as a RoleError row and closes both
	// dialogs; the current session stays attached. Same contract
	// as SessionSwitcher.SwitchToSession — see SwitchTarget.
	Submit func(value string) (SwitchTarget, error)
}
```

<details>
<summary><b>6 exported members</b></summary>


**`Title`** — field · `tui/capabilities.go:98`

```go
Title string
```

Title is the dialog title bar. Empty defaults to the row's
display name.


**`Prompt`** — field · `tui/capabilities.go:102`

```go
Prompt string
```

Prompt is the question line above the input box, e.g.
"Daemon URL:".


**`Placeholder`** — field · `tui/capabilities.go:106`

```go
Placeholder string
```

Placeholder is the dim hint shown while the box is empty,
e.g. "http://host:7778".


**`Initial`** — field · `tui/capabilities.go:109`

```go
Initial string
```

Initial pre-fills the box.


**`Validate`** — field · `tui/capabilities.go:114`

```go
Validate func(value string) string
```

Validate is called with the trimmed value on Enter. A
non-empty return renders inline under the box and keeps the
dialog open. Nil accepts anything (including "").


**`Submit`** — field · `tui/capabilities.go:120`

```go
Submit func(value string) (SwitchTarget, error)
```

Submit turns the trimmed value into a SwitchTarget. A
non-nil error surfaces as a RoleError row and closes both
dialogs; the current session stays attached. Same contract
as SessionSwitcher.SwitchToSession — see SwitchTarget.


</details>

---

#### `SessionSwitcher`

`type` · `tui/capabilities.go:61` · contract closure: **inside** · core-agent: **3 refs (`internal/coretuiremote/capabilities.go`, `internal/coretuiremote/session_switch_test.go`)**  
introduced in **v0.10.0** — [`b08dac6`](https://github.com/go-steer/core-tui/commit/b08dac6868e2fe588014d4d0c12fadc5aba908f1), 2026-07-14, _feat(switch): mid-session Agent swap via SlashResult.SwitchTo + /switch built-in (#54)_

SessionSwitcher backs the /switch built-in (issue #53) and lets
hosts that manage multiple sessions (e.g. a remote daemon with
per-caller bearer auth) offer a first-class in-run session picker.
Hosts that don't implement this capability can still ship a
/switch by registering an AsyncSlashProvider command that returns
a SlashResult with SwitchTo populated — /switch falls through to
the SlashProvider path when the capability is absent.

SwitchToSession returns a SwitchTarget the TUI applies via
applySwitchTarget: local detach from the outgoing Agent + attach
to the incoming one. See SwitchTarget's godoc for the lifecycle
contract (host owns old-Agent teardown; core-tui only cancels
LOCAL contexts, does not touch server-side sessions).


```go
type SessionSwitcher interface {
	Sessions() []SessionInfo
	SwitchToSession(id string) (SwitchTarget, error)
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Sessions`** — method · `tui/capabilities.go:62`

```go
Sessions() []SessionInfo
```

_No doc comment._


**`SwitchToSession`** — method · `tui/capabilities.go:63`

```go
SwitchToSession(id string) (SwitchTarget, error)
```

_No doc comment._


</details>

---

#### `SkillInfo`

`type` · `tui/capabilities.go:168` · contract closure: **inside** · core-agent: **6 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

SkillInfo is one entry in the /skills display.


```go
type SkillInfo struct {
	Name        string
	Description string
	Source      string // "local" / "<mcp-server>" / etc.
	ToolCount   int
}
```

<details>
<summary><b>4 exported members</b></summary>


**`Name`** — field · `tui/capabilities.go:169`

```go
Name string
```

_No doc comment._


**`Description`** — field · `tui/capabilities.go:170`

```go
Description string
```

_No doc comment._


**`Source`** — field · `tui/capabilities.go:171`

```go
Source string
```

"local" / "<mcp-server>" / etc.


**`ToolCount`** — field · `tui/capabilities.go:172`

```go
ToolCount int
```

_No doc comment._


</details>

---

#### `Status`

`type` · `tui/capabilities.go:372` · contract closure: **inside** · core-agent: **7 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter_test.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_<br>declaration changed since: **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3), _feat(tui-parity): tier 3 — polish (cursor + cwd + provider, ctrl-l/u, dynamic thinking, drill-in, /stats turns+duration, /memory bytes, perm echo, aliases, indent, elicit desc)_)

Status is the bundle StatusReporter returns.


```go
type Status struct {
	ModelName string
	State     string // "idle" / "running" / "deferred" / etc.
	Provider  string // "gemini" / "anthropic" / "vertex" / etc. — optional
}
```

<details>
<summary><b>3 exported members</b></summary>


**`ModelName`** — field · `tui/capabilities.go:373`

```go
ModelName string
```

_No doc comment._


**`State`** — field · `tui/capabilities.go:374`

```go
State string
```

"idle" / "running" / "deferred" / etc.


**`Provider`** — field · `tui/capabilities.go:375` · added in **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3))

```go
Provider string
```

"gemini" / "anthropic" / "vertex" / etc. — optional


</details>

---

#### `StatusReporter`

`type` · `tui/capabilities.go:367` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

StatusReporter backs the persistent status surface (R-USE-2)
when the host needs to surface non-trivial state. Most hosts
leave model name + state for the TUI to derive from Options;
implement StatusReporter when the agent has richer state
(deferred / waiting / etc.) to surface.

Status() must be safe for concurrent calls and must not block the
caller for long: the TUI refreshes the status header off its event
loop (see host_snapshot.go), so a slow implementation stalls only a
background goroutine — but a panicking or data-racy one still breaks
the TUI. Return cached/last-known state rather than doing unbounded
I/O inline.


```go
type StatusReporter interface {
	Status() Status
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Status`** — method · `tui/capabilities.go:368`

```go
Status() Status
```

_No doc comment._


</details>

---

#### `SubagentEvent`

`type` · `tui/capabilities.go:280` · contract closure: **inside** · core-agent: **7 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuievent/coretuievent.go`, `internal/coretuiremote/subagent_events.go`)**  
introduced in **v0.18.0** — [`eb881e5`](https://github.com/go-steer/core-tui/commit/eb881e52c608f7f456aee8a8aa151bae04d91f17), 2026-08-13, _feat(subagents): turn drill-down overlay + live inline tail (#70, #71) (#73)_

SubagentEvent is one turn inside a subagent: what it said, what it
called, and what came back. Every field is optional — a turn that
only calls a tool has no Text, and a turn that only speaks has no
calls.


```go
type SubagentEvent struct {
	// Seq is the host's monotonic ordering key, also used to
	// de-duplicate across overlapping pages. 0 when the host has no
	// sequence number, in which case the TUI falls back to
	// append-in-arrival-order.
	Seq int64

	// Timestamp is when the turn was recorded; zero suppresses the
	// time column.
	Timestamp time.Time

	// Author is who produced the turn — the model, the user proxy,
	// a tool name, whatever vocabulary the host uses.
	Author string

	// Text is the turn's prose (model output, a report body).
	Text string

	// ToolCalls / ToolResults are the structured tool traffic. Args
	// and Response are the raw maps, same shape as ToolCall /
	// ToolResult on the streaming surface — the TUI does its own
	// summarizing so subagent rows read like the parent's.
	ToolCalls   []SubagentToolCall
	ToolResults []SubagentToolResult
}
```

<details>
<summary><b>6 exported members</b></summary>


**`Seq`** — field · `tui/capabilities.go:285`

```go
Seq int64
```

Seq is the host's monotonic ordering key, also used to
de-duplicate across overlapping pages. 0 when the host has no
sequence number, in which case the TUI falls back to
append-in-arrival-order.


**`Timestamp`** — field · `tui/capabilities.go:289`

```go
Timestamp time.Time
```

Timestamp is when the turn was recorded; zero suppresses the
time column.


**`Author`** — field · `tui/capabilities.go:293`

```go
Author string
```

Author is who produced the turn — the model, the user proxy,
a tool name, whatever vocabulary the host uses.


**`Text`** — field · `tui/capabilities.go:296`

```go
Text string
```

Text is the turn's prose (model output, a report body).


**`ToolCalls`** — field · `tui/capabilities.go:302`

```go
ToolCalls []SubagentToolCall
```

ToolCalls / ToolResults are the structured tool traffic. Args
and Response are the raw maps, same shape as ToolCall /
ToolResult on the streaming surface — the TUI does its own
summarizing so subagent rows read like the parent's.


**`ToolResults`** — field · `tui/capabilities.go:303`

```go
ToolResults []SubagentToolResult
```

_No doc comment._


</details>

---

#### `SubagentEventPage`

`type` · `tui/capabilities.go:261` · contract closure: **inside** · core-agent: **9 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/subagent_events.go`)**  
introduced in **v0.18.0** — [`eb881e5`](https://github.com/go-steer/core-tui/commit/eb881e52c608f7f456aee8a8aa151bae04d91f17), 2026-08-13, _feat(subagents): turn drill-down overlay + live inline tail (#70, #71) (#73)_

SubagentEventPage is one page of a subagent's inner turns.


```go
type SubagentEventPage struct {
	// Events are the turns in chronological order.
	Events []SubagentEvent

	// NextSince is the cursor to pass back to fetch what comes
	// after this page. Hosts that can't produce a cursor may leave
	// it 0; the TUI then de-duplicates by Seq and re-reads from the
	// start each poll, which works but costs more.
	NextSince int64

	// Truncated reports that a page limit cut this response short
	// and there is more to fetch immediately from NextSince.
	Truncated bool
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Events`** — field · `tui/capabilities.go:263`

```go
Events []SubagentEvent
```

Events are the turns in chronological order.


**`NextSince`** — field · `tui/capabilities.go:269`

```go
NextSince int64
```

NextSince is the cursor to pass back to fetch what comes
after this page. Hosts that can't produce a cursor may leave
it 0; the TUI then de-duplicates by Seq and re-reads from the
start each poll, which works but costs more.


**`Truncated`** — field · `tui/capabilities.go:273`

```go
Truncated bool
```

Truncated reports that a page limit cut this response short
and there is more to fetch immediately from NextSince.


</details>

---

#### `SubagentEventReader`

`type` · `tui/capabilities.go:256` · contract closure: **inside** · core-agent: **7 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/subagent_events.go`, `internal/subagentlog/subagentlog.go`, +1 more)**  
introduced in **v0.18.0** — [`eb881e5`](https://github.com/go-steer/core-tui/commit/eb881e52c608f7f456aee8a8aa151bae04d91f17), 2026-08-13, _feat(subagents): turn drill-down overlay + live inline tail (#70, #71) (#73)_

SubagentEventReader backs the subagent turn drill-down (issue #71):
what a subagent actually DID, not just its name, status, and final
report. Hosts that keep a per-subagent turn log implement it; the
canonical one is core-agent's
GET /sessions/{id}/agents/{name}/events.

Two surfaces consume it, both off the render path:

  - `/subagents <name>` opens a detail overlay — the untruncated
    report (issue #70) above a scrollable turn log.
  - A running SYNC subagent's tool row grows a live preview block
    underneath it, tailed while the call is in flight, which
    collapses to a one-line summary when the result lands.

Contract:

  - Paged and cursored. since is a seq cursor; 0 means "from the
    start". Return the page plus the cursor to resume from. The
    tail poller calls this once a second with the previous
    NextSince, so an implementation that ignores since will make
    the TUI re-render the whole log every tick.
  - Honor ctx. The TUI always passes a bounded one and discards
    late results; a blocking implementation parks a background
    goroutine, never the event loop.
  - Report an unresolvable name as a *SubagentNotFoundError rather
    than an empty page, so the UI can say "no such subagent, here
    are the ones there are" instead of showing a plausible-looking
    empty log. An empty page means "this subagent has recorded no
    turns yet", which is a different and legitimate answer.


```go
type SubagentEventReader interface {
	SubagentEvents(ctx context.Context, name string, since int64) (SubagentEventPage, error)
}
```

<details>
<summary><b>1 exported members</b></summary>


**`SubagentEvents`** — method · `tui/capabilities.go:257`

```go
SubagentEvents(ctx context.Context, name string, since int64) (SubagentEventPage, error)
```

_No doc comment._


</details>

---

#### `SubagentInfo`

`type` · `tui/capabilities.go:220` · contract closure: **inside** · core-agent: **6 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

SubagentInfo is one entry in the /subagents display.


```go
type SubagentInfo struct {
	Name       string
	Status     string // "running" / "done" / "failed" / "paused"
	LastReport string // most recent alert / completion text (truncated)
	StartedAt  time.Time
}
```

<details>
<summary><b>4 exported members</b></summary>


**`Name`** — field · `tui/capabilities.go:221`

```go
Name string
```

_No doc comment._


**`Status`** — field · `tui/capabilities.go:222`

```go
Status string
```

"running" / "done" / "failed" / "paused"


**`LastReport`** — field · `tui/capabilities.go:223`

```go
LastReport string
```

most recent alert / completion text (truncated)


**`StartedAt`** — field · `tui/capabilities.go:224`

```go
StartedAt time.Time
```

_No doc comment._


</details>

---

#### `SubagentLister`

`type` · `tui/capabilities.go:215` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

SubagentLister backs /subagents (R-SUB-1 read-only v1).


```go
type SubagentLister interface {
	Subagents() []SubagentInfo
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Subagents`** — method · `tui/capabilities.go:216`

```go
Subagents() []SubagentInfo
```

_No doc comment._


</details>

---

#### `SubagentNotFoundError`

`type` · `tui/capabilities.go:331` · contract closure: **outside** · core-agent: **7 refs (`cmd/core-agent/coretui_enabled.go`, `cmd/core-agent/coretui_subagent_events_test.go`, `internal/coretuiremote/subagent_events.go`, +1 more)**  
introduced in **v0.18.0** — [`eb881e5`](https://github.com/go-steer/core-tui/commit/eb881e52c608f7f456aee8a8aa151bae04d91f17), 2026-08-13, _feat(subagents): turn drill-down overlay + live inline tail (#70, #71) (#73)_

SubagentNotFoundError is what a SubagentEventReader returns for a
name it cannot resolve. Available carries the names that WOULD
resolve, so the UI can name them instead of leaving the operator to
guess at a spelling.

The distinction this type exists to preserve: "no such subagent" is
not "this subagent did nothing". A reader that flattens the two
into an empty page makes the TUI render a convincing empty turn log
for a typo — the exact failure go-steer/core-agent#694 fixed on the
server side.


```go
type SubagentNotFoundError struct {
	Name      string
	Available []string
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Name`** — field · `tui/capabilities.go:332`

```go
Name string
```

_No doc comment._


**`Available`** — field · `tui/capabilities.go:333`

```go
Available []string
```

_No doc comment._


**`Error`** — method · `tui/capabilities.go:336`

```go
func (e *SubagentNotFoundError) Error() string
```

_No doc comment._


</details>

---

#### `SubagentToolCall`

`type` · `tui/capabilities.go:307` · contract closure: **inside** · core-agent: **1 refs (`internal/coretuievent/coretuievent.go`)**  
introduced in **v0.18.0** — [`eb881e5`](https://github.com/go-steer/core-tui/commit/eb881e52c608f7f456aee8a8aa151bae04d91f17), 2026-08-13, _feat(subagents): turn drill-down overlay + live inline tail (#70, #71) (#73)_

SubagentToolCall is one tool invocation inside a subagent turn.


```go
type SubagentToolCall struct {
	ID   string
	Name string
	Args map[string]any
}
```

<details>
<summary><b>3 exported members</b></summary>


**`ID`** — field · `tui/capabilities.go:308`

```go
ID string
```

_No doc comment._


**`Name`** — field · `tui/capabilities.go:309`

```go
Name string
```

_No doc comment._


**`Args`** — field · `tui/capabilities.go:310`

```go
Args map[string]any
```

_No doc comment._


</details>

---

#### `SubagentToolResult`

`type` · `tui/capabilities.go:314` · contract closure: **inside** · core-agent: **1 refs (`internal/coretuievent/coretuievent.go`)**  
introduced in **v0.18.0** — [`eb881e5`](https://github.com/go-steer/core-tui/commit/eb881e52c608f7f456aee8a8aa151bae04d91f17), 2026-08-13, _feat(subagents): turn drill-down overlay + live inline tail (#70, #71) (#73)_

SubagentToolResult is one tool result inside a subagent turn.


```go
type SubagentToolResult struct {
	ID       string
	Name     string
	Response map[string]any
	Error    string
}
```

<details>
<summary><b>4 exported members</b></summary>


**`ID`** — field · `tui/capabilities.go:315`

```go
ID string
```

_No doc comment._


**`Name`** — field · `tui/capabilities.go:316`

```go
Name string
```

_No doc comment._


**`Response`** — field · `tui/capabilities.go:317`

```go
Response map[string]any
```

_No doc comment._


**`Error`** — field · `tui/capabilities.go:318`

```go
Error string
```

_No doc comment._


</details>

---

#### `ToolInfo`

`type` · `tui/capabilities.go:207` · contract closure: **inside** · core-agent: **7 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

ToolInfo is one entry in the /tools modal.


```go
type ToolInfo struct {
	Name        string
	Description string
	Source      string // "builtin" / "<mcp-server>" / "skill:<name>"
	GateState   string // "allowed" / "denied" / "ask" — current gate disposition
}
```

<details>
<summary><b>4 exported members</b></summary>


**`Name`** — field · `tui/capabilities.go:208`

```go
Name string
```

_No doc comment._


**`Description`** — field · `tui/capabilities.go:209`

```go
Description string
```

_No doc comment._


**`Source`** — field · `tui/capabilities.go:210`

```go
Source string
```

"builtin" / "<mcp-server>" / "skill:<name>"


**`GateState`** — field · `tui/capabilities.go:211`

```go
GateState string
```

"allowed" / "denied" / "ask" — current gate disposition


</details>

---

#### `ToolLister`

`type` · `tui/capabilities.go:202` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_

ToolLister backs /tools (R-CMD-1 table).


```go
type ToolLister interface {
	Tools() []ToolInfo
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Tools`** — method · `tui/capabilities.go:203`

```go
Tools() []ToolInfo
```

_No doc comment._


</details>

---

#### `UsageTracker`

`type` · `tui/capabilities.go:386` · contract closure: **inside** · core-agent: **12 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/adapter.go`, `internal/coretuiremote/capabilities.go`, +1 more)**  
introduced in **v0.1.0** — [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a), 2026-05-25, _feat(tui): land remaining §3.3 capability surface (PR 7)_<br>declaration changed since: **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3), _feat(tui-parity): tier 3 — polish (cursor + cwd + provider, ctrl-l/u, dynamic thinking, drill-in, /stats turns+duration, /memory bytes, perm echo, aliases, indent, elicit desc)_)

UsageTracker is the read-only side of the host's per-turn /
session usage accounting (R-USE-1 / R-USE-3). The TUI snapshots
values on each turn end to render the per-turn footer and the
/stats output.

Like StatusReporter, these accessors must be safe for concurrent
calls and should return cached values without blocking — the TUI
pulls them off its event loop to keep View() non-blocking.


```go
type UsageTracker interface {
	SessionTotals() Usage           // input + output tokens, cumulative
	SessionCostUSD() float64        // accumulated dollar spend
	LastTurn() (Usage, float64)     // most-recent turn's usage + cost
	ContextWindowSize() int         // 0 when unknown
	ContextWindowUsed() int         // 0 when unknown
	SessionTurns() int              // 0 when unknown
	SessionDuration() time.Duration // 0 when unknown
}
```

<details>
<summary><b>7 exported members</b></summary>


**`SessionTotals`** — method · `tui/capabilities.go:387`

```go
SessionTotals() Usage
```

input + output tokens, cumulative


**`SessionCostUSD`** — method · `tui/capabilities.go:388`

```go
SessionCostUSD() float64
```

accumulated dollar spend


**`LastTurn`** — method · `tui/capabilities.go:389`

```go
LastTurn() (Usage, float64)
```

most-recent turn's usage + cost


**`ContextWindowSize`** — method · `tui/capabilities.go:390`

```go
ContextWindowSize() int
```

0 when unknown


**`ContextWindowUsed`** — method · `tui/capabilities.go:391`

```go
ContextWindowUsed() int
```

0 when unknown


**`SessionTurns`** — method · `tui/capabilities.go:392` · added in **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3))

```go
SessionTurns() int
```

0 when unknown


**`SessionDuration`** — method · `tui/capabilities.go:393` · added in **v0.1.0** ([`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3))

```go
SessionDuration() time.Duration
```

0 when unknown


</details>

---

#### `AsyncSlashProvider`

`type` · `tui/slash.go:166` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.6.0** — [`d100d44`](https://github.com/go-steer/core-tui/commit/d100d44a4a3ec6e0fd5f98f67f7b6d89a0516782), 2026-05-27, _feat(responsiveness): async slash dispatch + quieter wake + longer queue TTL_

AsyncSlashProvider is the non-blocking variant of SlashProvider
(issue #10). Hosts whose slash commands do network or file I/O
implement this so the dispatch runs off the Update goroutine
and the TUI stays responsive — every keystroke, render tick,
and toast continues processing while the host's call is in
flight.

Implementation contract:
  - InvokeSlashAsync returns a receive-only channel; core-tui
    reads exactly one value and closes its tea.Cmd. Hosts must
    send exactly one SlashResultOrErr and then close (or just
    send + abandon — core-tui doesn't re-read).
  - The supplied ctx is cancellable; when the operator hits
    Ctrl+C / Esc, core-tui cancels it and the host should bail
    as fast as the underlying work allows. The eventual sent
    value is discarded.
  - A host satisfying BOTH SlashProvider and AsyncSlashProvider
    prefers the async path. Built-in slash commands are not
    routed here — they're synchronous-and-fast by design.


```go
type AsyncSlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlashAsync(ctx context.Context, name, args string) <-chan SlashResultOrErr
}
```

<details>
<summary><b>2 exported members</b></summary>


**`SlashCommands`** — method · `tui/slash.go:167`

```go
SlashCommands() []SlashCommandSpec
```

_No doc comment._


**`InvokeSlashAsync`** — method · `tui/slash.go:168`

```go
InvokeSlashAsync(ctx context.Context, name, args string) <-chan SlashResultOrErr
```

_No doc comment._


</details>

---

#### `AsyncSlashProviderWithPreamble`

`type` · `tui/slash.go:208` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.6.3** — [`5992d79`](https://github.com/go-steer/core-tui/commit/5992d796492b5cad060ba10821f26eccd115b27b), 2026-05-28, _feat(slash): AsyncSlashProviderWithPreamble — chat-visible dispatch row (#16)_

AsyncSlashProviderWithPreamble is the variant of AsyncSlashProvider
for slashes whose work takes long enough that the operator wants a
chat-visible "this is running" row at dispatch time (issue #16).
The bottom-bar toast that AsyncSlashProvider relies on is easy to
miss on a 5–15s call (/done writing a checkpoint, /compact writing
a summary); the preamble lands directly in the chat flow so the
operator's eye picks it up next to the prompt they just typed.

Contract:
  - InvokeSlashAsync returns (preamble, results). The preamble is
    computed synchronously and appended to history as a RoleSystem
    row BEFORE the goroutine that drains `results` is launched.
    Empty preamble is the "no preamble" signal — the row is
    skipped and behavior matches the bare AsyncSlashProvider.
  - results follows the same single-shot contract as
    AsyncSlashProvider.InvokeSlashAsync: send exactly one
    SlashResultOrErr and close (or just send + abandon).
  - ctx is cancellable, same semantics as AsyncSlashProvider:
    core-tui cancels it on Esc; hosts honoring ctx bail.
  - A host satisfying BOTH AsyncSlashProvider and
    AsyncSlashProviderWithPreamble prefers the preamble variant.
    A host satisfying only the preamble variant works fine; one
    satisfying only the bare variant also works fine. Both can
    coexist in the same host on different commands.

Method name matches AsyncSlashProvider's `InvokeSlashAsync` but
the return signature differs, so a single Go type can satisfy
only one of the two — pick the variant that fits per-host. The
dispatch path type-asserts the preamble variant first.


```go
type AsyncSlashProviderWithPreamble interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlashAsync(ctx context.Context, name, args string) (preamble string, results <-chan SlashResultOrErr)
}
```

<details>
<summary><b>2 exported members</b></summary>


**`SlashCommands`** — method · `tui/slash.go:209`

```go
SlashCommands() []SlashCommandSpec
```

_No doc comment._


**`InvokeSlashAsync`** — method · `tui/slash.go:210`

```go
InvokeSlashAsync(ctx context.Context, name, args string) (preamble string, results <-chan SlashResultOrErr)
```

_No doc comment._


</details>

---

#### `SideAnswer`

`type` · `tui/slash.go:141` · contract closure: **inside** · core-agent: **4 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`75b7faa`](https://github.com/go-steer/core-tui/commit/75b7faae5021e9cb562cf55c8ff8a092dfaab37d), 2026-05-25, _feat(tui): SlashResult.ModalAnswer + side-answer modal (R-CMD-5, PR 1/5)_

SideAnswer carries the operator's question + the agent's response
for modal-style rendering. Used for /btw and similar side-channel
Q&A flows that should display once and disappear (not lodge in
chat history). When Err is non-nil the modal renders an error
state instead of the Glamour-rendered answer body.

See R-CMD-5 in requirements.md.


```go
type SideAnswer struct {
	Question string
	Answer   string
	Err      error
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Question`** — field · `tui/slash.go:142`

```go
Question string
```

_No doc comment._


**`Answer`** — field · `tui/slash.go:143`

```go
Answer string
```

_No doc comment._


**`Err`** — field · `tui/slash.go:144`

```go
Err error
```

_No doc comment._


</details>

---

#### `SlashCommandSpec`

`type` · `tui/slash.go:39` · contract closure: **inside** · core-agent: **10 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`75b7faa`](https://github.com/go-steer/core-tui/commit/75b7faae5021e9cb562cf55c8ff8a092dfaab37d), 2026-05-25, _feat(tui): SlashResult.ModalAnswer + side-answer modal (R-CMD-5, PR 1/5)_

SlashCommandSpec is one entry in the agent's command catalog.
Name is the bare identifier (no leading "/"). Aliases are
alternative invocations (e.g. {"by-the-way"} for /btw). Description
renders in /help and as the dim subtitle in the palette.


```go
type SlashCommandSpec struct {
	Name        string
	Aliases     []string
	Description string
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Name`** — field · `tui/slash.go:40`

```go
Name string
```

_No doc comment._


**`Aliases`** — field · `tui/slash.go:41`

```go
Aliases []string
```

_No doc comment._


**`Description`** — field · `tui/slash.go:42`

```go
Description string
```

_No doc comment._


</details>

---

#### `SlashProvider`

`type` · `tui/slash.go:30` · contract closure: **inside** · core-agent: **4 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`75b7faa`](https://github.com/go-steer/core-tui/commit/75b7faae5021e9cb562cf55c8ff8a092dfaab37d), 2026-05-25, _feat(tui): SlashResult.ModalAnswer + side-answer modal (R-CMD-5, PR 1/5)_

SlashProvider is an optional Agent capability: hosts that implement
it on their Agent type can advertise additional slash commands the
TUI merges into /help and the palette. Invocations dispatch back
via InvokeSlash. Built-in command names always win on collision; a
system warning is logged at startup when the agent's spec list
shadows a built-in.

See R-CMD-4 in requirements.md and design.md §3.3.


```go
type SlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlash(ctx context.Context, name, args string) (SlashResult, error)
}
```

<details>
<summary><b>2 exported members</b></summary>


**`SlashCommands`** — method · `tui/slash.go:31`

```go
SlashCommands() []SlashCommandSpec
```

_No doc comment._


**`InvokeSlash`** — method · `tui/slash.go:32`

```go
InvokeSlash(ctx context.Context, name, args string) (SlashResult, error)
```

_No doc comment._


</details>

---

#### `SlashResult`

`type` · `tui/slash.go:65` · contract closure: **inside** · core-agent: **77 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.1.0** — [`75b7faa`](https://github.com/go-steer/core-tui/commit/75b7faae5021e9cb562cf55c8ff8a092dfaab37d), 2026-05-25, _feat(tui): SlashResult.ModalAnswer + side-answer modal (R-CMD-5, PR 1/5)_<br>declaration changed since: **v0.10.0** ([`b08dac6`](https://github.com/go-steer/core-tui/commit/b08dac6868e2fe588014d4d0c12fadc5aba908f1), _feat(switch): mid-session Agent swap via SlashResult.SwitchTo + /switch built-in (#54)_)

SlashResult is what InvokeSlash returns. Any subset of the fields
may be populated:

  - SystemMessage — a one-line confirmation that renders as a dim
    italic system row in the chat history.
  - ModalAnswer — a richer Q+A overlay rendered as a dismissable
    Glamour-formatted modal. Used by /btw-style side questions
    whose answer shouldn't pollute the persistent chat history.
  - SwitchTo — instructs the TUI to detach from the current Agent
    and reattach to the supplied one (issue #48). Non-nil triggers
    a mid-run session swap through applySwitchTarget. Any
    SystemMessage / ModalAnswer set on the same result is applied
    against the OUTGOING session's chat (i.e. before the wipe)
    unless the host prefers to surface post-switch context via
    SwitchTarget.Note.

When SwitchTo is nil and both SystemMessage / ModalAnswer are
empty, the call ran but had nothing visible to say. When
SystemMessage + ModalAnswer are both set, the modal renders first
and the system message lands behind it.


```go
type SlashResult struct {
	SystemMessage string
	ModalAnswer   *SideAnswer
	SwitchTo      *SwitchTarget
}
```

<details>
<summary><b>3 exported members</b></summary>


**`SystemMessage`** — field · `tui/slash.go:66`

```go
SystemMessage string
```

_No doc comment._


**`ModalAnswer`** — field · `tui/slash.go:67`

```go
ModalAnswer *SideAnswer
```

_No doc comment._


**`SwitchTo`** — field · `tui/slash.go:68` · added in **v0.10.0** ([`b08dac6`](https://github.com/go-steer/core-tui/commit/b08dac6868e2fe588014d4d0c12fadc5aba908f1))

```go
SwitchTo *SwitchTarget
```

_No doc comment._


</details>

---

#### `SlashResultOrErr`

`type` · `tui/slash.go:174` · contract closure: **inside** · core-agent: **7 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/capabilities.go`)**  
introduced in **v0.6.0** — [`d100d44`](https://github.com/go-steer/core-tui/commit/d100d44a4a3ec6e0fd5f98f67f7b6d89a0516782), 2026-05-27, _feat(responsiveness): async slash dispatch + quieter wake + longer queue TTL_

SlashResultOrErr bundles the SlashResult + error pair that
InvokeSlashAsync's channel carries. Exactly one of Res / Err is
meaningful per send.


```go
type SlashResultOrErr struct {
	Res SlashResult
	Err error
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Res`** — field · `tui/slash.go:175`

```go
Res SlashResult
```

_No doc comment._


**`Err`** — field · `tui/slash.go:176`

```go
Err error
```

_No doc comment._


</details>

---

#### `SwitchTarget`

`type` · `tui/slash.go:98` · contract closure: **inside** · core-agent: **12 refs (`internal/coretuiremote/capabilities.go`)**  
introduced in **v0.10.0** — [`b08dac6`](https://github.com/go-steer/core-tui/commit/b08dac6868e2fe588014d4d0c12fadc5aba908f1), 2026-07-14, _feat(switch): mid-session Agent swap via SlashResult.SwitchTo + /switch built-in (#54)_

SwitchTarget instructs the TUI to detach the current Agent's
local subscriptions and attach to Agent (issue #48). Fields other
than Agent are optional — non-nil / non-empty values REPLACE the
corresponding Options field, nil / zero values leave the existing
value in place (Memory / Skills / MCPServers: nil = keep, non-nil
including empty = replace with the supplied slice).

Lifecycle contract:

  - Agent is required. SwitchTo with a nil Agent is rejected with a
    RoleError row; the current session stays attached.
  - Core-tui does NOT close, Detach, or otherwise touch the
    OUTGOING Agent. The host owns its lifecycle — if teardown is
    needed (closing a socket, releasing a bearer token) do it
    inside SwitchToSession() before returning the new
    SwitchTarget, or from the host's own slash handler before
    returning the SlashResult.
  - Core-tui cancels the LOCAL contexts it owns (streaming turn,
    async slash, LiveAgent Events). Server-side sessions are
    unaffected — a remote daemon observes a dropped reader and
    keeps the session ticking per its own reattach policy. This
    is a detach, NOT a kill.
  - History wipes; the new session paints on a blank canvas.
  - Chrome (theme, terminal size, permission mode, overlay stack
    minus any open session picker) survives.

See design.md §3.3 and issues #48 / #53.


```go
type SwitchTarget struct {
	// ... 10 exported members, documented below
}
```

<details>
<summary><b>10 exported members</b></summary>


**`Agent`** — field · `tui/slash.go:100`

```go
Agent Agent
```

Agent is the incoming Agent. Required.


**`UsageTracker`** — field · `tui/slash.go:105`

```go
UsageTracker UsageTracker
```

UsageTracker replaces Options.UsageTracker when non-nil.
Typical: a fresh per-session tracker so /stats and the
status header reflect the new session's totals.


**`Prompter`** — field · `tui/slash.go:112`

```go
Prompter PermissionPrompter
```

Prompter / Elicitor / Notifier replace the corresponding
Options fields when non-nil. Nil = keep the existing
subscriber so cross-session permission / elicit / notice
pipes keep working. Hosts that want to fully sever those
channels supply fresh instances here.


**`Elicitor`** — field · `tui/slash.go:113`

```go
Elicitor Elicitor
```

_No doc comment._


**`Notifier`** — field · `tui/slash.go:114`

```go
Notifier *Notifier
```

_No doc comment._


**`Memory`** — field · `tui/slash.go:119`

```go
Memory []MemoryFile
```

Memory / Skills / MCPServers replace the corresponding
Options fields when non-nil (nil-vs-empty matters: an empty
non-nil slice CLEARS the display).


**`Skills`** — field · `tui/slash.go:120`

```go
Skills []SkillInfo
```

_No doc comment._


**`MCPServers`** — field · `tui/slash.go:121`

```go
MCPServers []MCPServerInfo
```

_No doc comment._


**`Branding`** — field · `tui/slash.go:126`

```go
Branding *Branding
```

Branding replaces Options.Branding wholesale when non-nil.
Nil = keep existing (common; the chrome is per-operator,
not per-session).


**`Note`** — field · `tui/slash.go:131`

```go
Note string
```

Note is appended as a RoleSystem row after the switch
completes so the operator sees which session they landed
on (e.g. "Attached to session <sid>"). Empty = no row.


</details>

---

### 8.4 Permission, elicitation, notification

_Files: `tui/elicitor.go`, `tui/notifier.go`, `tui/prompter.go`_

#### `ElicitAction`

`type` · `tui/elicitor.go:103` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

ElicitAction is the operator's top-level decision.


```go
type ElicitAction int
```

---

#### `ElicitField`

`type` · `tui/elicitor.go:59` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

ElicitField describes one field in a form-mode elicit request.
Adapters translate their MCP server's schema (JSON Schema or
similar) into a slice of these.


```go
type ElicitField struct {
	Name        string
	Description string
	Type        ElicitFieldType

	// EnumChoices populated when Type == ElicitFieldEnum. The form
	// renders these as a Select picker.
	EnumChoices []string

	// Required is honored by the modal's submit-time validation:
	// empty values for required fields block submission.
	Required bool

	// Default seeds the field value at modal open. Type-coerced to
	// the field's Type by the renderer; passing a string here is
	// always safe.
	Default any
}
```

<details>
<summary><b>6 exported members</b></summary>


**`Name`** — field · `tui/elicitor.go:60`

```go
Name string
```

_No doc comment._


**`Description`** — field · `tui/elicitor.go:61`

```go
Description string
```

_No doc comment._


**`Type`** — field · `tui/elicitor.go:62`

```go
Type ElicitFieldType
```

_No doc comment._


**`EnumChoices`** — field · `tui/elicitor.go:66`

```go
EnumChoices []string
```

EnumChoices populated when Type == ElicitFieldEnum. The form
renders these as a Select picker.


**`Required`** — field · `tui/elicitor.go:70`

```go
Required bool
```

Required is honored by the modal's submit-time validation:
empty values for required fields block submission.


**`Default`** — field · `tui/elicitor.go:75`

```go
Default any
```

Default seeds the field value at modal open. Type-coerced to
the field's Type by the renderer; passing a string here is
always safe.


</details>

---

#### `ElicitFieldType`

`type` · `tui/elicitor.go:46` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

ElicitFieldType is the primitive type for one form field.


```go
type ElicitFieldType int
```

---

#### `ElicitMode`

`type` · `tui/elicitor.go:38` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

ElicitMode picks between the two modal shapes the TUI supports
(R-ELIC-1). FormMode renders one field per Schema property;
URLMode renders an open / accept / decline action row for a
URL-typed request.


```go
type ElicitMode int
```

---

#### `ElicitRequest`

`type` · `tui/elicitor.go:80` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

ElicitRequest carries everything the modal needs. Mode picks the
rendering shape; the other fields populate per mode.


```go
type ElicitRequest struct {
	Mode ElicitMode

	// Title shown in the modal header. Server name renders alongside.
	Title       string
	Description string // optional dim subtitle

	// Form-mode fields (Mode == ElicitFormMode).
	Fields []ElicitField

	// URL-mode payload (Mode == ElicitURLMode).
	URL string
}
```

<details>
<summary><b>5 exported members</b></summary>


**`Mode`** — field · `tui/elicitor.go:81`

```go
Mode ElicitMode
```

_No doc comment._


**`Title`** — field · `tui/elicitor.go:84`

```go
Title string
```

Title shown in the modal header. Server name renders alongside.


**`Description`** — field · `tui/elicitor.go:85`

```go
Description string
```

optional dim subtitle


**`Fields`** — field · `tui/elicitor.go:88`

```go
Fields []ElicitField
```

Form-mode fields (Mode == ElicitFormMode).


**`URL`** — field · `tui/elicitor.go:91`

```go
URL string
```

URL-mode payload (Mode == ElicitURLMode).


</details>

---

#### `ElicitResult`

`type` · `tui/elicitor.go:97` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

ElicitResult is what the operator's choice produces. Action picks
between submit / decline / cancel; Values carries the form field
answers when Action == ElicitActionSubmit.


```go
type ElicitResult struct {
	Action ElicitAction
	Values map[string]any
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Action`** — field · `tui/elicitor.go:98`

```go
Action ElicitAction
```

_No doc comment._


**`Values`** — field · `tui/elicitor.go:99`

```go
Values map[string]any
```

_No doc comment._


</details>

---

#### `Elicitor`

`type` · `tui/elicitor.go:30` · contract closure: **inside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

Elicitor is the interface the TUI implements and the host wires
into each MCP server's elicit hook. MCP servers call Elicit when
they need structured operator input mid-tool-call; the call
blocks on the TUI's modal until the operator submits, declines,
or cancels (ctx done).

See R-ELIC-1 / R-ELIC-2 / R-ELIC-3 in requirements.md and
design.md §3.5.


```go
type Elicitor interface {
	Elicit(ctx context.Context, serverName string, req ElicitRequest) (ElicitResult, error)
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Elicit`** — method · `tui/elicitor.go:31`

```go
Elicit(ctx context.Context, serverName string, req ElicitRequest) (ElicitResult, error)
```

_No doc comment._


</details>

---

#### `NewElicitor`

`func` · `tui/elicitor.go:144` · block `Elicitor` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `cmd/core-agent/tui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

NewElicitor constructs an Elicitor ready to be wired into each
MCP server's elicit callback + the TUI's Options. Returns the
interface so callers can swap impls in tests without referring
to the unexported concrete type.


```go
func NewElicitor() Elicitor
```

---

#### `ElicitActionCancel`

`const` · `tui/elicitor.go:108` · block `ElicitActionSubmit` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

both: Esc


```go
ElicitActionCancel // both: Esc
```

---

#### `ElicitActionDecline`

`const` · `tui/elicitor.go:107` · block `ElicitActionSubmit` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

form: n; url: n


```go
ElicitActionDecline // form: n; url: n
```

---

#### `ElicitActionSubmit`

`const` · `tui/elicitor.go:106` · block `ElicitActionSubmit` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

form: Enter; url: a/Enter


```go
ElicitActionSubmit ElicitAction = iota // form: Enter; url: a/Enter
```

---

#### `ElicitFieldBoolean`

`const` · `tui/elicitor.go:52` · block `ElicitFieldString` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
ElicitFieldBoolean
```

---

#### `ElicitFieldEnum`

`const` · `tui/elicitor.go:53` · block `ElicitFieldString` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
ElicitFieldEnum
```

---

#### `ElicitFieldInteger`

`const` · `tui/elicitor.go:51` · block `ElicitFieldString` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
ElicitFieldInteger
```

---

#### `ElicitFieldNumber`

`const` · `tui/elicitor.go:50` · block `ElicitFieldString` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
ElicitFieldNumber
```

---

#### `ElicitFieldString`

`const` · `tui/elicitor.go:49` · block `ElicitFieldString` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
ElicitFieldString ElicitFieldType = iota
```

---

#### `ElicitFormMode`

`const` · `tui/elicitor.go:41` · block `ElicitFormMode` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
ElicitFormMode ElicitMode = iota
```

---

#### `ElicitURLMode`

`const` · `tui/elicitor.go:42` · block `ElicitFormMode` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
ElicitURLMode
```

---

#### `Notifier`

`type` · `tui/notifier.go:60` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.8.0** — [`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b), 2026-06-07, _feat(notifier): out-of-band Notify() side channel for host-initiated chat rows (#30)_

Notifier is the host-facing handle for pushing notices into a
running TUI. Construct via NewNotifier; pass on
Options.Notifier; call Notify from any goroutine. Safe for
concurrent use.


```go
type Notifier struct {
	// contains filtered or unexported fields
}
```

<details>
<summary><b>1 exported members</b></summary>


**`Notify`** — method · `tui/notifier.go:91`

```go
func (n *Notifier) Notify(text string)
```

Notify pushes a chat-row notice to the TUI. Safe to call from
any goroutine. Non-blocking: when the in-flight buffer is full
(notifyBufferSize notices already queued), the call increments
a dropped counter and returns immediately; the next successful
enqueue carries the coalesced count so the operator sees
`(+N dropped)` appended to the rendered text.

Empty text is silently ignored — there's nothing to display.

Calls after the TUI has exited are silently dropped (the
Notifier's channel is closed and a guard rejects further
sends rather than panicking). Hosts don't need to track TUI
lifecycle.


</details>

---

#### `NewNotifier`

`func` · `tui/notifier.go:74` · block `Notifier` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.8.0** — [`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b), 2026-06-07, _feat(notifier): out-of-band Notify() side channel for host-initiated chat rows (#30)_

NewNotifier constructs a Notifier ready to be wired into
Options.Notifier. Returns the concrete type so hosts can
retain a typed handle for Notify calls.


```go
func NewNotifier() *Notifier
```

---

#### `DetailKind`

`type` · `tui/prompter.go:56` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

DetailKind picks the Glamour code-fence language tag the modal
uses when rendering Detail (R-PERM-1). DetailPlain renders
without syntax highlighting.


```go
type DetailKind int
```

---

#### `PermissionDecision`

`type` · `tui/prompter.go:100` · contract closure: **inside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionDecision is the operator's choice. Six values per
R-PERM-2 — every key the modal accepts maps to one of these.


```go
type PermissionDecision int
```

---

#### `PermissionKind`

`type` · `tui/prompter.go:36` · contract closure: **inside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionKind tags the request's source so the modal can pick
the right phrasing + scope-key glue (e.g. "bash command" vs
"file edit" vs "url fetch").


```go
type PermissionKind int
```

---

#### `PermissionPrompter`

`type` · `tui/prompter.go:29` · contract closure: **inside** · core-agent: **3 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionPrompter is the interface the TUI implements and the
host wires into its permission gate. The host's gate calls
AskApproval whenever a tool invocation needs explicit operator
approval; the call blocks on the TUI's modal until the operator
chooses a decision (or until ctx is cancelled).

See R-PERM-1 / R-PERM-2 in requirements.md and design.md §3.5.


```go
type PermissionPrompter interface {
	AskApproval(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
}
```

<details>
<summary><b>1 exported members</b></summary>


**`AskApproval`** — method · `tui/prompter.go:30`

```go
AskApproval(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
```

_No doc comment._


</details>

---

#### `PermissionRequest`

`type` · `tui/prompter.go:69` · contract closure: **inside** · core-agent: **4 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`, `pkg/attach/handlers_prompts.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionRequest carries everything the modal needs to render
the approval prompt. Hosts populate the fields they have; the
modal renders only what's set.


```go
type PermissionRequest struct {
	Kind     PermissionKind
	ToolName string

	// Detail is the rendered payload the operator is being asked to
	// approve (R-PERM-1). For file edits: a unified diff. For shell:
	// the verbatim command. For HTTP: URL + method + body summary.
	// For other tools: a key=value or JSON dump.
	Detail     string
	DetailKind DetailKind

	// Verb is the action extracted from the payload (e.g. "rm" from
	// "rm -rf /tmp/foo"). Empty when no verb is meaningful — the
	// modal suppresses the verb-scoped decision (R-PERM-2 "v") when
	// Verb is empty.
	Verb string

	// Source is the sub-agent name when the request originated from
	// a background agent (R-PERM-1 "originating sub-agent"). Empty
	// for the foreground agent.
	Source string

	// PersistTool / PersistKey are the host's persistence-key hint.
	// Round-tripped back via the AlwaysAllow callback (R-PERM-3) so
	// the host knows what scope to write to disk.
	PersistTool string
	PersistKey  string
}
```

<details>
<summary><b>8 exported members</b></summary>


**`Kind`** — field · `tui/prompter.go:70`

```go
Kind PermissionKind
```

_No doc comment._


**`ToolName`** — field · `tui/prompter.go:71`

```go
ToolName string
```

_No doc comment._


**`Detail`** — field · `tui/prompter.go:77`

```go
Detail string
```

Detail is the rendered payload the operator is being asked to
approve (R-PERM-1). For file edits: a unified diff. For shell:
the verbatim command. For HTTP: URL + method + body summary.
For other tools: a key=value or JSON dump.


**`DetailKind`** — field · `tui/prompter.go:78`

```go
DetailKind DetailKind
```

_No doc comment._


**`Verb`** — field · `tui/prompter.go:84`

```go
Verb string
```

Verb is the action extracted from the payload (e.g. "rm" from
"rm -rf /tmp/foo"). Empty when no verb is meaningful — the
modal suppresses the verb-scoped decision (R-PERM-2 "v") when
Verb is empty.


**`Source`** — field · `tui/prompter.go:89`

```go
Source string
```

Source is the sub-agent name when the request originated from
a background agent (R-PERM-1 "originating sub-agent"). Empty
for the foreground agent.


**`PersistTool`** — field · `tui/prompter.go:94`

```go
PersistTool string
```

PersistTool / PersistKey are the host's persistence-key hint.
Round-tripped back via the AlwaysAllow callback (R-PERM-3) so
the host knows what scope to write to disk.


**`PersistKey`** — field · `tui/prompter.go:95`

```go
PersistKey string
```

_No doc comment._


</details>

---

#### `Prompter`

`type` · `tui/prompter.go:140` · contract closure: **inside** · core-agent: **3 refs (`internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

Prompter is the TUI-side PermissionPrompter implementation. The
host obtains one via tui.NewPrompter() and wires it into its
permission gate. The Bubble Tea loop drains the request channel
via a listener Cmd; each request becomes a permissionRequestMsg
that Update routes to the permission modal renderer.

Concurrency model: AskApproval pushes a permissionFlow onto the
requests channel (buffered 1) and blocks on the per-flow
response channel. When the operator picks a decision, Update
sends the response and the AskApproval call unblocks. If the
caller's context cancels first, AskApproval returns the ctx
error and starts a background drainer on the response channel
so the eventual write (if Update hasn't seen the request yet,
or has dispatched but not yet sent) doesn't leak the goroutine.


```go
type Prompter struct {
	// contains filtered or unexported fields
}
```

<details>
<summary><b>1 exported members</b></summary>


**`AskApproval`** — method · `tui/prompter.go:159`

```go
func (p *Prompter) AskApproval(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
```

AskApproval blocks until the operator picks a decision via the
modal, or until ctx cancels. Implements PermissionPrompter.


</details>

---

#### `NewPrompter`

`func` · `tui/prompter.go:151` · block `Prompter` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

NewPrompter constructs a Prompter ready to be wired into the
host's permission gate and the TUI's Options. Returns a pointer
so the same instance can be shared between the gate callsite
and the TUI's Init.


```go
func NewPrompter() *Prompter
```

---

#### `DecisionAllowAlways`

`const` · `tui/prompter.go:108` · block `DecisionDeny` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

a — host persists via callback


```go
DecisionAllowAlways // a — host persists via callback
```

---

#### `DecisionAllowOnce`

`const` · `tui/prompter.go:104` · block `DecisionDeny` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

y


```go
DecisionAllowOnce // y
```

---

#### `DecisionAllowSession`

`const` · `tui/prompter.go:105` · block `DecisionDeny` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

s


```go
DecisionAllowSession // s
```

---

#### `DecisionAllowSessionTool`

`const` · `tui/prompter.go:107` · block `DecisionDeny` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

t


```go
DecisionAllowSessionTool // t
```

---

#### `DecisionAllowSessionVerb`

`const` · `tui/prompter.go:106` · block `DecisionDeny` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

v (when Verb is non-empty)


```go
DecisionAllowSessionVerb // v (when Verb is non-empty)
```

---

#### `DecisionDeny`

`const` · `tui/prompter.go:103` · block `DecisionDeny` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

n / esc


```go
DecisionDeny PermissionDecision = iota // n / esc
```

---

#### `DetailArgs`

`const` · `tui/prompter.go:63` · block `DetailPlain` · contract closure: **inside** · core-agent: **2 refs (`internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

JSON or key=value tool args


```go
DetailArgs // JSON or key=value tool args
```

---

#### `DetailDiff`

`const` · `tui/prompter.go:60` · block `DetailPlain` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

unified diff (red/green hunks)


```go
DetailDiff // unified diff (red/green hunks)
```

---

#### `DetailHTTP`

`const` · `tui/prompter.go:62` · block `DetailPlain` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

URL + method + body


```go
DetailHTTP // URL + method + body
```

---

#### `DetailPlain`

`const` · `tui/prompter.go:59` · block `DetailPlain` · contract closure: **inside** · core-agent: **1 refs (`cmd/core-agent/coretui_enabled.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

_No doc comment._


```go
DetailPlain DetailKind = iota
```

---

#### `DetailShell`

`const` · `tui/prompter.go:61` · block `DetailPlain` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

bash / sh command line


```go
DetailShell // bash / sh command line
```

---

#### `PermissionKindBash`

`const` · `tui/prompter.go:41` · block `PermissionKindBash` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionKindBash is a shell-command tool call (R-PERM-1's
"show the verbatim command" rule applies).


```go
// PermissionKindBash is a shell-command tool call (R-PERM-1's
// "show the verbatim command" rule applies).
PermissionKindBash PermissionKind = iota
```

---

#### `PermissionKindEdit`

`const` · `tui/prompter.go:44` · block `PermissionKindBash` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionKindEdit is a file-edit tool call (Detail should be
the rendered diff).


```go
// PermissionKindEdit is a file-edit tool call (Detail should be
// the rendered diff).
PermissionKindEdit
```

---

#### `PermissionKindHTTP`

`const` · `tui/prompter.go:47` · block `PermissionKindBash` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionKindHTTP is a network-fetch tool call (Detail
should be URL + method + body summary).


```go
// PermissionKindHTTP is a network-fetch tool call (Detail
// should be URL + method + body summary).
PermissionKindHTTP
```

---

#### `PermissionKindOther`

`const` · `tui/prompter.go:50` · block `PermissionKindBash` · contract closure: **inside** · core-agent: **2 refs (`cmd/core-agent/coretui_enabled.go`, `internal/coretuiremote/prompter.go`)**  
introduced in **v0.1.0** — [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda), 2026-05-25, _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_

PermissionKindOther is the catch-all — any tool call that
doesn't fit one of the above gets generic args rendering.


```go
// PermissionKindOther is the catch-all — any tool call that
// doesn't fit one of the above gets generic args rendering.
PermissionKindOther
```

---

### 8.5 Chat data model

_Files: `tui/history.go`, `tui/messages.go`, `tui/queue.go`, `tui/tool_savings.go`_

#### `History`

`type` · `tui/history.go:149` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

History is the in-memory transcript backing the viewport.


```go
type History struct {
	// contains filtered or unexported fields
}
```

<details>
<summary><b>12 exported members</b></summary>


**`Append`** — method · `tui/history.go:157`

```go
func (h *History) Append(m Message)
```

Append adds an entry to the end. Assigns a fresh Message.ID
(preserved across SetRendered mutations) so the lazy-render
cache can key entries stably.


**`BumpVersion`** — method · `tui/history.go:204` · added in **v0.1.0** ([`4bfe01b`](https://github.com/go-steer/core-tui/commit/4bfe01bb1261fbc0e3244d8256ba2cf9b1124978))

```go
func (h *History) BumpVersion(id uint64)
```

BumpVersion finds the entry with the given ID and bumps its
Version so the lazy-render cache invalidates the row. Used to
signal "active tool" transitions (active → done) without
touching the Message's content. No-op when id == 0 or no
matching entry.


**`FindByToolCallID`** — method · `tui/history.go:220` · added in **v0.3.0** ([`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc))

```go
func (h *History) FindByToolCallID(callID string) int
```

FindByToolCallID locates the RoleTool entry whose wire-level
ToolCallID matches the given id and returns its slice index, or
-1 when no match exists. Used by applyToolResult to attach a
freshly-arrived result to the correct row.


**`LastID`** — method · `tui/history.go:192` · added in **v0.1.0** ([`4bfe01b`](https://github.com/go-steer/core-tui/commit/4bfe01bb1261fbc0e3244d8256ba2cf9b1124978))

```go
func (h *History) LastID() uint64
```

LastID returns the Message.ID of the most-recent entry, or 0
when the history is empty. Used by the tool-call lifecycle to
stash the active tool's identity right after Append.


**`Len`** — method · `tui/history.go:280`

```go
func (h *History) Len() int
```

Len returns the entry count.


**`MarkLastUserAutoContinue`** — method · `tui/history.go:269` · added in **v0.6.0** ([`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f))

```go
func (h *History) MarkLastUserAutoContinue()
```

MarkLastUserAutoContinue flips Message.AutoContinue=true on the
most-recently-appended RoleUser entry (and bumps its Version so
the lazy-render cache invalidates). Used by the AutoContinueFromInbox
loop (issue #9): submitTurn appends the RoleUser as an operator-
typed prompt; this helper retro-fits the synthesized marker so
the renderer picks the ↻ glyph + muted style on the next paint.
No-op when there's no RoleUser entry in history.


**`Reset`** — method · `tui/history.go:172`

```go
func (h *History) Reset()
```

Reset empties the history. Used by /clear.


**`SetRendered`** — method · `tui/history.go:181` · added in **v0.1.0** ([`53ba0d6`](https://github.com/go-steer/core-tui/commit/53ba0d61537847eecba8c13a242e2790ee954944))

```go
func (h *History) SetRendered(i int, rendered string)
```

SetRendered overwrites the cached Glamour render on entry i and
bumps the entry's Version so the lazy-render cache invalidates.
Used by the resize path to refresh wrapping at the new width.
Out-of-range i is a silent no-op so callers can pass the
snapshot index without bounds-checking.


**`SetToolPreview`** — method · `tui/history.go:236` · added in **v0.3.0** ([`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc))

```go
func (h *History) SetToolPreview(i int, preview string)
```

SetToolPreview overwrites the cached tool preview on entry i and
bumps the entry's Version so the lazy-render cache invalidates.
Used by applyToolResult to swap the call-only preview for the
call+result preview. Out-of-range i is a silent no-op.


**`SetToolResult`** — method · `tui/history.go:251` · added in **v0.12.0** ([`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc)) · changed in **v0.12.0**, **v0.14.0**

```go
func (h *History) SetToolResult(i int, response map[string]any, errStr string, latencyMs int64, savings *ToolSavings)
```

SetToolResult stashes the raw response payload + error string +
per-call latency + digest savings on the tool row at index i so
the expand-single detail overlay (dialog_toolcall.go) can re-render
the full args + response later on demand, and the badges / chips
surface the wall-clock timing and digest reduction. Bumps Version
so any renderer that consults these fields invalidates its cache.
Out-of-range i is a silent no-op.


**`Snapshot`** — method · `tui/history.go:165`

```go
func (h *History) Snapshot() []Message
```

Snapshot returns a copy of every entry, in order.


**`StampLatestAssistantFooter`** — method · `tui/history.go:302` · added in **v0.10.2** ([`f1162dd`](https://github.com/go-steer/core-tui/commit/f1162ddd0176ad293c45fb02fe39de2fbafd9a13))

```go
func (h *History) StampLatestAssistantFooter(model string, usage *Usage, cost float64, elapsed time.Duration) bool
```

StampLatestAssistantFooter walks backward for the most-recent
RoleAssistant entry and fills in per-turn footer fields
(Model/Usage/CostUSD/Elapsed) that are currently unset. Bumps the
entry's Version so the lazy-render cache re-renders the row with
the footer. Missing / zero args leave the corresponding field
alone; each field is stamped independently so the same helper can
be called from multiple back-annotation sites (turnSummaryMsg
carries tokens+model+latency; usageUpdateMsg carries authoritative
cost via LastTurn).

Only-stamp-if-currently-unset semantics protect against clobbering
finalizeTurn's canonical stamp when both paths fire (they don't
today — LiveAgent has no turnDoneMsg — but the guard is cheap
insurance for future modes). Returns true iff the row was found
and any field was updated; false when the tail isn't an assistant
(e.g. right after a tool row) or when everything was already set.

Issue #57 (observer-mode footer) — the primary caller.


</details>

---

#### `Message`

`type` · `tui/history.go:41` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_<br>declaration changed since: **v0.1.0** ([`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2), _fix(tui): focus + wrap + tool-gear + per-turn usage + sidebar fallback_); **v0.1.0** ([`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27), _feat(tui-facelift): tier A — lazy list with Versioned items + per-item cache_); **v0.2.0** ([`9ccdedb`](https://github.com/go-steer/core-tui/commit/9ccdedbc77b9d9198cfdce7ab4a92aaaf1adc127), _feat(inline-tool-display): phase 1 — unified diff previews for apply_patch + edit_file_); **v0.3.0** ([`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc), _feat(tool-results): wire ToolResult events through the agent → TUI flow_); **v0.6.0** ([`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f), _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_); **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751), _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_); **v0.12.0** ([`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc), _feat(tui): expandable tool-call detail overlay + verbose flag (tiers 1+2 of #52) (#61)_); **v0.12.0** ([`6622f3a`](https://github.com/go-steer/core-tui/commit/6622f3a2fc366a529732a6df986b04ecbd1449a4), _feat(tui): consume per-tool-call latency_ms + spec bump v1.2.0 (#62)_); **v0.14.0** ([`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924), _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_)

Message is one entry in the rolling chat log.


```go
type Message struct {
	// ... 21 exported members, documented below
}
```

<details>
<summary><b>21 exported members</b></summary>


**`Role`** — field · `tui/history.go:42`

```go
Role Role
```

_No doc comment._


**`Text`** — field · `tui/history.go:43`

```go
Text string
```

_No doc comment._


**`Rendered`** — field · `tui/history.go:47`

```go
Rendered string
```

Rendered caches the Glamour-rendered form of Text for assistant
messages after a turn completes (R-CHAT-4). Empty during stream.


**`ToolName`** — field · `tui/history.go:50`

```go
ToolName string
```

ToolName, ToolArgs populated when Role == RoleTool.


**`ToolArgs`** — field · `tui/history.go:51`

```go
ToolArgs string
```

_No doc comment._


**`ToolPreview`** — field · `tui/history.go:59` · added in **v0.2.0** ([`9ccdedb`](https://github.com/go-steer/core-tui/commit/9ccdedbc77b9d9198cfdce7ab4a92aaaf1adc127))

```go
ToolPreview string
```

ToolPreview is the multi-line block that renders under the
tool row when the tool call has previewable content (unified
diff for apply_patch / edit_file, read scope summary,
result content). Pre-computed at applyToolCall time so the
lazy-list cache caches it as part of the row; re-computed at
applyToolResult time so the same field carries both call-only
and call+result variants. Empty = no preview.


**`ToolCallID`** — field · `tui/history.go:65` · added in **v0.3.0** ([`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc))

```go
ToolCallID string
```

ToolCallID is the wire-level tool-call ID from the agent
event (e.g. genai.FunctionCall.ID). Stored on RoleTool
messages so applyToolResult can locate the matching row when
a tool-result event arrives. Empty when the host doesn't
emit per-call IDs.


**`ToolArgsMap`** — field · `tui/history.go:71` · added in **v0.3.0** ([`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc))

```go
ToolArgsMap map[string]any
```

ToolArgsMap stashes the structured call-time args so
applyToolResult can re-render ToolPreview with both original
call info and the freshly-arrived result — renderToolPreview
needs path / range to format result content sensibly
(e.g. lang detection from the read_file path).


**`ToolResponseMap`** — field · `tui/history.go:81` · added in **v0.12.0** ([`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc))

```go
ToolResponseMap map[string]any
```

ToolResponseMap / ToolError stash the raw result payload so
the expand-single detail overlay (dialog_toolcall.go, core-tui
#52 tier 1) can re-render the full args + response for any
past tool call in the session on demand. ToolPreview stores
the compact rendered form which loses fidelity — this pair
preserves the structured data. Both remain nil / "" on
RoleTool rows whose result hasn't landed yet, and on non-Tool
rows.


**`ToolError`** — field · `tui/history.go:82` · added in **v0.12.0** ([`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc))

```go
ToolError string
```

_No doc comment._


**`ToolLatencyMs`** — field · `tui/history.go:90` · added in **v0.12.0** ([`6622f3a`](https://github.com/go-steer/core-tui/commit/6622f3a2fc366a529732a6df986b04ecbd1449a4))

```go
ToolLatencyMs int64
```

ToolLatencyMs is the per-call wall-clock latency (in ms)
reported by the host — populated from tui.ToolResult.LatencyMs
which core-tui auto-plucks from Response["latency_ms"] when
adapters don't set it explicitly. 0 = unknown / not reported;
renderers suppress the `[2.4s]` badge and the dialog chip in
that case. Consumer side of core-tui #60 / SSE spec v1.2.0.


**`ToolSavings`** — field · `tui/history.go:100` · added in **v0.14.0** ([`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924))

```go
ToolSavings *ToolSavings
```

ToolSavings is the digest wrap's per-call reduction (bytes +
tokens, path, agentic-path subagent usage). Populated from
tui.ToolResult.Savings which core-tui auto-plucks from
Response["savings"] when adapters don't set it explicitly.
Nil when the host didn't dispatch through a digest wrap;
renderers suppress the inline chip + dialog block in that
case. Consumer side of SSE spec v1.3.0 / core-agent #223
Phase 4.


**`Usage`** — field · `tui/history.go:106` · added in **v0.1.0** ([`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2))

```go
Usage *Usage
```

Per-turn metadata populated by the TUI on the final assistant
Message of each turn so the renderer can append a one-line
`◇ Model · 8.4K in · 2.1K out · $0.012 · 4s` footer (R-USE-1).
Nil / zero values suppress the footer.


**`Model`** — field · `tui/history.go:107` · added in **v0.1.0** ([`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2))

```go
Model string
```

_No doc comment._


**`Elapsed`** — field · `tui/history.go:108` · added in **v0.1.0** ([`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2))

```go
Elapsed time.Duration
```

_No doc comment._


**`CostUSD`** — field · `tui/history.go:109` · added in **v0.1.0** ([`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2))

```go
CostUSD float64
```

_No doc comment._


**`ID`** — field · `tui/history.go:114` · added in **v0.1.0** ([`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27))

```go
ID uint64
```

ID is the stable identity History.Append assigns so the lazy-
render cache (listcache.go) can key entries across refreshes.
0 until Append; preserved across SetRendered mutations.


**`Version`** — field · `tui/history.go:119` · added in **v0.1.0** ([`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27))

```go
Version uint64
```

Version increments on every mutation that changes rendered
output (currently SetRendered on resize). The lazy-render
cache treats version mismatch as an invalidation signal.


**`AutoContinue`** — field · `tui/history.go:128` · added in **v0.6.0** ([`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f))

```go
AutoContinue bool
```

AutoContinue marks a RoleUser message that was synthesized by
the AutoContinueFromInbox loop (issue #9) rather than typed
by the operator. The renderer swaps the usual ❯ prefix +
brand-bg card for a muted ↻ prefix so operators can tell at
a glance which turns they initiated. False (zero) on every
other Message; the field is meaningless for non-RoleUser
rows.


**`TurnError`** — field · `tui/history.go:136` · added in **v0.9.0** ([`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751))

```go
TurnError *TurnError
```

TurnError, when non-nil, carries the structured payload from
a push-mode turn-error event (spec §2.6 / issue #40) so the
renderer can paint a richer "kind · message · hint" block
than a bare text RoleError row. Only set on RoleError
messages produced by the turnErrorMsg handler in Update;
legacy error rows leave it nil and render as plain text.


**`Display`** — method · `tui/history.go:141`

```go
func (m Message) Display() string
```

Display returns the renderable string for this message, preferring
the cached Glamour render when available.


</details>

---

#### `Role`

`type` · `tui/history.go:21` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Role tags each entry in the chat log so the renderer can pick the
right style and glyph.


```go
type Role int
```

---

#### `RoleAssistant`

`const` · `tui/history.go:25` · block `RoleUser` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

_No doc comment._


```go
RoleAssistant
```

---

#### `RoleError`

`const` · `tui/history.go:27` · block `RoleUser` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

_No doc comment._


```go
RoleError
```

---

#### `RoleNotice`

`const` · `tui/history.go:37` · block `RoleUser` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.8.0** — [`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b), 2026-06-07, _feat(notifier): out-of-band Notify() side channel for host-initiated chat rows (#30)_

RoleNotice tags rows pushed by the HOST via Options.Notifier
(issue #30) — framework-initiated content that doesn't belong
to the agent event stream. Visually distinct from RoleSystem
so operators can tell "framework speaking" (reconnect notice,
multi-attach signal, version mismatch) from "agent system
response" (slash result, permission outcome, /clear armed).
Renders with a ◇ glyph + theme.Info muted color (no italic —
italic is RoleSystem's tier).


```go
// RoleNotice tags rows pushed by the HOST via Options.Notifier
// (issue #30) — framework-initiated content that doesn't belong
// to the agent event stream. Visually distinct from RoleSystem
// so operators can tell "framework speaking" (reconnect notice,
// multi-attach signal, version mismatch) from "agent system
// response" (slash result, permission outcome, /clear armed).
// Renders with a ◇ glyph + theme.Info muted color (no italic —
// italic is RoleSystem's tier).
RoleNotice
```

---

#### `RoleSystem`

`const` · `tui/history.go:26` · block `RoleUser` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

_No doc comment._


```go
RoleSystem
```

---

#### `RoleTool`

`const` · `tui/history.go:28` · block `RoleUser` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

_No doc comment._


```go
RoleTool
```

---

#### `RoleUser`

`const` · `tui/history.go:24` · block `RoleUser` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

_No doc comment._


```go
RoleUser Role = iota
```

---

#### `ThemeChangedMsg`

`type` · `tui/messages.go:299` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

ThemeChangedMsg is emitted by the /theme picker (and `/theme
<name>` with a known name) when the operator commits a new
theme. Hosts have two equivalent ways to persist:

  - Set Options.PersistThemeChoice — a callback the picker
    invokes inline (mirrors PersistModelChoice). Less host
    code; no Update-loop intercept needed.
  - Observe ThemeChangedMsg in the host's Update loop. Useful
    when the host already has a custom Update wrapper or
    wants to react to theme changes beyond persistence (e.g.
    emit telemetry).

Both fire on every committed change — pick one or both,
whichever fits the host's architecture. On next launch, hosts
seed the persisted name via Options.InitialThemeName.

Exported (capital M) because it crosses the package boundary
— unlike most msgs in this file, which are tui-internal.


```go
type ThemeChangedMsg struct{ Name string }
```

<details>
<summary><b>1 exported members</b></summary>


**`Name`** — field · `tui/messages.go:299`

```go
Name string
```

_No doc comment._


</details>

---

#### `QueueEntry`

`type` · `tui/queue.go:58` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158), 2026-05-25, _feat(tui): queue panel state machine (R-CHAT-10, PR 2/5)_<br>declaration changed since: **v0.1.0** ([`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5), _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_)

QueueEntry is one row in the operator-typed-during-streaming queue
panel (R-CHAT-10 / R-CHAT-11). Text holds the verbatim prompt;
State tracks the lifecycle; Err carries the failure reason when
State == QueueFailed; Created stamps when the entry was enqueued
(or transitioned to terminal state) so the TTL cull knows when to
drop it; Injected is true for entries routed through
InjectableAgent.Inject (`InjectIntoCurrent` mode) so the renderer
can label them distinctly from queue-drained entries.


```go
type QueueEntry struct {
	Text     string
	State    QueueState
	Err      string
	Created  time.Time
	Injected bool
}
```

<details>
<summary><b>5 exported members</b></summary>


**`Text`** — field · `tui/queue.go:59`

```go
Text string
```

_No doc comment._


**`State`** — field · `tui/queue.go:60`

```go
State QueueState
```

_No doc comment._


**`Err`** — field · `tui/queue.go:61`

```go
Err string
```

_No doc comment._


**`Created`** — field · `tui/queue.go:62`

```go
Created time.Time
```

_No doc comment._


**`Injected`** — field · `tui/queue.go:63` · added in **v0.1.0** ([`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5))

```go
Injected bool
```

_No doc comment._


</details>

---

#### `QueueState`

`type` · `tui/queue.go:27` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158), 2026-05-25, _feat(tui): queue panel state machine (R-CHAT-10, PR 2/5)_

QueueState is the lifecycle of one operator-typed-during-streaming
entry (R-CHAT-10). Each entry transitions:

	Queued → InFlight → Done   (clean turn end)
	                 → Failed  (turn error or interrupt)

Done and Failed entries linger in the panel for cullTTL so the
operator sees the result, then cull on the next render.


```go
type QueueState int
```

<details>
<summary><b>1 exported members</b></summary>


**`String`** — method · `tui/queue.go:37`

```go
func (s QueueState) String() string
```

String returns the lowercase state label used in tests + logs.


</details>

---

#### `QueueDone`

`const` · `tui/queue.go:32` · block `QueueQueued` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158), 2026-05-25, _feat(tui): queue panel state machine (R-CHAT-10, PR 2/5)_

turn finished cleanly


```go
QueueDone // turn finished cleanly
```

---

#### `QueueFailed`

`const` · `tui/queue.go:33` · block `QueueQueued` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158), 2026-05-25, _feat(tui): queue panel state machine (R-CHAT-10, PR 2/5)_

turn errored or was interrupted


```go
QueueFailed // turn errored or was interrupted
```

---

#### `QueueInFlight`

`const` · `tui/queue.go:31` · block `QueueQueued` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158), 2026-05-25, _feat(tui): queue panel state machine (R-CHAT-10, PR 2/5)_

drained from the queue, currently the streaming turn


```go
QueueInFlight // drained from the queue, currently the streaming turn
```

---

#### `QueueQueued`

`const` · `tui/queue.go:30` · block `QueueQueued` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158), 2026-05-25, _feat(tui): queue panel state machine (R-CHAT-10, PR 2/5)_

typed during streaming; waiting for the running turn to finish


```go
QueueQueued QueueState = iota // typed during streaming; waiting for the running turn to finish
```

---

#### `ToolSavings`

`type` · `tui/tool_savings.go:57` · contract closure: **inside** · core-agent: **unused**  
introduced in **v0.14.0** — [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924), 2026-07-17, _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_

ToolSavings surfaces the digest wrap's per-call reduction. Fields
mirror the wire shape core-agent's pkg/digest.Savings emits — see
docs/sse-event-stream-protocol.md §2.7 for the authoritative shape.

The four *Bytes / *Tokens* fields are always populated on wrapped
calls. Token counts use a 4-char-per-token heuristic (accurate to
±15%), suitable for savings display but NOT billing.

Subagent* fields are populated only on the agentic path (Path ==
SavingsPathAgentic) — the small-tier LLM digester's own usage.
Zero on structural / passthrough.


```go
type ToolSavings struct {
	Path                 string
	OriginalBytes        int
	DigestBytes          int
	OriginalTokensEst    int
	DigestTokensEst      int
	SubagentModel        string
	SubagentInputTokens  int
	SubagentOutputTokens int
}
```

<details>
<summary><b>9 exported members</b></summary>


**`Path`** — field · `tui/tool_savings.go:58`

```go
Path string
```

_No doc comment._


**`OriginalBytes`** — field · `tui/tool_savings.go:59`

```go
OriginalBytes int
```

_No doc comment._


**`DigestBytes`** — field · `tui/tool_savings.go:60`

```go
DigestBytes int
```

_No doc comment._


**`OriginalTokensEst`** — field · `tui/tool_savings.go:61`

```go
OriginalTokensEst int
```

_No doc comment._


**`DigestTokensEst`** — field · `tui/tool_savings.go:62`

```go
DigestTokensEst int
```

_No doc comment._


**`SubagentModel`** — field · `tui/tool_savings.go:63`

```go
SubagentModel string
```

_No doc comment._


**`SubagentInputTokens`** — field · `tui/tool_savings.go:64`

```go
SubagentInputTokens int
```

_No doc comment._


**`SubagentOutputTokens`** — field · `tui/tool_savings.go:65`

```go
SubagentOutputTokens int
```

_No doc comment._


**`SavedTokens`** — method · `tui/tool_savings.go:72`

```go
func (s *ToolSavings) SavedTokens() int
```

SavedTokens returns the parent-side token reduction (before any
subagent offset). Clamps to zero to avoid negative "savings" on the
passthrough path where a truncation marker can nominally inflate
the digest above the original.


</details>

---

#### `SavingsPathAgentic`

`const` · `tui/tool_savings.go:43` · block `SavingsPathPassthrough` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.14.0** — [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924), 2026-07-17, _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_

Path values the digest router populates on ToolSavings.Path.
Passthrough means the wrap layer decided the payload was small
enough to skip; structural means the JSON pruner reduced it; agentic
means the LLM subagent digested it after structural couldn't.


```go
SavingsPathAgentic = "llm_fallback"
```

---

#### `SavingsPathPassthrough`

`const` · `tui/tool_savings.go:41` · block `SavingsPathPassthrough` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.14.0** — [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924), 2026-07-17, _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_

Path values the digest router populates on ToolSavings.Path.
Passthrough means the wrap layer decided the payload was small
enough to skip; structural means the JSON pruner reduced it; agentic
means the LLM subagent digested it after structural couldn't.


```go
SavingsPathPassthrough = "passthrough"
```

---

#### `SavingsPathStructural`

`const` · `tui/tool_savings.go:42` · block `SavingsPathPassthrough` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.14.0** — [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924), 2026-07-17, _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_

Path values the digest router populates on ToolSavings.Path.
Passthrough means the wrap layer decided the payload was small
enough to skip; structural means the JSON pruner reduced it; agentic
means the LLM subagent digested it after structural couldn't.


```go
SavingsPathStructural = "structural_json"
```

---

### 8.6 Render extension points

_Files: `tui/dialog.go`, `tui/dialog_scroll.go`, `tui/dialog_textinput.go`, `tui/listcache.go`, `tui/model.go`, `tui/toolrender.go`_

#### `Dialog`

`type` · `tui/dialog.go:39` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf), 2026-05-26, _feat(tui-facelift): tier B — Dialog Overlay stack + model picker migrated_

Dialog is the contract for any modal that wants to ride the
Overlay stack. Each method is keystroke-driven; the front-most
dialog gets every key until it returns DialogActionClose.


```go
type Dialog interface {
	// ID is a stable identifier (e.g. "model-picker", "settings")
	// so Overlay.Close(id) can target a specific dialog regardless
	// of z-order.
	ID() string

	// HandleKey is invoked for every keystroke the front-most
	// dialog receives. Returns the action the Overlay should
	// take (consume + render; close + pop; etc.).
	HandleKey(stroke string, m *Model) DialogAction

	// Render returns the styled string for the dialog body at
	// the given total terminal width. The Overlay wraps the
	// result in chrome via RenderContext.
	Render(width int, m *Model) string
}
```

<details>
<summary><b>3 exported members</b></summary>


**`ID`** — method · `tui/dialog.go:43`

```go
ID() string
```

ID is a stable identifier (e.g. "model-picker", "settings")
so Overlay.Close(id) can target a specific dialog regardless
of z-order.


**`HandleKey`** — method · `tui/dialog.go:48`

```go
HandleKey(stroke string, m *Model) DialogAction
```

HandleKey is invoked for every keystroke the front-most
dialog receives. Returns the action the Overlay should
take (consume + render; close + pop; etc.).


**`Render`** — method · `tui/dialog.go:53`

```go
Render(width int, m *Model) string
```

Render returns the styled string for the dialog body at
the given total terminal width. The Overlay wraps the
result in chrome via RenderContext.


</details>

---

#### `DialogAction`

`type` · `tui/dialog.go:80` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf), 2026-05-26, _feat(tui-facelift): tier B — Dialog Overlay stack + model picker migrated_<br>declaration changed since: **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_)

DialogAction is the return shape of HandleKey. Composite so
dialogs can signal "consume key" + "close me" + "emit a Cmd"
(e.g. ThemeChangedMsg from the theme picker) in one go.


```go
type DialogAction struct {
	// Consumed reports whether the dialog handled the key. When
	// false, the Overlay lets the key fall through to the rest of
	// the handleKey switch (e.g. for Ctrl+C which always quits).
	Consumed bool
	// Close pops THIS dialog off the stack after the current
	// frame renders. Pair with Consumed=true to also stop the
	// key from falling through.
	Close bool
	// Cmd is an optional tea.Cmd to dispatch alongside the
	// state mutation — used by dialogs that need to notify the
	// host of a commit (e.g. ThemeChangedMsg). Nil for the
	// common case where the dialog just mutates Model.
	Cmd tea.Cmd
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Consumed`** — field · `tui/dialog.go:84`

```go
Consumed bool
```

Consumed reports whether the dialog handled the key. When
false, the Overlay lets the key fall through to the rest of
the handleKey switch (e.g. for Ctrl+C which always quits).


**`Close`** — field · `tui/dialog.go:88`

```go
Close bool
```

Close pops THIS dialog off the stack after the current
frame renders. Pair with Consumed=true to also stop the
key from falling through.


**`Cmd`** — field · `tui/dialog.go:93` · added in **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501))

```go
Cmd tea.Cmd
```

Cmd is an optional tea.Cmd to dispatch alongside the
state mutation — used by dialogs that need to notify the
host of a commit (e.g. ThemeChangedMsg). Nil for the
common case where the dialog just mutates Model.


</details>

---

#### `KeyMsgDialog`

`type` · `tui/dialog.go:70` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.18.0** — [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9), 2026-08-13, _feat(tui): text-input Dialog primitive + session-picker action rows (#56) (#72)_

KeyMsgDialog is an optional extension of Dialog for modals whose
body owns a real text-editing widget (issue #56's TextInputDialog
is the first). Dialog.HandleKey receives a NORMALIZED stroke
("ctrl+u", "shift+enter") which is lossy for an input widget: it
drops Key.Text — the grapheme(s) the terminal actually delivered,
which is exactly what a textinput wants to insert — and it can't
carry a bracketed paste.

Dialogs that implement this interface get the raw
tea.KeyPressMsg from Overlay.HandleKeyMsg; every other Dialog
keeps the stroke-string contract untouched. Implementations must
still provide HandleKey (Dialog is embedded) — the convention is
to synthesize a KeyPressMsg from the stroke and delegate, so both
entry points stay behaviorally identical.


```go
type KeyMsgDialog interface {
	Dialog

	// HandleKeyMsg is the full-fidelity twin of HandleKey.
	HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Dialog`** — embedded · `tui/dialog.go:71`

```go

```

_No doc comment._


**`HandleKeyMsg`** — method · `tui/dialog.go:74`

```go
HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction
```

HandleKeyMsg is the full-fidelity twin of HandleKey.


</details>

---

#### `Overlay`

`type` · `tui/dialog.go:99` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf), 2026-05-26, _feat(tui-facelift): tier B — Dialog Overlay stack + model picker migrated_

Overlay is the modal z-order stack. Open() pushes onto the top;
HandleKey routes only to the front; Render iterates in stack
order so later opens render on top. Empty stack = no modal.


```go
type Overlay struct {
	// contains filtered or unexported fields
}
```

<details>
<summary><b>11 exported members</b></summary>


**`Close`** — method · `tui/dialog.go:112`

```go
func (o *Overlay) Close(id string)
```

Close removes the dialog with id from the stack (any
position). No-op when not present.


**`CloseFront`** — method · `tui/dialog.go:124`

```go
func (o *Overlay) CloseFront()
```

CloseFront pops the front-most dialog. No-op on empty stack.


**`Front`** — method · `tui/dialog.go:158`

```go
func (o *Overlay) Front() Dialog
```

Front returns the front-most dialog, or nil on empty stack.


**`Get`** — method · `tui/dialog.go:137` · added in **v0.18.0** ([`eb881e5`](https://github.com/go-steer/core-tui/commit/eb881e52c608f7f456aee8a8aa151bae04d91f17))

```go
func (o *Overlay) Get(id string) Dialog
```

Get returns the dialog with id from anywhere in the stack, or nil.
Unlike Front it doesn't care what's on top: an async reply for a
dialog that another modal has since covered still belongs to it.


**`HandleKey`** — method · `tui/dialog.go:170` · changed in **v0.7.0**

```go
func (o *Overlay) HandleKey(stroke string, m *Model) (consumed bool, cmd tea.Cmd)
```

HandleKey routes the keystroke to the front-most dialog and
applies the returned action. Returns Consumed so the caller
(handleKey) can decide whether to fall through, plus an
optional Cmd for dialogs that need to emit a msg (e.g. the
theme picker firing ThemeChangedMsg on commit).


**`HandleKeyMsg`** — method · `tui/dialog.go:187` · added in **v0.18.0** ([`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9))

```go
func (o *Overlay) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) (consumed bool, cmd tea.Cmd)
```

HandleKeyMsg is HandleKey with the raw keystroke preserved. When
the front-most dialog implements KeyMsgDialog it gets the full
tea.KeyPressMsg; otherwise we fall back to the normalized-stroke
contract. This is the entry point handleKey uses — HandleKey
stays for callers that only hold a stroke string.


**`HandleWheel`** — method · `tui/dialog_scroll.go:289` · added in **v0.19.0** ([`890f1c3`](https://github.com/go-steer/core-tui/commit/890f1c355694bdb4bb42feb086d65e0343f46ac9))

```go
func (o *Overlay) HandleWheel(delta int, m *Model) (consumed bool, cmd tea.Cmd)
```

HandleWheel routes one wheel gesture to the front-most dialog.
delta is in body rows, signed. Returns Consumed so Update knows
whether to keep the event away from the chat viewport behind the
modal — always true when anything is open, since a wheel tick that
silently scrolls a surface hidden behind a modal is the bug this
whole file exists to fix.


**`HasDialogs`** — method · `tui/dialog.go:132`

```go
func (o *Overlay) HasDialogs() bool
```

HasDialogs reports whether anything is open.


**`HasID`** — method · `tui/dialog.go:148`

```go
func (o *Overlay) HasID(id string) bool
```

HasID reports whether a dialog with id is on the stack
(useful for singleton checks before Open).


**`Open`** — method · `tui/dialog.go:106`

```go
func (o *Overlay) Open(d Dialog)
```

Open pushes a new dialog onto the top of the stack. No
dedup — opening "model-picker" twice stacks twice; callers
that want singletons check HasID() first.


**`Render`** — method · `tui/dialog.go:208`

```go
func (o *Overlay) Render(width int, m *Model) string
```

Render iterates the stack and returns the front-most dialog's
styled string wrapped in modal chrome. Empty stack returns "".
Today we only render the FRONT (no layered painting); future
translucent overlays would draw deeper dialogs first.


</details>

---

#### `RenderContext`

`type` · `tui/dialog.go:220` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf), 2026-05-26, _feat(tui-facelift): tier B — Dialog Overlay stack + model picker migrated_

RenderContext assembles a dialog body with consistent chrome:
title bar, body, footer. Mirrors agentic-tui skill §9.C —
every dialog inherits identical border / title styling without
duplicating the lipgloss boilerplate.


```go
type RenderContext struct {
	Title  string
	Body   string
	Footer string
	Width  int

	Styles Styles
}
```

<details>
<summary><b>6 exported members</b></summary>


**`Title`** — field · `tui/dialog.go:221`

```go
Title string
```

_No doc comment._


**`Body`** — field · `tui/dialog.go:222`

```go
Body string
```

_No doc comment._


**`Footer`** — field · `tui/dialog.go:223`

```go
Footer string
```

_No doc comment._


**`Width`** — field · `tui/dialog.go:224`

```go
Width int
```

_No doc comment._


**`Styles`** — field · `tui/dialog.go:226`

```go
Styles Styles
```

_No doc comment._


**`Render`** — method · `tui/dialog.go:234`

```go
func (rc RenderContext) Render() string
```

Render returns the framed dialog as a styled string. Title
renders bold-accent with a horizontal rule continuing to the
right edge; body sits in the middle with a single blank line
above and below; footer renders muted at the bottom with its
own rule.


</details>

---

#### `Scrollbar`

`func` · `tui/dialog.go:263` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf), 2026-05-26, _feat(tui-facelift): tier B — Dialog Overlay stack + model picker migrated_

Scrollbar renders a vertical scrollbar character column of
`height` rows showing thumb position relative to (contentSize,
viewportSize, offset). Returns "" when content fits in viewport
or when height <= 0. Lifted from agentic-tui skill §9.F so
any dialog with overflowing content can frame a consistent
scroll indicator without writing the math twice.


```go
func Scrollbar(s Styles, height, contentSize, viewportSize, offset int) string
```

---

#### `ScrollDialog`

`type` · `tui/dialog_scroll.go:275` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.19.0** — [`890f1c3`](https://github.com/go-steer/core-tui/commit/890f1c355694bdb4bb42feb086d65e0343f46ac9), 2026-08-14, _fix(tui): make every modal scrollable + route the mouse wheel to it (#75)_

ScrollDialog is the optional extension for a Dialog whose body
scrolls by lines rather than by cursor rows. The Overlay routes
mouse-wheel ticks to ScrollBy; dialogs that don't implement it get
one cursor step per tick synthesized from their up/down keys
instead (the right behavior for the pickers).


```go
type ScrollDialog interface {
	Dialog

	// ScrollBy moves the dialog's body by delta rows — negative is
	// toward the top. Clamping is the dialog's business.
	ScrollBy(delta int, m *Model)
}
```

<details>
<summary><b>2 exported members</b></summary>


**`Dialog`** — embedded · `tui/dialog_scroll.go:276`

```go

```

_No doc comment._


**`ScrollBy`** — method · `tui/dialog_scroll.go:280`

```go
ScrollBy(delta int, m *Model)
```

ScrollBy moves the dialog's body by delta rows — negative is
toward the top. Clamping is the dialog's business.


</details>

---

#### `TextInputConfig`

`type` · `tui/dialog_textinput.go:56` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.18.0** — [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9), 2026-08-13, _feat(tui): text-input Dialog primitive + session-picker action rows (#56) (#72)_

TextInputConfig configures a text-input Dialog. Only Submit is
strictly required — everything else has a sane default:

	ID     → "text-input"
	Title  → "Enter a Value"
	Width  → 64 columns (clamped to the terminal)

The zero value is therefore usable for a throwaway prompt, but a
Title + Prompt pair is what makes the modal self-explanatory.


```go
type TextInputConfig struct {
	// ... 10 exported members, documented below
}
```

<details>
<summary><b>10 exported members</b></summary>


**`ID`** — field · `tui/dialog_textinput.go:59`

```go
ID string
```

ID is the Overlay identity (Overlay.Close / HasID). Empty
falls back to textInputDialogID.


**`Title`** — field · `tui/dialog_textinput.go:62`

```go
Title string
```

Title renders in the dialog's title bar.


**`Prompt`** — field · `tui/dialog_textinput.go:66`

```go
Prompt string
```

Prompt is the one-line question above the input box, e.g.
"Attach to endpoint (URL):". Empty renders no prompt line.


**`Placeholder`** — field · `tui/dialog_textinput.go:69`

```go
Placeholder string
```

Placeholder is the dim hint shown while the input is empty.


**`Initial`** — field · `tui/dialog_textinput.go:73`

```go
Initial string
```

Initial pre-fills the input (cursor lands at end) — useful
for "edit this value" flows.


**`CharLimit`** — field · `tui/dialog_textinput.go:76`

```go
CharLimit int
```

CharLimit caps the typed value. 0 = unlimited.


**`Width`** — field · `tui/dialog_textinput.go:80`

```go
Width int
```

Width is the dialog width in columns. 0 = defaultTextInputWidth.
Always clamped to the terminal width at render time.


**`Validate`** — field · `tui/dialog_textinput.go:86`

```go
Validate func(value string) string
```

Validate is called on Enter with the trimmed value. A
non-empty return is rendered as an inline error under the
input and the dialog STAYS OPEN so the operator can fix the
value. Nil = every value is accepted.


**`Submit`** — field · `tui/dialog_textinput.go:94`

```go
Submit func(value string, m *Model) DialogAction
```

Submit is called on Enter once Validate passes. It receives
the trimmed value and the live Model, and returns the
DialogAction the Overlay applies — so a submit closure can
close this dialog (Close: true), leave it open, emit a Cmd,
and/or Open another dialog on the stack. Nil Submit closes
the dialog without doing anything.


**`Footer`** — field · `tui/dialog_textinput.go:98`

```go
Footer string
```

Footer overrides the default "enter submit · esc cancel"
hint line.


</details>

---

#### `NewTextInputDialog`

`func` · `tui/dialog_textinput.go:144` · block `Dialog` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.18.0** — [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9), 2026-08-13, _feat(tui): text-input Dialog primitive + session-picker action rows (#56) (#72)_

NewTextInputDialog builds a single-line text-entry Dialog ready
for Overlay.Open. Typical use from inside another Dialog's
HandleKey — note the submit closure closing BOTH dialogs:

	m.overlayStack.Open(NewTextInputDialog(TextInputConfig{
	    Title:  "Attach to Endpoint",
	    Prompt: "Daemon URL:",
	    Validate: func(v string) string {
	        if v == "" { return "endpoint is required" }
	        return ""
	    },
	    Submit: func(v string, m *Model) DialogAction {
	        tgt, err := dialTheThing(v)
	        if err != nil {
	            m.history.Append(Message{Role: RoleError, Text: err.Error()})
	            m.refreshViewport()
	            return DialogAction{Consumed: true, Close: true}
	        }
	        m.overlayStack.Close(sessionPickerDialogID) // close the parent
	        return DialogAction{Consumed: true, Close: true, Cmd: m.applySwitchTarget(&tgt)}
	    },
	}))


```go
func NewTextInputDialog(cfg TextInputConfig) Dialog
```

---

#### `Focusable`

`type` · `tui/listcache.go:77` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27), 2026-05-26, _feat(tui-facelift): tier A — lazy list with Versioned items + per-item cache_

Focusable receives focus state from the list (the selected
row sets it before render). Items use the bit to apply hover
/ selection styling without inline `if focused` branches in
every Render method.


```go
type Focusable interface {
	SetFocused(bool)
}
```

<details>
<summary><b>1 exported members</b></summary>


**`SetFocused`** — method · `tui/listcache.go:78`

```go
SetFocused(bool)
```

_No doc comment._


</details>

---

#### `Item`

`type` · `tui/listcache.go:40` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27), 2026-05-26, _feat(tui-facelift): tier A — lazy list with Versioned items + per-item cache_

Item is the contract for any history entry that can be cached
by the renderItem cache. The current implementation has only
one concrete impl (messageItem wrapping a Message), but the
interface is exposed so future surfaces (search results, code-
review rows) can opt into the same caching path.


```go
type Item interface {
	// Identity returns the stable opaque key the cache uses to
	// look up the item across refreshes. Two items with the same
	// Identity are considered the same logical entry.
	Identity() uint64

	// Version returns the monotonic mutation counter; cache
	// entries with a different version are invalidated.
	Version() uint64

	// Finished reports whether the item has reached a terminal
	// state. Cache marks finished entries as frozen — even a
	// width-keyed re-render skips work for these unless the
	// content was explicitly invalidated.
	Finished() bool

	// Render returns the styled string for the given viewport
	// width. Called only on cache miss / version bump.
	Render(m *Model, width int) string
}
```

<details>
<summary><b>4 exported members</b></summary>


**`Identity`** — method · `tui/listcache.go:44`

```go
Identity() uint64
```

Identity returns the stable opaque key the cache uses to
look up the item across refreshes. Two items with the same
Identity are considered the same logical entry.


**`Version`** — method · `tui/listcache.go:48`

```go
Version() uint64
```

Version returns the monotonic mutation counter; cache
entries with a different version are invalidated.


**`Finished`** — method · `tui/listcache.go:54`

```go
Finished() bool
```

Finished reports whether the item has reached a terminal
state. Cache marks finished entries as frozen — even a
width-keyed re-render skips work for these unless the
content was explicitly invalidated.


**`Render`** — method · `tui/listcache.go:58`

```go
Render(m *Model, width int) string
```

Render returns the styled string for the given viewport
width. Called only on cache miss / version bump.


</details>

---

#### `RawRenderable`

`type` · `tui/listcache.go:69` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27), 2026-05-26, _feat(tui-facelift): tier A — lazy list with Versioned items + per-item cache_

RawRenderable lets clipboard / transcript paths grab unstyled
text without ANSI escapes. Falls back to ansi.Strip(Render(...))
when not implemented.


```go
type RawRenderable interface {
	RawRender(width int) string
}
```

<details>
<summary><b>1 exported members</b></summary>


**`RawRender`** — method · `tui/listcache.go:70`

```go
RawRender(width int) string
```

_No doc comment._


</details>

---

#### `Model`

`type` · `tui/model.go:68` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Model is the Bubble Tea model that drives the TUI. Field set is the
minimum needed for the v0 visual-preview slice; later slices add
streaming state, modal forms, transcript persistence, etc.


```go
type Model struct {
	// contains filtered or unexported fields
}
```

<details>
<summary><b>4 exported members</b></summary>


**`ApplyTranscript`** — method · `tui/transcript.go:327` · added in **v0.1.0** ([`00bc303`](https://github.com/go-steer/core-tui/commit/00bc30313ffae248824875c97c1724af847c86ff))

```go
func (m *Model) ApplyTranscript(t Transcript)
```

ApplyTranscript replaces the model's history with the loaded
transcript's messages and re-renders any assistant markdown at
the current viewport width so wrapping is correct. The list
cache is reset so the next refreshViewport regenerates every
row from the new identities.

Doesn't restore the in-flight turn, queue, or modal state — a
resumed session starts idle.


**`Init`** — method · `tui/update.go:34`

```go
func (m Model) Init() tea.Cmd
```

Init asks the terminal for its background color so the style bundle
can resolve dark vs light at startup (R-MD-2), starts the textarea
cursor blink, primes the event listener that drains messages from
the agent dispatch goroutine, and (when the host's agent
implements WakeRequester) subscribes to the wake channel for
transient toast banners (R-WAKE-1).


**`Update`** — method · `tui/update.go:98`

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)
```

Update is the Bubble Tea dispatcher. The visual-preview slice
handles window-resize, background-color, and a small keymap; later
slices add agent-event dispatch, modal forms, etc.


**`View`** — method · `tui/view.go:118`

```go
func (m Model) View() tea.View
```

View composes the full TUI. Returns a tea.View with AltScreen on
and the brand cursor block. Layout is governed by m.statusLayout
(R-USE-2).


</details>

---

#### `NewModel`

`func` · `tui/model.go:381` · block `Model` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

NewModel constructs a Model from Options. SeedHistory entries are
appended in order before the first render.


```go
func NewModel(opts Options) Model
```

---

#### `ToolRenderer`

`type` · `tui/toolrender.go:47` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`221bd9e`](https://github.com/go-steer/core-tui/commit/221bd9ea0a1d9958eafe8128da7a808095b2c6c1), 2026-05-26, _feat(tui-facelift): tier B — ToolRenderer strategy pattern_

ToolRenderer is the contract for one tool's call/result
rendering. renderMessage feeds it the message, the styled head
(already glyph + bold name), and the available width; the
renderer returns the full styled string for the row, including
any inline preview (diff for apply_patch / edit_file, etc.)
rendered as a block under the call line.

Implementations should be stateless / value receivers so the
factory can hand out a single shared instance per tool. The
preview block is shared across all renderers via withPreview;
per-tool renderers focus on the call-line layout only.


```go
type ToolRenderer interface {
	RenderCall(msg Message, head string, width int, styles Styles) string
}
```

<details>
<summary><b>1 exported members</b></summary>


**`RenderCall`** — method · `tui/toolrender.go:48`

```go
RenderCall(msg Message, head string, width int, styles Styles) string
```

_No doc comment._


</details>

---

### 8.7 Theming and styling

_Files: `tui/chroma.go`, `tui/style.go`, `tui/theme.go`_

#### `LipglossFormatter`

`func` · `tui/chroma.go:47` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`22b1ee7`](https://github.com/go-steer/core-tui/commit/22b1ee7995ec806157588212cf0485548cb5a8a5), 2026-05-26, _feat(tui-facelift): tier A — custom Chroma formatter via Lipgloss_

LipglossFormatter returns a chroma.Formatter that emits tokens
through Lipgloss. bg is applied as the background of each
styled token so the code-fence background reads as a single
uniform surface even when individual tokens override fg/style.
Pass `nil` (or `color.Color(nil)`) when no background tint is
wanted — Lipgloss simply skips the Background call.


```go
func LipglossFormatter(bg color.Color) chroma.Formatter
```

---

#### `Styles`

`type` · `tui/style.go:73` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_<br>declaration changed since: **v0.1.0** ([`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_); **v0.8.0** ([`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b), _feat(notifier): out-of-band Notify() side channel for host-initiated chat rows (#30)_)

Styles bundles every resolved lipgloss style for the current
terminal background. NewStyles picks the variant for light vs dark
from BackgroundColorMsg.IsDark() at startup (R-MD-2). Theme is
the semantic-token bundle every per-field style derives from
(agentic-tui skill §10).


```go
type Styles struct {
	// ... 27 exported members, documented below
}
```

<details>
<summary><b>27 exported members</b></summary>


**`Dark`** — field · `tui/style.go:74`

```go
Dark bool
```

_No doc comment._


**`Theme`** — field · `tui/style.go:75` · added in **v0.1.0** ([`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca))

```go
Theme Theme
```

_No doc comment._


**`UserPrefix`** — field · `tui/style.go:77`

```go
UserPrefix lipgloss.Style
```

_No doc comment._


**`UserText`** — field · `tui/style.go:78`

```go
UserText lipgloss.Style
```

_No doc comment._


**`AssistantText`** — field · `tui/style.go:79`

```go
AssistantText lipgloss.Style
```

_No doc comment._


**`SystemText`** — field · `tui/style.go:80`

```go
SystemText lipgloss.Style
```

_No doc comment._


**`NoticeText`** — field · `tui/style.go:81` · added in **v0.8.0** ([`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b))

```go
NoticeText lipgloss.Style
```

RoleNotice — host-initiated rows (issue #30)


**`ErrorText`** — field · `tui/style.go:82`

```go
ErrorText lipgloss.Style
```

_No doc comment._


**`ToolHead`** — field · `tui/style.go:83`

```go
ToolHead lipgloss.Style
```

_No doc comment._


**`ToolBody`** — field · `tui/style.go:84`

```go
ToolBody lipgloss.Style
```

_No doc comment._


**`Wordmark`** — field · `tui/style.go:86`

```go
Wordmark lipgloss.Style
```

_No doc comment._


**`AgentIdentity`** — field · `tui/style.go:87`

```go
AgentIdentity lipgloss.Style
```

_No doc comment._


**`Accent`** — field · `tui/style.go:88`

```go
Accent lipgloss.Style
```

_No doc comment._


**`Muted`** — field · `tui/style.go:89`

```go
Muted lipgloss.Style
```

_No doc comment._


**`Rule`** — field · `tui/style.go:90`

```go
Rule lipgloss.Style
```

_No doc comment._


**`Border`** — field · `tui/style.go:91`

```go
Border lipgloss.Style
```

_No doc comment._


**`SidebarDivider`** — field · `tui/style.go:92`

```go
SidebarDivider lipgloss.Style
```

_No doc comment._


**`SidebarHeading`** — field · `tui/style.go:93`

```go
SidebarHeading lipgloss.Style
```

_No doc comment._


**`InputBorderTop`** — field · `tui/style.go:94`

```go
InputBorderTop lipgloss.Style
```

_No doc comment._


**`InputPlaceholder`** — field · `tui/style.go:95`

```go
InputPlaceholder lipgloss.Style
```

_No doc comment._


**`Footer`** — field · `tui/style.go:96`

```go
Footer lipgloss.Style
```

_No doc comment._


**`PermissionChip`** — field · `tui/style.go:98`

```go
PermissionChip lipgloss.Style
```

_No doc comment._


**`PermissionWarn`** — field · `tui/style.go:99`

```go
PermissionWarn lipgloss.Style
```

_No doc comment._


**`ModalBorder`** — field · `tui/style.go:101`

```go
ModalBorder lipgloss.Style
```

_No doc comment._


**`ModalTitle`** — field · `tui/style.go:102`

```go
ModalTitle lipgloss.Style
```

_No doc comment._


**`ModalFooter`** — field · `tui/style.go:103`

```go
ModalFooter lipgloss.Style
```

_No doc comment._


**`RenderWordmark`** — method · `tui/style.go:183` · added in **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501))

```go
func (s Styles) RenderWordmark(text string) string
```

RenderWordmark paints the brand wordmark. When the active
Theme defines a WordmarkSequence, one color per rune is
applied (cycling the sequence over chars longer than the
sequence). This is the hook the Google theme uses to mimic
the iconic B-R-Y-B-G-R logo sequence; other themes leave the
sequence nil and fall through to the single-color path.

Bold is preserved in both paths so the wordmark keeps its
chrome weight regardless of which path runs.


</details>

---

#### `NewStyles`

`func` · `tui/style.go:110` · block `Styles` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

NewStyles assembles the style bundle for the given background
brightness, applying any Branding overrides on top of the
DefaultTheme. Hosts that want per-provider tinting should pass
a theme via NewStylesWithTheme directly.


```go
func NewStyles(dark bool, brand Branding) Styles
```

---

#### `NewStylesWithTheme`

`func` · `tui/style.go:129` · block `Styles` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), 2026-05-26, _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_

NewStylesWithTheme is the per-token construction path: every
component style derives from the Theme so a palette swap is a
one-line change (no per-field updates). UserPrefix / UserText
keep an explicit blue tone — the user-bubble color is semantic
to the operator's voice and shouldn't shift with provider.


```go
func NewStylesWithTheme(dark bool, theme Theme) Styles
```

---

#### `GlyphAutoContinue`

`const` · `tui/style.go:60` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.6.0** — [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f), 2026-05-27, _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_

GlyphAutoContinue marks RoleUser messages synthesized by the
AutoContinueFromInbox loop (issue #9). Visually distinct
from GlyphUserPrompt so operators can tell at a glance which
turns they typed vs which came from the inbox-drain.


```go
// GlyphAutoContinue marks RoleUser messages synthesized by the
// AutoContinueFromInbox loop (issue #9). Visually distinct
// from GlyphUserPrompt so operators can tell at a glance which
// turns they typed vs which came from the inbox-drain.
GlyphAutoContinue = "↻"
```

---

#### `GlyphCollapsed`

`const` · `tui/style.go:52` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphCollapsed = "▸"
```

---

#### `GlyphColumn`

`const` · `tui/style.go:65` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphColumn = "│"
```

---

#### `GlyphCursor`

`const` · `tui/style.go:63` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphCursor = "█"
```

---

#### `GlyphExpanded`

`const` · `tui/style.go:53` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphExpanded = "▾"
```

---

#### `GlyphModel`

`const` · `tui/style.go:38` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphModel = "◇"
```

---

#### `GlyphRule`

`const` · `tui/style.go:64` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphRule = "─"
```

---

#### `GlyphSeparator`

`const` · `tui/style.go:62` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphSeparator = "·"
```

---

#### `GlyphTool`

`const` · `tui/style.go:43` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_<br>declaration changed since: **v0.1.0** ([`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2), _fix(tui): focus + wrap + tool-gear + per-turn usage + sidebar fallback_); **v0.1.0** ([`4bfe01b`](https://github.com/go-steer/core-tui/commit/4bfe01bb1261fbc0e3244d8256ba2cf9b1124978), _feat(tui-facelift): smaller tool glyph + active-call highlight + non-breaking footer_)

GlyphTool is the inline marker for completed tool calls.
Single-cell, text-class (not emoji-class) so terminals
render it in the foreground color we asked for instead of
the system emoji default.


```go
// GlyphTool is the inline marker for completed tool calls.
// Single-cell, text-class (not emoji-class) so terminals
// render it in the foreground color we asked for instead of
// the system emoji default.
GlyphTool = "›"
```

---

#### `GlyphToolActive`

`const` · `tui/style.go:48` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`4bfe01b`](https://github.com/go-steer/core-tui/commit/4bfe01bb1261fbc0e3244d8256ba2cf9b1124978), 2026-05-26, _feat(tui-facelift): smaller tool glyph + active-call highlight + non-breaking footer_

GlyphToolActive is the inline marker for the in-flight
tool call (the most recent RoleTool that hasn't been
followed by any text yet). Solid right-pointer reads as
"currently running."


```go
// GlyphToolActive is the inline marker for the in-flight
// tool call (the most recent RoleTool that hasn't been
// followed by any text yet). Solid right-pointer reads as
// "currently running."
GlyphToolActive = "▶"
```

---

#### `GlyphToolDone`

`const` · `tui/style.go:50` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphToolDone = "✓"
```

---

#### `GlyphToolFail`

`const` · `tui/style.go:51` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphToolFail = "✗"
```

---

#### `GlyphToolPending`

`const` · `tui/style.go:49` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphToolPending = "○"
```

---

#### `GlyphTruncate`

`const` · `tui/style.go:61` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphTruncate = "…"
```

---

#### `GlyphUserPrompt`

`const` · `tui/style.go:55` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphUserPrompt = "❯"
```

---

#### `GlyphWarn`

`const` · `tui/style.go:54` · block `GlyphModel` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Glyph vocabulary (style.md §2). One anchor glyph per row, ever.


```go
GlyphWarn = "⚠"
```

---

#### `BrandCyan`

`var` · `tui/style.go:33` · block `BrandViolet` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Brand colors, fixed across light/dark backgrounds (style.md §1.1).
Hosts override AccentColor / SecondaryColor / CursorColor through
Options.Branding; the slate and cyan derive deterministically from
the brand line.


```go
BrandCyan = lipgloss.Color("#5FD7FF")
```

---

#### `BrandPink`

`var` · `tui/style.go:30` · block `BrandViolet` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Brand colors, fixed across light/dark backgrounds (style.md §1.1).
Hosts override AccentColor / SecondaryColor / CursorColor through
Options.Branding; the slate and cyan derive deterministically from
the brand line.


```go
BrandPink = lipgloss.Color("#FF79C6")
```

---

#### `BrandPinkBright`

`var` · `tui/style.go:31` · block `BrandViolet` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Brand colors, fixed across light/dark backgrounds (style.md §1.1).
Hosts override AccentColor / SecondaryColor / CursorColor through
Options.Branding; the slate and cyan derive deterministically from
the brand line.


```go
BrandPinkBright = lipgloss.Color("#FFB6E1")
```

---

#### `BrandSlate`

`var` · `tui/style.go:32` · block `BrandViolet` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Brand colors, fixed across light/dark backgrounds (style.md §1.1).
Hosts override AccentColor / SecondaryColor / CursorColor through
Options.Branding; the slate and cyan derive deterministically from
the brand line.


```go
BrandSlate = lipgloss.Color("#6272A4")
```

---

#### `BrandViolet`

`var` · `tui/style.go:29` · block `BrandViolet` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944), 2026-05-25, _feat(tui): visual-preview slice — interactive Bubble Tea harness_

Brand colors, fixed across light/dark backgrounds (style.md §1.1).
Hosts override AccentColor / SecondaryColor / CursorColor through
Options.Branding; the slate and cyan derive deterministically from
the brand line.


```go
BrandViolet = lipgloss.Color("#BD93F9")
```

---

#### `BuiltinTheme`

`type` · `tui/theme.go:480` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

BuiltinTheme describes one entry in the built-in theme registry —
the seed list that the /theme picker iterates and that
ThemeByName resolves against. Hosts that want to advertise a
custom theme today can apply it via Options.Branding overrides;
a future PR could extend this to accept host-registered themes.


```go
type BuiltinTheme struct {
	// Name is the canonical lower-case slug ("default", "google",
	// "gopher", "anthropic", "gemini", "openai"). Matched case-
	// insensitively by ThemeByName + /theme <name>.
	Name string
	// Description is a one-line palette summary shown in the
	// picker's row (muted style).
	Description string
	// Build is the constructor — called with the current dark
	// flag every time the theme is applied, so /theme transitions
	// pick up the correct foreground hierarchy without a restart.
	Build func(dark bool) Theme
}
```

<details>
<summary><b>3 exported members</b></summary>


**`Name`** — field · `tui/theme.go:484`

```go
Name string
```

Name is the canonical lower-case slug ("default", "google",
"gopher", "anthropic", "gemini", "openai"). Matched case-
insensitively by ThemeByName + /theme <name>.


**`Description`** — field · `tui/theme.go:487`

```go
Description string
```

Description is a one-line palette summary shown in the
picker's row (muted style).


**`Build`** — field · `tui/theme.go:491`

```go
Build func(dark bool) Theme
```

Build is the constructor — called with the current dark
flag every time the theme is applied, so /theme transitions
pick up the correct foreground hierarchy without a restart.


</details>

---

#### `Theme`

`type` · `tui/theme.go:40` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), 2026-05-26, _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_<br>declaration changed since: **v0.2.0** ([`68b9b3c`](https://github.com/go-steer/core-tui/commit/68b9b3cb05522b29e82de88a2a2c32ccc5df568f), _feat(inline-tool-display): phase 3 — bg-tinted diff lines + line-number gutter + per-line byte cap_); **v0.2.0** ([`1d8ed22`](https://github.com/go-steer/core-tui/commit/1d8ed22800a1b11eec32f5c19f9600fc62142c32), _feat(inline-tool-display): tiered gutter bg + lexer cache (diffview.md §1, §4)_); **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_)

Theme is the bundle of semantic color tokens. ~15 fields drive
every styled component in the TUI. New themes are typically
~15 lines of `lipgloss.Color("#...")` plus the Builtin/Per-
Provider constructor.


```go
type Theme struct {
	// ... 22 exported members, documented below
}
```

<details>
<summary><b>22 exported members</b></summary>


**`Name`** — field · `tui/theme.go:41`

```go
Name string
```

_No doc comment._


**`Primary`** — field · `tui/theme.go:44`

```go
Primary color.Color
```

Brand: drive the wordmark, accents, and primary highlights.


**`Secondary`** — field · `tui/theme.go:45`

```go
Secondary color.Color
```

agent identity, model-picker focus


**`Accent`** — field · `tui/theme.go:46`

```go
Accent color.Color
```

section heads, palette selection


**`Success`** — field · `tui/theme.go:49`

```go
Success color.Color
```

Semantic signal: drive feedback rows + chip/badge states.


**`Warning`** — field · `tui/theme.go:50`

```go
Warning color.Color
```

permission warn, rate-limit notices


**`Error`** — field · `tui/theme.go:51`

```go
Error color.Color
```

error rows, denied permissions


**`Info`** — field · `tui/theme.go:52`

```go
Info color.Color
```

system rows, hints


**`FgBase`** — field · `tui/theme.go:55`

```go
FgBase color.Color
```

Foreground hierarchy: most→least prominent text.


**`FgMuted`** — field · `tui/theme.go:56`

```go
FgMuted color.Color
```

hints, labels, separators


**`FgSubtle`** — field · `tui/theme.go:57`

```go
FgSubtle color.Color
```

backgrounded text, disabled


**`BgBase`** — field · `tui/theme.go:61`

```go
BgBase color.Color
```

Background tiers: rarely used but available for surfaces
that need a tinted backdrop (code fences, dialog body).


**`BgElevated`** — field · `tui/theme.go:62`

```go
BgElevated color.Color
```

dialog / modal body


**`BgOverlay`** — field · `tui/theme.go:63`

```go
BgOverlay color.Color
```

tooltip / floater


**`BorderActive`** — field · `tui/theme.go:66`

```go
BorderActive color.Color
```

Borders + rules.


**`BorderQuiet`** — field · `tui/theme.go:67`

```go
BorderQuiet color.Color
```

sidebar dividers, message rules


**`DiffAddBg`** — field · `tui/theme.go:76` · added in **v0.2.0** ([`68b9b3c`](https://github.com/go-steer/core-tui/commit/68b9b3cb05522b29e82de88a2a2c32ccc5df568f))

```go
DiffAddBg color.Color
```

Diff surfaces: dim tints used as backgrounds behind + / -
lines in inline tool-display diffs. Foreground stays
Success / Error; the bg makes the change region scannable
at a glance the way `git diff --color` and GitHub render.
The *GutterBg variants are a step deeper than the code
bg so the line-number column reads as a distinct "rail"
next to the change region (pattern from docs/diffview.md §1).


**`DiffDelBg`** — field · `tui/theme.go:77` · added in **v0.2.0** ([`68b9b3c`](https://github.com/go-steer/core-tui/commit/68b9b3cb05522b29e82de88a2a2c32ccc5df568f))

```go
DiffDelBg color.Color
```

_No doc comment._


**`DiffAddGutterBg`** — field · `tui/theme.go:78` · added in **v0.2.0** ([`1d8ed22`](https://github.com/go-steer/core-tui/commit/1d8ed22800a1b11eec32f5c19f9600fc62142c32))

```go
DiffAddGutterBg color.Color
```

_No doc comment._


**`DiffDelGutterBg`** — field · `tui/theme.go:79` · added in **v0.2.0** ([`1d8ed22`](https://github.com/go-steer/core-tui/commit/1d8ed22800a1b11eec32f5c19f9600fc62142c32))

```go
DiffDelGutterBg color.Color
```

_No doc comment._


**`WordmarkSequence`** — field · `tui/theme.go:89` · added in **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501))

```go
WordmarkSequence []color.Color
```

WordmarkSequence, when non-nil, causes the brand wordmark
to render with one color per rune from this slice (cycling
when the wordmark is longer than the sequence). The Google
theme uses this to mimic the iconic B-R-Y-B-G-R logo
sequence — the single visual signature no palette
distribution alone can produce. Nil falls back to the
single-color Primary render (Styles.Wordmark style), which
is what every other theme should keep doing.


**`PromptGlyph`** — field · `tui/theme.go:99` · added in **v0.7.0** ([`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501))

```go
PromptGlyph string
```

PromptGlyph overrides the textarea's left-edge prompt rail
for themes whose identity has a distinctive glyph (e.g. GKE
uses ⎈ , the Unicode helm-symbol, since GKE is Kubernetes).
Empty (zero value) keeps the house default "▎ " — every
theme that doesn't have a glyph identity should leave this
empty. The glyph picks up the active prompt color from
Styles.Focused.Prompt, so foreground color is theme-
controlled regardless.


</details>

---

#### `AnthropicTheme`

`func` · `tui/theme.go:153` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), 2026-05-26, _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_

AnthropicTheme tints toward Claude's warm orange identity. Used
when the host's StatusReporter reports Provider == "anthropic".


```go
func AnthropicTheme(dark bool) Theme
```

---

#### `BuiltinThemes`

`func` · `tui/theme.go:504` · block `BuiltinTheme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

BuiltinThemes returns the seed registry in display order. The
picker shows them in this exact order, grouped:

  - "default" first as the neutral baseline.
  - Brand themes (google / gopher) — operator-facing identities.
  - Per-provider variants (anthropic / gemini / openai) — auto-
    applied by ThemeForProvider; available manually too.
  - Fun / show-off themes leveraging the multicolor wordmark
    (matrix / pride / cyberpunk / vaporwave / christmas) — pure
    personality, last so the "serious" set is scannable first.


```go
func BuiltinThemes() []BuiltinTheme
```

---

#### `ChristmasTheme`

`func` · `tui/theme.go:453` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

ChristmasTheme — red + green + gold festive. Wordmark
alternates R-G-R-G-R-G. Use in December (or whenever). Doubles
as a perfectly-themed diff palette (red = remove, green = add
matches the chrome).


```go
func ChristmasTheme(dark bool) Theme
```

---

#### `CyberpunkTheme`

`func` · `tui/theme.go:392` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

CyberpunkTheme paints a deep-magenta chrome with neon
yellow/cyan/magenta accents. The wordmark cycles Y-C-M-Y-C-M
for an arcade-marquee feel. Loud on purpose — this is the
"I'm hacking the planet" theme.


```go
func CyberpunkTheme(dark bool) Theme
```

---

#### `DefaultTheme`

`func` · `tui/theme.go:113` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), 2026-05-26, _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_

DefaultTheme returns the canonical "core-tui" palette — the
purple-pink Dracula-adjacent identity used by the visual-
preview slice and inherited by core-agent's launchTUIv2 today.
`dark` flips the foreground hierarchy so light terminals stay
readable.


```go
func DefaultTheme(dark bool) Theme
```

---

#### `GKETheme`

`func` · `tui/theme.go:267` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

GKETheme is the Google Kubernetes Engine variant of the Google
theme. Two GKE-specific signatures vs plain Google:

 1. Wordmark cycles R-B-G-Y (the GKE icon's clockwise quadrant
    order: top-red, right-blue, bottom-green, left-yellow)
    instead of Google's B-R-Y-B-G-R logo letter order. Anyone
    who's seen the GKE hexagonal icon will recognize the
    sequence.
 2. Prompt glyph is ⎈ (U+2388 HELM SYMBOL), the Unicode K8s
    logo character. Replaces the house ▎ prompt rail so every
    input row carries the Kubernetes signature.

Everything else (chrome, signal colors, focus ring, diff bgs)
inherits from GoogleTheme — GKE IS a Google product, the brand
chrome should match.


```go
func GKETheme(dark bool) Theme
```

---

#### `GeminiTheme`

`func` · `tui/theme.go:165` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), 2026-05-26, _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_

GeminiTheme tints toward Google's blue/teal palette. Used for
Provider == "gemini" / "vertex".


```go
func GeminiTheme(dark bool) Theme
```

---

#### `GoogleTheme`

`func` · `tui/theme.go:209` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

GoogleTheme paints the surface in Google's full brand palette
from the 15-color Google News set, distributing all five logo
hues across the decorative + signal slots so the surface reads
as a Google product (Search / Maps / Drive chrome) rather than
a single-hue blue tint:

  - Primary deep blue (#174EA6) stamps brand identity on the
    wordmark (authoritative; mirrors the dark blue used in the
    Google logo).
  - BorderActive Medium blue (#4285F4) frames focused inputs in
    a lighter blue so the focus ring sits visually distinct
    from the deeper-blue identity above.
  - Accent yellow (#FBBC04) makes section heads + palette
    selection pop — yellow is high-contrast against the
    blue/red base.
  - Warning brand orange (#E37400) keeps Warning visually
    separated from Accent yellow so a yellow header doesn't
    read as "warning" at a glance.

Success green and Error red stay on the medium-tone brand
variants for readable foreground text on dark backdrops.
Diff bgs flip with dark/light so light terminals stay readable.


```go
func GoogleTheme(dark bool) Theme
```

---

#### `GopherTheme`

`func` · `tui/theme.go:284` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

GopherTheme paints the surface in the Go brand palette from the
Go Brand Book (Gopher Blue → Aqua gradient, Fuchsia / Yellow
secondaries). Source: cogo-wasm2/docs/color-palette.md.


```go
func GopherTheme(dark bool) Theme
```

---

#### `MatrixTheme`

`func` · `tui/theme.go:330` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

MatrixTheme paints the surface in green-on-black phosphor —
terminal-hacker aesthetic. The wordmark cycles 6 shades of
green light-to-dim, mimicking the "rain head" → trailing
tail look from the films. Error stays CRT-red so failures
pop hard against the monochromatic green base.


```go
func MatrixTheme(dark bool) Theme
```

---

#### `OpenAITheme`

`func` · `tui/theme.go:177` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), 2026-05-26, _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_

OpenAITheme tints toward OpenAI's green identity. Used for
Provider == "openai".


```go
func OpenAITheme(dark bool) Theme
```

---

#### `PrideTheme`

`func` · `tui/theme.go:362` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

PrideTheme paints a neutral violet chrome with a full
rainbow-flag wordmark. The 6 flag colors R-O-Y-G-B-V land
on the wordmark; body text stays calm so the rainbow is a
signature, not a constant assault.


```go
func PrideTheme(dark bool) Theme
```

---

#### `ThemeByName`

`func` · `tui/theme.go:526` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

ThemeByName resolves a case-insensitive name against the
builtin registry and returns the constructed Theme. Unknown
names fall back to DefaultTheme so a stale persisted name or
a typo in /theme <name> never strands the operator on a
half-painted UI.


```go
func ThemeByName(name string, dark bool) Theme
```

---

#### `ThemeForProvider`

`func` · `tui/theme.go:539` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca), 2026-05-26, _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_

ThemeForProvider returns the per-provider theme variant for
the given provider tag, or DefaultTheme on empty / unknown.
The provider string is matched case-insensitively and tolerates
vendor suffixes ("anthropic-vertex" → anthropic).


```go
func ThemeForProvider(provider string, dark bool) Theme
```

---

#### `VaporwaveTheme`

`func` · `tui/theme.go:421` · block `Theme` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

VaporwaveTheme paints a pink/purple/cyan synthwave palette.
The wordmark gradients pink→cyan across 6 stops for that
80s-Miami-poolside-screensaver feel. Less aggressive than
Cyberpunk; same chromatic family but softer.


```go
func VaporwaveTheme(dark bool) Theme
```

---

#### `DefaultPromptGlyph`

`const` · `tui/theme.go:106` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.7.0** — [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501), 2026-06-06, _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_

DefaultPromptGlyph is the house textarea prompt rail used by
every theme that doesn't set Theme.PromptGlyph. A thin vertical
half-block + space gives a 2-cell-wide focus marker that
doesn't shift the textarea's column position on theme swap.


```go
DefaultPromptGlyph = "▎ "
```

---

### 8.8 Transcripts

_Files: `tui/transcript.go`_

#### `Transcript`

`type` · `tui/transcript.go:52` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1), 2026-05-25, _feat(tui-parity): tier 1 — UX blockers (viewport scroll, glamour heads, mouse, ctrl-c, /clear confirm, prompt history, AlwaysAllow, transcript)_

Transcript is the on-disk session record.


```go
type Transcript struct {
	Version   int             `json:"version"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   time.Time       `json:"ended_at"`
	Model     string          `json:"model"`
	Messages  []TranscriptMsg `json:"messages"`
	Usage     TranscriptUsage `json:"usage"`
}
```

<details>
<summary><b>6 exported members</b></summary>


**`Version`** — field · `tui/transcript.go:53`

```go
Version int `json:"version"`
```

_No doc comment._


**`StartedAt`** — field · `tui/transcript.go:54`

```go
StartedAt time.Time `json:"started_at"`
```

_No doc comment._


**`EndedAt`** — field · `tui/transcript.go:55`

```go
EndedAt time.Time `json:"ended_at"`
```

_No doc comment._


**`Model`** — field · `tui/transcript.go:56`

```go
Model string `json:"model"`
```

_No doc comment._


**`Messages`** — field · `tui/transcript.go:57`

```go
Messages []TranscriptMsg `json:"messages"`
```

_No doc comment._


**`Usage`** — field · `tui/transcript.go:58`

```go
Usage TranscriptUsage `json:"usage"`
```

_No doc comment._


</details>

---

#### `TranscriptInfo`

`type` · `tui/transcript.go:312` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`00bc303`](https://github.com/go-steer/core-tui/commit/00bc30313ffae248824875c97c1724af847c86ff), 2026-05-26, _feat(tui-facelift): tier C — /resume slash + session transcript restore_

TranscriptInfo is one entry in the /resume picker.


```go
type TranscriptInfo struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
}
```

<details>
<summary><b>4 exported members</b></summary>


**`Path`** — field · `tui/transcript.go:313`

```go
Path string
```

_No doc comment._


**`Name`** — field · `tui/transcript.go:314`

```go
Name string
```

_No doc comment._


**`Size`** — field · `tui/transcript.go:315`

```go
Size int64
```

_No doc comment._


**`ModTime`** — field · `tui/transcript.go:316`

```go
ModTime time.Time
```

_No doc comment._


</details>

---

#### `TranscriptMsg`

`type` · `tui/transcript.go:71` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1), 2026-05-25, _feat(tui-parity): tier 1 — UX blockers (viewport scroll, glamour heads, mouse, ctrl-c, /clear confirm, prompt history, AlwaysAllow, transcript)_<br>declaration changed since: **v0.9.0** ([`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186), _fix(transcript): preserve tool-call fields on session save (v2 schema) (#45)_)

TranscriptMsg is one entry in the chat. Role uses the lowercase
string form ("user" / "assistant" / "system" / "error" / "tool" /
"notice") so consumers don't have to import the package's enum.

Tool-call fields (ToolName, ToolArgs, ToolPreview, ToolCallID) are
populated only when Role == "tool". For other roles they're empty
and omitted from the JSON via omitempty. Text is intentionally
empty for tool rows — the in-memory renderer assembles the visible
row from ToolName/ToolArgs/ToolPreview rather than a single string,
and that structure is preserved here.


```go
type TranscriptMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`

	// ToolName is the tool's canonical name (e.g. "read_file",
	// "mcp.gke.list_clusters"). Populated when Role == "tool".
	ToolName string `json:"tool_name,omitempty"`

	// ToolArgs is the JSON-serialized call arguments (or a
	// human-readable rendering when JSON serialization isn't
	// available). Populated when Role == "tool".
	ToolArgs string `json:"tool_args,omitempty"`

	// ToolPreview is the pre-rendered multi-line block the renderer
	// shows under the tool row — a unified diff for edit_file, a
	// read-scope summary, a result excerpt, etc. Populated when
	// Role == "tool"; empty when the tool call has no preview yet
	// (call-only row, before the result arrived).
	ToolPreview string `json:"tool_preview,omitempty"`

	// ToolCallID is the wire-level identifier the host emitted for
	// this call (e.g. genai.FunctionCall.ID). Populated when
	// Role == "tool" and the host supplied an ID. Useful for
	// cross-referencing the transcript against the host's audit log.
	ToolCallID string `json:"tool_call_id,omitempty"`
}
```

<details>
<summary><b>6 exported members</b></summary>


**`Role`** — field · `tui/transcript.go:72`

```go
Role string `json:"role"`
```

_No doc comment._


**`Text`** — field · `tui/transcript.go:73`

```go
Text string `json:"text"`
```

_No doc comment._


**`ToolName`** — field · `tui/transcript.go:77` · added in **v0.9.0** ([`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186))

```go
ToolName string `json:"tool_name,omitempty"`
```

ToolName is the tool's canonical name (e.g. "read_file",
"mcp.gke.list_clusters"). Populated when Role == "tool".


**`ToolArgs`** — field · `tui/transcript.go:82` · added in **v0.9.0** ([`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186))

```go
ToolArgs string `json:"tool_args,omitempty"`
```

ToolArgs is the JSON-serialized call arguments (or a
human-readable rendering when JSON serialization isn't
available). Populated when Role == "tool".


**`ToolPreview`** — field · `tui/transcript.go:89` · added in **v0.9.0** ([`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186))

```go
ToolPreview string `json:"tool_preview,omitempty"`
```

ToolPreview is the pre-rendered multi-line block the renderer
shows under the tool row — a unified diff for edit_file, a
read-scope summary, a result excerpt, etc. Populated when
Role == "tool"; empty when the tool call has no preview yet
(call-only row, before the result arrived).


**`ToolCallID`** — field · `tui/transcript.go:95` · added in **v0.9.0** ([`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186))

```go
ToolCallID string `json:"tool_call_id,omitempty"`
```

ToolCallID is the wire-level identifier the host emitted for
this call (e.g. genai.FunctionCall.ID). Populated when
Role == "tool" and the host supplied an ID. Useful for
cross-referencing the transcript against the host's audit log.


</details>

---

#### `TranscriptUsage`

`type` · `tui/transcript.go:99` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1), 2026-05-25, _feat(tui-parity): tier 1 — UX blockers (viewport scroll, glamour heads, mouse, ctrl-c, /clear confirm, prompt history, AlwaysAllow, transcript)_

TranscriptUsage mirrors the host UsageTracker's session totals.


```go
type TranscriptUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}
```

<details>
<summary><b>3 exported members</b></summary>


**`InputTokens`** — field · `tui/transcript.go:100`

```go
InputTokens int `json:"input_tokens"`
```

_No doc comment._


**`OutputTokens`** — field · `tui/transcript.go:101`

```go
OutputTokens int `json:"output_tokens"`
```

_No doc comment._


**`CostUSD`** — field · `tui/transcript.go:102`

```go
CostUSD float64 `json:"cost_usd"`
```

_No doc comment._


</details>

---

#### `ListTranscripts`

`func` · `tui/transcript.go:276` · block `TranscriptInfo` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`00bc303`](https://github.com/go-steer/core-tui/commit/00bc30313ffae248824875c97c1724af847c86ff), 2026-05-26, _feat(tui-facelift): tier C — /resume slash + session transcript restore_

ListTranscripts returns every transcript file under
<agentsDir>/sessions, most-recent first by modification time.
Empty agentsDir or missing dir returns ([], nil) — no error,
just no sessions to surface.


```go
func ListTranscripts(agentsDir string) ([]TranscriptInfo, error)
```

---

#### `LoadTranscript`

`func` · `tui/transcript.go:260` · block `Transcript` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`00bc303`](https://github.com/go-steer/core-tui/commit/00bc30313ffae248824875c97c1724af847c86ff), 2026-05-26, _feat(tui-facelift): tier C — /resume slash + session transcript restore_

LoadTranscript reads a transcript JSON file from disk. Returns
the decoded Transcript ready for ApplyTranscript. Errors
propagate as-is so the caller (slash dispatcher) can surface
them inline.


```go
func LoadTranscript(path string) (Transcript, error)
```

---

#### `TranscriptSchemaVersion`

`const` · `tui/transcript.go:49` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1), 2026-05-25, _feat(tui-parity): tier 1 — UX blockers (viewport scroll, glamour heads, mouse, ctrl-c, /clear confirm, prompt history, AlwaysAllow, transcript)_<br>declaration changed since: **v0.9.0** ([`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186), _fix(transcript): preserve tool-call fields on session save (v2 schema) (#45)_)

TranscriptSchemaVersion is the on-disk schema version. Bump when
the JSON shape changes in a non-backwards-compatible way.

v2 (2026-06-09): TranscriptMsg gained optional tool-call fields
(tool_name, tool_args, tool_preview, tool_call_id). Backwards-
compatible: v1 files load fine (the new fields default to empty)
and v2 files written by newer code load fine in older readers
(json.Unmarshal silently drops unknown fields). The version bump
is a signal to consumers that tool rows now carry their structured
data instead of serializing as {role: "tool", text: ""}.


```go
TranscriptSchemaVersion = 2
```

---

### 8.9 Terminal capabilities

_Files: `tui/terminal_caps.go`_

#### `TerminalCapabilities`

`type` · `tui/terminal_caps.go:36` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`db4a297`](https://github.com/go-steer/core-tui/commit/db4a2974e1a72dd14d49d1ee72954238be7a651e), 2026-05-26, _feat(tui-facelift): tier C — terminal capability detection_

TerminalCapabilities is the bag of optional terminal features
the TUI knows how to exploit when present and degrade past when
absent. Zero value = "assume nothing supported" (safe default).


```go
type TerminalCapabilities struct {
	// TrueColor is true when the terminal advertises 24-bit color
	// via COLORTERM. Lipgloss v2 picks the best output path
	// regardless, but renderers that want to gate gradient/blend
	// effects on real truecolor support read this bit.
	TrueColor bool

	// Hyperlinks reports whether OSC 8 hyperlinks should render
	// as actual clickable terminal hyperlinks. Sniff by allowlist
	// because there's no portable query — common modern emulators
	// (kitty, iTerm2, wezterm, foot, alacritty 0.14+, vte 0.50+,
	// vscode integrated) support them.
	Hyperlinks bool

	// Clipboard reports whether OSC 52 "set clipboard" sequences
	// are likely honored. Mostly the same allowlist as Hyperlinks;
	// users still need to enable the feature in their term config.
	Clipboard bool

	// KittyGraphics reports whether the terminal supports the
	// Kitty graphics protocol for inline images. Reserved for
	// future image-rendering paths.
	KittyGraphics bool

	// TermProgram is the canonical name of the terminal program
	// when known (TERM_PROGRAM / KITTY_WINDOW_ID / WT_SESSION /
	// VSCODE_PID etc.). Used by other capability checks; surfaced
	// so the host can log it.
	TermProgram string
}
```

<details>
<summary><b>6 exported members</b></summary>


**`TrueColor`** — field · `tui/terminal_caps.go:41`

```go
TrueColor bool
```

TrueColor is true when the terminal advertises 24-bit color
via COLORTERM. Lipgloss v2 picks the best output path
regardless, but renderers that want to gate gradient/blend
effects on real truecolor support read this bit.


**`Hyperlinks`** — field · `tui/terminal_caps.go:48`

```go
Hyperlinks bool
```

Hyperlinks reports whether OSC 8 hyperlinks should render
as actual clickable terminal hyperlinks. Sniff by allowlist
because there's no portable query — common modern emulators
(kitty, iTerm2, wezterm, foot, alacritty 0.14+, vte 0.50+,
vscode integrated) support them.


**`Clipboard`** — field · `tui/terminal_caps.go:53`

```go
Clipboard bool
```

Clipboard reports whether OSC 52 "set clipboard" sequences
are likely honored. Mostly the same allowlist as Hyperlinks;
users still need to enable the feature in their term config.


**`KittyGraphics`** — field · `tui/terminal_caps.go:58`

```go
KittyGraphics bool
```

KittyGraphics reports whether the terminal supports the
Kitty graphics protocol for inline images. Reserved for
future image-rendering paths.


**`TermProgram`** — field · `tui/terminal_caps.go:64`

```go
TermProgram string
```

TermProgram is the canonical name of the terminal program
when known (TERM_PROGRAM / KITTY_WINDOW_ID / WT_SESSION /
VSCODE_PID etc.). Used by other capability checks; surfaced
so the host can log it.


**`Hyperlink`** — method · `tui/terminal_caps.go:140`

```go
func (c TerminalCapabilities) Hyperlink(url, s string) string
```

Hyperlink renders s as an OSC 8 hyperlink to url when the
capability is supported, otherwise returns s unchanged. Lets
renderers always call Hyperlink without branching themselves.


</details>

---

#### `DetectCapabilities`

`func` · `tui/terminal_caps.go:71` · block `TerminalCapabilities` · contract closure: **outside** · core-agent: **unused**  
introduced in **v0.1.0** — [`db4a297`](https://github.com/go-steer/core-tui/commit/db4a2974e1a72dd14d49d1ee72954238be7a651e), 2026-05-26, _feat(tui-facelift): tier C — terminal capability detection_

DetectCapabilities probes the environment once and returns the
best-guess capability bag. Called from NewModel; hosts can
override on Model.caps after NewModel returns if they have a
better signal.


```go
func DetectCapabilities() TerminalCapabilities
```

---

---

## 9. Appendices

### 9.1 Appendix A — every declaration that changed after introduction

Keys whose declaration today is not the one they shipped with. Two are signature-breaking method changes; the rest are struct growth, which is non-breaking only because the documented usage is a keyed struct literal. After the freeze, every row in this table becomes a major-version event.

| symbol | introduced | later changes |
|---|---|---|
| `Branding` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | **v0.6.2** [`0086682`](https://github.com/go-steer/core-tui/commit/008668259ba74f8485f2d31d1edacb4c8e6f703b) — _feat(banner): drop cursor block; add Branding.AgentIdentity segment_ |
| `DialogAction` | v0.1.0 [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf) | **v0.7.0** [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) — _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_ |
| `Event` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | **v0.1.0** [`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd) — _feat(tui): real /mcp /tools /skills + cost + scrollable palette_<br>**v0.3.0** [`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc) — _feat(tool-results): wire ToolResult events through the agent → TUI flow_<br>**v0.9.0** [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) — _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_ |
| `GlyphTool` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | **v0.1.0** [`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2) — _fix(tui): focus + wrap + tool-gear + per-turn usage + sidebar fallback_<br>**v0.1.0** [`4bfe01b`](https://github.com/go-steer/core-tui/commit/4bfe01bb1261fbc0e3244d8256ba2cf9b1124978) — _feat(tui-facelift): smaller tool glyph + active-call highlight + non-breaking footer_ |
| `History.SetToolResult` | v0.12.0 [`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc) | **v0.12.0** [`6622f3a`](https://github.com/go-steer/core-tui/commit/6622f3a2fc366a529732a6df986b04ecbd1449a4) — _feat(tui): consume per-tool-call latency_ms + spec bump v1.2.0 (#62)_<br>**v0.14.0** [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924) — _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_ |
| `MCPServerInfo` | v0.1.0 [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a) | **v0.1.0** [`5d052e8`](https://github.com/go-steer/core-tui/commit/5d052e8845ef46f758bb36ed389d04cb5ffac4dd) — _feat(tui): real /mcp /tools /skills + cost + scrollable palette_ |
| `MemoryFile` | v0.1.0 [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a) | **v0.1.0** [`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3) — _feat(tui-parity): tier 3 — polish (cursor + cwd + provider, ctrl-l/u, dynamic thinking, drill-in, /stats turns+duration, /memory bytes, perm echo, aliases, indent, elicit desc)_ |
| `Message` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | **v0.1.0** [`3ed6693`](https://github.com/go-steer/core-tui/commit/3ed6693b151a58482cc7aefd6b72aa10424e9bc2) — _fix(tui): focus + wrap + tool-gear + per-turn usage + sidebar fallback_<br>**v0.1.0** [`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27) — _feat(tui-facelift): tier A — lazy list with Versioned items + per-item cache_<br>**v0.2.0** [`9ccdedb`](https://github.com/go-steer/core-tui/commit/9ccdedbc77b9d9198cfdce7ab4a92aaaf1adc127) — _feat(inline-tool-display): phase 1 — unified diff previews for apply_patch + edit_file_<br>**v0.3.0** [`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc) — _feat(tool-results): wire ToolResult events through the agent → TUI flow_<br>**v0.6.0** [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f) — _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_<br>**v0.9.0** [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) — _feat(remote): consume SSE event-stream protocol v1.1.0 — closes #40 (#43)_<br>**v0.12.0** [`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc) — _feat(tui): expandable tool-call detail overlay + verbose flag (tiers 1+2 of #52) (#61)_<br>**v0.12.0** [`6622f3a`](https://github.com/go-steer/core-tui/commit/6622f3a2fc366a529732a6df986b04ecbd1449a4) — _feat(tui): consume per-tool-call latency_ms + spec bump v1.2.0 (#62)_<br>**v0.14.0** [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924) — _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_ |
| `Options` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | **v0.1.0** [`6bbee7e`](https://github.com/go-steer/core-tui/commit/6bbee7e14b4e16a7ef355b81eaa7f7281cac2ecb) — _feat(tui): persist user's status-layout choice across sessions_<br>**v0.1.0** [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5) — _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_<br>**v0.1.0** [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) — _feat(tui): PermissionPrompter + Elicitor implementations (PR 6/N)_<br>**v0.1.0** [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a) — _feat(tui): land remaining §3.3 capability surface (PR 7)_<br>**v0.1.0** [`41e2e51`](https://github.com/go-steer/core-tui/commit/41e2e51d11e791a0309d96336131a72b1f29b7bd) — _feat(tui): add Options.PersistModelChoice (R-MOD-3)_<br>**v0.1.0** [`2193696`](https://github.com/go-steer/core-tui/commit/21936966b0c07480dac078a97c6a9c356badeab0) — _fix(tui-facelift): per-provider theming is now opt-in (default = brand palette)_<br>**v0.1.0** [`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5) — _feat(tui-facelift): configurable permission layout (inline default, overlay opt-in)_<br>**v0.5.0** [`1b9845c`](https://github.com/go-steer/core-tui/commit/1b9845ca2dedee13181dd536fe944779f191c93e) — _feat(options): expose ForceTheme + Mouse hooks; /mouse becomes a real toggle_<br>**v0.6.0** [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f) — _feat(injection): AutoContinueFromInbox mode for opaque-runner hosts (#9)_<br>**v0.7.0** [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) — _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_<br>**v0.8.0** [`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b) — _feat(notifier): out-of-band Notify() side channel for host-initiated chat rows (#30)_<br>**v0.12.0** [`aa5cc80`](https://github.com/go-steer/core-tui/commit/aa5cc801aa7fecc6c723f6bfbb5d943eff8599fc) — _feat(tui): expandable tool-call detail overlay + verbose flag (tiers 1+2 of #52) (#61)_<br>**v0.13.0** [`926e722`](https://github.com/go-steer/core-tui/commit/926e72244cf84edd2dce1a4100e95261487e9638) — _feat(tui): add Options.InitialPrompt for seeded interactive sessions (#63)_ |
| `Overlay.HandleKey` | v0.1.0 [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf) | **v0.7.0** [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) — _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_ |
| `QueueEntry` | v0.1.0 [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158) | **v0.1.0** [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5) — _feat(tui): InjectableAgent + MidTurnInjectionMode (R-CHAT-11, PR 3/5)_ |
| `SessionInfo` | v0.10.0 [`b08dac6`](https://github.com/go-steer/core-tui/commit/b08dac6868e2fe588014d4d0c12fadc5aba908f1) | **v0.18.0** [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9) — _feat(tui): text-input Dialog primitive + session-picker action rows (#56) (#72)_ |
| `SlashResult` | v0.1.0 [`75b7faa`](https://github.com/go-steer/core-tui/commit/75b7faae5021e9cb562cf55c8ff8a092dfaab37d) | **v0.10.0** [`b08dac6`](https://github.com/go-steer/core-tui/commit/b08dac6868e2fe588014d4d0c12fadc5aba908f1) — _feat(switch): mid-session Agent swap via SlashResult.SwitchTo + /switch built-in (#54)_ |
| `Status` | v0.1.0 [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a) | **v0.1.0** [`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3) — _feat(tui-parity): tier 3 — polish (cursor + cwd + provider, ctrl-l/u, dynamic thinking, drill-in, /stats turns+duration, /memory bytes, perm echo, aliases, indent, elicit desc)_ |
| `Styles` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | **v0.1.0** [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) — _feat(tui-facelift): tier B — semantic theme tokens + per-provider themes_<br>**v0.8.0** [`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b) — _feat(notifier): out-of-band Notify() side channel for host-initiated chat rows (#30)_ |
| `Theme` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | **v0.2.0** [`68b9b3c`](https://github.com/go-steer/core-tui/commit/68b9b3cb05522b29e82de88a2a2c32ccc5df568f) — _feat(inline-tool-display): phase 3 — bg-tinted diff lines + line-number gutter + per-line byte cap_<br>**v0.2.0** [`1d8ed22`](https://github.com/go-steer/core-tui/commit/1d8ed22800a1b11eec32f5c19f9600fc62142c32) — _feat(inline-tool-display): tiered gutter bg + lexer cache (diffview.md §1, §4)_<br>**v0.7.0** [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) — _feat(theme): add /theme picker, 7 new themes, multicolor wordmark + prompt glyph mechanisms_ |
| `ToolResult` | v0.3.0 [`9237dcb`](https://github.com/go-steer/core-tui/commit/9237dcb173086b2b1d350cee9900136792a1febc) | **v0.12.0** [`6622f3a`](https://github.com/go-steer/core-tui/commit/6622f3a2fc366a529732a6df986b04ecbd1449a4) — _feat(tui): consume per-tool-call latency_ms + spec bump v1.2.0 (#62)_<br>**v0.14.0** [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924) — _feat(tool-savings): render digest-wrap savings on tool rows + dialog (#64)_ |
| `TranscriptMsg` | v0.1.0 [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1) | **v0.9.0** [`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186) — _fix(transcript): preserve tool-call fields on session save (v2 schema) (#45)_ |
| `TranscriptSchemaVersion` | v0.1.0 [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1) | **v0.9.0** [`107c9bd`](https://github.com/go-steer/core-tui/commit/107c9bd404b22766352a64d1edc6bdc55f587186) — _fix(transcript): preserve tool-call fields on session save (v2 schema) (#45)_ |
| `UsageTracker` | v0.1.0 [`164ad3c`](https://github.com/go-steer/core-tui/commit/164ad3c7159dd912af1326d69a4c610329dc384a) | **v0.1.0** [`a32cfdd`](https://github.com/go-steer/core-tui/commit/a32cfdd268b2bb5b0912d5591c207e7f2e49cad3) — _feat(tui-parity): tier 3 — polish (cursor + cwd + provider, ctrl-l/u, dynamic thinking, drill-in, /stats turns+duration, /memory bytes, perm echo, aliases, indent, elicit desc)_ |
| `UsageUpdate` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | **v0.10.2** [`f1162dd`](https://github.com/go-steer/core-tui/commit/f1162ddd0176ad293c45fb02fe39de2fbafd9a13) — _fix(observer): stamp per-turn footer on tail assistant Message so LiveAgent mode renders it (#58)_ |

### 9.2 Appendix B — outside the contract closure and unused by core-agent

Quadrant A: the unexport candidates. The "group" column is the split described in §6.2; it is this audit's recommendation, not a decision.

| symbol | kind | location | introduced | group |
|---|---|---|---|---|
| `AnthropicTheme` | func | `tui/theme.go:153` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | 2 · theming |
| `BrandCyan` | var | `tui/style.go:33` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `BrandPink` | var | `tui/style.go:30` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `BrandPinkBright` | var | `tui/style.go:31` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `BrandSlate` | var | `tui/style.go:32` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `BrandViolet` | var | `tui/style.go:29` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `BuiltinTheme` | type | `tui/theme.go:480` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `BuiltinThemes` | func | `tui/theme.go:504` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `ChristmasTheme` | func | `tui/theme.go:453` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `CyberpunkTheme` | func | `tui/theme.go:392` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `DefaultAutoContinueCap` | const | `tui/options.go:278` | v0.6.0 [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f) | 1 · incidental |
| `DefaultPromptGlyph` | const | `tui/theme.go:106` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 1 · incidental |
| `DefaultTheme` | func | `tui/theme.go:113` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | 2 · theming |
| `DetectCapabilities` | func | `tui/terminal_caps.go:71` | v0.1.0 [`db4a297`](https://github.com/go-steer/core-tui/commit/db4a2974e1a72dd14d49d1ee72954238be7a651e) | 1 · incidental |
| `Dialog` | type | `tui/dialog.go:39` | v0.1.0 [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf) | 3 · render extension |
| `DialogAction` | type | `tui/dialog.go:80` | v0.1.0 [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf) | 3 · render extension |
| `Focusable` | type | `tui/listcache.go:77` | v0.1.0 [`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27) | 3 · render extension |
| `GKETheme` | func | `tui/theme.go:267` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `GeminiTheme` | func | `tui/theme.go:165` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | 2 · theming |
| `GlyphAutoContinue` | const | `tui/style.go:60` | v0.6.0 [`fde95cf`](https://github.com/go-steer/core-tui/commit/fde95cf257712541b8402e9ae85bf5adcb05e34f) | 1 · incidental |
| `GlyphCollapsed` | const | `tui/style.go:52` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphColumn` | const | `tui/style.go:65` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphCursor` | const | `tui/style.go:63` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphExpanded` | const | `tui/style.go:53` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphModel` | const | `tui/style.go:38` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphRule` | const | `tui/style.go:64` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphSeparator` | const | `tui/style.go:62` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphTool` | const | `tui/style.go:43` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphToolActive` | const | `tui/style.go:48` | v0.1.0 [`4bfe01b`](https://github.com/go-steer/core-tui/commit/4bfe01bb1261fbc0e3244d8256ba2cf9b1124978) | 1 · incidental |
| `GlyphToolDone` | const | `tui/style.go:50` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphToolFail` | const | `tui/style.go:51` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphToolPending` | const | `tui/style.go:49` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphTruncate` | const | `tui/style.go:61` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphUserPrompt` | const | `tui/style.go:55` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GlyphWarn` | const | `tui/style.go:54` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `GoogleTheme` | func | `tui/theme.go:209` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `GopherTheme` | func | `tui/theme.go:284` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `History` | type | `tui/history.go:149` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `InboxStateDequeued` | const | `tui/remote_events.go:122` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `InboxStateQueued` | const | `tui/remote_events.go:121` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `Item` | type | `tui/listcache.go:40` | v0.1.0 [`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27) | 3 · render extension |
| `KeyMsgDialog` | type | `tui/dialog.go:70` | v0.18.0 [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9) | 3 · render extension |
| `LipglossFormatter` | func | `tui/chroma.go:47` | v0.1.0 [`22b1ee7`](https://github.com/go-steer/core-tui/commit/22b1ee7995ec806157588212cf0485548cb5a8a5) | 1 · incidental |
| `ListTranscripts` | func | `tui/transcript.go:276` | v0.1.0 [`00bc303`](https://github.com/go-steer/core-tui/commit/00bc30313ffae248824875c97c1724af847c86ff) | 4 · transcripts |
| `LoadTranscript` | func | `tui/transcript.go:260` | v0.1.0 [`00bc303`](https://github.com/go-steer/core-tui/commit/00bc30313ffae248824875c97c1724af847c86ff) | 4 · transcripts |
| `MatrixTheme` | func | `tui/theme.go:330` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `Model` | type | `tui/model.go:68` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `NewModel` | func | `tui/model.go:381` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 1 · incidental |
| `NewStyles` | func | `tui/style.go:110` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 2 · theming |
| `NewStylesWithTheme` | func | `tui/style.go:129` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | 2 · theming |
| `NewTextInputDialog` | func | `tui/dialog_textinput.go:144` | v0.18.0 [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9) | 3 · render extension |
| `OpenAITheme` | func | `tui/theme.go:177` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | 2 · theming |
| `Overlay` | type | `tui/dialog.go:99` | v0.1.0 [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf) | 1 · incidental |
| `PrideTheme` | func | `tui/theme.go:362` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `QueueDone` | const | `tui/queue.go:32` | v0.1.0 [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158) | 1 · incidental |
| `QueueEntry` | type | `tui/queue.go:58` | v0.1.0 [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158) | 1 · incidental |
| `QueueFailed` | const | `tui/queue.go:33` | v0.1.0 [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158) | 1 · incidental |
| `QueueInFlight` | const | `tui/queue.go:31` | v0.1.0 [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158) | 1 · incidental |
| `QueueQueued` | const | `tui/queue.go:30` | v0.1.0 [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158) | 1 · incidental |
| `QueueState` | type | `tui/queue.go:27` | v0.1.0 [`5fd7456`](https://github.com/go-steer/core-tui/commit/5fd7456be81d5b01d87ed1b88e3c73ef918af158) | 1 · incidental |
| `RawRenderable` | type | `tui/listcache.go:69` | v0.1.0 [`5177602`](https://github.com/go-steer/core-tui/commit/51776029f5dca42f715514b82358b794f493ce27) | 3 · render extension |
| `RenderContext` | type | `tui/dialog.go:220` | v0.1.0 [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf) | 1 · incidental |
| `SavingsPathAgentic` | const | `tui/tool_savings.go:43` | v0.14.0 [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924) | 1 · incidental |
| `SavingsPathPassthrough` | const | `tui/tool_savings.go:41` | v0.14.0 [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924) | 1 · incidental |
| `SavingsPathStructural` | const | `tui/tool_savings.go:42` | v0.14.0 [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924) | 1 · incidental |
| `ScrollDialog` | type | `tui/dialog_scroll.go:275` | v0.19.0 [`890f1c3`](https://github.com/go-steer/core-tui/commit/890f1c355694bdb4bb42feb086d65e0343f46ac9) | 3 · render extension |
| `Scrollbar` | func | `tui/dialog.go:263` | v0.1.0 [`6f29721`](https://github.com/go-steer/core-tui/commit/6f297214032253f118c754b12c05172de7c6e8cf) | 1 · incidental |
| `Styles` | type | `tui/style.go:73` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) | 2 · theming |
| `TerminalCapabilities` | type | `tui/terminal_caps.go:36` | v0.1.0 [`db4a297`](https://github.com/go-steer/core-tui/commit/db4a2974e1a72dd14d49d1ee72954238be7a651e) | 1 · incidental |
| `TextInputConfig` | type | `tui/dialog_textinput.go:56` | v0.18.0 [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9) | 3 · render extension |
| `Theme` | type | `tui/theme.go:40` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | 2 · theming |
| `ThemeByName` | func | `tui/theme.go:526` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `ThemeChangedMsg` | type | `tui/messages.go:299` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |
| `ThemeForProvider` | func | `tui/theme.go:539` | v0.1.0 [`d4ecdd5`](https://github.com/go-steer/core-tui/commit/d4ecdd5dbe9e4e375cfca499a0a29fb021ce4dca) | 2 · theming |
| `ToolRenderer` | type | `tui/toolrender.go:47` | v0.1.0 [`221bd9e`](https://github.com/go-steer/core-tui/commit/221bd9ea0a1d9958eafe8128da7a808095b2c6c1) | 3 · render extension |
| `Transcript` | type | `tui/transcript.go:52` | v0.1.0 [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1) | 4 · transcripts |
| `TranscriptInfo` | type | `tui/transcript.go:312` | v0.1.0 [`00bc303`](https://github.com/go-steer/core-tui/commit/00bc30313ffae248824875c97c1724af847c86ff) | 4 · transcripts |
| `TranscriptMsg` | type | `tui/transcript.go:71` | v0.1.0 [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1) | 4 · transcripts |
| `TranscriptSchemaVersion` | const | `tui/transcript.go:49` | v0.1.0 [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1) | 4 · transcripts |
| `TranscriptUsage` | type | `tui/transcript.go:99` | v0.1.0 [`781b52b`](https://github.com/go-steer/core-tui/commit/781b52b3a78c37fa6fac17555b783acc133194f1) | 4 · transcripts |
| `TurnErrorAuth` | const | `tui/remote_events.go:157` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnErrorConfig` | const | `tui/remote_events.go:156` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnErrorModelNotFound` | const | `tui/remote_events.go:158` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnErrorRateLimited` | const | `tui/remote_events.go:159` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnErrorTransientNet` | const | `tui/remote_events.go:160` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnErrorUnknown` | const | `tui/remote_events.go:161` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnStateAwaitingElicit` | const | `tui/remote_events.go:57` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnStateAwaitingPermission` | const | `tui/remote_events.go:56` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnStateIdle` | const | `tui/remote_events.go:54` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `TurnStateStreaming` | const | `tui/remote_events.go:55` | v0.9.0 [`7c862e5`](https://github.com/go-steer/core-tui/commit/7c862e593b6e96fb92a37ca6c44187f92a5a6751) | 1 · incidental |
| `VaporwaveTheme` | func | `tui/theme.go:421` | v0.7.0 [`eb5c66d`](https://github.com/go-steer/core-tui/commit/eb5c66d942d51516d74e2e82361d19e2a365f501) | 2 · theming |

### 9.3 Appendix C — inside the closure but unused by core-agent

Quadrant C: contract by design, dark in practice. Mostly the unused half of a two-branch choice. **Keep** — a second host will use the other branch.

| symbol | kind | location | introduced |
|---|---|---|---|
| `AsyncSlashProvider` | type | `tui/slash.go:166` | v0.6.0 [`d100d44`](https://github.com/go-steer/core-tui/commit/d100d44a4a3ec6e0fd5f98f67f7b6d89a0516782) |
| `Content` | type | `tui/agent.go:301` | v0.1.0 [`2b09061`](https://github.com/go-steer/core-tui/commit/2b09061e223672bd9c42f42d067761de887765dd) |
| `ContentPart` | type | `tui/agent.go:312` | v0.1.0 [`2b09061`](https://github.com/go-steer/core-tui/commit/2b09061e223672bd9c42f42d067761de887765dd) |
| `ContentRunner` | type | `tui/agent.go:328` | v0.1.0 [`2b09061`](https://github.com/go-steer/core-tui/commit/2b09061e223672bd9c42f42d067761de887765dd) |
| `DecisionDeny` | const | `tui/prompter.go:103` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `DetailHTTP` | const | `tui/prompter.go:62` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `ElicitActionCancel` | const | `tui/elicitor.go:108` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `ElicitFieldType` | type | `tui/elicitor.go:46` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `ElicitMode` | type | `tui/elicitor.go:38` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `ElicitResult` | type | `tui/elicitor.go:97` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `ElicitURLMode` | const | `tui/elicitor.go:42` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `InjectIntoCurrent` | const | `tui/options.go:254` | v0.1.0 [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5) |
| `Message` | type | `tui/history.go:41` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `MidTurnInjectionMode` | type | `tui/options.go:245` | v0.1.0 [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5) |
| `ModelTotals` | type | `tui/capabilities.go:402` | v0.6.4 [`df18400`](https://github.com/go-steer/core-tui/commit/df184001a93752715dcbdd60b1f348b5ccc335f4) |
| `Notifier` | type | `tui/notifier.go:60` | v0.8.0 [`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b) |
| `PermanentStreamError` | type | `tui/agent.go:219` | v0.11.0 [`691bd82`](https://github.com/go-steer/core-tui/commit/691bd826439623942506cc8106e0b2deeb7e7d40) |
| `PermissionInline` | const | `tui/options.go:318` | v0.1.0 [`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5) |
| `PermissionKindHTTP` | const | `tui/prompter.go:47` | v0.1.0 [`bba788e`](https://github.com/go-steer/core-tui/commit/bba788e4161183e6819b26b82dd61c8ded90bcda) |
| `PermissionLayout` | type | `tui/options.go:311` | v0.1.0 [`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5) |
| `PermissionOverlay` | const | `tui/options.go:322` | v0.1.0 [`000c601`](https://github.com/go-steer/core-tui/commit/000c60103a42ad560424453ae57f12ca9b05c9c5) |
| `QueueForNext` | const | `tui/options.go:250` | v0.1.0 [`96db3d0`](https://github.com/go-steer/core-tui/commit/96db3d061fa2b774f5b10c74470888a52e1f84d5) |
| `Role` | type | `tui/history.go:21` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `RoleAssistant` | const | `tui/history.go:25` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `RoleError` | const | `tui/history.go:27` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `RoleNotice` | const | `tui/history.go:37` | v0.8.0 [`e9be0dd`](https://github.com/go-steer/core-tui/commit/e9be0dd166ca7d9bf105dde769daa6e4ee93463b) |
| `RoleSystem` | const | `tui/history.go:26` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `RoleTool` | const | `tui/history.go:28` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `RoleUser` | const | `tui/history.go:24` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `SessionByModelTracker` | type | `tui/capabilities.go:422` | v0.6.4 [`df18400`](https://github.com/go-steer/core-tui/commit/df184001a93752715dcbdd60b1f348b5ccc335f4) |
| `SessionInput` | type | `tui/capabilities.go:95` | v0.18.0 [`c5317dd`](https://github.com/go-steer/core-tui/commit/c5317dd7dfdcba5968bef8b9db8a49eaf96cb8c9) |
| `StatusHeader` | const | `tui/options.go:305` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `StatusLayout` | type | `tui/options.go:301` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `StatusSidebar` | const | `tui/options.go:307` | v0.1.0 [`c024303`](https://github.com/go-steer/core-tui/commit/c02430380debe058e0292157a3f53a00ee86f944) |
| `ToolSavings` | type | `tui/tool_savings.go:57` | v0.14.0 [`6434bf3`](https://github.com/go-steer/core-tui/commit/6434bf3f03ec30edff219e235bbd3878a536d924) |
| `UsageLastTurn` | type | `tui/remote_events.go:89` | v0.10.2 [`f1162dd`](https://github.com/go-steer/core-tui/commit/f1162ddd0176ad293c45fb02fe39de2fbafd9a13) |

### 9.4 Appendix D — how the surface grew, by release

New exported symbols per release. Useful context for the freeze: the surface is still growing, and every symbol added after the freeze is a permanent promise.

| release | new symbols | names |
|---|---|---|
| **v0.1.0** | 154 | `Agent`, `AnthropicTheme`, `ApprovalLog`, `BrandCyan`, `BrandPink`, `BrandPinkBright`, `BrandSlate`, `BrandViolet`, `Branding`, `Content`, `ContentPart`, `ContentRunner`, `DecisionAllowAlways`, `DecisionAllowOnce`, `DecisionAllowSession`, `DecisionAllowSessionTool`, `DecisionAllowSessionVerb`, `DecisionDeny`, `DefaultTheme`, `DetailArgs`, `DetailDiff`, `DetailHTTP`, `DetailKind`, `DetailPlain`, `DetailShell`, `DetectCapabilities`, `Dialog`, `DialogAction`, `ElicitAction`, `ElicitActionCancel`, `ElicitActionDecline`, `ElicitActionSubmit`, `ElicitField`, `ElicitFieldBoolean`, `ElicitFieldEnum`, `ElicitFieldInteger`, `ElicitFieldNumber`, `ElicitFieldString`, `ElicitFieldType`, `ElicitFormMode`, `ElicitMode`, `ElicitRequest`, `ElicitResult`, `ElicitURLMode`, `Elicitor`, `Event`, `Focusable`, `GeminiTheme`, `GlyphCollapsed`, `GlyphColumn`, `GlyphCursor`, `GlyphExpanded`, `GlyphModel`, `GlyphRule`, `GlyphSeparator`, `GlyphTool`, `GlyphToolActive`, `GlyphToolDone`, `GlyphToolFail`, `GlyphToolPending`, `GlyphTruncate`, `GlyphUserPrompt`, `GlyphWarn`, `History`, `InjectIntoCurrent`, `InjectableAgent`, `Item`, `LipglossFormatter`, `ListTranscripts`, `LoadTranscript`, `MCPServerInfo`, `MCPToolInfo`, `MemoryFile`, `Message`, `MidTurnInjectionMode`, `Model`, `ModelInfo`, `ModelSwapper`, `NewElicitor`, `NewModel`, `NewPrompter`, `NewStyles`, `NewStylesWithTheme`, `OpenAITheme`, `Options`, `Overlay`, `PathScope`, `PermissionController`, `PermissionDecision`, `PermissionInline`, `PermissionKind`, `PermissionKindBash`, `PermissionKindEdit`, `PermissionKindHTTP`, `PermissionKindOther`, `PermissionLayout`, `PermissionMode`, `PermissionModeAcceptEdits`, `PermissionModeBypass`, `PermissionModeDefault`, `PermissionModePlan`, `PermissionModeWiring`, `PermissionOverlay`, `PermissionPrompter`, `PermissionRequest`, `PricingController`, `Prompter`, `QueueDone`, `QueueEntry`, `QueueFailed`, `QueueForNext`, `QueueInFlight`, `QueueQueued`, `QueueState`, `RawRenderable`, `ReloadResult`, `Reloader`, `RenderContext`, `Role`, `RoleAssistant`, `RoleError`, `RoleSystem`, `RoleTool`, `RoleUser`, `Run`, `Scrollbar`, `SideAnswer`, `SkillInfo`, `SlashCommandSpec`, `SlashProvider`, `SlashResult`, `Status`, `StatusHeader`, `StatusLayout`, `StatusReporter`, `StatusSidebar`, `Styles`, `SubagentInfo`, `SubagentLister`, `TerminalCapabilities`, `Theme`, `ThemeForProvider`, `ToolCall`, `ToolInfo`, `ToolLister`, `ToolRenderer`, `Transcript`, `TranscriptInfo`, `TranscriptMsg`, `TranscriptSchemaVersion`, `TranscriptUsage`, `Usage`, `UsageTracker`, `WakeRequester` |
| **v0.3.0** | 1 | `ToolResult` |
| **v0.5.0** | 3 | `ThemeAuto`, `ThemeDark`, `ThemeLight` |
| **v0.6.0** | 6 | `AsyncSlashProvider`, `AutoContinueFromInbox`, `DefaultAutoContinueCap`, `GlyphAutoContinue`, `InboxDrainer`, `SlashResultOrErr` |
| **v0.6.3** | 1 | `AsyncSlashProviderWithPreamble` |
| **v0.6.4** | 2 | `ModelTotals`, `SessionByModelTracker` |
| **v0.6.6** | 1 | `LiveAgent` |
| **v0.7.0** | 13 | `BuiltinTheme`, `BuiltinThemes`, `ChristmasTheme`, `CyberpunkTheme`, `DefaultPromptGlyph`, `GKETheme`, `GoogleTheme`, `GopherTheme`, `MatrixTheme`, `PrideTheme`, `ThemeByName`, `ThemeChangedMsg`, `VaporwaveTheme` |
| **v0.8.0** | 3 | `NewNotifier`, `Notifier`, `RoleNotice` |
| **v0.9.0** | 18 | `InboxEvent`, `InboxStateDequeued`, `InboxStateQueued`, `StatusUpdate`, `TurnError`, `TurnErrorAuth`, `TurnErrorConfig`, `TurnErrorModelNotFound`, `TurnErrorRateLimited`, `TurnErrorTransientNet`, `TurnErrorUnknown`, `TurnStateAwaitingElicit`, `TurnStateAwaitingPermission`, `TurnStateIdle`, `TurnStateStreaming`, `TurnSummary`, `UsageByModel`, `UsageUpdate` |
| **v0.10.0** | 3 | `SessionInfo`, `SessionSwitcher`, `SwitchTarget` |
| **v0.10.2** | 1 | `UsageLastTurn` |
| **v0.11.0** | 1 | `PermanentStreamError` |
| **v0.14.0** | 4 | `SavingsPathAgentic`, `SavingsPathPassthrough`, `SavingsPathStructural`, `ToolSavings` |
| **v0.15.0** | 1 | `RemoteInterrupter` |
| **v0.18.0** | 10 | `KeyMsgDialog`, `NewTextInputDialog`, `SessionInput`, `SubagentEvent`, `SubagentEventPage`, `SubagentEventReader`, `SubagentNotFoundError`, `SubagentToolCall`, `SubagentToolResult`, `TextInputConfig` |
| **v0.19.0** | 1 | `ScrollDialog` |
