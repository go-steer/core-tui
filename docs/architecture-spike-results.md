# Architecture spike — results

> **Where this came from.** This file was written on the throwaway
> branch `spike/arch-bets`, which was never merged and has been
> deleted. That branch is archived at the annotated tag
> **`spike/arch-bets-2026-08-16`** (commit `31c73ce`), and everything
> this file links to — the pre-registered kill criteria, the mechanism
> notes, the probes, both arms — is readable there. It was moved here
> on 2026-08-18 with two edits and no others: its four relative links
> now point into the archive, with their link text unchanged, and the
> **Provenance and disposal** licence sentences no longer name the
> upstream project. That section is otherwise intact and is the point
> of it — it is the written record that no FSL-licensed source was
> copied or opened. The archived `NOTES.md` names the project, and
> every mechanism note in it is prose, which is what was implemented
> from.
>
> **Step 0 has already landed.** F1, F2 and F3 were each re-verified
> against `main` before the move and all three are fixed — see
> [`decisions.md` D29](./decisions.md#d29-what-the-rendering-rewrite-targets)
> for which issue did which. The spike measured a snapshot taken
> before that work; the verdicts and Steps 1–3 are live, the "do it
> now" in Step 0 is history.

Six hypotheses about core-tui's rendering architecture, each with a kill
criterion **fixed in writing before any challenger code existed**
([`README.md`](https://github.com/go-steer/core-tui/blob/31c73ce1c9bedd9709c34e3141ac0a5676dfff7a/spike/README.md)). This file reports what the measurements
said. Every verdict below quotes its criterion verbatim and traces to a
number; where a criterion could not be answered, it says so instead of
rounding it into a verdict.

Measured 2026-08-16 on linux/amd64, AMD EPYC 7B12, 16 cores, Go 1.26.6.
Wall-clock figures are from one machine and should be read as ratios
between arms rather than as absolutes. The primary metric is **counted
work** — item `Render()` invocations per frame — because it is the one
number that does not move when the machine does.

Reproduce:

```
cd spike
go test ./... -v                                  # every probe, with its table
go test ./... -bench . -benchtime 100x -run '^$' -benchmem
go run ./cmd/spike-preview -arm=a -turns=400      # and -arm=b
```

---

## Verdicts

| | Hypothesis | Verdict | The number that decided it |
|---|---|---|---|
| **H6** | negative control: the O(N) refresh is not user-visible | **FAILS** — rewrite case survives | p99 keystroke latency under stream: 84 ms at 400 turns, 154 ms at 1000, against a 25 ms kill gate |
| **H1** | item-addressed lazy list | **PASSES** | renders/frame flat at 7 from 10 to 4000 items; warm frame 18 µs / 42 KB against gates of 500 µs / 1 MB |
| **H2** | cell-buffer compositor | **KILLED** | the cheap control matches it on every correctness clause at 0.79× the time and 0.14× the bytes |
| **H3** | interactive resize | **PASSES** for the challenger, **KILLS** the baseline | drag p95: arm_a 62 / 239 / 143 ms vs arm_b 6.8 / 6.3 / 6.5 ms, gate 16 ms |
| **H4** | 20 fps spinner | **PASSES** | exactly 1 item render per tick at every size; 4.02% CPU against a 5% gate |
| **H5** | typed-`Action` dialogs | **PASSES**, including the partial-failure clause | dialog tests compile in an external package importing only `dialog`; grace period touches 1 file |

H6 was evaluated first, because firing it would have cancelled steps
3–6 and saved a quarter of engineering. It did not fire.

---

## H6 — negative control

> **Kill H1, H2 and H4** — abandon the list and compositor rewrite
> entirely — if arm_a's p99 keystroke-to-frame latency at 400 turns
> stays under the threshold under realistic scripted streaming.
> *Kill if p99-under-stream at 400 turns < 25 ms; inconclusive band
> 25–30 ms; H6 fails above 30 ms.*

A real `tea.Program`, the real renderer, the real command goroutine.
Latency is measured from the moment a keystroke is **enqueued** to the
moment the `View()` that follows it returns, so it includes queueing
behind stream events — which is the entire point.

| turns | p50 | p95 | p99 | verdict |
|---|---|---|---|---|
| 100 | 3.91 ms | 20.1 ms | 28.1 ms | inconclusive |
| 400 | 11.8 ms | 66.2 ms | **84.3 ms** | H6 fails |
| 1000 | 42.3 ms | 111.0 ms | **154.2 ms** | H6 fails |

At 1000 turns the **median** keystroke is 42 ms late. Nine per cent of
keystrokes take over 100 ms.

**The idle control is what makes this evidence rather than a number.**
Same probe, same corpus, same typing cadence, no stream:

| turns | idle p50 | streaming p50 |
|---|---|---|
| 100 | 3.97 ms | 3.91 ms |
| 400 | 4.07 ms | 11.8 ms |
| 1000 | 4.15 ms | 42.3 ms |

Typing costs a constant ~4 ms at any transcript size. **Every bit of the
size dependence comes from the stream-driven rebuild**, which is the
O(N) refresh landing directly on the keystroke path. Without this arm
the streaming numbers would have been consistent with "big transcripts
are slow", which is not an actionable claim about anything.

Secondary, and worth its own ticket: the idle tail is worse than the
median deserves — p99 of 22.8 / 24.7 / 28.8 ms with one 212 ms outlier
at 1000 turns, on a frame that allocates 1.29 MB across **50,297
allocations**. That smells like GC, not layout.

---

## H1 — item-addressed lazy list

> **Kill if** arm_b's per-frame item-`Render()` count grows with N
> rather than staying bounded by visible items; **or** warm-cache frame
> cost at 400 turns exceeds 500 µs; **or** bytes/frame exceeds 1 MB at
> 400 turns.

**Counted work.** Cold-frame item `Render()` calls, 40-row viewport over
6-row items:

| items | 10 | 100 | 400 | 1000 | 4000 |
|---|---|---|---|---|---|
| renders/frame | 7 | 7 | 7 | 7 | 7 |

Flat. A warm frame renders 0. A version bump on the live item renders
exactly 1 — which is what makes H4 affordable. `Overflows()` costs 7
renders at any size; `TotalHeight()` costs N, which is exactly why they
are separate methods and only one of them is allowed near a frame.

**Against the gates**, at 400 turns, with real Glamour and real chroma
in every item:

| | measured | gate |
|---|---|---|
| warm frame | **18.0 µs**, 41.7 KB, 2 allocs | 500 µs, 1 MB |
| cold frame | 3.56 ms, 740 KB | once per width change |
| stream frame | 579 µs, 142 KB | — |
| `Overflows()` | 3.4 µs, 312 B | — |
| scroll one line | 159 µs, 53 KB | — |

All flat from 10 to 1000 turns. **H1 passes on every clause.**

### The correction H1 needed

The hypothesis assumed the steady frame was the O(N) problem. It is not.
arm_a's `BenchmarkView` is *also* flat — ~3.5 ms and 1.28 MB at every
size — because it renders a pre-sliced viewport. The O(N) lives in the
refresh and resize paths, exactly where H6's probe put it.

Whole-frame against whole-frame (arm_b assembles the same five regions
arm_a does, so this is a UI against a UI, not a UI against a chat body):

| | 10 | 100 | 400 | 1000 |
|---|---|---|---|---|
| **warm repaint** arm_a | 3.91 ms | 6.92 ms\* | 3.59 ms | 3.48 ms |
| arm_b | 1.20 ms | 1.54 ms | **1.14 ms** | 1.13 ms |
| **width resize** arm_a | 17.9 ms | 19.1 ms | 29.5 ms | 55.8 ms |
| arm_b | 4.55 ms | 4.03 ms | 3.95 ms | **3.96 ms** |
| **stream flush** arm_a | 4.00 ms | 4.05 ms | 3.82 ms | 3.68 ms |
| arm_b | 2.07 ms | 1.71 ms | 1.73 ms | 1.71 ms |

\* an outlier; arm_a's repaint is flat at ~3.5 ms.

Allocation is the wider gap. A warm arm_a frame costs 1.28 MB across
**50,297** allocations; the same frame in arm_b costs 328 KB across
**1,267** — 40× fewer. That is the number behind H6's idle tail.

So the real win is **14× on resize at 1000 turns and flat instead of
linear**, plus 3× on the steady frame — but see F1, because a large
part of that 3× turned out not to be about laziness at all.

---

## H2 — compositor — KILLED

> **Kill if** the `strjoin+clip` backend passes 100% of the overflow
> goldens **and** a floating modal is deliverable via `lipgloss.Place`
> layering **and** `Compositor.Hit` cannot route a click into
> modal-local coordinates. Also kill if canvas frame cost exceeds 8 ms
> at 400 turns. **H2 may not be reported as a performance win under any
> outcome.**

Three interchangeable backends behind one interface, one golden suite
run against all three.

**Overflow goldens** (5 widths × 3 heights × {plain, modal}):

| backend | plain | modal |
|---|---|---|
| strjoin | 0/15 | 9/15 |
| **strjoin+clip** | **15/15** | **15/15** |
| canvas | 15/15 | 15/15 |

**Floating modal over live content.** `lipgloss.Place` discards the
body, so the control needs its own ~50-line ANSI-aware `Overlay`. With
it:

| backend | live rows visible behind the modal |
|---|---|
| strjoin / strjoin+clip | 0 |
| canvas | 14 |
| **clip + overlay** | **16** |

**Hit testing.** The criterion assumed only `Compositor.Hit` can route a
click into modal-local coordinates. The layout already computes every
rectangle, so a containment test over that struct answers the same
question — and agrees exactly: same layer, same local coordinates, same
z-order result for a click outside the modal.

**Cost**, 100×40, same content, with a modal:

| backend | ns/op | B/op | allocs/op |
|---|---|---|---|
| clip + overlay | 901 k | 74.7 KB | 279 |
| canvas | 1143 k | 521.8 KB | 202 |

The compositor is **1.27× slower and allocates 7.0× the bytes** for
capability the control also has. All three clauses met. **H2 is killed.**

### Unprompted, and against the compositor

The design predicted a width *disagreement* between lipgloss's
grapheme-cluster width and Bubble Tea's wcwidth. What is actually there
is content **loss**: the canvas backend drops combining marks.

| case | round-trips? |
|---|---|
| ZWJ family, flags, CJK, precomposed U+00E9, skin-tone modifiers | yes |
| **NFD combining** (`e` + U+0301) | **no** — 54 runes in, 51 out |
| **keycap sequences** (base + U+FE0F + U+20E3) | **no** — 47 in, 43 out |

The rule is: a combining mark on a narrow base is dropped. macOS hands
NFD out of the filesystem, so this is the path an accented **filename**
takes into a diff header or a file picker.

---

## H3 — resize

> **Kill if** per-event cost during a simulated drag exceeds 16 ms at
> 400 turns; **or** the settle/warm step blocks a frame for more than
> 16 ms; **or** a golden shows text at a stale wrap width for more than
> one frame after the drag ends. **Also kill the coupling claim** (and
> demote H1) if suppressing the Glamour pass *alone*, without the lazy
> list, already gets under 16 ms.

Both arms are driven by **one harness** ([`internal/dragprobe`](https://github.com/go-steer/core-tui/blob/31c73ce1c9bedd9709c34e3141ac0a5676dfff7a/spike/internal/dragprobe/dragprobe.go))
against a real `tea.Program`: 30 events, one column narrower each, 30 ms
apart, then 1.5 s of quiet. Sharing the harness removes "the arms were
measured differently" as an explanation for the gap.

**Clause 1 — enqueue-to-frame p95**, gate 16 ms:

| turns | arm_a | arm_b |
|---|---|---|
| 100 | 62.5 ms | **6.8 ms** |
| 400 | 238.5 ms | **6.3 ms** |
| 1000 | 143.4 ms | **6.5 ms** |

arm_a is killed at every size. arm_b passes at every size and is flat.
(arm_a's drag latency is noisy run to run — an earlier sweep read
43/79/136 ms — but it has never come within 2.7× of the gate.)

**Clause 2 — settle-frame block**, gate 16 ms: arm_a max 5.1 / 6.0 /
4.6 ms, arm_b max 2.1 / 2.5 / 1.4 ms. Neither arm is killed here.

**Clause 3 — stale wrap width — RETIRED.** Zero overflow frames, zero
underfilled frames, convergence to the final width within 3–45 ms
(arm_a) and 10–11 ms (arm_b) of the drag stopping. The debounce and the
clip pass are both doing their jobs. The criterion anticipated a
correctness cost that does not exist.

**Clause 4 — the coupling claim — NOT ANSWERED.** A suppression-only
arm without the lazy list was never built, so the criterion is
unresolved on its own terms. What *is* known: core-tui already ships a
debounce, which is a suppression mechanism, and the numbers above are
that debounce measured under a real drag. It misses the gate by 4–15×.
That is evidence against suppression-alone being sufficient in its
current form; it is not proof that no stronger suppression would do.
**Anyone relying on H3 should build that arm before committing.**

**Where the cost is not: `View()`.** It is flat at ~3.5 ms in arm_a and
~1.6 ms in arm_b at every size. All of arm_a's growth is in `Update` —
the linear reassembly, exactly where H6 put it. *A rewrite that
optimised rendering and left the refresh path alone would move none of
these numbers.*

---

## H4 — 20 fps spinner

> **Kill if** a spinner tick forces more than one item re-render, or if
> sustained 20 fps animation over a 400-turn corpus costs more than 5%
> CPU on this machine.

**Clause 1**, the one that matters architecturally:

| turns | 10 | 100 | 400 | 1000 |
|---|---|---|---|---|
| item renders/tick | 1 | 1 | 1 | 1 |

Exactly one, flat in N. Scrolled off-screen and paused: **zero**, and it
resumes correctly. Against a rebuild-per-tick strategy over the same
corpus, one second of animation costs 24 ms and 20 item renders versus
83 ms and 100 renders.

**Clause 2**, 400-turn corpus, three seconds, gate 5%:

| arm | CPU | per-frame render |
|---|---|---|
| animated 20 fps | **4.02%** | 1.378 ms |
| static 20 fps (control) | 4.07% | 1.331 ms |
| idle, no repaint | 0.34% | — |

Attributable to the animation: **−0.05%**. Within noise of zero, which
is the correct answer for a prerendered frame table indexed by a
counter.

**This clause failed at 8.89% earlier in the spike, and the reason it
now passes is the whole of F1 below.** The animation never cost
anything; the frame around it did. The static control is what made that
legible — measuring only the animated arm would have produced a red
gate with no way to tell what was red.

---

## H5 — typed-`Action` dialogs

> **Kill if** arm_b's modal and picker tests still require an app model
> to construct. **Partial failure** if the keystroke grace period
> touches more than one file in arm_b.

Passes on both clauses, and both are checked mechanically rather than
asserted.

- **The main clause is checked by the compiler.** `dialog_test.go` is an
  external test package whose only arm_b import is `dialog`. No app, no
  list, no frame, no model, no theme, no terminal. If the decoupling
  were cosmetic, the file would not compile.
- **The partial-failure clause is checked by a filesystem walk.** A test
  greps every non-test `.go` file under `arm_b` for the grace period and
  reports the count. It is 1.
- **`Action` is sealed, and a test parses `dialog.go` with `go/ast` to
  prove it.** This is a deliberate divergence from the prior art's bare
  `any`: a bare `any` defeats exhaustiveness at every call site, so a
  `switch` over it can silently stop covering a variant that was added
  later.

14 tests: decisions rather than effects (a dialog cannot double-write a
write-once channel because it never holds one), tolerance of every
message type, the grace period's quiet threshold *and* its cap *and* its
reopen window, the picker's materialised filter set, cursor clamping
when the set shrinks, filtered-to-source index mapping, the `(i+1)%0`
guard, and inner width derived from the style's own frame size rather
than a hardcoded pad.

The host side is fifteen lines in
[`cmd/spike-preview/armb.go`](https://github.com/go-steer/core-tui/blob/31c73ce1c9bedd9709c34e3141ac0a5676dfff7a/spike/cmd/spike-preview/armb.go): receive an
`Action`, decide what it means. That is the claim made runnable.

---

## Findings nobody hypothesised

These are the results the spike did not go looking for. Two of them are
worth more than some of the hypotheses.

### F1 — the per-frame floor, and how much of it is self-inflicted

A warm arm_b frame renders **zero** items and still cost 2.71 ms. That
is a surprising enough number that reporting it undecomposed would have
been reporting something nobody can act on. Decomposed at 100×40:

| stage | ns/op | B/op | allocs |
|---|---|---|---|
| compose + clip (5 ready-made strings) | 707 k | 52.1 KB | 231 |
| clip post-pass alone | 259 k | 14.7 KB | 202 |
| 40 rows via `lipgloss.Style.Render` | 435 k | 39.7 KB | 2080 |
| **40 rows via measure + truncate + pad** | **37 k** | **4.5 KB** | **40** |
| 40 × `ansi.StringWidth`, plain text | 93 k | 0 | 0 |
| 40 × `ansi.StringWidth`, styled text | 96 k | 0 | 0 |

The last pair is a small result of its own: SGR-laden text — which is
what a real frame contains — costs only ~4% more to measure than plain
text. Escape scanning is not where the floor is.

Two things follow.

**The floor is real and every arm pays it.** Each row of a frame gets
measured several times over — once by a style's wrap, once by a join's
pad, once by the clip pass — and the total is proportional to **screen
area, not session length**. Neither H1 nor H2 addresses it.

**But most of what looked like floor was avoidable.** `Style.Render` is
a *word-wrapper*. Asking it to square off a row you have already
measured buys a re-measure, a break-point search, allocations, and the
possibility of it returning **more rows than you gave it** — which
silently breaks whatever height budget the frame was assembled against.
Measure-truncate-pad does the same job **11.7× faster, with 8.9× fewer
bytes and 52× fewer allocations**.

Replacing that one idiom, plus fixing the oversized panel in F2, took
the warm frame from **2.71 ms to 1.13 ms** and moved H4 from 8.89% CPU
to 4.02%. Neither change touches the architecture; both apply to
core-tui as it stands today.

### F2 — a clip pass hides panel loss, and the tests cannot see it

arm_b's sidebar capped its loop at *h sessions* rather than *h lines*. A
session that wrapped to two lines made the panel taller than its own
rectangle, the horizontal join made the whole body that tall, and the
clip pass dropped the input row and the footer off the bottom of every
frame.

**Every frame-invariant test passed the entire time, and they had to.**
Clipping is exactly what makes an oversized frame legal. "No line too
wide, no row too many" says nothing whatsoever about whether a panel
that was supposed to be on screen still is.

The bug was found by running the preview binary and looking at it.

The fix is one assertion — `TestPanelsSurviveComposition`, which checks
that each region's content is actually present in the composed frame —
and the *class* of assertion is the finding. **core-tui already has a
clip pass and does not have this check.** A safety net that also hides
the fall should never ship without one.

There is a performance corollary: an oversized panel makes the clip pass
do real work on every row of every frame instead of passing through
byte-identically. The bug had a steady-state cost, not only a visual one.

### F3 — tabs make cell-width arithmetic wrong, silently

A tab is one byte, **one cell** to a cell-width measurer, and **four
columns** to lipgloss when the line is finally joined. A code-fence line
can therefore be measured, found to fit, clamped to nothing, and still
be four columns over budget by the time it reaches the compositor —
shifting everything downstream and getting the neighbouring panel eaten.

Any width arithmetic performed with a cell-width measurer over
un-expanded tabs is wrong. Expand at the boundary where content enters
the layout, at the tab width the renderer itself uses.

### F4 — the width contract belongs to the producer

Items overrun the width they are given, and not out of carelessness:
Glamour adds a margin its word-wrap width does not account for, and a
tool row prefixes its result lines. An oversized line is the caller's
problem the moment it leaves the package — and the caller *cannot* fix
it, because the only remedy available to them is a re-wrap, and a
re-wrap changes the row count the whole lazy walk budgets against.

Truncation is the only bound that leaves the line count alone. Enforce
it where the render happens, on the cache-miss path, so the steady frame
never pays for it.

---

## Measurement bugs found in this spike

Recorded because they all pointed the same way — **every one of them
flattered the baseline** — and because a results file that does not
describe how its numbers were wrong before they were right is asking to
be trusted rather than checked.

1. **The original settle benchmark was invalid.** Draining a command
   tree synchronously executes `tea.Tick`'s real sleep inline, so it
   reported the debounce delay (~340 ms, flat from 100 turns up) instead
   of any work. *A debounce is a statement about time and can only be
   measured in real time.* Replaced with the real-time drag probe.
2. **A live "stale frame" counter reported 55 consecutive stale frames.**
   It was describing itself: the probe races the program by
   construction, so no live counter can distinguish a frame that is
   legitimately mid-flight from one that converged wrong. Recording raw
   samples and analysing after the run gives overflow=0, underfill=0 —
   the opposite conclusion.
3. **Enqueue times were indexed by a count of size messages `Update` had
   seen, and were off by one**, because the program emits its own
   startup `WindowSizeMsg`. arm_b's entire histogram came back empty and
   would have been reported as a missing measurement. The obvious fix —
   stamp the latest event — *understates* the wait whenever `Update`
   coalesces, which is most of the time. The probe now queues enqueue
   times and charges each frame with the **oldest event not yet on
   screen**, which is what the person dragging the mouse is waiting for.
   This alone moved arm_a's 100-turn case from 3.8 ms and PASS to 33.6 ms
   and KILL: the backlog was always there and was simply not counted.
4. **A permanently-red suite.** `strjoin`'s invariant violations *are*
   the H2 finding, not a defect, so they now appear in a pass-rate table
   instead of failing the build. A suite that is always red is one in
   which a real regression can no longer be seen.

---

## Recommendation

**Do the rewrite, but not the one the hypotheses were arranged around,
and not in the order they were numbered.**

**Step 0 — hygiene, no architecture, do it now.** F1, F2 and F3 are
independent of every hypothesis and apply to the code as it stands:
stop using a word-wrapper to pad already-measured rows, expand tabs at
the layout boundary, and add the panel-survival assertion next to the
existing clip pass. Measured effect in arm_b was 2.4× on the steady
frame and 52× fewer allocations in the affected path, for no design
change and no risk. Nothing else here is that cheap.

**Step 1 — H5, typed-`Action` dialogs.** Independent of the rendering
work, mechanical, and it unblocks the picker backlog. Seal the
interface; a bare `any` costs exhaustiveness at every call site. Put the
grace period in one file and keep it there.

**Step 2 — H1 and H3 together, as one work item.** They are not
separable: H3's win *is* the width-keyed per-item cache, which is H1's
list. Sequencing them apart would build the drag mode on top of the
thing it depends on. Budget them as one.

The justification is H6's failure and H3's kill of the baseline, and the
target is the **refresh and resize paths, not `View()`**. `View()` is
already flat in both arms. A rewrite aimed at rendering would move none
of the numbers in this document.

**Step 3 — H4 falls out for free** once items are individually
invalidatable. It needs no separate work beyond a prerendered frame
table and pausing off-screen animation. The 3000 ms cadence can go.

**Do not adopt the compositor.** H2 is killed on its own terms: the
cheap control matches it on every correctness clause, beats it on live
content behind a modal, costs less on both axes, and does not drop
combining marks. Keep `strjoin+clip` and the ~50-line `Overlay`.

---

## What this spike did not measure

Stated so that nobody reads a verdict as broader than its evidence.

- **Mouse, selection, search and copy paths.** Not exercised at all. The
  lazy list changes what is in memory at a given moment, which is
  exactly the sort of thing a selection implementation cares about.
- **Real terminal throughput.** Every probe writes to `io.Discard`.
  Nothing here says what a real emulator does with the byte stream.
- **Long-session memory.** The corpus is built once and never grows
  during a run; cache eviction was never needed and so was never
  designed.
- **Windows, and anything other than this one Linux box.**
- **A suppression-only arm for H3** — see clause 4 above.
- **The environment sensitivity of arm_a.** `DetectCapabilities()` and
  the `TERM_PROGRAM` newline hint both reach the frame and neither is
  pinnable through `Options`, so arm_a's absolute numbers will drift
  between machines in ways arm_b's will not. The ratios are the
  trustworthy part.

---

## Provenance and disposal

**Licence.** The prior-art TUI this spike drew its mechanisms from is
FSL-1.1-MIT — source-available, not open source — and the MIT Future
License has not vested for any file under its `internal/ui/`. core-tui
is Apache-2.0. **No source from it was copied, and none was open while
any of this was written.** Mechanisms were
recorded as prose in [`NOTES.md`](https://github.com/go-steer/core-tui/blob/31c73ce1c9bedd9709c34e3141ac0a5676dfff7a/spike/NOTES.md) and implemented from those
notes plus the public MIT library APIs; every challenger file carries
the marker naming the note it came from. Algorithms and methods of
operation are not copyrightable (17 U.S.C. §102(b)); literal code,
structure, comments and identifier sets are.

**Isolation.** `spike/` is a nested Go module with a `replace` back to
the parent, verified — not assumed — to be invisible to `build`, `vet`,
`lint-go`, `test-unit` and `verify-mod-tidy`. The single leak is
`verify-go-format`, which walks the filesystem rather than the module
graph; the remedy was to keep spike sources formatted, which leaves
**zero edits outside `spike/`**.

**Disposal.** This branch is not for merging. Archive it as a tag and
delete the branch; the deliverable is this file. `rm -rf spike/` is a
complete kill with nothing to revert.
