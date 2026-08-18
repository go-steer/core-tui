# Capability-surface consolidation — proposal for #77

This document is the written proposal `docs/design.md` §10 risk 1 asks
for. It is a decision aid, not a change: nothing here is implemented,
and the PR that carries it touches no Go file.

The question is narrow and the deadline is real. §10 risk 1 says:

> **Adapter boilerplate fatigue.** If the capability interfaces grow
> past ~10, each host's adapter becomes annoying to write. Mitigation:
> when a capability is required for "most" hosts, fold it into the base
> `Agent` interface (**and accept the breaking change before v1.0**).

Everything the mitigation authorizes is a *removal* from the exported
surface, and removals are the one class of change 1.0 forecloses. So
the deadline is genuine — but it applies to a smaller set of moves than
the issue assumes, and §7 below argues that is the most important thing
in this document.

`docs/api-audit.md` §3 and §5 already reached a recommendation on this
issue. This proposal disagrees with it in three places (§1.3, §3.1,
§3.3) and supersedes it where they conflict; the reasoning for each
disagreement is given inline rather than left for the reader to
reconcile.

### Settled decisions (do not relitigate)

Nothing yet. This document is the input to the decision, not its
record. Once §10's eight questions are answered, the answers belong
here and the rest of the document becomes the reasoning behind them.

Two things are settled elsewhere and are taken as given throughout:
`decisions.md` **D4** (small required core plus feature-detected
capability interfaces, on the `io.Reader` / `io.ReaderAt` model) stands
— nothing here proposes going back to one large interface; and
`design.md` §8's rule that a break lands before 1.0 or not at all
stands, which is the only reason this issue has a deadline.

### Out of scope

- **Any change to `Agent`, `Event`, or the streaming surface.** §3.2's
  and §4's edits stop at the capability layer.
- **The `Options` surface**, including the `MentionProvider` /
  `Options.Commands` extension points.
- **`Dialog` and the render extension points.** `Dialog`'s contract
  work is #115; `Focusable` and `ToolRenderer` are §10 Q6 and belong
  to #78.
- **The 42 "host-useful but unpromised" and 67 "incidental" symbols**
  of `design.md` §3 — that narrowing is #78.
- **The vestigial overlay enum and `usageMsg`** — #79, and this
  proposal sequences after it.
- **A `Run`-translation helper** to lower the minimum-adapter floor
  (§7). Additive, no deadline, its own issue.

---

## 1. The verified inventory

### 1.1 Method

Every exported interface in the module was enumerated with a `go/ast`
pass over the 51 non-test files of `package tui` — declaration by
declaration, with the method set read off the AST rather than off a
grep. The same pass was run over `tui/testagent` and `tui/testdata`:
both export zero interfaces, so `package tui` is the whole surface.

Signature precision matters more than usual here. `AsyncSlashProvider`
and `AsyncSlashProviderWithPreamble` share the method *name*
`InvokeSlashAsync` and differ only in return signature, so no
name-based method can answer "which one does this host implement",
and no Go type can satisfy both.

### 1.2 The count

**31 exported interfaces.** The issue's figure is correct. So is its
split: **20 optional agent-side capabilities** plus the base `Agent`,
and 10 others. The classification below is finer than the issue's
"agent-side / render-and-host-side" cut, because three of the ten turn
out not to be extension points at all.

| class | count | members |
|---|---|---|
| Host-implemented capability, feature-detected off `Agent` | 20 | the issue's list |
| Host-implemented, required | 1 | `Agent` |
| Host-implemented, detected off an `error` | 1 | `PermanentStreamError` |
| TUI-implemented, host calls it | 2 | `Elicitor`, `PermissionPrompter` |
| TUI-side render extension point, live | 5 | `Dialog`, `KeyMsgDialog`, `ScrollDialog`, `Item`, `RawRenderable` |
| Exported extension point with no way to supply one | 2 | `ToolRenderer`, `Focusable` |
| **total** | **31** | |

The last row is a finding rather than a category. `ToolRenderer`
(`tui/toolrender.go:47`) is an exported contract whose only
implementations — `genericToolRenderer`, `bashToolRenderer`,
`fileToolRenderer` — are unexported and in-package, and `Options` has
no field through which a host could supply one. `Focusable`
(`tui/listcache.go:88`) has no implementor and no consumer anywhere in
the tree, tests included. Both are exported surface a host cannot use
and 1.0 would promise anyway. They are removals, so they share this
issue's deadline even though they belong to #78's cleanup; see §10 Q6.

### 1.3 The evidence columns, and a correction

Four data points, in descending order of how much they prove:

- **CA-L** — `core-agent`'s local adapter, `*coreAgentAdapter`
  (`cmd/core-agent/coretui_enabled.go`).
- **CA-R** — `core-agent`'s remote adapter, `*coretuiremote.Adapter`
  (`internal/coretuiremote/{adapter,capabilities,subagent_events}.go`).
- **EX** — the in-repo examples: `examples/core-agent/local.go` (L),
  `examples/core-agent/attach.go` (A), `examples/local/main.go` (M).
- **T** — the library's own test fakes in `tui/*_test.go`.

`docs/api-audit.md` §3 reports "**12** of the 20 optional capabilities
are implemented by both core-agent adapters, **5** by exactly one,
**3** by neither". **That is wrong, and the arithmetic is where it
shows.** The audit's own table has 21 rows, because it includes the
base `Agent` — which is not one of the 20 optional capabilities — in
its "both" column, and then reconciles to twenty by dropping a
one-adapter capability from the middle count. Re-reading the same
table: 11 both, 6 one, 3 neither.

Two further corrections come out of re-checking the adapters directly
rather than through `types.Implements` on the adapter *pair*:

- **`UsageTracker` is not implemented by the local adapter.** It is
  implemented by a separate type, `coreUsageBridge`
  (`coretui_enabled.go:994`), handed in through `Options.UsageTracker`
  at `:167`. That is a perfectly good way to satisfy the capability —
  the TUI never asserts `UsageTracker` off the `Agent` — but it means
  "both adapters implement it" is not the fact on the ground.
- **`RemoteInterrupter` is not implemented by the local adapter
  either**, and the adapter believes it is.
  `coretui_enabled.go:602-603` reads:

  ```go
  // Interrupt satisfies coretui.Interruptible.
  func (a *coreAgentAdapter) Interrupt() bool { return a.inner.Interrupt() }
  ```

  `coretui.Interruptible` has never existed in `package tui`; the
  capability is `RemoteInterrupter.Interrupt(ctx context.Context) error`.
  This is the second instance of the same bug — the first,
  `coretuiremote/adapter.go:705`'s `RequestWake` claiming to satisfy
  `WakeRequester.WakeRequested() <-chan struct{}`, is already recorded
  in `api-audit.md` §3.1. Both are silent, because capabilities are
  feature-detected. Both are on the exactly-two capabilities that lack
  a `var _` assertion on that side while the *other* side has one.

