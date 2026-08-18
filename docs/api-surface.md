# Exported surface of `package tui` — classification

Generated from `go doc -all ./tui` at commit
[`102ecb1`](https://github.com/go-steer/core-tui/commit/102ecb1cee553a152e2ccbbdfbf76d9e768abfde),
cross-checked against a `go/doc` + `go/ast` walk of the 67 non-test files of
`package tui`. Both enumerations agree on **265 exported top-level symbols**.

The census figures below are that snapshot and are not re-derived on every
change; §3.1's "Optional capability" table is the exception, because
`TestDesignDocDeclarationsMatchSource` reads it as the live roster of §3.3
capability interfaces and fails when it drifts from `package tui`. It is
seven rows shorter than the snapshot after [#77](https://github.com/go-steer/core-tui/issues/77)
deleted the unimplemented capabilities and merged the redundant pair.

This document answers [#78](https://github.com/go-steer/core-tui/issues/78):
which of those 265 the project intends to keep promising at 1.0, and which
are exported only because nobody stopped them. It is the classification that
[`docs/design.md`](./design.md) §3 points at, and it supersedes
[`docs/api-audit.md`](./api-audit.md) §6.2 where the two disagree — §6.2 was
derived from a reachability cross-tab without reading the render interfaces'
signatures, and those signatures change the answer (§4 below).

## Contents

- **1** [What was counted, and how](#1-what-was-counted-and-how)
- **2** [The three buckets at a glance](#2-the-three-buckets-at-a-glance)
- **3** [The classification](#3-the-classification)
    - **3.1** [Contract](#31-contract--156-symbols)
    - **3.2** [Host-useful, not contract](#32-host-useful-not-contract--42-symbols)
    - **3.3** [Incidental](#33-incidental--67-symbols)
- **4** [The §2.1 re-check: does the flat package still serve?](#4-the-21-re-check-does-the-flat-package-still-serve)
- **5** [N-DOC](#5-n-doc)

---

## 1. What was counted, and how

The unit of classification is the **exported top-level symbol** — a type, a
function, a constant, a package-level variable — plus every **exported method
on an exported type**, because a method is as much a compatibility promise as
the type carrying it. Struct fields and interface methods are not enumerated
individually here; they inherit their parent's bucket, and
[`docs/api-audit.md`](./api-audit.md) §8 already lists all 438 of them with
provenance.

| | count |
|---|---|
| non-test files | 67 |
| exported types | 107 |
| exported constants | 85 |
| exported functions | 29 |
| exported package vars | 5 |
| exported methods on exported types | 39 |
| **total classified** | **265** |

Issue #78's headline figure of **174** is 107 types + 29 functions + 39
methods, computed at the time the issue was filed against 51 files. That
arithmetic still holds — it is 175 today, one function having been added —
but it silently omits the 85 constants and 5 variables, which are promises
too. Six of the untyped string constants (`TurnError*`) are a documented wire
vocabulary that a host will pattern-match against; freezing them by accident
is exactly the failure mode #78 exists to prevent. The number to reason about
is 265, not 174.

The three buckets are defined as:

- **contract** — named by `design.md` §3, or reachable from a §3 root
  (`Run`, `Options`, `Agent`, `Event`, the capability interfaces, the
  prompter / elicitor pair) without naming a type that §3 does not already
  name. Equivalently: a host cannot write a working adapter without it.
  Vocabularies carried in plainly-typed fields (`StatusUpdate.TurnState` is a
  `string`) count as reachable even though a type-level walk misses them —
  see `api-audit.md` §6.1.
- **host-useful, not contract** — a host could reasonably want it, and in
  several cases an `Options` doc comment already mentions it in prose, but
  nothing in §3 promises it. Each row carries an explicit
  promote-or-unexport recommendation.
- **incidental** — exported for no reachable purpose, or exported only
  because a same-package test named it. Same-package tests do not need
  exported identifiers, so that is not a reason.

Where a symbol was genuinely ambiguous it was put in the middle bucket with
the reason stated, rather than being forced into one of the outer two.

---

## 2. The three buckets at a glance

| bucket | symbols | share |
|---|---|---|
| contract | 156 | 59% |
| host-useful, not contract | 42 | 16% |
| incidental | 67 | 25% |
| **total** | **265** | |

Two things fall out of the split that are worth stating before the tables.

**The contract is bigger than §3 reads as promising.** §3 gestures at "Agent
+ Event + capability interfaces + Options field names" — about forty things.
The transitive closure is 156, because `Event` alone drags in the whole SSE
payload vocabulary (`StatusUpdate`, `UsageUpdate`, `TurnError`, `TurnSummary`,
`InboxEvent` and their twenty-odd string constants) and the capability
interfaces drag in their argument and result structs. None of that is
negotiable at 1.0 — a host that consumes push events names those types
directly — but none of it is written down as frozen either. §3 has been
updated to enumerate it.

**The incidental bucket is real but smaller than `api-audit.md` §6.2
implies.** §6.2 put 91 symbols in quadrant A. Sixty-seven of them survive as
incidental here; the other twenty-four move to the middle bucket, mostly the
render-extension set, because "no host uses it" and "no host could use it"
are different findings and only the second justifies unexporting without
discussion.

---

## 3. The classification

### 3.1 Contract — 156 symbols

Named by `design.md` §3, or reachable from a §3 root without naming
anything §3 does not already name. A host cannot compile against the
library without these. Frozen at 1.0.

#### Entry point (31)

| symbol | kind | declared | reached from |
|---|---|---|---|
| `AutoContinueFromInbox` | const (`MidTurnInjectionMode`) | `options.go:292` | `Options.MidTurnInjectionMode`. |
| `Branding` | type | `options.go:304` | `Options.Branding`. |
| `DefaultAutoContinueCap` | const | `options.go:300` | Documented default for `Options.AutoContinueCap`. |
| `InjectIntoCurrent` | const (`MidTurnInjectionMode`) | `options.go:276` | `Options.MidTurnInjectionMode`. |
| `MCPServerInfo` | type | `capabilities.go:150` | `Options.Memory` / `.MCPServers` / `.Skills`. |
| `MCPToolInfo` | type | `capabilities.go:162` | `Options.Memory` / `.MCPServers` / `.Skills`. |
| `MemoryFile` | type | `capabilities.go:140` | `Options.Memory` / `.MCPServers` / `.Skills`. |
| `MidTurnInjectionMode` | type | `options.go:267` | `Options.MidTurnInjectionMode`. |
| `Options` | type | `options.go:28` | §3 root; every field name is frozen per §8. |
| `PathScope` | type | `capabilities.go:428` | `Options.PathScope`. |
| `PathScope.Allows` | method | `capabilities.go:433` | `Options.PathScope`. |
| `PermissionInline` | const (`PermissionLayout`) | `options.go:340` | `Options.PermissionLayout`. |
| `PermissionLayout` | type | `options.go:333` | `Options.PermissionLayout`. |
| `PermissionMode` | type | `options.go:356` | `Options.PermissionMode`. |
| `PermissionMode.Next` | method | `options.go:380` | `Options.PermissionMode`. |
| `PermissionMode.String` | method | `options.go:366` | `Options.PermissionMode`. |
| `PermissionModeAcceptEdits` | const (`PermissionMode`) | `options.go:360` | `Options.PermissionMode`. |
| `PermissionModeBypass` | const (`PermissionMode`) | `options.go:362` | `Options.PermissionMode`. |
| `PermissionModeDefault` | const (`PermissionMode`) | `options.go:359` | `Options.PermissionMode`. |
| `PermissionModePlan` | const (`PermissionMode`) | `options.go:361` | `Options.PermissionMode`. |
| `PermissionModeWiring` | type | `options.go:349` | `Options.PermissionMode`. |
| `PermissionOverlay` | const (`PermissionLayout`) | `options.go:344` | `Options.PermissionLayout`. |
| `QueueForNext` | const (`MidTurnInjectionMode`) | `options.go:272` | `Options.MidTurnInjectionMode`. |
| `Run` | func | `program.go:39` | §3 root; the documented way to start the TUI. |
| `SkillInfo` | type | `capabilities.go:168` | `Options.Memory` / `.MCPServers` / `.Skills`. |
| `StatusHeader` | const (`StatusLayout`) | `options.go:327` | `Options.StatusLayout`. |
| `StatusLayout` | type | `options.go:323` | `Options.StatusLayout`. |
| `StatusSidebar` | const (`StatusLayout`) | `options.go:329` | `Options.StatusLayout`. |
| `ThemeAuto` | const | `options.go:22` | `Options.ForceTheme` vocabulary. |
| `ThemeDark` | const | `options.go:23` | `Options.ForceTheme` vocabulary. |
| `ThemeLight` | const | `options.go:24` | `Options.ForceTheme` vocabulary. |

#### Agent + Event (38)

| symbol | kind | declared | reached from |
|---|---|---|---|
| `Agent` | type | `agent.go:28` | §3.1 / §3.2 root. |
| `Event` | type | `agent.go:35` | §3.1 / §3.2 root. |
| `InboxEvent` | type | `remote_events.go:111` | `Event.Inbox` (SSE spec §2.4). |
| `InboxStateDequeued` | const | `remote_events.go:122` | `Event.Inbox` (SSE spec §2.4). |
| `InboxStateQueued` | const | `remote_events.go:121` | `Event.Inbox` (SSE spec §2.4). |
| `Message` | type | `history.go:41` | `Options.SeedHistory []Message`. |
| `Message.Display` | method | `history.go:153` | `Options.SeedHistory []Message`. |
| `Role` | type | `history.go:21` | `Options.SeedHistory []Message`. |
| `RoleAssistant` | const (`Role`) | `history.go:25` | `Options.SeedHistory []Message`. |
| `RoleError` | const (`Role`) | `history.go:27` | `Options.SeedHistory []Message`. |
| `RoleNotice` | const (`Role`) | `history.go:37` | `Options.SeedHistory []Message`. |
| `RoleSystem` | const (`Role`) | `history.go:26` | `Options.SeedHistory []Message`. |
| `RoleTool` | const (`Role`) | `history.go:28` | `Options.SeedHistory []Message`. |
| `RoleUser` | const (`Role`) | `history.go:24` | `Options.SeedHistory []Message`. |
| `SavingsPathAgentic` | const | `tool_savings.go:43` | `ToolResult.Savings` (SSE spec v1.3.0). |
| `SavingsPathPassthrough` | const | `tool_savings.go:41` | `ToolResult.Savings` (SSE spec v1.3.0). |
| `SavingsPathStructural` | const | `tool_savings.go:42` | `ToolResult.Savings` (SSE spec v1.3.0). |
| `StatusUpdate` | type | `remote_events.go:42` | `Event.StatusUpdate` (SSE spec §2.2). |
| `ToolCall` | type | `agent.go:94` | `Event.ToolCalls` / `.ToolResults` / `.Usage`. |
| `ToolResult` | type | `agent.go:106` | `Event.ToolCalls` / `.ToolResults` / `.Usage`. |
| `ToolSavings` | type | `tool_savings.go:57` | `ToolResult.Savings` (SSE spec v1.3.0). |
| `ToolSavings.SavedTokens` | method | `tool_savings.go:72` | `ToolResult.Savings` (SSE spec v1.3.0). |
| `TurnError` | type | `remote_events.go:145` | `Event.TurnError` (SSE spec §2.6). |
| `TurnErrorAuth` | const | `remote_events.go:157` | `Event.TurnError` (SSE spec §2.6). |
| `TurnErrorConfig` | const | `remote_events.go:156` | `Event.TurnError` (SSE spec §2.6). |
| `TurnErrorModelNotFound` | const | `remote_events.go:158` | `Event.TurnError` (SSE spec §2.6). |
| `TurnErrorRateLimited` | const | `remote_events.go:159` | `Event.TurnError` (SSE spec §2.6). |
| `TurnErrorTransientNet` | const | `remote_events.go:160` | `Event.TurnError` (SSE spec §2.6). |
| `TurnErrorUnknown` | const | `remote_events.go:161` | `Event.TurnError` (SSE spec §2.6). |
| `TurnStateAwaitingElicit` | const | `remote_events.go:57` | `Event.StatusUpdate` (SSE spec §2.2). |
| `TurnStateAwaitingPermission` | const | `remote_events.go:56` | `Event.StatusUpdate` (SSE spec §2.2). |
| `TurnStateIdle` | const | `remote_events.go:54` | `Event.StatusUpdate` (SSE spec §2.2). |
| `TurnStateStreaming` | const | `remote_events.go:55` | `Event.StatusUpdate` (SSE spec §2.2). |
| `TurnSummary` | type | `remote_events.go:131` | `Event.TurnComplete` (SSE spec §2.5). |
| `Usage` | type | `agent.go:146` | `Event.ToolCalls` / `.ToolResults` / `.Usage`. |
| `UsageByModel` | type | `remote_events.go:100` | `Event.UsageUpdate` (SSE spec §2.3). |
| `UsageLastTurn` | type | `remote_events.go:89` | `Event.UsageUpdate` (SSE spec §2.3). |
| `UsageUpdate` | type | `remote_events.go:73` | `Event.UsageUpdate` (SSE spec §2.3). |

#### Optional capability (36)

| symbol | kind | declared | reached from |
|---|---|---|---|
| `ApprovalLog` | type | `capabilities.go:200` | Reached from `PermissionController`. |
| `AsyncSlashProvider` | type | `slash.go:188` | §3.3 capability interface. |
| `InboxDrainer` | type | `agent.go:242` | §3.3 capability interface. |
| `InjectableAgent` | type | `agent.go:161` | §3.3 capability interface. |
| `LiveAgent` | type | `agent.go:203` | §3.3 capability interface. |
| `ModelInfo` | type | `capabilities.go:42` | Reached from `ModelSwapper`. |
| `ModelSwapper` | type | `capabilities.go:36` | §3.3 capability interface. |
| `PermanentStreamError` | type | `agent.go:219` | Reached from `LiveAgent`. |
| `PermissionController` | type | `capabilities.go:190` | §3.3 capability interface. |
| `PricingController` | type | `capabilities.go:208` | §3.3 capability interface. |
| `ReloadResult` | type | `capabilities.go:143` | Reached from `Reloader`. |
| `Reloader` | type | `capabilities.go:136` | §3.3 capability interface. |
| `RemoteInterrupter` | type | `agent.go:288` | §3.3 capability interface. |
| `SessionInfo` | type | `capabilities.go:67` | Reached from `SessionSwitcher`. |
| `SessionInput` | type | `capabilities.go:95` | Reached from `SessionSwitcher`. |
| `SessionSwitcher` | type | `capabilities.go:61` | §3.3 capability interface. |
| `SideAnswer` | type | `slash.go:141` | Reached from `SlashProvider`. |
| `SlashCommandSpec` | type | `slash.go:39` | Reached from `SlashProvider`. |
| `SlashProvider` | type | `slash.go:30` | §3.3 capability interface. |
| `SlashResult` | type | `slash.go:65` | Reached from `SlashProvider`. |
| `SlashResultOrErr` | type | `slash.go:150` | Reached from `AsyncSlashProvider`. |
| `Status` | type | `capabilities.go:389` | Reached from `StatusReporter`. |
| `StatusReporter` | type | `capabilities.go:384` | §3.3 capability interface. |
| `SubagentEvent` | type | `capabilities.go:296` | Reached from `SubagentReporter`. |
| `SubagentEventPage` | type | `capabilities.go:277` | Reached from `SubagentReporter`. |
| `SubagentInfo` | type | `capabilities.go:269` | Reached from `SubagentReporter`. |
| `SubagentNotFoundError` | type | `capabilities.go:348` | Reached from `SubagentReporter`. |
| `SubagentNotFoundError.Error` | method | `capabilities.go:353` | Reached from `SubagentReporter`. |
| `SubagentReporter` | type | `capabilities.go:263` | §3.3 capability interface. |
| `SubagentToolCall` | type | `capabilities.go:323` | Reached from `SubagentReporter`. |
| `SubagentToolResult` | type | `capabilities.go:330` | Reached from `SubagentReporter`. |
| `SwitchTarget` | type | `slash.go:98` | Reached from `SlashProvider`. |
| `ToolInfo` | type | `capabilities.go:219` | Reached from `ToolLister`. |
| `ToolLister` | type | `capabilities.go:214` | §3.3 capability interface. |
| `UsageTracker` | type | `capabilities.go:403` | §3.3 capability interface. |
| `WakeRequester` | type | `agent.go:260` | §3.3 capability interface. |

#### Prompter / elicitor (41)

> **Grew by 16 top-level symbols in v0.23**, in
> [#255](https://github.com/go-steer/core-tui/issues/255): `Asker`,
> `NewAsker`, `AskKind` + its 5 constants, `AskOption`, `AskRequest`,
> `AskAction` + its 3 constants, `AskResult`, `ErrAskUnsupported` — plus the
> `Options.Asker` and `SwitchTarget.Asker` fields, which §1 does not
> enumerate because a field inherits its parent's bucket. All are contract, on the
> same ground as the two interfaces beside them: the TUI implements them and
> the host wires them in, so there is no version of this that is reachable
> only from inside. All are additions — `verify-apidiff` reports compatible
> and `api-breaks.txt` is untouched. The table is left as the snapshot it
> was; this note is the status.

| symbol | kind | declared | reached from |
|---|---|---|---|
| `DecisionAllowAlways` | const (`PermissionDecision`) | `prompter.go:108` | `PermissionPrompter.AskApproval` result. |
| `DecisionAllowOnce` | const (`PermissionDecision`) | `prompter.go:104` | `PermissionPrompter.AskApproval` result. |
| `DecisionAllowSession` | const (`PermissionDecision`) | `prompter.go:105` | `PermissionPrompter.AskApproval` result. |
| `DecisionAllowSessionTool` | const (`PermissionDecision`) | `prompter.go:107` | `PermissionPrompter.AskApproval` result. |
| `DecisionAllowSessionVerb` | const (`PermissionDecision`) | `prompter.go:106` | `PermissionPrompter.AskApproval` result. |
| `DecisionDeny` | const (`PermissionDecision`) | `prompter.go:103` | `PermissionPrompter.AskApproval` result. |
| `DetailArgs` | const (`DetailKind`) | `prompter.go:63` | `PermissionRequest.DetailKind`. |
| `DetailDiff` | const (`DetailKind`) | `prompter.go:60` | `PermissionRequest.DetailKind`. |
| `DetailHTTP` | const (`DetailKind`) | `prompter.go:62` | `PermissionRequest.DetailKind`. |
| `DetailKind` | type | `prompter.go:56` | `PermissionRequest.DetailKind`. |
| `DetailPlain` | const (`DetailKind`) | `prompter.go:59` | `PermissionRequest.DetailKind`. |
| `DetailShell` | const (`DetailKind`) | `prompter.go:61` | `PermissionRequest.DetailKind`. |
| `ElicitAction` | type | `elicitor.go:122` | `ElicitResult.Action`. |
| `ElicitActionCancel` | const (`ElicitAction`) | `elicitor.go:127` | `ElicitResult.Action`. |
| `ElicitActionDecline` | const (`ElicitAction`) | `elicitor.go:126` | `ElicitResult.Action`. |
| `ElicitActionSubmit` | const (`ElicitAction`) | `elicitor.go:125` | `ElicitResult.Action`. |
| `ElicitField` | type | `elicitor.go:68` | `Elicitor.Elicit` argument / result. |
| `ElicitFieldBoolean` | const (`ElicitFieldType`) | `elicitor.go:61` | `ElicitField.Type`. |
| `ElicitFieldEnum` | const (`ElicitFieldType`) | `elicitor.go:62` | `ElicitField.Type`. |
| `ElicitFieldInteger` | const (`ElicitFieldType`) | `elicitor.go:60` | `ElicitField.Type`. |
| `ElicitFieldNumber` | const (`ElicitFieldType`) | `elicitor.go:59` | `ElicitField.Type`. |
| `ElicitFieldString` | const (`ElicitFieldType`) | `elicitor.go:58` | `ElicitField.Type`. |
| `ElicitFieldType` | type | `elicitor.go:55` | `ElicitField.Type`. |
| `ElicitFormMode` | const (`ElicitMode`) | `elicitor.go:50` | `ElicitRequest.Mode`. |
| `ElicitMode` | type | `elicitor.go:47` | `ElicitRequest.Mode`. |
| `ElicitRequest` | type | `elicitor.go:89` | `Elicitor.Elicit` argument / result. |
| `ElicitResult` | type | `elicitor.go:106` | `Elicitor.Elicit` argument / result. |
| `ElicitURLMode` | const (`ElicitMode`) | `elicitor.go:51` | `ElicitRequest.Mode`. |
| `Elicitor` | type | `elicitor.go:39` | §3.5 interface the host may replace. |
| `ErrElicitUnsupported` | var (`error`) | `elicitor.go:150` | `Elicitor.Elicit` result; matched with `errors.Is` (R-ELIC-3). |
| `NewElicitor` | func | `elicitor.go:185` | §3.5 built-in implementation; wired into `Options.Elicitor`. |
| `NewPrompter` | func | `prompter.go:151` | §3.5 built-in implementation; wired into `Options.Prompter`. |
| `PermissionDecision` | type | `prompter.go:100` | `PermissionPrompter.AskApproval` result. |
| `PermissionKind` | type | `prompter.go:36` | `PermissionRequest.Kind`. |
| `PermissionKindBash` | const (`PermissionKind`) | `prompter.go:41` | `PermissionRequest.Kind`. |
| `PermissionKindEdit` | const (`PermissionKind`) | `prompter.go:44` | `PermissionRequest.Kind`. |
| `PermissionKindHTTP` | const (`PermissionKind`) | `prompter.go:47` | `PermissionRequest.Kind`. |
| `PermissionKindOther` | const (`PermissionKind`) | `prompter.go:50` | `PermissionRequest.Kind`. |
| `PermissionPrompter` | type | `prompter.go:29` | §3.5 interface the host may replace. |
| `PermissionRequest` | type | `prompter.go:69` | `PermissionPrompter.AskApproval` argument; also `Options.AlwaysAllow`. |
| `Prompter` | type | `prompter.go:140` | §3.5 built-in implementation; wired into `Options.Prompter`. |
| `Prompter.AskApproval` | method | `prompter.go:159` | §3.5 built-in implementation; wired into `Options.Prompter`. |

#### Notifier (3)

| symbol | kind | declared | reached from |
|---|---|---|---|
| `NewNotifier` | func | `notifier.go:74` | `Options.Notifier`; §3.4 TUI-to-host callback. |
| `Notifier` | type | `notifier.go:60` | `Options.Notifier`; §3.4 TUI-to-host callback. |
| `Notifier.Notify` | method | `notifier.go:91` | `Options.Notifier`; §3.4 TUI-to-host callback. |

### 3.2 Host-useful, not contract — 42 symbols

#### Render extension points (25)

> **Carried out.** All 25 are unexported as of v0.22 — `Focusable` (deleted
> outright) and `ToolRenderer` in
> [#213](https://github.com/go-steer/core-tui/issues/213), four in
> [#254](https://github.com/go-steer/core-tui/issues/254), the remaining
> nineteen in [#257](https://github.com/go-steer/core-tui/issues/257).
> `RawRenderable` was deleted rather than unexported: taking it off the
> surface made the compiler report what the table below could not, which is
> that nothing had ever type-asserted to it. The table is left as the
> snapshot it was — this note is the status, the rows are the audit.

`Dialog`, `Item`, `Focusable`, `RawRenderable`, `ToolRenderer`, `Overlay`,
`RenderContext`, `Scrollbar` and the text-input dialog helpers are a coherent,
well-documented extension surface — `ToolRenderer`'s own comment calls itself
"the contract for one tool's call/result rendering". They are in this bucket
rather than in the contract because **not one of them can be used from outside
the package today**:

- `Dialog.HandleKey(stroke string, m *Model) DialogAction` and
  `Item.Render(m *Model, width int) string` take a `*Model` whose every field
  is unexported. An external implementation compiles and can do nothing with
  its argument.
- `Overlay` is the only thing that accepts a `Dialog` (`Overlay.Open`), and
  the only `Overlay` in existence is an unexported field of `Model`. There is
  no way for a host to open a dialog.
- `ToolRenderer` is the one interface with a `Model`-free signature — and it
  has no registration seam at all. `toolRendererFor` in `tui/toolrender.go`
  switches over three package-private values and `Options` has no renderer
  field, so an external implementation has nowhere to be installed.

**Recommendation: unexport, as a set.** An exported interface with no
installation seam is a promise the library cannot keep, and freezing it at
1.0 freezes the seam's absence too. The alternative — add
`Options.ToolRenderers map[string]ToolRenderer` and a dialog-registration
seam, then promote the whole set — is a feature, not a cleanup, and should be
argued on its own issue with a host asking for it. Until then the honest
state is unexported. Note that unexporting them is *not* a prerequisite for
the subpackage question (§4); the two are independent.

| symbol | kind | declared | recommendation |
|---|---|---|---|
| `Dialog` | type | `dialog.go:39` | **unexport** |
| `DialogAction` | type | `dialog.go:80` | **unexport** |
| `Focusable` | type | `listcache.go:88` | **unexport** |
| `Item` | type | `listcache.go:51` | **unexport** |
| `KeyMsgDialog` | type | `dialog.go:70` | **unexport** |
| `NewTextInputDialog` | func | `dialog_textinput.go:144` | **unexport** |
| `Overlay` | type | `dialog.go:99` | **unexport** |
| `Overlay.Close` | method | `dialog.go:112` | **unexport** |
| `Overlay.CloseFront` | method | `dialog.go:124` | **unexport** |
| `Overlay.Front` | method | `dialog.go:158` | **unexport** |
| `Overlay.Get` | method | `dialog.go:137` | **unexport** |
| `Overlay.HandleKey` | method | `dialog.go:170` | **unexport** |
| `Overlay.HandleKeyMsg` | method | `dialog.go:187` | **unexport** |
| `Overlay.HandleWheel` | method | `dialog_scroll.go:454` | **unexport** |
| `Overlay.HasDialogs` | method | `dialog.go:132` | **unexport** |
| `Overlay.HasID` | method | `dialog.go:148` | **unexport** |
| `Overlay.Open` | method | `dialog.go:106` | **unexport** |
| `Overlay.Render` | method | `dialog.go:208` | **unexport** |
| `RawRenderable` | type | `listcache.go:80` | **unexport** |
| `RenderContext` | type | `dialog.go:220` | **unexport** |
| `RenderContext.Render` | method | `dialog.go:242` | **unexport** |
| `ScrollDialog` | type | `dialog_scroll.go:440` | **unexport** |
| `Scrollbar` | func | `dialog.go:365` | **unexport** |
| `TextInputConfig` | type | `dialog_textinput.go:56` | **unexport** |
| `ToolRenderer` | type | `toolrender.go:47` | **unexport** |

#### Styling (4)

> **Carried out**, in [#257](https://github.com/go-steer/core-tui/issues/257),
> on the conditional this section states: `ToolRenderer` was unexported, so
> these four followed. `Styles` is `styleSet` rather than `styles`, because 63
> parameters were already spelled `styles Styles` and Go resolves a
> parameter's type in the scope its own name is declared in.

`Styles` is load-bearing for `ToolRenderer.RenderCall`, and `NewStyles` /
`NewStylesWithTheme` are the only ways to obtain one. Their fate is therefore
tied to the render set above: **unexport** if `ToolRenderer` is unexported,
keep if it gains a seam. They are listed separately so the coupling is
visible rather than implied.

| symbol | kind | declared | recommendation |
|---|---|---|---|
| `NewStyles` | func | `style.go:117` | **unexport** |
| `NewStylesWithTheme` | func | `style.go:147` | **unexport** |
| `Styles` | type | `style.go:80` | **unexport** |
| `Styles.RenderWordmark` | method | `style.go:202` | **unexport** |

#### Theming (5)

This group is already contract in prose and only in prose.
`tui/options.go:54` documents `Options.InitialThemeName` as "resolved
case-insensitively against `tui.BuiltinThemes`", and `:57` and `:103` tell
the host to "observe `ThemeChangedMsg` in the host's `Update` loop". A doc
comment on a frozen field that names a symbol is a promise whether or not §3
repeats it.

**Recommendation: promote all five to contract.** `Theme` comes along because
`BuiltinTheme` embeds it and `ThemeByName` returns it; promoting the registry
without the type it hands back would be pointless. The alternative —
narrowing the `Options` doc comments so they stop naming these — costs the
host the only documented way to enumerate or observe themes, for no gain.

| symbol | kind | declared | recommendation |
|---|---|---|---|
| `BuiltinTheme` | type | `theme.go:672` | **promote to contract** |
| `BuiltinThemes` | func | `theme.go:696` | **promote to contract** |
| `Theme` | type | `theme.go:42` | **promote to contract** |
| `ThemeByName` | func | `theme.go:718` | **promote to contract** |
| `ThemeChangedMsg` | type | `messages.go:535` | **promote to contract** |

#### Transcripts (7)

core-tui writes transcripts into a directory the *host* supplies
(`Options.AgentsDir`), under a schema whose version is a constant
(`TranscriptSchemaVersion`, currently v2). An on-disk format written into
somebody else's directory is already a public interface; the only open
question is whether reading it back is a supported API or a re-implemented
parser. `core-agent` does not call these today, which is why
`api-audit.md` §7.1 left them open.

**Recommendation: promote all seven to contract.** Freezing the schema
version constant at 1.0 should be a deliberate yes, and this is that yes.
`ListTranscripts` / `LoadTranscript` are the read side of a format the
library already commits to on disk; withholding them does not un-commit it.

| symbol | kind | declared | recommendation |
|---|---|---|---|
| `ListTranscripts` | func | `transcript.go:276` | **promote to contract** |
| `LoadTranscript` | func | `transcript.go:260` | **promote to contract** |
| `Transcript` | type | `transcript.go:52` | **promote to contract** |
| `TranscriptInfo` | type | `transcript.go:312` | **promote to contract** |
| `TranscriptMsg` | type | `transcript.go:71` | **promote to contract** |
| `TranscriptSchemaVersion` | const | `transcript.go:49` | **promote to contract** |
| `TranscriptUsage` | type | `transcript.go:99` | **promote to contract** |

#### Clipboard (1)

`SystemClipboardWriter` is documented at `tui/options.go:121` as the
supported value for `Options.ClipboardWriter`, is named in
`docs/requirements.md:620`, and is used by `examples/`. It is not
*structurally* required — the field is a bare `func(text string) error` — so
it misses the contract bucket on the strict definition, but it fails it on a
technicality.

**Recommendation: promote to contract.**

| symbol | kind | declared | recommendation |
|---|---|---|---|
| `SystemClipboardWriter` | func | `clipboard.go:136` | **promote to contract** |

### 3.3 Incidental — 67 symbols

#### Glyph vocabulary (17)

Seventeen single-rune display constants. Nothing outside the package reads
them, no `Options` field takes one, and the terminal-capability fallback that
would make them a host-tunable knob does not exist. Unexporting is a pure
rename; the only cost is diff size, and thirteen of the seventeen have at
least one call site in a file another change currently holds
(`tui/view.go`, `tui/update.go`, `tui/chatlist.go`), so those thirteen have
to wait for that work to land.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `GlyphAutoContinue` | const | `style.go:60` | 3 refs in 2 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphCollapsed` | const | `style.go:52` | 8 refs in 4 non-test file(s), 2 in 1 test file(s) |
| `GlyphColumn` | const | `style.go:65` | 3 refs in 2 non-test file(s), 4 in 2 test file(s) — **blocked**: touches `view.go` |
| `GlyphCursor` | const | `style.go:63` | 1 refs in 1 non-test file(s), 2 in 1 test file(s) |
| `GlyphExpanded` | const | `style.go:53` | 1 refs in 1 non-test file(s) |
| `GlyphModel` | const | `style.go:38` | 4 refs in 2 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphRule` | const | `style.go:64` | 18 refs in 7 non-test file(s), 3 in 3 test file(s) — **blocked**: touches `chatlist.go,view.go` |
| `GlyphSelectBar` | const | `style.go:72` | 4 refs in 2 non-test file(s), 3 in 3 test file(s) |
| `GlyphSeparator` | const | `style.go:62` | 40 refs in 11 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphTool` | const | `style.go:43` | 5 refs in 3 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphToolActive` | const | `style.go:48` | 4 refs in 3 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphToolDone` | const | `style.go:50` | 3 refs in 3 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphToolFail` | const | `style.go:51` | 4 refs in 4 non-test file(s), 1 in 1 test file(s) — **blocked**: touches `view.go` |
| `GlyphToolPending` | const | `style.go:49` | 2 refs in 2 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphTruncate` | const | `style.go:61` | 22 refs in 10 non-test file(s), 15 in 2 test file(s) — **blocked**: touches `update.go,view.go` |
| `GlyphUserPrompt` | const | `style.go:55` | 3 refs in 2 non-test file(s) — **blocked**: touches `view.go` |
| `GlyphWarn` | const | `style.go:54` | 6 refs in 4 non-test file(s) — **blocked**: touches `view.go` |

#### Brand colors (5)

Five `lipgloss` colors that predate the `Theme` struct. `Theme` now carries
`Primary` / `Secondary` / `Accent` and the themes reference these vars as
seed values internally. No host reads them.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `BrandCyan` | var | `style.go:33` | 1 refs in 1 non-test file(s) |
| `BrandPink` | var | `style.go:30` | 4 refs in 4 non-test file(s) |
| `BrandPinkBright` | var | `style.go:31` | 1 refs in 1 non-test file(s) |
| `BrandSlate` | var | `style.go:32` | 1 refs in 1 non-test file(s) |
| `BrandViolet` | var | `style.go:29` | 6 refs in 4 non-test file(s) |

#### Theme defaults (2)

Two seed constants read only by the theme constructors in the same file.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `DefaultChromaStyleName` | const | `theme.go:138` | 11 refs in 2 non-test file(s), 2 in 1 test file(s) |
| `DefaultPromptGlyph` | const | `theme.go:130` | 4 refs in 2 non-test file(s), 3 in 1 test file(s) |

#### Named theme constructors (13)

Twelve named constructors plus `ThemeForProvider`. Every one is already
reachable by name through `BuiltinThemes()` / `ThemeByName()`, which are
recommended for promotion above — so unexporting the constructors removes
nothing a host can do, it only removes thirteen redundant spellings of it.
This is the largest clean win in the incidental bucket: all thirteen live in
`tui/theme.go` with call sites confined to `tui/theme.go` and its tests.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `AnthropicTheme` | func | `theme.go:313` | 5 refs in 1 non-test file(s) |
| `ChristmasTheme` | func | `theme.go:642` | 3 refs in 1 non-test file(s), 6 in 3 test file(s) |
| `CyberpunkTheme` | func | `theme.go:575` | 3 refs in 1 non-test file(s) |
| `DefaultTheme` | func | `theme.go:269` | 32 refs in 6 non-test file(s), 15 in 5 test file(s) |
| `GKETheme` | func | `theme.go:437` | 3 refs in 1 non-test file(s), 4 in 1 test file(s) |
| `GeminiTheme` | func | `theme.go:328` | 5 refs in 1 non-test file(s) |
| `GoogleTheme` | func | `theme.go:376` | 5 refs in 1 non-test file(s), 6 in 2 test file(s) |
| `GopherTheme` | func | `theme.go:454` | 3 refs in 1 non-test file(s), 4 in 2 test file(s) |
| `MatrixTheme` | func | `theme.go:503` | 3 refs in 1 non-test file(s), 7 in 2 test file(s) |
| `OpenAITheme` | func | `theme.go:341` | 5 refs in 1 non-test file(s) |
| `PrideTheme` | func | `theme.go:541` | 3 refs in 1 non-test file(s) |
| `ThemeForProvider` | func | `theme.go:731` | 5 refs in 2 non-test file(s) |
| `VaporwaveTheme` | func | `theme.go:607` | 3 refs in 1 non-test file(s) |

#### Syntax highlighting (1)

`LipglossFormatter` returns a `chroma.Formatter`, which means the exported
signature leaks `github.com/alecthomas/chroma/v2` into the library's API and
pins its major version for hosts. No host calls it. Unexporting removes a
transitive API dependency, which is worth more than the symbol.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `LipglossFormatter` | func | `chroma.go:47` | 7 refs in 3 non-test file(s) |

#### Terminal capabilities (3)

`DetectCapabilities` probes the environment and `TerminalCapabilities` holds
the result; the TUI calls both at startup. No `Options` field accepts one, so
a host cannot override detection — which is the only thing exporting them
would be for.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `DetectCapabilities` | func | `terminal_caps.go:71` | 4 refs in 2 non-test file(s), 1 in 1 test file(s) |
| `TerminalCapabilities` | type | `terminal_caps.go:36` | 6 refs in 2 non-test file(s), 1 in 1 test file(s) |
| `TerminalCapabilities.Hyperlink` | method | `terminal_caps.go:140` | follows `TerminalCapabilities` |

#### Chat history store (13)

`History` is the in-memory transcript backing the viewport. The host-facing
way to seed it is `Options.SeedHistory []Message`, and the host-facing way to
read it is the transcript API; `History` itself is mutable internal state
with twelve mutator methods. It is exported because same-package tests call
`h.Append` / `h.Snapshot` directly, which same-package tests do not need.
Blocked on `tui/view.go`.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `History` | type | `history.go:161` | 21 refs in 5 non-test file(s), 15 in 6 test file(s) — **blocked**: touches `view.go` |
| `History.Append` | method | `history.go:169` | follows `History` |
| `History.BumpVersion` | method | `history.go:237` | follows `History` |
| `History.FindByToolCallID` | method | `history.go:253` | follows `History` |
| `History.LastID` | method | `history.go:225` | follows `History` |
| `History.Len` | method | `history.go:313` | follows `History` |
| `History.MarkLastUserAutoContinue` | method | `history.go:302` | follows `History` |
| `History.Reset` | method | `history.go:195` | follows `History` |
| `History.SetRendered` | method | `history.go:204` | follows `History` |
| `History.SetToolPreview` | method | `history.go:269` | follows `History` |
| `History.SetToolResult` | method | `history.go:284` | follows `History` |
| `History.Snapshot` | method | `history.go:188` | follows `History` |
| `History.StampLatestAssistantFooter` | method | `history.go:335` | follows `History` |

#### Submission queue (7)

The operator-typed-during-streaming queue panel. Entirely internal UI state;
no host observes it and no `Event` carries it. Blocked on `tui/update.go` and
`tui/view.go`.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `QueueDone` | const (`QueueState`) | `queue.go:32` | 10 refs in 4 non-test file(s), 7 in 3 test file(s) — **blocked**: touches `update.go,view.go` |
| `QueueEntry` | type | `queue.go:58` | 9 refs in 4 non-test file(s), 14 in 8 test file(s) — **blocked**: touches `update.go,view.go` |
| `QueueFailed` | const (`QueueState`) | `queue.go:33` | 10 refs in 3 non-test file(s), 5 in 2 test file(s) — **blocked**: touches `update.go,view.go` |
| `QueueInFlight` | const (`QueueState`) | `queue.go:31` | 8 refs in 4 non-test file(s), 9 in 3 test file(s) — **blocked**: touches `update.go,view.go` |
| `QueueQueued` | const (`QueueState`) | `queue.go:30` | 8 refs in 4 non-test file(s), 17 in 8 test file(s) — **blocked**: touches `update.go,view.go` |
| `QueueState` | type | `queue.go:27` | 7 refs in 2 non-test file(s), 1 in 1 test file(s) — **blocked**: touches `view.go` |
| `QueueState.String` | method | `queue.go:37` | follows `QueueState` |

#### Bubble Tea model (6)

`Model` is incidental by the same test as everything else here — no host
constructs one, `Run` is the documented entry point, and `Model` cannot be
usefully embedded because a parent `tea.Model` silently drops the
`AltScreen` / `MouseMode` / `Cursor` fields set in `Model.View`.

**But it is not this issue's to unexport.**
[#115](https://github.com/go-steer/core-tui/issues/115) owns `Model` /
`NewModel` explicitly, pairs the unexport with replacing `NewModel` by
`NewProgram(opts Options) (*tea.Program, error)` so external benchmarking
survives, and requires both in one commit because either alone is a break.
#115 is deferred past the architecture spike. Listed here so the
classification is complete; excluded from any unexport sweep until #115 runs.
The mechanical cost is also the largest in the table by an order of magnitude
— 373 non-test references across 48 files, all five currently-held files
among them.

| symbol | kind | declared | cost to unexport |
|---|---|---|---|
| `Model` | type | `model.go:54` | 373 refs in 48 non-test file(s), 562 in 53 test file(s) — **blocked**: touches `chatlist.go,cursor.go,dialog_scroll.go,update.go,view.go` |
| `Model.ApplyTranscript` | method | `transcript.go:327` | follows `Model` |
| `Model.Init` | method | `update.go:35` | follows `Model` |
| `Model.Update` | method | `update.go:99` | follows `Model` |
| `Model.View` | method | `view.go:134` | follows `Model` |
| `NewModel` | func | `model.go:570` | 18 refs in 9 non-test file(s), 262 in 44 test file(s) — **blocked**: touches `chatlist.go,dialog_scroll.go,update.go` |

---

## 4. The §2.1 re-check: does the flat package still serve?

`design.md` §2.1 argued for one flat package on two grounds: the types are
highly interconnected, and neither source project had felt splitting
pressure. #78 asks whether that still holds at 1.0, or whether the
render-side interfaces (`Dialog`, `Item`, `Focusable`, `ToolRenderer`, …)
want a subpackage so the contract becomes structural rather than
conventional. `api-audit.md` §6.2 group 3 suggested they do: *"keep exported,
move to `tui/render` (or similar), and let the package boundary carry the
promise."*

**Recommendation: keep the flat package. Do not create `tui/render`.**

Two reasons, and the first is dispositive.

**It is not currently possible.** `Dialog.HandleKey(stroke string, m *Model)
DialogAction`, `Dialog.Render(width int, m *Model) string` and
`Item.Render(m *Model, width int) string` all take `*Model`. A `tui/render`
package holding those interfaces must import `tui` to name `*Model`, and
`tui` must import `tui/render` to use the interfaces — an import cycle.
Moving them is therefore not a boundary change that can be evaluated on its
merits; it is gated on first removing `*Model` from those signatures, which
is a redesign of how dialogs reach TUI state. `api-audit.md` §6.2 did not see
this because it worked from the reachability cross-tab rather than the
signatures. That recommendation should be treated as superseded.

**It would not buy what it is meant to buy.** The appeal of a subpackage is
that the package boundary carries the promise: what is in `tui/render` is
extension surface, what is in `tui` is not. But as §3.2 sets out, none of
these interfaces is extensible from outside the package right now —
`*Model`'s state is unexported, `Overlay` is unreachable, and `ToolRenderer`,
the one interface a host *could* implement, has no registration seam. Moving
thirteen unusable interfaces into a package whose name advertises them as
usable makes the surface more misleading, not more structural. The real
defect §2.1 is being blamed for is not package topology; it is thirteen
exported interfaces with no seam. That is fixable inside a flat package (add
the seam, or unexport), and unfixable by moving files.

What §2.1 actually got right and still gets right: the flat package is what
lets `Options`, `Model`, the history, the palette and the overlays share
unexported state without a web of accessors, and no host has yet asked for a
narrower import. The one claim in §2.1 that has aged badly is the implicit
one — that "the design contracts consumers should depend on are called out
explicitly in §3" was sufficient. It was not, which is what this document
fixes: the boundary is now enumerated rather than conventional, and
`dev/tools/verify-apidiff` polices it. Enumeration plus a machine gate
achieves what the subpackage was wanted for, without the cycle.

**Reconciling with #115.** [#115](https://github.com/go-steer/core-tui/issues/115)
proposes decomposing `tui` into `internal/` subpackages and is **deferred**
past the architecture spike; nothing here proposes doing it now. The two do
not conflict. #115's move list is already restricted to the `Model`-free
files (`toolrender.go`, `tool_preview*.go`, `tool_detail.go`,
`tool_latency.go`, `tool_savings.go`, `theme.go`, `style.go`, the markdown
set) — the same `*Model` constraint found here, hit from the other side.
What this re-check contributes to #115 is one narrowing: `api-audit.md` §6.2's
caveat that `ToolRenderer` must land in a *non-`internal`* `tui/render` no
longer applies. Absent a registration seam, `ToolRenderer` has no external
implementer to protect, so when #115 eventually runs it can go to
`internal/render/tools` with the rest of the file. That removes the one
exported symbol that would have forced #115 to create a public subpackage.

---

## 5. N-DOC

`docs/requirements.md` §4 N-DOC: *"Every exported type and function has a doc
comment."*

**Read literally — types and functions — N-DOC holds with one miss:**

| symbol | declared | note |
|---|---|---|
| `SubagentNotFoundError.Error` | `capabilities.go:336` | the `error` implementation; contract bucket, so this is a real gap |

**Read as intended — every exported symbol — there are 14 misses.** The other
thirteen are constants, and all thirteen are in the contract bucket:

| symbol | declared | mitigating |
|---|---|---|
| `RoleUser` | `history.go:24` | the `Role` type's comment explains the vocabulary; the sibling `RoleNotice` in the same block *does* carry one, so the omission reads as an oversight rather than a convention |
| `RoleAssistant` | `history.go:25` | as above |
| `RoleSystem` | `history.go:26` | as above |
| `RoleError` | `history.go:27` | as above |
| `RoleTool` | `history.go:28` | as above |
| `ElicitFormMode` | `elicitor.go:50` | `ElicitMode`'s comment describes both modes |
| `ElicitURLMode` | `elicitor.go:51` | as above |
| `ElicitFieldString` | `elicitor.go:58` | `ElicitFieldType`'s comment covers the set generically, not per value |
| `ElicitFieldNumber` | `elicitor.go:59` | as above |
| `ElicitFieldInteger` | `elicitor.go:60` | as above; the `Number` / `Integer` distinction is exactly what a per-value comment would carry |
| `ElicitFieldBoolean` | `elicitor.go:61` | as above |
| `ElicitFieldEnum` | `elicitor.go:62` | as above |
| `DetailPlain` | `prompter.go:59` | `DetailKind`'s comment mentions it; the four siblings in the same block carry trailing line comments and this one does not |

Every other exported symbol in all three buckets has a doc comment, including
all 67 in the incidental bucket. That is worth noting for what it says about
how the surface grew: these were not exported carelessly and left
undocumented, they were exported carefully and documented, which is precisely
why nothing flagged them.

Fixing the fourteen is mechanical and non-breaking, so it does not need to
wait for the freeze. It is not done in this document's PR, which is
documentation-only by design.
