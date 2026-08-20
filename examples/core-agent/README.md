# core-agent example — driving it by hand

The package doc in [`main.go`](./main.go) covers what this example *is* and how
to start it. This file covers what it's like to sit in front of, which is a
different question: the same feature behaves differently in each of the three
shapes below, and a few things you might reach for will mislead you.

Everything here is about manual exercise. The automated coverage lives in
`tui/*_test.go` — see [Where the tests already are](#where-the-tests-already-are).

```bash
go run ./examples/core-agent                       # A. local
go run ./examples/core-agent -flavor attach        # B. per-turn
go run ./examples/core-agent -flavor attach -observer   # C. live
```

## The three shapes

They differ in who drives the loop, and that changes what you see.

| | drives the turn | event delivery | what it stands in for |
|---|---|---|---|
| **A. local** | `Agent.Run`, in-process | direct | a host with no daemon |
| **B. per-turn** | `Agent.Run` over HTTP; the subscription opens per turn | 1 s poll for host state | a client that only speaks while it's asking |
| **C. live** | the daemon; core-tui watches | standing SSE stream | a real core-agent |

Two consequences worth holding onto:

**In B, host state arrives on a 1 s tick.** Press a key and wait a beat before
deciding nothing happened. In C the same change is painted immediately, because
the push beats the poll. If you want to know whether a row came from the push or
the poll, that latency *is* the tell.

**In B, nothing parks at the gate on its own.** A per-turn client never starts a
turn the operator didn't ask for, so a hold has nothing queued behind it: hold,
then resume, and no turn follows — because none was owed. That reads like a
no-op and it's the honest answer. C is where a resume visibly continues work,
since there the daemon had a turn of its own in flight.

**A has no `Pauser` at all.** It's the degrade path: the hold key does nothing,
and `/pause`, `/continue`, `/abandon` each answer *"agent doesn't implement
Pauser"*. That's the graceful degradation the capability design promises, and
seeing it is the point of keeping the flavors split.

## Slowing the turn down

The canned turn steps every 90 ms, so it's over in well under a second and
there's no window to type into. To exercise anything mid-turn, stretch it:

```bash
# one occurrence, in the streaming loop of fakehost/agent.go
grep -n '90 \* time.Millisecond' examples/core-agent/fakehost/agent.go
sed -i 's/90 \* time.Millisecond/1500 * time.Millisecond/' examples/core-agent/fakehost/agent.go
```

Turns become ~15 s. Revert with `git checkout -- examples/core-agent/fakehost/agent.go`
before doing anything else — at 1.5 s a step, the non-mid-turn flows get tedious
to read.

### Order matters: interrupting ends the turn

The mid-turn path is gated on `turnInFlight()` (`tui/view.go:706`). The moment
you see `(interrupted)`, the turn is over and every later `/cmd` takes the
ordinary **idle** path — where `/compact` is `unknown command`, `/clear` arms its
confirmation, and prose starting with a slash dispatches instead of queueing.
All correct, and none of it is testing the mid-turn gate.

So do the interrupting cases **last**, and start a fresh turn for each one.

### Both routes into the gate

Typing `/` opens the slash palette, which has its own Enter handler. So there
are two ways a command reaches the dispatcher and they have to agree. Check both:
type `/clear` and press Enter with the palette open, then press Escape to dismiss
the palette and type the same line by hand.

## What this example wires, and what it doesn't

The mid-turn allowlist (`tui/slash_builtin.go:101`) is a static map sized for the
**real** core-agent host. Several names in it were never implemented here, so
testing them against this example tells you about the fake, not about core-tui.

| command | mid-turn routing | serviced here? |
|---|---|---|
| `/interrupt`, `/int` | dispatch | ✅ `RemoteInterrupter` + `Pauser` |
| `/pause`, `/continue`, `/cont`, `/abandon` | dispatch | ✅ `Pauser` |
| `/btw <text>` | dispatch | ✅ the only host-side slash it advertises |
| `/stats` | dispatch | ✅ `UsageTracker` |
| `/tools` | dispatch | ✅ `ToolLister` |
| `/subagents` | dispatch | ✅ `SubagentReporter` |
| `/memory`, `/mcp`, `/skills` | dispatch | ✅ static feeds (`main.go`) |
| `/help`, `/?`, `/keys` | dispatch | ✅ always |
| `/clear` | **refuse** | ✅ a real core-tui built-in |
| `/compact`, `/done`, `/replan`, `/subagent` | **refuse** | ❌ unimplemented — `unknown command` at idle |
| `/status`, `/context`, `/agents` | dispatch | ❌ not advertised — `unknown command` |

`/clear` is the strongest mid-turn probe: it's the only refused name that's a
working command at idle, so you're watching behaviour *change* rather than one
flavour of nothing become another. The mismatch on the last two rows is
[#284](https://github.com/go-steer/core-tui/issues/284).

The capability blocks in [`attach.go`](./attach.go) and [`local.go`](./local.go)
are the authority on what's implemented; the comment under the attach block
explains what's deliberately left out and why.

Also absent: the fake emits no `turn_error` frame at all, so nothing that depends
on one — error rows, the retryable label ([#285](https://github.com/go-steer/core-tui/issues/285)) —
can be reproduced here. That needs a real daemon.

## Where the tests already are

Most of what you'd think to check by hand is covered, and hand-checking it again
is a poor use of an afternoon:

- **Mid-turn slash routing**, both buckets and both routes — `tui/midturn_slash_test.go`,
  `tui/palette_submit_test.go`.
- **The hold**: state transitions, poll/push reconciliation, resume dispositions —
  `tui/pause_test.go`, `tui/hold_interrupt_test.go`.
- **The banner**, including its height budget across widths — `tui/pause_test.go`.
- **Startup, teardown and the permission round-trip** under a headless
  `tea.Program` — `tui/program_smoke_test.go`.

What stays manual is the part the tests are structurally blind to: whether a
remote turn genuinely stops when you interrupt it, whether the push and the poll
narrate the same change twice, and how any of it actually looks. The first two
need a real core-agent daemon rather than this fake.
