# Scheduled wakes in the running-tasks bar

**Status:** proposed. Builds on the running-tasks bar
([#369](https://github.com/go-steer/core-tui/pull/369), R-SUB-4 in
[`requirements.md`](./requirements.md)). API:
[`design.md`](./design.md) §3 (`SubagentReporter`). core-agent side:
[go-steer/core-agent#1283](https://github.com/go-steer/core-agent/issues/1283).

## Problem

A core-agent subagent spawned with a scheduler (`spawn_agent
{scheduler: "sleep"}`) can end a turn by calling `schedule_next_turn`:
"wake me in 10 minutes, and here is what I'll be doing — *polling
cluster-A on 10m cadence*". It then sleeps until that time. The TUI
cannot tell. The roster still says `running`, so the bar draws a
`▶ … · 14m32s` row counting up as if the subagent were busy, when it
is in fact idle until a known time, for a known reason.

The fire time and the reason both exist inside core-agent
(`tools.ScheduleEvent.WakeAt` and `.Detail`). Neither reaches the
roster: `attach.AgentInfo` has no wake field, the background `Handle`
does not keep the event, and `attach.StatusInfo.NextWakeAt` is
declared but never populated.

What core-agent can schedule today decides the shape of this design:

- **Only background subagents.** core-agent's own modes (REPL, `--tui`,
  daemon, `core-agent-tui` attach) never hand the top-level session
  `schedule_next_turn`. Only a spawned subagent with a scheduler gets
  it.
- **At most one pending wake per subagent.** The event channel is
  buffered to one, so a second call in the same turn is dropped. It is
  "the next turn", not a list of timers.
- **No cancel and no reschedule.** A wake fires on time, fires early
  when that subagent's own wake channel is signalled, or dies with the
  run. No verb removes a pending wake and keeps the loop alive.

## Shape

Two optional fields on the existing roster entry:

```go
type SubagentInfo struct {
    Name       string
    Status     string
    LastReport string
    StartedAt  time.Time

    // NextWakeAt is when a subagent that has scheduled its next turn
    // will run it. Zero means none is pending: the subagent is
    // working, or it has finished. Set only while it sleeps; a host
    // clears it when the turn starts.
    NextWakeAt time.Time

    // WakeDetail is the one-line reason the subagent gave when it
    // scheduled the wake ("polling cluster-A on 10m cadence"). It may
    // be empty even when NextWakeAt is set.
    WakeDetail string
}
```

On the bar, a subagent with a pending wake gets its own row shape:

```
  ▶ reviewer · 1m05s · Load and pin actions were examined…
  ◷ cluster-watch · wakes in 8m12s · polling cluster-A on 10m cadence
```

The status count separates the two states:
`1 subagent running · 1 scheduled`.

## Settled decisions (do not relitigate)

1. **Fields on `SubagentInfo`, not a new capability.** A pending wake
   always belongs to a subagent and there is at most one per subagent,
   so it is an attribute of a roster entry, not a separate roster. A
   `ScheduleReporter` interface would be a second list keyed by the
   same names. Every host would have to join the two, and the TUI
   would have to reconcile them across two snapshots. Adding fields to
   a struct is additive under `verify-apidiff`. A host that never sets
   them gets exactly today's behaviour, and the exported surface (which
   v1.0 is narrowing) gains no interface.
2. **`Status` stays `running` while asleep.** The subagent's loop is
   alive, and `running` is what core-agent reports today. A new status
   word (`sleeping`, `deferred`) would break every TUI already shipped:
   `taskActive` would treat it as finished, and the bar would linger
   it as an outcome and then drop it. A zero/non-zero `NextWakeAt` is
   additive and old TUIs ignore it. core-agent's `deferred` status
   already means something else (the loop exited cleanly to be resumed
   elsewhere), which is one more reason not to reuse it.
3. **The row counts down to the wake, not up from the start.** For a
   sleeping subagent, time since spawn answers nothing. "When does it
   act next" is the question the row exists to answer. `◷` marks the
   state, and the state column reads `wakes in 8m12s` using
   `formatTurnElapsed`'s units.
4. **Past due reads `waking`, never a negative or a frozen `0s`.** The
   roster is polled once a second, so a wake that has fired can still
   show `NextWakeAt` for up to a tick. A row that says `wakes in 0s`
   for a second, or worse, counts below zero, is drawing stale data as
   fact.
5. **`WakeDetail` takes the report column while asleep.** While the
   subagent sleeps, the reason it gave for sleeping is the best
   one-line description of what it is doing. With no detail the column
   falls back to `LastReport`, exactly as for a running row.
6. **Sleeping subagents are on the bar but not counted as running.**
   The status line count is what survives when the bar is squeezed
   out, so it has to tell the two apart: `1 subagent running ·
   1 scheduled`, and `2 scheduled` alone when nothing is working.
7. **No session-level wake in this change.** `tui.Status` could carry
   a `NextWakeAt` for a host whose top-level loop schedules itself.
   core-agent's attach `StatusInfo` declares one. But no core-agent
   mode produces it, and a field with no producer is the kind of
   surface v1.0 is removing, not adding. It is a one-field follow-up
   when a host has the data.
8. **Ordering is unchanged.** Sleeping rows sort by `StartedAt` with
   everything else. Sorting by wake time would move rows each time a
   wake fired and was rescheduled, and a bar whose rows jump around is
   harder to read at a glance than one that stays put.

## core-agent's side

Tracked in [go-steer/core-agent#1283](https://github.com/go-steer/core-agent/issues/1283),
landed with its pin bump. In outline:

- The autonomous driver records the event it is about to sleep on.
  This could be the `WithScheduleHook(func(ScheduleEvent))` that
  `scheduled-monitoring-design.md` already sketches as "deferred unless
  a consumer asks" (this design is that consumer), or a field on the
  autonomous `Handle`. It clears the record when the next turn starts
  or the run ends.
- `attach.AgentInfo` gains `next_wake_at` and `wake_detail` (both
  optional): a protocol minor bump. `GET /sessions/{id}/agents` fills
  them in from the background `Handle`.
- Both core-tui adapters (in-process `Subagents()` and
  `coretuiremote`) map the two fields across.
- The checkpoint already persists `next_wake_at`. Persisting `Detail`
  alongside it would let a resumed subagent show its reason after a
  restart; that is optional.
- **Related gap, same code paths:** `Manager.ListSubagents` fills
  `LastReport` only from the final result (`Result().FinalText`). So
  while a subagent is running, including a declarative specialist like
  `cluster`, its bar row shows a name and elapsed time but no progress
  text. Filling `LastReport` while the subagent runs (from its latest
  `report_alert`, or its latest model text) is what makes the report
  column useful against core-agent. It touches the same
  listing-plus-adapters path as the wake fields, so it belongs in the
  same issue.

## Out of scope

- **Cancelling, rescheduling or firing a wake early from the TUI.**
  core-agent has no verb that does any of these while keeping the loop
  alive. A key on the bar that pretended otherwise would be worse than
  none. If core-agent grows one, it is its own design.
- **A session-level wake.** See decision 7.
- **Non-subagent timers** (wait_and_verify deadlines, WakeLoop backoff,
  auto-continue retries). The first already renders as an in-flight
  tool row. The other two have no label and no published fire time,
  and there is no proposal to add them.
- **Notifying when a wake fires.** The row switching back to `▶` is
  the signal. A toast per wake on a 30-second polling cadence would
  bury the toasts that matter.
