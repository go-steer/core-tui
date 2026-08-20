# Operator hold — Esc arms a hold, the TUI renders it, slashes work mid-turn

**Status:** implemented, in
[#260](https://github.com/go-steer/core-tui/issues/260) — the last open
item on the [v1.0 milestone](https://github.com/go-steer/core-tui/milestone/1).

**What it adds:** the `Pauser` capability, an Esc that reaches it, a
banner that says the agent is held and what to do about it, and
mid-turn slash dispatch.

Line references are against `main` at the time of writing.

---

## 1. Problem

An operator watching an agent work wants one reflex: stop, tell me
what's happening, let me redirect you or wave you on. Neither half of
that worked from core-tui, for two independent reasons.

**Every slash typed during a turn was swallowed.** The enter handler
routed to `enqueueDuringStream(text)` *before* the `/`-prefix dispatch,
so typing `/interrupt` or `/btw …` mid-turn queued the literal string
as a prompt and dispatched nothing. Mid-turn is exactly when those two
exist.

**Esc was a no-op against a daemon-driven loop.** The Esc cascade's
last arm required `m.state == stateStreaming && m.cancelTurn != nil`,
which only a locally driven turn sets. `/interrupt` had already grown a
`RemoteInterrupter` arm for this; Esc never learned it. In observer
mode — the mode where the agent is working on its own and stopping it
matters most — Esc did nothing at all.

**And the host moved underneath us.** core-agent shipped a pause gate
as protocol 1.5.0: `POST /interrupt` now defaults `hold=true`, and
interrupting *parks* the loop rather than cancelling one turn and
letting the scheduler start another. `Agent.Run` awaits the gate as its
first step. So the moment Esc reaches a 1.5.0 host, the agent parks —
and a TUI that does not know it is parked will call `submitTurn` and
spin a spinner forever against a gate nobody will open. Tracking the
hold is what makes Enter route to `Resume` instead. The two defect
fixes and the paused UI are one change, not three.

---

## 2. Settled decisions (do not relitigate)

1. **`/continue` + `/cont`, not `/resume`.** `/resume` already means
   "load a saved transcript". #260's body says `/resume`;
   [#268](https://github.com/go-steer/core-tui/issues/268), split out
   of #260, records that `/continue` was picked to route around the
   collision. #268's reading stands. Renaming the transcript `/resume`
   is #268's call and out of scope here.
2. **Held is a field, not a third `turnState`.** `docs/decisions.md`
   D30 has the argument: `m.state == stateStreaming` is paired with a
   non-nil `m.cancelTurn`, the two conditions are not mutually
   exclusive (a `/pause` lets a running turn finish), and the observer
   path stays `stateIdle` while the daemon streams — so a
   `statePaused` would be unreachable in the mode the feature is for.
3. **Esc always reaches the strongest stop the host offers, in flight
   or not.** In observer mode the spinner is off between daemon-driven
   turns, which is precisely the window where an operator wants to get
   ahead of the next one. Holding is never a dead end: Enter with text
   un-holds.
4. **`Pauser` mirrors core-agent's wire types field for field**, not
   the shape a sketch would reach for. Struct returns rather than a
   three-value tuple and two adjacent bare strings, so an adapter is a
   copy rather than a translation layer, and the `Interrupted` bit
   survives the trip. Plain `string` for the mode with a
   named-constant vocabulary, matching `TurnError.Kind` and
   `InboxEvent.State` rather than introducing a named string type.
5. **One PR.** Landing the Esc arm on its own would ship a key that
   parks the agent with nothing on screen saying so and no way back.

---

## 3. The capability

```go
type Pauser interface {
	Pause(ctx context.Context, reason string) error
	Resume(ctx context.Context, req ResumeRequest) error
	PauseState() PauseInfo
}
```

`Pause` and `Resume` take `RemoteInterrupter`'s contract: they MAY
block briefly on network I/O, the TUI calls them off the Update-loop
path with a short deadline, and errors surface as an inline `RoleError`
row.

`PauseState` takes `StatusReporter.Status`'s contract instead — it is
polled from the once-a-second `hostSnapshot` tick and MUST NOT block.
It exists even for hosts that push a `PauseEvent`, because it is what
makes a TUI attaching to an already-held session render the banner
without waiting for a transition that already happened.

`PauseInfo.Interrupted` distinguishes the two ways into a hold: true
when a turn was actually cancelled on the way in, false for a plain
`/pause` or an interrupt that landed while the agent was idle. "Your
work was killed" and "the loop just won't start" are different
situations and the banner says which. It is the first thing an operator
asks.

The three `ResumeMode*` values are the dispositions: `steer` (resume
with this new instruction), `continue` (carry on where you left off),
`abandon` (open the gate, drop the interrupted work, do not wake the
loop).

### What a steer means depends on who owns the loop

`ResumeModeSteer` asks the **host** to make the operator's text the
next turn. That only works for a client that can watch the host's loop.

A `LiveAgent` host has a standing `Events` stream, so it sees the
steered turn like any other. A per-turn host does not: `Agent.Run`
opens a subscription for the turn *it* starts and closes it when that
turn ends. Send `ResumeModeSteer` there and the host dutifully runs the
turn against a stream nobody has open — the operator watches their
prompt land as a user row and then nothing, while the host's turn
counter ticks. That is a real bug this design shipped with, found by
running `examples/core-agent -flavor attach` (per-turn) by hand.

So Enter-while-held branches on `m.liveMode`:

| host shape | mode sent | who runs the turn |
|---|---|---|
| `LiveAgent` | `ResumeModeSteer` with the text | the host; `Events` shows it |
| per-turn | `ResumeModeAbandon`, no text | the TUI, via `submitTurn` once the resume acks |

`abandon` rather than `continue` on the per-turn side because the
client is about to run the operator's instruction itself; asking the
host to also pick its held work back up would run two turns for one
keypress.

The per-turn path is the only one that is asynchronous — the gate has
to be open before `Run` is called, or `Run` blocks in the host's
`awaitResume`, which is the hang this whole feature exists to prevent.
So the text rides back on `resumeDoneMsg.submit` and `submitTurn`
happens in that arm. The user row is appended by `submitTurn` and
nowhere else, so the transcript gets exactly one copy; and a resume the
host refuses puts the text back in the input box, because a steer the
operator already committed to is not something to make them retype.

What a host resumes on its own after `/continue` follows the same rule:
visible to an observer, invisible to a per-turn client. That is a
property of the host shape, not of the gate, and it is not something
the TUI can paper over.

---

## 4. Two sources of truth, and how they are reconciled

The gate's state arrives two ways, and they can disagree:

- the `pause` SSE event (spec §2.8) — immediate, authoritative at the
  instant it fires, the primary path in attach mode;
- `PauseState()` polled by `pullHostSnapshot` — slow, authoritative
  eventually, and the only path for a host with no push stream.

A poll already in flight when a resume lands returns the pre-resume
answer and would flip the banner back on for a tick. So an applied
transition wins over a contradicting poll for `pauseSettleWindow`
(2 s), and after that the host's answer wins — it is the truth, and a
TUI that ignored it would stay wrong forever after a missed event.

---

## 5. What Esc does now

The cascade, in order, stopping at the first arm that fires:

1. Modal/overlay/palette dismissal — unchanged, and still first.
2. **Held already?** Dismiss the banner. Leave the gate shut. Esc must
   never accidentally resume, so the escape from a hold is always an
   explicit disposition.
3. **A locally driven turn?** Cancel it first. That is instant and it
   unwedges the local stack before any network call.
4. **`Pauser` wired?** Hold, off-loop, with a bounded context.
5. **`RemoteInterrupter` wired, nothing cancelled locally, and a turn
   in flight?** Interrupt — which parks server-side on hosts new
   enough to hold, and which the `PauseState` poll will surface once
   that host's adapter grows `Pauser`.
6. Otherwise nothing: no row, no error. A host that granted neither
   capability gets the Esc it has today.

---

## 6. The banner

```
‖  Interrupted — what do you want me to do instead? (operator interrupt)
   type to steer · /continue to carry on · /abandon to drop it · esc dismiss
   1 background subagent still running
```

Three things about it are load-bearing:

**It is charged to the layout budget.** The banner is unshrinkable
chrome, like the toast and the footer. `allocateChrome` measures it —
by calling `renderPauseBanner` and taking its height, not by guessing
from `m.pause` — and reserves those rows off the top before the
viewport gets what is left. An unbudgeted banner does not overflow
gracefully; it pushes the footer off the bottom of the frame.

**Every row fits, at every width.** A wrapped row would silently cost
the viewport a line and move the input's origin. Both variable-width
pieces degrade instead: the host's reason is dropped if it does not
fit, and the key legend has three tiers that shed the glosses before
they shed a verb. The narrowest tier still names all three of steer,
`/continue` and `/abandon` — an operator who cannot see `/abandon` has
no way out of the hold.

**The subagent count comes from the roster we already poll.** No wire
widening: the `running_subagents` field on core-agent's interrupt
response is not reachable through `RemoteInterrupter`, and
`SubagentReporter`'s roster is already cached off-loop.

---

## 7. Mid-turn slash dispatch

The alias fold at the top of `dispatchBuiltinSlash` is extracted as
`canonicalSlashName`, so the allowlist and the dispatcher cannot
disagree about what `/int` or `/sess` means. The enter handler consults
it *before* the streaming branch, and sorts the name into three
buckets:

| bucket | names | behaviour |
|---|---|---|
| safe | `help`/`?`, `stats`, `tools`, `subagents`, `mcp`, `memory`, `skills`, `keys`, `interrupt`/`int`, `pause`, `continue`/`cont`, `abandon`, plus the host-side `btw`, `status`, `context`, `agents` | dispatch now |
| refused | `compact`, `done`, `replan`, `clear`, `subagent` | "not while a turn is running — /interrupt first" |
| everything else, including any unknown `/word` | | queue as a literal prompt, exactly as today |

The third bucket is the default on purpose: `/foo bar` typed as prose
must not be hijacked by a command that does not exist. The refused set
is the one that mutates conversation state or writes a boundary;
`Agent.Compact` and `Checkpoint` already refuse mid-turn server-side,
so this makes the client agree with the server rather than silently
queueing text that will be rejected.

---

## 8. Degrading

A host with no `Pauser` is not a broken host, and the degrade path is
exercised in the same binary as the working one: `examples/core-agent`
implements `Pauser` on the attach adapter and deliberately does **not**
implement it on the local one. The local flavor is per-turn — nothing
but the operator starts a turn, so there is nothing to hold and a steer
would have no loop to redirect.

Without the capability: Esc falls through to the local cancel or to
`RemoteInterrupter`; `/pause`, `/continue` and `/abandon` report
themselves not available in this host, the same row every other
ungranted capability produces; and the banner never renders, because
nothing ever reports a hold.

---

## 9. Out of scope

- **Renaming the transcript `/resume`** — #268.
- **The REST half of the 1.5.0 protocol revision**, and the 1.6.0
  `title` field — [#270](https://github.com/go-steer/core-tui/issues/270).
  This change takes `docs/sse-event-stream-protocol.md` to 1.5.0 by
  writing the `pause` event it actually consumes. The document has
  never specified REST; #270 decides whether it starts.
- **The core-agent side** — the module pin bump,
  `coreAgentAdapter.Interrupt(ctx) error`, and `coretuiremote.Adapter`
  implementing `Pauser`. That lands there, after this tags.
- **`stop_subagents` / `POST /agents/{name}/stop`.** The banner
  *reports* running subagents; stopping one from the TUI is a
  follow-up.
- **Coalescing rapid holds.** Each transition renders; there is no
  debounce, on the same reasoning `WakeRequester` uses.