So the honest figure is **10 both / 7 one / 3 neither** counting only
methods on the adapter type, or **11 / 6 / 3** if `coreUsageBridge`
counts as the local host implementing `UsageTracker` — which it should,
for the purpose this number is being used for. Neither reading is
12/5/3, and the difference matters: two of the capabilities the audit
counts as universal are in fact implemented on one side only, one of
them by accident.

`core-agent` carries **three** `var _ coretui.X` assertions in its
entire non-test tree (`coretui_enabled.go:831`,
`coretuiremote/adapter.go:734`, `coretuiremote/subagent_events.go:79`).
Seventeen of twenty capabilities are unasserted.

### 1.4 The table

Verdict vocabulary: **delete** (remove from the surface), **fold**
(absorb into another type's contract), **leave** (keep as-is),
**defer** (a merge that is available additively later, so it should not
be spent on this deadline). "n" is the method count.

#### Host-implemented capabilities

| interface | n | CA-L | CA-R | EX | T | verdict |
|---|---|---|---|---|---|---|
| `Agent` | 1 | ✅ | ✅ | L A M | ✅ | leave — required, untouched |
| `ModelSwapper` | 2 | ✅ | ✗ | L | ✅ | leave |
| `SessionSwitcher` | 2 | ✗ | ✅ | A M | ✅ | leave |
| `Reloader` | 1 | ✅ | ✅ | L | ✅ | leave |
| `PermissionController` | 4 | ✅ | ✅ (1 of 4 stubbed) | L | ✅ | leave |
| `PricingController` | 2 | ✅ | ✅ | L | ✅ | leave |
| `ToolLister` | 1 | ✅ | ✅ | L A | ✅ | leave |
| `SubagentLister` | 1 | ✅ | ✅ | L A | ✅ | **defer** merge (§3.3) |
| `SubagentEventReader` | 1 | ✅ | ✅ | A | ✅ | **defer** merge (§3.3) |
| `StatusReporter` | 1 | ✅ | ✅ | L A | ✅ (anti-fake only) | leave |
| `UsageTracker` | 7 | ✅ via `coreUsageBridge` | ✅ (1 of 7 stubbed) | L A | ✅ | leave |
| `SessionByModelTracker` | 1 | ✗ | ✗ | ✗ | ✅ | **delete** (§3.2) |
| `InjectableAgent` | 1 | ✅ | ✅ | L A M | ✅ | leave |
| `LiveAgent` | 1 | ✗ | ✅ | A | ✅ | leave |
| `InboxDrainer` | 2 | ✅ | ✗ | L | ✅ | leave |
| `WakeRequester` | 1 | ✅ | ✗ (believes it does) | L M | ✅ | leave |
| `RemoteInterrupter` | 1 | ✗ (believes it does) | ✅ | A | ✅ | leave |
| `ContentRunner` | 1 | ✗ | ✗ | ✗ | ✅ | **delete** (§4.2) |
| `SlashProvider` | 2 | ✅ | ✅ | L M | ✅ | **keep, enrich result** (§3.1) |
| `AsyncSlashProvider` | 2 | ✗ | ✗ | ✗ | ✅ | **fold** into `SlashResult` (§3.1) |
| `AsyncSlashProviderWithPreamble` | 2 | ✅ | ✅ | A | ✅ | **fold** into `SlashResult` (§3.1) |
| `PermanentStreamError` | 1+`error` | ✅ (`*httpStatusError`) | — | ✗ | ✅ | leave |

#### TUI-implemented, host consumes

| interface | n | who implements | verdict |
|---|---|---|---|
| `PermissionPrompter` | 1 | `tui.NewPrompter()`; also `coretuiremote.StartRemotePrompter` | leave |
| `Elicitor` | 1 | `tui.NewElicitor()` — no other implementation anywhere, tests included | leave |

#### TUI-side render extension points

| interface | n | implementors | verdict |
|---|---|---|---|
| `Dialog` | 3 | 6 production dialogs | leave (contract work is #115) |
| `KeyMsgDialog` | 1 + `Dialog` | text-input dialogs | leave |
| `ScrollDialog` | 1 + `Dialog` | scrollable dialogs | leave |
| `Item` | 4 | `messageItem`, `fakeItem` | leave |
| `RawRenderable` | 1 | `messageItem` only | leave |
| `ToolRenderer` | 1 | three unexported, no `Options` field | out of scope — see §10 Q6 |
| `Focusable` | 1 | **none** | out of scope — see §10 Q6 |

**Net under this proposal: 20 optional capabilities → 17.**

Every "leave" in the render-extension rows above was a scope call for
this proposal, not a verdict on the symbols; `docs/api-surface.md` §3.2
took them up and all seven are off the exported surface as of v0.22
(#213, #254, #257). One correction the table earned: `RawRenderable`'s
implementor count of 1 was counting a *method*, not a use. Nothing ever
type-asserted to the interface, so it was deleted rather than
unexported.

---

## 2. The evidence problem

State it plainly: there is **one host**. `core-agent`'s two adapters
are not two data points. They were written by the same author against
the same document, they wrap the same agent, and where they differ they
differ because of transport, not because of independent judgment about
what a TUI host needs. `examples/core-agent` is a condensation of those
same two adapters and inherits their opinions; `examples/local` is a
demo, not a host, and its four capabilities were chosen to exercise the
TUI rather than to serve a program. The test fakes are the weakest
evidence of all — every capability has a fake, because the library
tests the code it ships.

So the mitigation's trigger condition — *"required for 'most' hosts"* —
is not measurable from this tree. At n=1, "most" is a synonym for
"core-agent", and folding a capability into base `Agent` on that basis
makes one host's architecture mandatory for every future host, at the
exact moment the surface stops being changeable. The audit reaches the
same conclusion and is right to.

But "the evidence is thin" is not a uniform verdict, and treating it as
one is how a proposal ends up recommending nothing. The claims this
document makes come in three kinds, and they are not equally exposed to
n=1:

1. **Negative claims about the surface itself**, checkable in-tree
   without reference to any host: *`ContentRunner` is never asserted
   anywhere in `tui/`*; *`SlashProvider` is a hard gate on three code
   paths, so the async variants cannot stand alone*. These are facts
   about `package tui`. One host does not weaken them.

2. **Negative claims about the one host**, of the form *nothing
   implements X*. n=1 is a genuinely adequate sample for these,
   because a capability nobody has ever implemented has produced no
   evidence that it is needed, and because getting it wrong is cheap
   and reversible: **re-adding an interface after 1.0 is additive and
   non-breaking.** The error is one-directional.

3. **Positive predictions about hosts that do not exist yet**, of the
   form *no future host will want A without B*. n=1 cannot support
   these at all, and getting them wrong is expensive: a merge you
   regret after 1.0 needs a v2.

Every "delete" in §1.4 rests on kind 1 or kind 2. The one merge the
issue names that would require kind 3 — the subagent pair — is
deferred, in §3.3, on exactly that ground. That is the whole shape of
the recommendation: **three deletions made confidently, one merge
declined honestly**, and the count lands at 17 rather than the ~10 §10
imagined. §7 argues 17 is fine, because the threshold was measuring the
wrong thing.

---

## 3. The three consolidations the issue names

### 3.1 `SlashProvider` / `AsyncSlashProvider` / `AsyncSlashProviderWithPreamble`

The three declarations, as they stand (`tui/slash.go:30`, `:166`, `:208`):

```go
type SlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlash(ctx context.Context, name, args string) (SlashResult, error)
}

type AsyncSlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlashAsync(ctx context.Context, name, args string) <-chan SlashResultOrErr
}

type AsyncSlashProviderWithPreamble interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlashAsync(ctx context.Context, name, args string) (preamble string, results <-chan SlashResultOrErr)
}
```

Reading every call site turns up two things the godoc says that are not
true, and they change the answer.

**The split is not a split. `SlashProvider` is mandatory.** Every path
that reaches a host's slash surface gates on `SlashProvider` first and
only then looks for an async shape:

- `tui/update.go:2298` — `provider, ok := m.opts.Agent.(SlashProvider)`;
  on failure the operator gets *"unknown command /x — the agent doesn't
  expose any slash commands"*, and dispatch ends there.
- `tui/host_async.go:136` — `slashCommandsCmd` returns a nil `Cmd`, so
  the host's commands never enter the `/` palette.
- `tui/host_async.go:382` — `helpCommandsCmd(provider SlashProvider, …)`,
  so they never enter `/help` either.
- `tui/palette.go:270` — `slashProviderItems(provider SlashProvider)`.

Only after that gate does `applySlashDispatch` choose a shape:
`AsyncSlashProviderWithPreamble` (`update.go:2368`), then
`AsyncSlashProvider` (`:2384`), then the plain call (`:2405`). So the
effective contract is **`SlashProvider` plus an optional second
`InvokeSlashAsync` method**, which is not what three sibling interfaces
communicate. `slash.go:200-202` states the opposite in as many words:
*"A host satisfying only the preamble variant works fine; one
satisfying only the bare variant also works fine."*

The test suite knows. `slash_test.go:573-576`:

> `InvokeSlash` is the `SlashProvider` fallback. `dispatchSlash`
> requires `SlashProvider` for command-name lookup before it
> type-asserts to the async variant; an error here is a tripwire — the
> preamble path should always win on agents satisfying both.

So the constraint is understood in-tree, written down in a test fake,
and absent from the interface's own godoc, which asserts the opposite.

This is not hypothetical. `examples/core-agent/attach.go` implements
`SlashCommands` (`:381`) and `InvokeSlashAsync` (`:389`) and **no**
`InvokeSlash` — so it satisfies only `AsyncSlashProviderWithPreamble`,
fails the gate at `update.go:2298`, and ships with every one of its
slash commands dead. `examples/core-agent/main.go:190` seeds the chat
with *"Capabilities wired: /tools /subagents /switch /interrupt /btw
/stats"*. The example's own test does not catch it because
`adapter_test.go:441` calls `InvokeSlashAsync` directly on the adapter
instead of driving `dispatchSlash`. The library's three async test
fakes all implement `InvokeSlash` as well (`slash_test.go:264`, `:352`,
`:577`), which is why the suite is green. The one shipped artifact that
took the godoc at its word is broken.

**The per-command claim is also false.** `slash.go:203` says *"Both can
coexist in the same host on different commands."* They cannot: the
shape is selected by type assertion on the `Agent`, once, for every
command. A host that satisfies `AsyncSlashProviderWithPreamble` routes
`/stats` through the async machinery exactly as it routes `/compact`.

Given both, the cheap consolidation — delete the bare
`AsyncSlashProvider`, promote the preamble variant into its name — is
the wrong move. It takes three interfaces to two, changes nothing for
`core-agent`, and leaves the mandatory-base trap and the false godoc
exactly where they are. It is the consolidation that looks tidiest in
the interface count and fixes none of the actual defects.

**Recommendation: fold both async interfaces into `SlashResult`.** One
interface, unchanged signature, the asynchrony expressed as data:

```go
type SlashResult struct {
	SystemMessage string
	ModalAnswer   *SideAnswer
	SwitchTo      *SwitchTarget

	// Preamble is a chat-visible "this is running" row, appended as
	// RoleSystem before the TUI begins draining Follow. Meaningful
	// only when Follow is non-nil; ignored otherwise, since a
	// synchronous result has nothing to announce. Empty = no row.
	Preamble string

	// Follow, when non-nil, makes this call asynchronous ...
	Follow <-chan SlashResultOrErr
}
```

Full declarations and the contract text are in §4.1. What it buys:

- **One interface. One method to implement.** The bug class that broke
  `attach.go` cannot occur, because there is no second interface to
  satisfy instead of the first.
- **Per-command choice becomes real** — the thing the current godoc
  falsely promises. `InvokeSlash` decides per invocation whether to set
  `Follow`, so `/stats` can be synchronous and `/compact` asynchronous
  on the same host.
- **`SlashProvider`'s signature does not change.** A host that only
  ever ran synchronous commands recompiles untouched. See §5: the
  interface contributes *zero* incompatible apidiff lines.
- **The dispatch path gets shorter, not longer.** `update.go`'s
  three-branch `applySlashDispatch` collapses to one branch on
  `res.Follow != nil`, and `slashDispatchCmd`'s `invoke bool` parameter
  — which exists only to encode the shape decision made at
  `update.go:2321-2325` — goes away.

What it costs, stated honestly:

- **One behavioral delta.** Today the toast and preamble are armed
  *before* the host is called, because the shape is known from the
  type. Under the proposal they can only be armed once `InvokeSlash`
  returns. Mitigation: arm the toast optimistically on the loop before
  issuing the dispatch `Cmd` and clear it when a synchronous result
  comes back — which restores today's timing exactly, at the price of a
  toast that exists for one Update tick on fast commands and is not
  rendered.
- **`SlashResult` becomes a slightly baggier type**: two of its five
  fields are meaningful only together. That is the standard cost of
  trading an interface for a result field, and it is the trade the
  issue explicitly asks to consider.
- **Migration for `core-agent` is real but small** — §6 shows it at
  roughly ten changed lines per adapter, no new logic.

Alternatives considered and rejected:

- *Keep three, fix the gates so the async variants stand alone.*
  Requires `slashCommandsCmd`, `helpCommandsCmd`, `slashProviderItems`
  and `dispatchSlash` each to assert three interfaces instead of one,
  and preserves a three-way split whose only remaining justification is
  that it already exists. It fixes the bug and keeps the cause.
- *Keep three, document `SlashProvider` as mandatory.* Cheapest of all,
  and it makes the surface honest, but it enshrines "two interfaces,
  one of which is a required prefix of the other" as a documented
  pattern in a frozen API.
- *`SlashCommandSpec.Async bool`.* Puts the choice in the catalog
  rather than the result, so the host declares asynchrony per command
  up front and the TUI knows the shape before calling. Rejected: the
  catalog is fetched once at startup and after `Reload`, so a command
  whose cost depends on its argument (`/compact` with and without a
  focus) cannot describe itself, and the host ends up declaring
  everything async.

### 3.2 `UsageTracker` / `SessionByModelTracker`

```go
type UsageTracker interface {
	SessionTotals() Usage
	SessionCostUSD() float64
	LastTurn() (Usage, float64)
	ContextWindowSize() int
	ContextWindowUsed() int
	SessionTurns() int
	SessionDuration() time.Duration
}

type SessionByModelTracker interface {
	SessionByModel() map[string]ModelTotals
}
```

`SessionByModelTracker` has **zero implementations** — not in
`core-agent` (no `SessionByModel` symbol exists anywhere in the host),
not in the examples, only in `slash_stats_test.go:40`. It has had none
since it shipped in v0.6.4 (`df18400`). `examples/core-agent/local.go:82`
says so out loud: *"ContentRunner, SessionByModelTracker — no host
wiring today."*

The feature it backs is not dark, though, and this is the detail that
decides the verdict. `/stats` renders its per-model breakdown from two
sources (`slash_builtin.go:862-870`), and **push wins**:

```go
switch {
case m.sessionUsage != nil && len(m.sessionUsage.ByModel) > 1:
	b.WriteString(formatModelBreakdown(usageByModelToTotals(m.sessionUsage.ByModel)))
default:
	if mt, ok := tracker.(SessionByModelTracker); ok {
		...
	}
}
```

The push path — `UsageByModel` off the SSE usage-update event, v0.9.0+
— is populated and works. So deleting the capability removes the
*pull-mode fallback* for a breakdown that a remote host already gets,
and that no embedded host has ever produced.

**Recommendation: delete `SessionByModelTracker`. Do not fold it into
`UsageTracker`.** Folding costs the one host that implements all seven
existing methods a compile break, in exchange for a method it would
implement by returning nil. Deleting is a kind-2 claim: nothing
implements it, and if a host ever wants it, re-adding the interface is
additive and non-breaking.

`ModelTotals` survives the deletion as an internal type — it is still
the argument of `formatModelBreakdown` and the result of
`usageByModelToTotals` on the push path. It should be unexported in the
same change; it was exported solely as the deleted interface's result
type. That is a seventh break rather than a sixth, and it is §10 Q2.

### 3.3 `SubagentLister` / `SubagentEventReader`

```go
type SubagentLister interface {
	Subagents() []SubagentInfo
}

type SubagentEventReader interface {
	SubagentEvents(ctx context.Context, name string, since int64) (SubagentEventPage, error)
}
```

Both one method, both about subagents, both implemented by both
`core-agent` adapters. `api-audit.md` §5.3 recommends merging them, on
the grounds that *"there is no host that has one and not the other, and
no plausible one"*, and that the usual objection — merging destroys the
"not available in this host" signal — does not apply because a host
with no subagents returns an empty list.

**That argument is sound for the lister half and wrong for the reader
half**, and the reason is written into `capabilities.go:243-248`, which
the merge would contradict:

> Report an unresolvable name as a `*SubagentNotFoundError` rather than
> an empty page, so the UI can say "no such subagent, here are the ones
> there are" instead of showing a plausible-looking empty log. An empty
> page means "this subagent has recorded no turns yet", which is a
> different and legitimate answer.

An empty `[]SubagentInfo` honestly means *no subagents*. An empty
`SubagentEventPage` is contractually defined to mean *this subagent has
recorded no turns yet* — so a host that lists subagents but keeps no
per-subagent turn log has no honest value to return. It must either lie
(empty page: "ran, did nothing") or misuse the error type. Merging
therefore does destroy a distinction, and it destroys the specific one
the code goes out of its way to preserve. `slash_builtin.go:370` reads
that distinction directly:

```go
_, drillable := m.opts.Agent.(SubagentEventReader)
```

— it is what decides whether roster rows are drillable at all.

Set that aside and the merge is still a **kind-3 claim**: *no future
host will list subagents without logging their turns*. `core-agent`
does both because it is a daemon with an event log and a
`GET /sessions/{id}/agents/{name}/events` endpoint. A host that spawns
subagents as goroutines and keeps only their final reports is not
exotic — it is `SubagentEventReader`'s own v0.18.0 origin story, since
`SubagentLister` predates it by seventeen minor versions and the lister
was the whole feature until then.

And the payoff is the smallest of any move in this document: one
interface off the count, and for `core-agent`, deleting one `var _`
line.

**Recommendation: defer.** Not "reject" — the merge may well be right,
and this is why deferring costs nothing:

> **A merge is additive. A deletion is not.** Adding
> `SubagentReporter` with both methods, teaching the TUI to prefer it
> and fall back to the two singles, is a compatible change apidiff
> classifies as `added`. It can land at any point in the 1.x line. The
> only thing 1.0 forecloses is *removing* `SubagentLister` and
> `SubagentEventReader` afterwards — and the case for removing them has
> to be made against evidence that does not exist yet.

§4.3 gives the declaration so the shape is on record. Spending the
freeze deadline on it, when the deadline only binds removals, is
spending it on the wrong thing.

---

## 4. The proposal

### 4.1 Unified slash surface

`AsyncSlashProvider` and `AsyncSlashProviderWithPreamble` are removed.
`SlashProvider` is unchanged. `SlashResult` gains two fields.

```go
// SlashProvider is the optional Agent capability for host-defined
// slash commands. The TUI queries SlashCommands at startup and after
// Reload, merges the entries into /help and the palette under an
// agent-scoped section, and routes invocations back via InvokeSlash.
// Built-in names win on collision; Options.Commands shadows an agent
// entry of the same name.
//
// InvokeSlash always runs off the Update goroutine, so it MAY block —
// the TUI stays responsive either way. What blocking costs is the
// operator's ability to see and cancel the work: a command that
// returns only when it is finished shows no running indicator and
// cannot be interrupted. Commands whose wall-clock exceeds roughly a
// second should return promptly with SlashResult.Follow set instead.
// ctx is cancelled when the operator presses Esc or Ctrl+C.
type SlashProvider interface {
	SlashCommands() []SlashCommandSpec
	InvokeSlash(ctx context.Context, name, args string) (SlashResult, error)
}

// SlashResult is what InvokeSlash returns. Any subset of the fields
// may be populated.
//
// Synchronous form — Follow nil. SystemMessage / ModalAnswer /
// SwitchTo are applied as soon as the call returns; Preamble is
// ignored, because a finished call has nothing to announce.
//
// Asynchronous form — Follow non-nil. The call has STARTED the work
// and must have returned promptly. The TUI appends Preamble as a
// RoleSystem row, arms the "▸ /<name> running…" toast and the
// in-flight record, applies any SystemMessage / ModalAnswer / SwitchTo
// set on THIS result immediately, and then reads exactly one
// SlashResultOrErr from Follow. Hosts send exactly one value and
// close (or send and abandon — the TUI does not re-read). A Follow
// set on a result delivered THROUGH Follow is ignored; there is no
// chaining. Cancelling ctx discards whatever eventually arrives.
type SlashResult struct {
	SystemMessage string
	ModalAnswer   *SideAnswer
	SwitchTo      *SwitchTarget

	// Preamble is the chat-visible "this is running" row for a long
	// asynchronous command — the bottom-bar toast is easy to miss on a
	// 5-15s call. Rendered as RoleSystem before Follow is drained.
	// Empty = no row. Ignored when Follow is nil.
	Preamble string

	// Follow, when non-nil, marks this result as the FIRST half of an
	// asynchronous command; the second half arrives on the channel.
	// Nil = the command is complete.
	Follow <-chan SlashResultOrErr
}

// SlashResultOrErr bundles the SlashResult + error pair that Follow
// carries. Exactly one of Res / Err is meaningful per send. Res.Follow
// and Res.Preamble are ignored on a follow-up.
type SlashResultOrErr struct {
	Res SlashResult
	Err error
}
```

`SlashCommandSpec`, `SideAnswer` and `SwitchTarget` are unchanged.

TUI-side consequences, for scoping the implementation PR:

- `dispatchSlash` (`update.go:2280`) drops the
  `bareAsync` / `preambleAsync` probe at `:2321-2325` and always
  dispatches off-loop. It creates the cancellable ctx, stores
  `m.cancelSlash`, applies the #13 concurrent-slash refusal against
  `m.inFlightSlash`, and optimistically arms the toast.
- `slashDispatchCmd` (`host_async.go:358`) loses its `invoke bool`
  parameter and always invokes.
- `applySlashDispatch` (`update.go:2334`) collapses its three shape
  branches into one `if msg.res.Follow != nil`.
- `invokeSlashAsync` (`update.go:2449`) and `slashFlight`'s two
  arming sites merge into one.
- `refuseConcurrentSlash` keeps its policy and loses one caller.

### 4.2 Deletions

```go
// tui/agent.go — removed entirely.
type Content struct {
	Role  string
	Text  string
	Parts []ContentPart
}
type ContentPart struct {
	Kind string
	Data any
}
type ContentRunner interface {
	RunWithContents(ctx context.Context, contents []Content) iter.Seq2[Event, error]
}

// tui/capabilities.go — removed.
type SessionByModelTracker interface {
	SessionByModel() map[string]ModelTotals
}

// tui/capabilities.go — ModelTotals unexported (still used internally
// by formatModelBreakdown and usageByModelToTotals on the push path).
type modelTotals struct {
	Turns        int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}
```

`ContentRunner` is a kind-1 deletion, and the strongest one here: it is
not merely unimplemented, it is **never asserted by `package tui`**.
The only non-declaration references in the whole tree are in
`content_test.go`, which asserts that the type assertion compiles.
Nothing in `update.go`, `model.go`, `view.go` or `agentcmd.go` ever
reaches for it. The library declares an interface so that hosts can
call each other through it. `R-CHAT-12` in `requirements.md` should be
dropped in the same change; §10 Q7.

### 4.3 On record but not proposed

Not part of this proposal. Recorded so the shape is settled if a second
host ever supplies the evidence:

```go
// SubagentReporter merges SubagentLister and SubagentEventReader.
// Additive: the TUI asserts this first and falls back to the two
// single-method interfaces, so it can land in any 1.x release.
type SubagentReporter interface {
	Subagents() []SubagentInfo
	SubagentEvents(ctx context.Context, name string, since int64) (SubagentEventPage, error)
}
```

---

## 5. API impact

This is the section to read first.

### 5.1 Removed

| symbol | kind |
|---|---|
| `tui.AsyncSlashProvider` | interface |
| `tui.AsyncSlashProviderWithPreamble` | interface |
| `tui.Content` | struct |
| `tui.ContentPart` | struct |
| `tui.ContentRunner` | interface |
| `tui.SessionByModelTracker` | interface |
| `tui.ModelTotals` | struct — only if §10 Q2 is answered yes |

### 5.2 Added

| symbol | kind |
|---|---|
| `tui.SlashResult.Preamble` | field, `string` |
| `tui.SlashResult.Follow` | field, `<-chan SlashResultOrErr` |

### 5.3 Changed

**Nothing.** `SlashProvider.InvokeSlash` keeps its signature; the
asynchrony rides in `SlashResult`. This is the single best property of
the §3.1 recommendation and the reason it is cheaper than it looks: a
host that implements only synchronous slash commands sees no compile
break at all.

### 5.4 What `verify-apidiff` reports

`dev/tools/verify-apidiff` diffs the module against the nearest
reachable release tag (`v0.20.0` today), prints everything, and fails
on incompatible changes not listed in `dev/api-breaks.txt`. The
allowlist is currently empty — `# (empty — no acknowledged breaks
against v0.20.0)`.

apidiff reports a removed top-level name as one line and does not
descend into its members, and reports interface method-set changes as
one line per method. Verified by running the pinned apidiff
(`v0.0.0-20260813180055-c1d0aacb2297`) over a reduced reproduction of
exactly these three shapes — a removed interface, a removed struct, and
an interface whose method changed signature. So the output is:

```
Incompatible changes:
- ./tui.AsyncSlashProvider: removed
- ./tui.AsyncSlashProviderWithPreamble: removed
- ./tui.Content: removed
- ./tui.ContentPart: removed
- ./tui.ContentRunner: removed
- ./tui.ModelTotals: removed
- ./tui.SessionByModelTracker: removed
Compatible changes:
- ./tui.SlashResult.Follow: added
- ./tui.SlashResult.Preamble: added
```

`dev/api-breaks.txt` grows by **seven entries** — six if `ModelTotals`
stays exported — plus their comment block. The file's own format
(`# <issue>: <reason>` above the bare `symbol: verb` lines, comments
and blanks stripped before comparison) makes this the shape to commit:

```
# #77: the three-way slash split collapses into one interface; the
# async shapes are expressed as SlashResult.Preamble + SlashResult.Follow.
# Nothing implemented the bare AsyncSlashProvider; both core-agent
# adapters move from the preamble variant to the enriched result.
./tui.AsyncSlashProvider: removed
./tui.AsyncSlashProviderWithPreamble: removed

# #77: ContentRunner is never asserted by package tui — the library
# declared it so hosts could call each other through it. Zero
# implementations in core-agent or the examples. R-CHAT-12 dropped.
./tui.Content: removed
./tui.ContentPart: removed
./tui.ContentRunner: removed

# #77: zero implementations since v0.6.4 (df18400). /stats keeps its
# per-model breakdown via the push-mode UsageByModel path, which is the
# only one any host has ever populated. Re-adding is additive.
./tui.ModelTotals: removed
./tui.SessionByModelTracker: removed
```

Each removal also needs a `CHANGELOG.md` **Removed** entry, per the
tool's header and `CONTRIBUTING.md`'s "Changing the exported API". The
allowlist is emptied when the release is cut, so these entries live
only until the tag moves.

### 5.5 Effect on the frozen-surface count

`design.md` §3 pins the frozen surface at **156 exported symbols** of
265, enumerated one by one in `api-surface.md` §3.1. **All seven
removals are on that list** — checked line by line: `AsyncSlashProvider`
(`api-surface.md:202`), `AsyncSlashProviderWithPreamble` (`:203`),
`Content` (`:204`), `ContentPart` (`:205`), `ContentRunner` (`:206`),
`ModelTotals` (`:212`), `SessionByModelTracker` (`:219`). The two
additions are struct fields, which the 156 does not count (it counts
top-level symbols; fields ride in as members).

So the contract goes **156 → 149**, and the total surface 265 → 258.
`api-surface.md` §3.1 loses seven rows, and `design.md` §3's "The
frozen surface is 156 exported symbols" changes with it. One row
survives with a changed justification rather than being deleted:
`SlashResultOrErr` (`api-surface.md:227`) is currently annotated
*"Reached from `AsyncSlashProvider`"* and becomes *"Reached from
`SlashResult.Follow`"*.

(`api-audit.md` §2 counts 223 exported top-level symbols against
`api-surface.md`'s 265. The two documents were produced at different
commits with different definitions of "top-level"; the discrepancy is
pre-existing, is not this proposal's to settle, and does not change the
seven-row delta either way. It is worth reconciling before the freeze
regardless, since §3 quotes one of them as a promise.)

---

## 6. Migration

`core-agent` is the host that pays. Everything below is against its
tree at the time of writing.

### 6.1 What does not change

Sixteen of the seventeen surviving capabilities are untouched. Neither
adapter implements `ContentRunner` or `SessionByModelTracker`, so those
two deletions cost `core-agent` nothing at all — not a line. The
`var _` assertions at `coretui_enabled.go:831`,
`coretuiremote/adapter.go:734` and `coretuiremote/subagent_events.go:79`
all still compile.

### 6.2 The slash change, local adapter

`core-agent`'s async wrapper is already a thin shell over its
synchronous handler. `coretui_enabled.go:1262`:

```go
// before
func (a *coreAgentAdapter) InvokeSlashAsync(ctx context.Context, name, args string) (string, <-chan coretui.SlashResultOrErr) {
	preamble := preambleFor(name, args)
	ch := make(chan coretui.SlashResultOrErr, 1)
	go func() {
		defer close(ch)
		res, err := a.InvokeSlash(ctx, name, args)
		ch <- coretui.SlashResultOrErr{Res: res, Err: err}
	}()
	return preamble, ch
}

// InvokeSlash satisfies coretui.SlashProvider. /btw calls
// AskSideQuestion + surfaces the answer through a SideAnswer modal; ...
func (a *coreAgentAdapter) InvokeSlash(ctx context.Context, name, args string) (coretui.SlashResult, error) {
	switch name {
	case "btw", "by-the-way":
	...
```

```go
// after
func (a *coreAgentAdapter) InvokeSlash(ctx context.Context, name, args string) (coretui.SlashResult, error) {
	ch := make(chan coretui.SlashResultOrErr, 1)
	go func() {
		defer close(ch)
		res, err := a.invokeSlashBlocking(ctx, name, args)
		ch <- coretui.SlashResultOrErr{Res: res, Err: err}
	}()
	return coretui.SlashResult{Preamble: preambleFor(name, args), Follow: ch}, nil
}

// invokeSlashBlocking is the body of every host slash command; it runs
// on the goroutine InvokeSlash starts. /btw calls AskSideQuestion + ...
func (a *coreAgentAdapter) invokeSlashBlocking(ctx context.Context, name, args string) (coretui.SlashResult, error) {
	switch name {
	case "btw", "by-the-way":
	...
```

The whole migration is: rename the old `InvokeSlash` to
`invokeSlashBlocking`, and rewrite `InvokeSlashAsync` into the new
`InvokeSlash`. **Nine lines deleted, eight added, one identifier
renamed, no logic touched.** `preambleFor` is unchanged. The 200-line
`switch` is unchanged.

`coretuiremote` takes the same edit at `capabilities.go:939` / `:960`
/ `:1040`, with the same shape.

The upgrade is also strictly better for the host than what it has now:
today the type-level choice forces *every* `core-agent` command through
the async path, including the ones `preambleFor` deliberately returns
`""` for. Under the proposal `invokeSlashBlocking` can return a
`SlashResult` directly for the fast commands and let the slow ones set
`Follow` — but it does not have to, and the migration above does not.

### 6.3 The examples

`examples/core-agent/local.go` — no change. It implements
`SlashProvider` synchronously and always did.

`examples/core-agent/attach.go` — the change **fixes** it. Today it
implements `SlashCommands` + `InvokeSlashAsync` and therefore has no
working slash commands (§3.1). Renaming `InvokeSlashAsync` to
`InvokeSlash` and returning `SlashResult{Preamble: …, Follow: ch}`
satisfies the one interface there is, and `/btw` starts working for the
first time. Net ±0 lines and one live bug closed.

`examples/local/main.go` — no change.

### 6.4 Test suite

`content_test.go` is deleted with `ContentRunner`.
`slash_stats_test.go`'s `modelAwareTracker` (`:40-45`) loses its
`SessionByModel` method and the by-model assertion moves to the push
path, which is where every real host's breakdown comes from anyway.
`slash_test.go`'s three async fakes (`asyncSlashAgent` `:254`,
`blockingAsyncSlashAgent` `:342`, `preambleAsyncSlashAgent` `:561`)
collapse into one fake with a `Follow`-setting `InvokeSlash`, and
`slash_test.go:573-576`'s tripwire comment — which exists solely to
remind fakes to implement `InvokeSlash` too — is deleted along with the
trap it guards.

---

## 7. The "<50-line adapter" claim

The claim is not in an `I-COGO` document any more. `I-COGO` was
withdrawn when cogo stopped gating (`requirements.md:757`), and the
claim now lives, already retired, in `design.md` §1 goal 4:

> This goal used to say "a ≤ 50-line adapter". `examples/core-agent`
> (issue #82) was written partly to measure that number, and the
> measurement says it was never true: the *minimum* adapter —
> `Agent.Run` translating a host's nested event tree into `tui.Event`,
> and nothing else — is **64 non-comment lines** […] Bringing it down
> is what issue #77's capability consolidation is for.

**That last sentence is wrong, and this proposal cannot fix it.** The
floor is `Agent.Run`. A minimal adapter under this scheme implements
`Agent` and nothing else, exactly as today:

```go
type minimalAdapter struct{ inner *host.Agent }

func (a *minimalAdapter) Run(ctx context.Context, prompt string) iter.Seq2[tui.Event, error] {
	return func(yield func(tui.Event, error) bool) {
		for ev, err := range a.inner.Run(ctx, prompt) {
			if err != nil {
				yield(tui.Event{}, err)
				return
			}
			// …translate the host's event tree into tui.Event…
			if !yield(te, nil) {
				return
			}
		}
	}
}
```

Fourteen lines of frame plus however many the host's event tree costs —
50 in `examples/core-agent`'s measurement, hence 64. **No capability
consolidation can move that number, because no capability is
involved.** The ≤50-line goal was a claim about event translation
misfiled as a claim about interface count, and deleting three unused
interfaces changes it by zero.

`examples/core-agent/local.go:84-89` frames the right question, and
this proposal should be read as answering it:

> This file satisfies 13 interfaces; `attach.go` adds 5 more it doesn't
> share, for 18 of the plug-in surface between them. The distance
> between "the required agent surface is tiny" (`design.md` §1 goal 3)
> and what a real host ends up writing is the input this example owes
> issue #77.

The answer is that the distance is method bodies, not interface
headers. The twelve optional capabilities in `local.go` cost 113 lines
of body between them; the `var _` block that declares all thirteen
interfaces costs 21 (`local.go:44-64`). Any regrouping moves the 21 and
leaves the 113 exactly where it is.

What does move:

| adapter | today | after |
|---|---|---|
| minimum (`Agent` only) | 64 non-comment lines | **64** — unchanged |
| `examples/core-agent/local.go` | 341 lines / 248 adapter | **341 / 248** — unchanged |
| `examples/core-agent/attach.go` | 467 lines / ~303 adapter | **~466 / ~302**, and `/btw` works |
| `core-agent` local | ~1,050 | **~1,049** |
| `core-agent` attach | ~1,630 | **~1,629** |

**Recommendation:** strike "Bringing it down is what issue #77's
capability consolidation is for" from §1 and say what the number
actually depends on. §6.1's ~150-line and §6.2's ~400-line per-host
budgets are unaffected and remain the honest targets. If the owner
wants the floor lowered, the lever is a helper in `package tui` that
turns a callback into an `iter.Seq2[Event, error]` — a different issue,
and an additive one.

---

## 8. Replacement text for the two documents

Not edited in this PR. Quoted here so it can be lifted.

### 8.1 `MIGRATION.md` §1, item 2

Currently the list omits six capabilities that exist and names them in
an order that suggests a hierarchy that is not there.

> 2. **Implement zero or more capability interfaces** from §3.3.
>    Each lights up the corresponding slash command or UI affordance;
>    missing ones degrade to a "not available in this host" message.
>    The full roster, and what each one costs to decline:
>
>    | capability | lights up | declining it means |
>    |---|---|---|
>    | `ModelSwapper` | `/model` | "not available" |
>    | `SessionSwitcher` | `/switch` | falls through to a `SlashProvider` command returning `SlashResult.SwitchTo` |
>    | `Reloader` | `/reload` | "not available" |
>    | `PermissionController` | `/permissions`, `/allow`, `/deny`, allow-always persistence | "not available"; the modal still works per-turn |
>    | `PricingController` | `/pricing` | "not available" |
>    | `ToolLister` | `/tools` | "not available" |
>    | `SubagentLister` | `/subagents`, the sidebar roster | "none (no SubagentLister)" |
>    | `SubagentEventReader` | the `/subagents <name>` turn log, live sync-subagent preview | roster rows are not drillable |
>    | `StatusReporter` | the status header's state field | the TUI derives model + state from `Options` |
>    | `UsageTracker` | the per-turn footer, `/stats`, `/export` totals | "no UsageTracker wired" |
>    | `InjectableAgent` | mid-turn injection under `InjectIntoCurrent` | silently degrades to `QueueForNext` |
>    | `LiveAgent` | observer sessions on a daemon's autonomous turns | per-turn `Run` only |
>    | `InboxDrainer` | the auto-continue loop | no auto-continue |
>    | `WakeRequester` | wake toasts (R-WAKE-1) | no toasts |
>    | `RemoteInterrupter` | `/interrupt` on a turn the TUI has no local context for | "no turn in flight" on remote sessions |
>    | `SlashProvider` | host-defined slash commands in `/help`, the palette and dispatch | the built-in catalog is the whole palette |
>    | `PermanentStreamError` | precise permanent-vs-transient stream failure | the TUI falls back to a string heuristic |
>
>    Two things to know before you start. `UsageTracker` is supplied
>    through `Options.UsageTracker`, not detected off the `Agent`, so it
>    may live on its own type. And a capability is detected by type
>    assertion, which means a method with the wrong name or signature
>    fails **silently** — write `var _ tui.X = (*yourAdapter)(nil)` for
>    every capability you intend to satisfy. `examples/core-agent`
>    carries such a block for exactly this reason, and the two bugs it
>    would have caught in the reference host are recorded in
>    `docs/design-capability-consolidation.md` §1.3.

### 8.2 `docs/design.md` §3.3

§3.3 is normative and has drifted. It declares 14 of the 20
capabilities — `UsageTracker`, `SessionByModelTracker`, `LiveAgent`,
`InboxDrainer` and both async slash variants appear nowhere — and two
of the declarations it does carry are wrong: `PermissionController` is
shown with six methods including `AlwaysAllow(req PermissionRequest) error`
and `Snapshot() PermissionSnapshot`, neither of which exists (the
allow-always hook became `Options.AlwaysAllow`, and `PermissionSnapshot`
never existed at all), and `Status` is shown without its `Provider`
field. Fixing the drift is a bigger edit than this proposal's own
changes and should ride along with it.

The opening paragraph becomes:

> ### 3.3 Optional capability interfaces
>
> The TUI feature-detects each via type assertion. Each interface
> matches one user-visible feature and is documented as such. There are
> **seventeen**; this section declares all of them, and a capability
> that is not declared here is not part of the plug-in surface.
>
> Detection is by type assertion, so a near-miss — a method with the
> right name and the wrong signature — is indistinguishable from
> declining the capability. That failure mode has cost the reference
> host two live features (`docs/design-capability-consolidation.md`
> §1.3). Hosts should write `var _ tui.X = (*adapter)(nil)` for every
> capability they mean to satisfy; `examples/core-agent` does.

The slash block becomes the declarations in §4.1 above. The
`ContentRunner`, `Content`, `ContentPart` and `SessionByModelTracker`
blocks are struck.

### 8.3 `docs/design.md` §10 risk 1

The mitigation as written points at the one move this proposal rejects.

> 1. **Adapter boilerplate fatigue.** The capability interfaces are
>    past the ~10 this risk originally set as a threshold — 17 after
>    #77's consolidation. The threshold turned out to measure the wrong
>    thing: an adapter's cost is its total *method* count and the
>    translation work behind each method, not the number of interfaces
>    those methods are grouped into. `examples/core-agent`'s local
>    adapter is 248 lines across twelve capabilities; regrouping them
>    into four would not delete a line.
>
>    The mitigation this risk originally proposed — folding a
>    near-universal capability into base `Agent` — is **rejected**.
>    Doing so would make one host's architecture mandatory for every
>    future host, force `examples/local` and `tui/testagent` to grow
>    stubs for features they do not have, and destroy the "not
>    available in this host" degradation §3 treats as load-bearing.
>    Deciding what is "required for most hosts" from a sample of one
>    host is not a decision 1.0 should make permanent.
>
>    The count is managed instead by (a) deleting capabilities nothing
>    implements — the only removals 1.0 forecloses, so they happen
>    before the freeze; and (b) merging redundant ones **additively**,
>    by adding a union interface the TUI prefers and falling back to
>    its parts, which is compatible and can therefore wait for the
>    second host that would justify it.

---

## 9. Staged plan

Three stages, in order. The deprecate-then-remove split exists so
`core-agent` is never in a window where it cannot compile against a
release.

**Stage A — additive, no breaks. Ships as a minor release.**

1. Add `SlashResult.Preamble` and `SlashResult.Follow`. Teach
   `dispatchSlash` / `applySlashDispatch` to honor them, **while
   leaving both async interfaces working**, asserted after the new
   path. Both routes live; a host on either behaves identically.
2. Fix the `design.md` §3.3 drift and rewrite §10 risk 1 (§8.2, §8.3).
3. Fix `MIGRATION.md` §1 (§8.1).
4. Document that `SlashProvider` is currently a mandatory base for the
   async variants, so the surface stops lying while it still has them.
5. Add the missing `var _` canaries to `examples/core-agent`, and fix
   `attach.go` to implement `InvokeSlash`.
6. `verify-apidiff` reports two compatible additions and nothing else.
   `dev/api-breaks.txt` stays empty.

**Stage B — `core-agent` migrates.** The host PR in §6.2, against
Stage A's release. Both adapters move to the enriched `SlashResult` and
drop `InvokeSlashAsync`. Nothing in core-tui has been removed yet, so
this PR can be reverted freely. Independently, `core-agent` should fix
the two silent capability misses at `coretui_enabled.go:603` and
`coretuiremote/adapter.go:705`, and add `var _` lines for all
seventeen.

**Stage C — the removals. This is the point of no return.**

1. Delete `AsyncSlashProvider`, `AsyncSlashProviderWithPreamble`,
   `ContentRunner`, `Content`, `ContentPart`,
   `SessionByModelTracker`; unexport `ModelTotals`.
2. Collapse the dispatch path (§4.1) and delete the now-dead branches.
3. Delete `content_test.go`; fold the three async slash fakes into one.
4. Seven lines and their comments into `dev/api-breaks.txt`; seven
   entries under **Removed** in `CHANGELOG.md`; `api-surface.md` §3.1
   loses seven rows and `design.md` §3's "156 exported symbols" → 149.
5. Drop `R-CHAT-12` from `requirements.md`.

Stage C is the last release before 1.0 in which any of this is
possible. Everything else in this document — the subagent merge, a
`Run`-translation helper, `ToolRenderer`'s fate if it is promoted
rather than deleted — is additive and has no deadline.

Sequencing against the other pre-freeze issues is unchanged from
`api-audit.md` §7: after #79 (vestigial paths), before #78 (the
exported-surface narrowing), because #78's symbol counts should be
computed once against the post-consolidation surface rather than
twice.

---

## 10. Open questions

**Q1 — Unify the slash surface, or take the cheap rename?**
The cheap move (delete the bare `AsyncSlashProvider`, promote the
preamble variant into its name) is one PR, zero host change, 3 → 2
interfaces. The unification is 3 → 1, fixes the mandatory-base trap and
the false godoc, makes per-command asynchrony real, and costs
`core-agent` about ten lines per adapter.
*Recommendation: unify.* The cheap move buys a smaller number and
leaves both defects, and `examples/core-agent/attach.go` is proof the
defects bite.

**Q2 — Unexport `ModelTotals` in the same PR?**
It is still used internally by the push-mode `/stats` path. Leaving it
exported means shipping 1.0 with a type whose only reason to exist was
the interface being deleted.
*Recommendation: yes.* One extra `api-breaks.txt` line, and it is
precisely the vestige #78 would otherwise find.

**Q3 — Merge the subagent pair now, or defer?**
Merging is 17 → 16 and costs `core-agent` one deleted `var _` line.
Deferring keeps a distinction `capabilities.go:243-248` documents and
`slash_builtin.go:370` reads.
*Recommendation: defer.* The merge is additive (§4.3), so the freeze
deadline does not bind it, and the claim it rests on — no host lists
subagents without logging their turns — cannot be checked at n=1.

**Q4 — Rewrite `design.md` §10 risk 1, or delete it?**
It is the only written rule on this subject and its mitigation is
wrong.
*Recommendation: rewrite, per §8.3.* A risk register that records a
rejected mitigation and why is worth more than a deleted entry.

**Q5 — Does the surviving `SlashProvider` need a hard compile-time
canary, or is documentation enough?**
Three capabilities are implemented in this tree by types that do not
satisfy them, and all three are silent. Options: leave it to host
discipline; ship `var _` examples (Stage A item 5); or add an opt-in
`tui.CheckCapabilities(agent any) []string` that a host can call in a
test to list the capabilities it *nearly* satisfies.
*Recommendation: Stage A item 5 now; treat the checker as #82's, since
it is additive and needs its own design.*

**Q6 — `Focusable` and `ToolRenderer` — this issue or #78?**
Both are exported extension points a host cannot use: `Focusable` has
no implementor or consumer at all, `ToolRenderer` has no `Options`
field to supply one through. Removing either is breaking, so whichever
issue owns them, they must land before the freeze.
*Recommendation: #78 owns them, and #78's schedule must be inside the
freeze window. If it slips, fold them into Stage C — two more
`api-breaks.txt` lines and no host impact, since neither has ever been
implementable.*

**Q7 — Drop `R-CHAT-12`, or keep it as an unimplemented requirement?**
`ContentRunner` was declared to satisfy it and never wired to anything
in the TUI.
*Recommendation: drop it.* A requirement whose design artifact was an
interface the library never calls is not a requirement; if
structured-prompt entry is wanted, it needs a UI affordance first and
an interface second.

**Q8 — Do the `core-agent` fixes gate the freeze?**
`Interrupt() bool` and `RequestWake()` are host bugs, not library bugs,
and 1.0 does not depend on them. But they are the evidence for Q5 and
they mean two capabilities the matrix in §1.4 counts as "one adapter"
were intended as "both".
*Recommendation: no gate; file them against `core-agent` and reference
them from #82.*
