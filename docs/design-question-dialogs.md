# Typed question dialogs for human-in-the-loop and elicitation

**Status:** design — not implemented. Proposal for
[#164](https://github.com/go-steer/core-tui/issues/164), the last
open item in the "Post-spike usability & rendering" milestone.

**Decision requested:** whether to adopt the sealed-answer contract
described in §5, and if so, in which of the two API postures set out
in §10. Everything else in this document follows from that one choice.

Line references are against `main` at the time of writing.

---

## Settled decisions (do not relitigate)

These are established elsewhere and this document takes them as given.

1. **The host owns the permission gate and the MCP client; the TUI
   owns the prompt.** `docs/decisions.md` D5 / D6. Nothing here moves
   policy into `package tui`.
2. **`package tui` stays flat.** `docs/design.md` §2.1 and
   `docs/api-surface.md` §4. [#115](https://github.com/go-steer/core-tui/issues/115)
   (decompose into `internal/` subpackages) is **deferred**, so no
   part of this plan may depend on it. §8 is written to that
   constraint.
3. **The exported surface is being narrowed, not grown.**
   `AGENTS.md`, "Status". `docs/api-surface.md` §3.2 recommends
   unexporting the entire dialog set — `Dialog`, `DialogAction`,
   `KeyMsgDialog`, `ScrollDialog`, `Overlay` and its eleven methods,
   `RenderContext`, `Scrollbar`, `NewTextInputDialog`,
   `TextInputConfig` — on the grounds that not one of them is usable
   from outside the package. This proposal has to be compatible with
   that recommendation or argue it down. It does the former (§10).
4. **huh v2 is the form primitive where a form is what is wanted.**
   `docs/decisions.md` D26. This document does not re-open
   hand-rolled-vs-huh; it explains where each belongs (§7.7).
5. **The grace window exists and its duration is 300 ms.**
   `requirements.md` R-PERM-8 / R-ELIC-4, `tui/update.go:1122-1134`.
   The question here is *where it lives*, not whether.

---

## 1. The problem, from the host's side

A host wires two blocking, typed, question-shaped calls:

```go
AskApproval(ctx context.Context, req PermissionRequest) (PermissionDecision, error)   // prompter.go:30
Elicit(ctx context.Context, s string, req ElicitRequest) (ElicitResult, error)        // elicitor.go:31
```

Both already carry a decision back across the module boundary. So the
first thing to say plainly is that **the host-facing contract is not
where the problem is.** The problem is one layer down, and it leaks
outward in three ways the host does feel.

### 1.1 `DialogAction` expresses routing, not decision

```go
// tui/dialog.go:80
type DialogAction struct {
	Consumed bool
	Close    bool
	Cmd      tea.Cmd
}
```

A dialog that has an answer has nowhere to put it, so it puts the
*consequence* of the answer in `Cmd` and hopes the consequence was
computed correctly at the widget. The theme picker is the clearest
specimen — `tui/dialog_themepicker.go:140-157`:

```go
case "enter":
	pick := themes[d.idx]
	m.applyNamedTheme(pick.Name)
	m.history.Append(Message{Role: RoleSystem, Text: "/theme: switched to " + pick.Name})
	m.refreshViewport()
	name := pick.Name
	cmd := tea.Batch(
		func() tea.Msg { return ThemeChangedMsg{Name: name} },
		persistChoiceCmd(m.sessionGen, "/theme", m.opts.PersistThemeChoice, name),
	)
	return DialogAction{Consumed: true, Close: true, Cmd: cmd}
```

Six things happen inside a list widget's key handler: a global theme
mutation, a transcript append, a viewport repaint, an outbound host
message, a host persistence callback, and a stack pop. The *answer* —
"the operator picked the theme named X" — is never named. It exists
only as a local variable that five effects were derived from. There is
no value a test can assert on and no value a caller can switch over.

The same shape recurs at `tui/dialog_modelpicker.go:243`
(`Cmd: switchModelCmd(...)`), `tui/dialog_sessionpicker.go:213`
(`Cmd: switchToSessionCmd(...)`) and `tui/dialog_sessionpicker.go:490`
(`Cmd: m.applySwitchTarget(&tgt)`).

### 1.2 The two load-bearing questions are not dialogs at all

The permission prompt and the elicitation form — the two surfaces this
milestone actually cares about — are **not** on the overlay stack.
`tui/dialog.go:21-25` says why, in as many words:

> Permission and Elicit modals still use their inline state in Model
> (pendingPermission / pendingElicit) because they're tied to the
> channel-based Prompter / Elicitor lifecycle that needs special
> dispatch semantics.

"Needs special dispatch semantics" means "has an answer, and
`DialogAction` cannot carry one". The cost of that exclusion is the
whole of §7.7: they get no shared chrome, no shared scroll, no shared
cursor extension, no shared grace, and their key handling is 90 lines
of `switch` inline in `Model.handleKey` (`tui/update.go:1273-1325`)
plus a 130-line `handleElicitKey` (`tui/update.go:2821-2939`).

### 1.3 The third answer channel: downcast-by-ID

There is a third way an answer moves, used for asynchronous replies:
`Overlay.Get(id)` followed by a type assertion to the dialog's
unexported concrete type. Five sites in `Update`:

```go
tui/update.go:521   m.overlayStack.Get(subagentDialogID).(*subagentDialog)
tui/update.go:575   m.overlayStack.Get(modelPickerDialogID).(*modelPickerDialog)
tui/update.go:593   m.overlayStack.Get(modelPickerDialogID).(*modelPickerDialog)
tui/update.go:602   m.overlayStack.Get(sessionPickerDialogID).(*sessionPickerDialog)
tui/update.go:612   m.overlayStack.Get(sessionPickerDialogID).(*sessionPickerDialog)
```

`Overlay` is nominally an abstraction over `Dialog`, but `Update`
knows every concrete dialog type in the program. Adding a question
dialog today means adding a case to that set.

So the honest statement of the problem is: **there are three
uncoordinated mechanisms for getting an answer out of a modal —
`Cmd`, a host closure, and a downcast — and the two most important
modals use none of them because they predate all three.**

### 1.4 What the host actually notices

Three observable consequences, each verified against
`go-steer/core-agent` (the reference host, pinned at
`core-tui v0.18.0`, no `replace` directive):

- **A stranded blocked call.** `applySwitchTarget`
  (`tui/update.go:2507`) clears `m.pendingPermission` and
  `m.pendingElicit` at `tui/update.go:2550-2551` **without
  dispatching**. The goroutine blocked in `AskApproval` on the
  outgoing agent is not answered; it hangs until its own `ctx`
  cancels, and `Prompter.pending` still points at its response
  channel until the next `nextRequest` overwrites it. There is no
  mechanism today that guarantees a question is answered exactly
  once, because there is no *thing* that represents "the answer".
- **A documented decline path that cannot be reached.**
  `tui/elicitor.go:106-108` documents `ElicitActionDecline` as
  "form: n". Form mode has no `n` case: `handleElicitKey`'s form
  branch handles `enter`/`tab`/`shift+tab`/`space`/`left`/`right`/
  `backspace` and then falls into the printable-rune arm at
  `tui/update.go:2929`, so `n` is typed into the focused string
  field. `isElicitCommitKey` (`tui/update.go:2945`) agrees: form mode
  commits on Enter only. The only producer of `ElicitActionDecline`
  in form mode is `supportedElicit` returning false at
  `tui/elicitor.go:150` — i.e. a schema the TUI cannot render, not a
  human saying no. core-agent maps `ElicitActionDecline → "accept"`'s
  sibling `"decline"` and returns it to the MCP server, so the server
  is being told "the user declined" for a case where no user was ever
  asked.
- **`ThemeChangedMsg` has no consumer.** `grep -rn ThemeChangedMsg`
  across core-agent returns zero hits; the host learns about a theme
  pick only through the `PersistThemeChoice` callback. The
  msg-through-`Cmd` channel that `DialogAction.Cmd` exists to serve
  is, for the one gating host, dead weight. That is not an argument
  to delete it — it is an argument that **changing it is cheap**,
  which matters for §10.

---

## 2. Inventory: every question the TUI asks today

Twelve surfaces. "Answer channel" is how the operator's decision
reaches the code that acts on it.

| # | Surface | Declared | Asks | Answer today | Channel |
|---|---|---|---|---|---|
| 1 | Permission prompt | `update.go:1273`, `view.go:1315` | approve this tool call? | `PermissionDecision` (6-valued) | `Model` field → `dispatchPermission` (`update.go:2762`) → `Prompter.dispatchDecision` (`prompter.go:204`) → channel |
| 2 | Elicit form | `update.go:2821`, `view.go:1439` | fill these fields | `ElicitResult{Submit, Values}` | `Model` fields → `dispatchElicit` (`update.go:2806`) → channel |
| 3 | Elicit URL | same | accept / decline this URL | `ElicitResult{Submit\|Decline}` | same |
| 4 | Model picker | `dialog_modelpicker.go:49` | which model? | *none* — fires `switchModelCmd` | `DialogAction.Cmd` (`:243`), reply via `Overlay.Get` downcast (`update.go:593`) |
| 5 | Session picker | `dialog_sessionpicker.go:59` | which session? | *none* — fires `switchToSessionCmd` | `DialogAction.Cmd` (`:213`) + downcast (`update.go:612`) |
| 6 | Session input | `dialog_sessionpicker.go:452` | a value for this row | `string` → host's `SessionInput.Submit` returns `SwitchTarget` | host closure returning a `DialogAction` (`:490`) |
| 7 | Text input | `dialog_textinput.go:105` | free text | `string` → `TextInputConfig.Submit(value, m) DialogAction` | caller closure (`:209`) |
| 8 | Theme picker | `dialog_themepicker.go:34` | which theme? | *none* — mutates `m`, emits `ThemeChangedMsg` | `DialogAction.Cmd` (`:157`) |
| 9 | Pricing form (huh) | `pricing_form.go:110` | three named values | `form.GetString(...)` ×3 | direct dispatch on `huh.StateCompleted` (`:132`) |
| 10 | Tool-call detail | `dialog_toolcall.go:63` | *nothing* — viewer | — | — |
| 11 | Subagent detail | `dialog_subagent.go:59` | *nothing* — viewer | — | — |
| 12 | `/btw` side answer | `model.go:209`, `view.go` | *nothing* — viewer | — | — |

### 2.1 What the inventory says

**Three of the twelve are viewers.** #10-#12 have no answer and never
will. They are `Dialog`s and should stay `Dialog`s. A "question"
abstraction that also has to cover a read-only scroll pane is not an
abstraction, it is a union. **The first split this design makes is
question vs. viewer**, and it is the split that keeps the shared base
honest (§7).

**Four of the nine questions do not name their answer at all.** #4,
#5, #8 and #9 compute an effect and discard the decision. Under a
typed result they gain a value; nothing else about them changes.

**Two of them route a host closure through the widget.** #6 and #7
put `func(value string, m *Model) DialogAction` in the *config* of the
widget. That is a callback-shaped answer: it works, it is the closest
thing in the codebase to what #164 asks for, and it has the two
defects a typed result fixes — the closure must construct a
`DialogAction` (so the widget's routing protocol leaks into host
code), and it receives `*Model` (so the closure is untestable outside
a live app).

**The answer *shapes* actually present are six, not five, and they are
not the prototype's five.** Reading the table by shape rather than by
surface:

| Shape | Surfaces |
|---|---|
| no answer / dismissed | all nine, via `esc` |
| an explicit refusal that is not a dismissal | #3 (`n` = decline, distinct from `esc` = cancel) |
| exactly one option from a list | #4, #5, #8, and the enum fields inside #2 |
| zero or more options from a list | *none shipped* — R-PERM-4's `/permissions` review picker is specified and unbuilt |
| one free-text value | #6, #7 |
| a named value per field | #2, #9 |
| a permission decision | #1 |

The prototype's set — chosen / allow-once / allow-session / deny /
dismissed — mixes two axes. `chosen` and `dismissed` are shapes;
`allow-once`, `allow-session` and `deny` are three of the six members
of one question's answer *domain*. Adopting it means every switch over
a theme-picker answer must mention `allow-session`, and it means the
sealed set grows by roughly one variant per new question type — which
is precisely the growth that sealing is supposed to make expensive.
§5.2 takes the alternative.

---

## 3. What this design is trying to buy

In priority order, because they are not all worth the same.

1. **Bring #1 and #2/#3 onto the overlay stack.** This is the prize.
   It deletes the parallel modal machinery in `Model` (five fields,
   two render paths, two cursor arms, two wheel arms, two esc arms,
   two grace call sites) and gives the two most safety-relevant
   modals the chrome, scroll, fit-to-terminal and cursor handling that
   every other dialog already gets for free.
2. **Make an answer a value.** So it can be asserted on in a test,
   logged, replayed, and switched over in exactly one place per
   question rather than derived inline at the keystroke.
3. **One grace window, one implementation.** §9.
4. **A family for the HITL work.** yes/no, single-select,
   multi-select, free text, multi-field form, editor-backed long form.
   Listed last deliberately: the widgets are the easy part, and three
   of the six already exist in some form.

Explicitly *not* a goal: making dialogs implementable by hosts. That
is the extension-seam question `docs/api-surface.md` §3.2 defers, and
it is separable (§10).

---

## 4. The proposal in one paragraph

A question dialog is a value that consumes keystrokes and eventually
produces exactly one `answer`, where `answer` is a sealed interface
whose variants are *shapes* of answer, not domains. It performs no
effects: it does not touch `Model`, does not append to history, does
not emit host messages. The code that *opened* the question registers
a **resolver** — `func(answer, *Model) tea.Cmd` — which is the single
place that turns "the operator picked X" into effects. The overlay
stack guarantees the resolver runs exactly once, with
`dismissed{...}` if the question is torn down unanswered. `*Model`
therefore lives entirely on the resolver side of the seam, and the
widget side becomes constructible and drivable with no app model.

---

## 5. The sealed answer

### 5.1 Declarations

```go
// answer is what a question dialog produces. It is a sealed
// interface: the marker method is unexported, so the complete set of
// variants is the set declared in this file and the compiler can be
// asked (via gochecksumtype) to prove a switch covers it.
//
// Variants are shapes, not domains. "The operator picked the third
// row" is a shape; "the operator granted this tool for the session"
// is a domain, and domains ride inside a shape (decision) or as the
// resolver's interpretation of an option ID. Adding a variant here
// is a real cost — every switch in the package has to grow an arm —
// and that cost is the point.
type answer interface{ isAnswer() }

// dismissed is "no answer". Every question can produce it and every
// resolver must handle it.
//
// Reason exists because the three ways a question dies are not
// interchangeable to a host: an operator pressing esc on a permission
// prompt is a deny, a session switch tearing the prompt down is a
// request the host should re-ask on the new agent, and a schema the
// TUI cannot render is an MCP "decline" the server must be told about
// (elicitor.go:149). Today those three are conflated or, in the
// second case, simply dropped (update.go:2550).
type dismissed struct{ Reason dismissReason }

type dismissReason uint8

const (
	dismissEscape      dismissReason = iota // operator pressed esc
	dismissSuperseded                       // torn down by a session switch / reload
	dismissShutdown                         // program is quitting
	dismissUnrenderable                     // the request could not be shown at all
)

// declined is an explicit "no" that is not a dismissal. MCP
// distinguishes decline ("the user said no") from cancel ("the user
// dismissed it"), and core-agent forwards both distinctly
// (coretui_enabled.go's translateElicitAction). Folding this into
// dismissed{Reason: ...} would encode an answer as a reason for not
// answering.
type declined struct{}

// chosen is exactly one option from an ordered list.
//
// Callers: the model picker, the session picker, the theme picker,
// enum fields inside an elicit form, URL-mode accept, and R-PROMPT-1's
// unbuilt AskUser. ID is the stable option identity supplied by
// whoever built the option list; Index is its position in the list
// AS FILTERED, and is provided because three of those callers already
// track an index and one (the theme picker's live preview) is
// index-driven. Resolvers should prefer ID.
type chosen struct {
	ID    string
	Index int
}

// selected is zero or more options. Distinct from chosen rather than
// chosen-with-a-slice: a single-select resolver that has to handle
// len(IDs) == 0 and len(IDs) == 2 is a single-select resolver with
// two unreachable branches, and the permission prompt is not a place
// to have unreachable branches.
type selected struct {
	IDs     []string
	Indexes []int
}

// text is one free-text value, already trimmed. Callers: the text
// input dialog, the session-input dialog, and the editor-backed
// long-form question (where Value is the buffer the external editor
// wrote).
type text struct{ Value string }

// fields is a named value per field, the shape an elicit form and the
// /pricing form both produce. Values holds Go primitives — string,
// float64, int64, bool — matching what ElicitResult.Values already
// carries to the host verbatim.
type fields struct{ Values map[string]any }

// decision is a permission decision. It is the one domain-typed
// variant, and §5.2 argues for it against the alternative of
// chosen{ID: "allow-session"}.
type decision struct{ Value PermissionDecision }

func (dismissed) isAnswer() {}
func (declined) isAnswer()  {}
func (chosen) isAnswer()    {}
func (selected) isAnswer()  {}
func (text) isAnswer()      {}
func (fields) isAnswer()    {}
func (decision) isAnswer()  {}
```

Seven variants. Each is justified by at least one shipped caller
except `selected`, which is justified by one *specified and unbuilt*
caller (R-PERM-4). §14 Q3 asks whether to include it on that basis.

### 5.2 Why `decision` and not `chosen{ID: "allow-session"}`

The elegant move is to note that a permission prompt is a
single-select over six labelled options and let `chosen` cover it.
`permissionDecisionLabel` (`tui/update.go:2787`) already produces
exactly the six strings that would be the option IDs.

Rejected, for one reason: the mapping back would need a fallback, and
the only safe fallback for an unrecognised permission option is
`DecisionDeny`. That would install, inside the library, in the
security-critical path, the same silent-downgrade the reference host
already has — core-agent's `translateDecision`
(`cmd/core-agent/coretui_enabled.go:1374-1389`) ends in
`default: return permissions.DecisionDeny`, so a decision constant the
host does not know about becomes a deny with no diagnostic. One
instance of that is a known cost. Two, with the second one *inside*
the component whose job is to be correct about permissions, is not.

`decision{Value PermissionDecision}` keeps the permission domain in
`prompter.go` where D5 put it, adds no exported enum, and makes the
widget→gate path checkable end to end by the compiler.

A cuter variant — give the existing `PermissionDecision` an
`isAnswer()` method so the enum *is* a variant — was considered and
rejected on two counts: it adds a method to an exported type for a
purely internal purpose, and it makes the zero `answer`-typed
`PermissionDecision` silently equal to `DecisionDeny`, which is a
fail-open-shaped hazard dressed as fail-closed.

### 5.3 Why a sealed interface — and what sealing does *not* buy

The alternatives, in the order they should be dismissed.

**`any`.** Rejected without much argument. It is what
`ElicitResult.Values` uses for field values, where the values really
are arbitrary JSON scalars; it is wrong for a fixed set of seven
shapes, because every resolver becomes a type switch with an
unbounded default and no tool can tell a missing case from a
deliberate one.

**Enum plus payload struct** — `struct{ Kind answerKind; ID string;
Values map[string]any; ... }`. This is what `ElicitResult` already is
(`elicitor.go:97`) and what `PermissionRequest` already is, so it has
the strongest claim on precedent. Rejected *inside the package*
because it makes every field optional-by-convention: nothing stops a
`Kind: answerChosen` value carrying a populated `Values` map, and
nothing forces a `Kind: answerText` value to populate `Value`. With
seven shapes and nine question types that is nine opportunities to
read a field that was never set. A sealed interface makes the invalid
states unconstructible instead of merely undocumented. Note this
argument is scoped to *inside the package* — §10.3 keeps enum+payload
at the host boundary, on purpose, and explains why that is not
inconsistent.

**A distinct message type per dialog** — `ModelPickedMsg`,
`ThemePickedMsg`, … routed through `tea.Cmd`. Rejected because it is
what the code does today (§1.1) and it is the thing being replaced.
It also scales badly in the specific way #164 predicts: the routing
lives in `Update`'s top-level `switch`, so every new question adds a
case there, and `Update` is already 3,173 lines.

**What sealing costs.** A host cannot add a variant. That is correct
here and would be correct even if the type were exported: the set of
answer *shapes* a terminal modal can produce is a property of the
widget library, not of the host, and a host that needs a shape we do
not have needs a widget we do not have. There is no partial-sealing
compromise worth taking (an exported marker method would let hosts
add variants and immediately break every switch in the package).

**What sealing does not buy — and this contradicts the issue's
framing.** #164 says a bare `any` "defeats exhaustiveness at every
call site, so a `switch` silently stops covering a variant added
later." A sealed interface does not fix that on its own. **Go type
switches are never exhaustiveness-checked by the compiler**, sealed or
not; adding an eighth variant compiles fine against every existing
seven-arm switch. Sealing buys two things: (a) the variant set is
closed, so *some* tool can enumerate it, and (b) that tool exists —
`gochecksumtype`, available in golangci-lint v2, fails a type switch
over a sealed interface that misses a variant or omits a default.
`dev/tools/.golangci.yml` does not enable it today.

**Therefore the sealing is only real if the lint lands with it.**
Enabling `gochecksumtype` (and `exhaustive`, for the existing
`PermissionDecision` / `ElicitAction` / `PermissionKind` switches) is
a config change with no API impact and should be **stage 0** of the
plan in §12. If the owner declines the linter, the sealed interface
should be declined too and the enum-plus-payload struct taken instead,
because an unchecked sealed interface is a more expensive way to get
the same guarantee that a struct gets from being a struct.

---

## 6. The question contract

### 6.1 The seam

```go
// question is a modal that asks exactly one thing and produces
// exactly one answer. Note what is absent: no *Model, no tea.Cmd
// dispatch of effects, no history, no overlay manipulation. A
// question is a pure state machine over keystrokes plus a renderer.
type question interface {
	// ID is the overlay identity, as Dialog.ID today.
	ID() string

	// Key advances the question by one keystroke.
	//
	// A non-nil answer means the question is finished: the overlay
	// pops it and hands the answer to the resolver registered at Ask
	// time. A nil answer means "still asking" — the keystroke was
	// consumed either way, because an open modal is exclusive
	// (see 6.4).
	//
	// The returned Cmd is for the question's OWN widgets — a bubbles
	// textinput's cursor-blink teardown, huh's tick. It is not an
	// answer channel and must not carry host-visible messages.
	Key(msg tea.KeyPressMsg) (answer, tea.Cmd)

	// Title and Footer are the chrome strings. The overlay owns the
	// frame; the question owns the words in it.
	Title() string
	Footer() string

	// Body renders the question's content to exactly width columns.
	// Styles is passed by value: a question never reaches back for
	// the app's theme, and the zero Styles renders unstyled, which is
	// what makes an external test possible (§8).
	Body(width int, st Styles) string
}
```

**As shipped in stage 1 the seam has two more numbers on it**, and
both are corrections to the sketch above rather than additions to it.

`Body(width, termHeight int, st Styles)`. A question whose body is
longer than the terminal has to window it, and the row budget depends
on how many chrome rows the question itself spends inside `Body` — the
pickers pay `modalChromeRows+1` for their filter row. Leaving the
height overlay-owned would mean the overlay computing a budget it
cannot know the inputs to. `termHeight` carries `RenderContext.Height`'s
contract exactly, zero included: unknown geometry, no windowing.
`scrollQuestion.Measure` already presupposed the question knew this
number; this only says where it comes from.

`Width(avail int) int`. How wide a modal should be is a property of
its content — 72 columns for a theme list, 96 for a tool-call body —
and every dialog already carried its own preferred width and floor.
The overlay owns the chrome inside that width; it has no basis for
choosing it.

Optional extensions, unchanged in spirit from today's:

```go
// scrollQuestion is a question whose body scrolls by rows (the
// permission prompt with a 200-line diff; the long-form editor
// preview). Mirrors today's ScrollDialog (dialog_scroll.go:453).
type scrollQuestion interface {
	question
	ScrollBy(delta int)
	// Measure reports (contentRows, viewportRows, offset) so the
	// overlay can draw the shared scrollbar without the question
	// calling Scrollbar itself.
	Measure() (content, viewport, offset int)
}

// cursorQuestion is a question with a text caret (anything with a
// filter row or an input box). Mirrors today's cursorDialog
// (cursor.go:67); note DialogCursor's *Model argument is gone, and
// with it the only reason that interface could not move.
type cursorQuestion interface {
	question
	Cursor(width int) *tea.Cursor
}
```

### 6.2 The resolver — where `*Model` goes

```go
// resolver turns an answer into effects. It runs on the Update
// goroutine, exactly once per question, and it is the ONLY place a
// question's answer meets the app model.
//
// It is registered by whoever opened the question, which is where the
// knowledge of what the answer means already lives: openModelPicker
// knows a pick means SwitchModel; the permission listener knows a
// decision means dispatchDecision. Today that knowledge is inside the
// widget (dialog_themepicker.go:140) and the widget is untestable
// because of it.
type resolver func(a answer, m *Model) tea.Cmd

// Ask pushes a question onto the overlay stack together with the
// resolver that will receive its answer. Ask replaces Open for
// questions; Open remains for viewers.
func (o *Overlay) Ask(q question, r resolver)
```

Shipped as `ask`, lower-case. Its parameters are unexported, so an
exported `Ask` would be a method a host can see and can never call —
API surface with no caller, and posture A's whole claim is that stage
1 adds none. Exporting the family is posture B (§10.2) and is a
separate decision.

The `resolver` signature is also load-bearing in a way worth spelling
out: `m` is a **parameter**, not something the resolver closes over.
`Model.Update` has a value receiver, so a `*Model` captured when the
question was asked points at the per-`Update` copy that opened it and
is dead by the time the answer arrives. Every effect a widget wants to
cause has to route through something the Update loop hands it.

Exactly-once is the overlay's job, and it is what closes the
`applySwitchTarget` hole from §1.4:

```go
// resolveAll answers every outstanding question with reason and
// clears the stack. Called from applySwitchTarget (superseded) and
// from the quit path (shutdown). Viewers are simply dropped.
func (o *Overlay) resolveAll(reason dismissReason, m *Model) tea.Cmd
```

Nothing else changes about the stack: IDs, z-order, `HasID`
singleton checks, `Close(id)`, `Front` all stay. `Get(id)` stays too
— the asynchronous-reply route in §1.3 is a *seeding* channel
(`applyModels`, `applySessions`, `subagentDialog.apply`), not an
answer channel, and it is orthogonal to this proposal.

### 6.3 Worked example: the theme picker

Before (`tui/dialog_themepicker.go:140-157`, quoted in full in §1.1):
six effects inside a list widget.

After — widget:

```go
case "enter":
	return chosen{ID: themes[d.idx].Name, Index: d.idx}, nil
```

After — opener, at the one place `/theme` opens the picker
(`tui/slash_builtin.go:255`):

```go
prev := m.themeName
m.overlayStack.Ask(newThemePicker(m.themeName), askOperator, func(a answer, m *Model) tea.Cmd {
	switch a := a.(type) {
	case chosen:
		m.applyNamedTheme(a.ID)
		m.history.Append(Message{Role: RoleSystem, Text: "/theme: switched to " + a.ID})
		m.refreshViewport()
		return tea.Batch(
			func() tea.Msg { return ThemeChangedMsg{Name: a.ID} },
			persistChoiceCmd(m.sessionGen, "/theme", m.opts.PersistThemeChoice, a.ID),
		)
	case dismissed:
		m.applyNamedTheme(prev) // restore the previewed-away theme
		m.refreshViewport()
		return nil
	}
	return nil
})
```

**What this example leaves out is the live preview**, and it is the
one effect that could not simply move. Cursor movement has to apply a
theme *before* there is an answer, and by the paragraph above the
widget has no `*Model` to apply it with. So it returns a
`themePreviewMsg` on the `Cmd` that `Key` already provides for a
question's own machinery, and the Update loop applies it — guarded on
the picker still being open, because the message is asynchronous and
an operator who arrows and immediately escapes would otherwise have
the stale preview overwrite the restore. That guard is a bug that did
not exist before the change and now cannot happen; the point of
recording it here is that it is the shape of every "the widget needs
to touch the model mid-question" case that follows.

The effects did not get smaller — they moved. That is the whole
change, and it is worth being blunt that the line count is roughly
flat. What changed is that `newThemePicker` now takes its option list
as an argument, returns a value, and can be driven to completion in a
test with no `Model`; and that the esc-restores-the-preview rule is
stated once next to the enter-commits rule instead of 60 lines apart.

### 6.4 What happened to `Consumed`

`DialogAction.Consumed` (`tui/dialog.go:84`) documents a fall-through
path for keys the dialog declines. **No dialog in the package ever
returns `Consumed: false`** — `grep -n "Consumed: false"` over
`tui/*.go` matches nothing, and every `DialogAction` literal in the
seven dialog files sets `Consumed: true`. The one caller that relies
on the false case is the esc cascade's fallback at
`tui/update.go:1190-1197`, which is dead in practice because every
dialog handles esc.

`question.Key` therefore does not return `Consumed`; an open question
consumes every keystroke, which is the rule the code already follows.
Ctrl+C is handled above the overlay in `handleKey` and is unaffected.
If a future question genuinely wants fall-through, that is a new
optional extension interface, not a field every question must set to
the same value.

### 6.5 The answer that does not come from a keystroke

*Added in stage 3.* §6.2 describes one way an answer reaches its
resolver: `Key` returns it, and `askedQuestion.HandleKeyMsg` runs the
resolver and pops. That covers every synchronous question — the theme
picker's Enter is both the pick and the moment the pick exists.

It does not cover the shape the model picker, the session picker, the
permission prompt and the elicit form all share. There, Enter starts a
**host call** and the picker stays on screen showing progress, because
a list that freezes for as long as the host takes reads as a hang. The
answer arrives from `Update`, several hundred milliseconds later, when
the reply lands:

```go
// resolve answers the question under id, pops it, and returns
// whatever its resolver scheduled. A no-op when nothing matches or
// when the question has already been answered.
func (o *Overlay) resolve(id string, ans answer, m *Model) tea.Cmd
```

Without it the Update-side handler would have to `Overlay.Close` the
question, which pops it with **its resolver never run** — the exact
"torn down and nobody was told" shape §1.4 is about, reintroduced by
the code meant to remove it. `resolve` goes through the same
exactly-once latch as `resolveAll`, so a question answered by a
keystroke and a reply in the same frame resolves once.

Two rules fall out of it, both recorded on the method:

- **A resolver must not `Open` a dialog.** `resolve` pops *after* the
  resolver returns, and `Overlay.HandleKeyMsg` pops after it too, so
  anything a resolver pushed would be what got popped. A resolver that
  needs another modal returns a `tea.Cmd` and lets `Update` open it —
  the same route the theme picker's live preview takes (§6.3), and for
  the same underlying reason (§6.2: no `*Model` survives an `Update`).
- **A committed question is not an answered one.** The model picker
  carries a `switching` field for the window between the two, during
  which every keystroke but esc is swallowed so a second switch cannot
  be queued against a list that is about to be replaced. Esc in that
  window is `dismissed{dismissEscape}` and the in-flight call still
  applies — the host call was committed the moment it left, and the
  operator is declining to watch it, not cancelling it.

Reading a question without ending it goes through `Overlay.asked`,
which returns the wrapper rather than the widget: the wrapper is what
knows whether it has already been answered, and a caller holding only
the widget could not.

---

## 7. The dialog family, and what the base really shares

Six members, per #164. For each: whether it exists, and what it is.

### 7.1 Yes/no — `confirmQuestion`

```go
func newConfirm(prompt, yesLabel, noLabel string) *confirmQuestion
// Answers: chosen{ID: "yes"|"no"}, or dismissed{dismissEscape} on esc.
```

Does not exist today. Nearest thing is the `/clear` confirmation
(`m.confirmingClear`, another inline `Model` field). Trivial once the
base exists; it is a `selectQuestion` with two options and a
horizontal layout.

### 7.2 Single-select — `selectQuestion`

```go
type option struct {
	ID          string
	Label       string
	Description string // dim second line; empty for one-line rows
	Key         string // optional accelerator ("y", "s", "t", …)
	Disabled    bool   // rendered dim, not selectable
}

func newSelect(opts []option) *selectQuestion
// Answers: chosen, or dismissed.
```

Three shipped widgets collapse into this: the model picker, the
session picker (two-line cells → `Description`), the theme picker.
**And the permission prompt**, which is a six-option select whose
options carry accelerator keys, whose `Verb` option is `Disabled` when
`PermissionRequest.Verb == ""` (`tui/update.go:1294`), and whose
resolver is `dispatchPermission`. That is the single most valuable
consequence of this design: R-PERM-2's six keys stop being a `switch`
in `handleKey` and become data.

Two notes on that.

- **Accelerators are data, so the "always" option's presence is
  data.** core-agent wires `AlwaysAllow: func(coretui.PermissionRequest) error { return nil }`
  — a deliberate no-op — solely because "a nil callback makes core-tui
  downgrade to allow-session"
  (`cmd/core-agent/coretui_enabled.go:219-229`). A host is using the
  nil-ness of a persistence callback as a feature flag for whether a
  key appears in a modal. Under `[]option` the prompt's option list is
  built once, in one function, from `PermissionRequest` + `Options`,
  and that is where "does this host offer allow-always" belongs.
- **`permissions.StdinPrompter` in core-agent
  (`pkg/permissions/stdin.go:41-83`) offers only five of the six** —
  it cannot express `DecisionAllowSessionVerb`. Not our bug, but it
  confirms that "which options exist" is per-front-end and should not
  be hard-coded in a key switch.

### 7.3 Multi-select — `multiSelectQuestion`

```go
func newMultiSelect(opts []option, preselected []string) *multiSelectQuestion
// Answers: selected, or dismissed.
```

No shipped caller. R-PERM-4's `/permissions` review picker is the
specified one; D26 already names `huh.NewMultiSelect` for it. See §12
Q3.

### 7.4 Free text — `textQuestion`

Exists, as `textInputDialog` (`tui/dialog_textinput.go:105`). The
migration is: `TextInputConfig.Submit func(value string, m *Model) DialogAction`
→ a resolver at the open site; `Validate func(string) string` stays on
the config, because validation is a property of the question (it keeps
the dialog open, which is a widget behaviour, not an effect).

### 7.5 Multi-field form — `formQuestion`

```go
func newForm(fields []formField) *formQuestion
// Answers: fields, declined, or dismissed.
```

Two shipped callers with two different implementations: the elicit
form (hand-rolled, `tui/update.go:2821` + `tui/view.go:1439`) and the
pricing form (huh, `tui/pricing_form.go`). §7.7.

### 7.6 Editor-backed long form — `editorQuestion`

Does not exist. A question whose body is a preview and whose commit
key shells out to `$EDITOR`, returning `text{Value: buffer}`. It is
the only family member that needs to suspend the program
(`tea.ExecProcess`), which is why it is last in every staging plan
below: it introduces a process-lifecycle concern that none of the
others have, and it is the one member with no requirement behind it
yet.

### 7.7 What the base actually provides

A "shared base" that every widget overrides is not shared. So,
concretely:

**Genuinely shared, and already is:**

- Chrome — `RenderContext` (`tui/dialog.go:220`) composes title rule /
  blank / body / blank / footer rule / footer, and `fitModalContent`
  (`tui/dialog.go:297`) sheds rows in a fixed order so the footer key
  hint survives a short terminal. Under this design `Title()`,
  `Footer()` and `Body()` feed it directly instead of each dialog
  calling it.
- Scroll geometry — `scrollState` (`tui/dialog_scroll.go:179`),
  `modalBodyHeight`, `scrollView`, `Scrollbar`.
- The filter row — `pickerFilter` (`tui/dialog_filter.go:66`), already
  shared by all three pickers, including its cursor
  (`filterRowCursor`) and its highlight span.

**Newly shared, and this is where the win is:**

- The answer/close protocol: one `Key` signature, one exactly-once
  resolver, one `dismissed` on teardown.
- The grace gate (§9) — currently implemented twice and absent from
  every overlay dialog.
- `esc` → `dismissed{dismissEscape}`, implemented once in `Overlay`
  instead of seven times in seven `HandleKeyMsg` methods.
- Exclusivity: consume everything not otherwise handled. Seven
  duplicate `return DialogAction{Consumed: true}` tails.

**Not shared, and the design should stop pretending otherwise:**

- **Body layout.** A one-line model row, a two-line session cell, a
  theme swatch, an input box with an inline error, a scrolling diff
  and a field list have nothing in common below `Body(width, st) string`.
  There is no useful base *renderer*, only a base *frame*.
- **Key semantics.** `enter` commits in a select, inserts a newline in
  long-form, and advances a field in a form. A base cannot own
  `enter`.
- **Validation.** Text-only.

So the base is a small embedded struct providing ID, title, footer and
the grace stamp, plus the `Overlay` behaviours above — not an abstract
widget.

**And on huh (D26):** `formQuestion` should wrap `huh.Form`, and
`selectQuestion` should not wrap `huh.Select`. The forms need field
focus, per-field validation and Tab semantics, which is exactly what
huh is for and exactly what `handleElicitKey`'s 130 lines
hand-rolled badly (see the UTF-8 backspace comment at
`tui/update.go:2911` and the missing decline path in §1.4). The
selects need a filter row, a two-line cell, a live preview on cursor
movement, and per-row accelerator keys — four things the three
existing pickers already do and huh does not. D26's table maps the
permission modal to `huh.NewSelect`; that mapping should be revised,
and this document is the place to record it (§14 Q5).

The obstacle huh presents is real and already documented at
`tui/pricing_form.go:21-26`: `huh.Form` needs every `tea.Msg`, not
just key presses, which is why the pricing form sits on
`Model.pendingForm` outside the overlay stack. That is a fourth
optional extension:

```go
// msgQuestion is a question whose body owns a widget that needs the
// full tea.Msg stream (huh.Form: WindowSizeMsg, ticks, focus msgs).
// The overlay forwards everything to it. Retires the "future PR will
// extend Dialog with a tea.Msg variant" note at pricing_form.go:24.
type msgQuestion interface {
	question
	Msg(msg tea.Msg) (answer, tea.Cmd)
}
```

---

## 8. The `*Model` coupling and the external-test criterion

#164 proposes as an acceptance criterion that the dialog tests compile
"in an external test package importing only the dialog package, with
no app model, no theme and no terminal."

**That criterion cannot be met as literally written, and not because
of anything in this proposal.** There is no dialog package. Creating
one is #115, which is deferred, and `docs/api-surface.md` §4 shows it
is not merely deferred but currently impossible: a `tui/render`
package naming `*Model` in an interface signature and imported by
`tui` is an import cycle.

What *is* in this proposal's power is the substance behind the
criterion. Here is the coupling, counted. Across the seven dialog
files, `*Model` is dereferenced 116 times:

| Use | Count | Fate under this proposal |
|---|---|---|
| `m.styles` | 45 | → `Body(width int, st Styles)` parameter |
| `m.height` | 14 | → overlay-owned; `Body` gets a width, the frame gets the height |
| `m.history` | 12 | → resolver |
| `m.refreshViewport` | 10 | → resolver |
| `m.opts` | 10 | → constructor argument (the pickers already seed via `applyModels` / `applySessions`) |
| `m.overlayStack` | 8 | → resolver (`Ask` a nested question; `Close` the parent) |
| `m.sessionGen` | 4 | → resolver |
| `m.applyNamedTheme` | 4 | → resolver |
| `m.applySwitchTarget` | 3 | → resolver |
| `m.displayModelName`, `m.themeName`, `m.refreshTheme`, `m.sessionsCmd`, `m.availableModelsCmd`, `m.Role` | 6 | → constructor argument or resolver |

Every one of the 116 lands in one of three places: a `Styles` value
parameter, a constructor argument, or the resolver. **None of them
requires #115.** The typed result is not merely compatible with
removing `*Model` from the widget signatures — it is the mechanism
that removes it, because 33 of the uses are effects that only exist
because the widget had nowhere to return an answer to.

So the achievable criterion, which this design should be held to, is:

> A test in `package tui_test` constructs each question with its
> option list or config, drives it with a sequence of
> `tea.KeyPressMsg` values, and asserts on the returned `answer` —
> without calling `NewModel`, without `tea.NewProgram`, and passing
> the zero `Styles{}` to `Body`.

That is checkable today and it is the whole of the decoupling except
the package boundary. For scale: `tui/dialog_textinput_test.go` calls
`NewModel` eleven times, and its helpers
(`renderPlain(d Dialog, m *Model)`, `typeInto(t, d, m, s)`) take a
`*Model` purely to pass it through. Both helpers lose the parameter.

Two caveats stated rather than papered over.

1. **`package tui_test` still imports `tui`, so `Model` is compiled.**
   The test does not *construct* one, which is the substance; but if
   the owner's intent behind the criterion was "the widgets live in a
   package that cannot see `Model`", that is #115 and it is blocked.
   §14 Q6.

   A third caveat surfaced on contact, and it has a standard answer.
   Under posture A the whole family is unexported, so an external test
   cannot *name* `newThemePickerQuestion` or `chosen` either. Stage 1
   reaches them through `tui/export_test.go` — a `package tui` file the
   go tool compiles only under `go test`, so apidiff does not see it
   and a host cannot import it. The bridge is type **aliases**, not
   wrappers: the external test therefore asserts against the very
   types the package switches over, and a variant renamed or a field
   retyped fails to compile in the test rather than being absorbed by
   a shim that still builds.
2. **`Styles` is exported and stays exported**, so "no theme" is met
   in the sense of "the zero value works", not "no theme type is
   reachable". `RenderContext.Render` already tolerates a zero
   `Styles` (every field is a zero `lipgloss.Style`, which renders
   unstyled), so this costs nothing.

---

## 9. The keystroke grace period

### 9.1 Where it lives today

Not sprawled — yet — but split across two modals and absent from a
third category:

| Piece | Location |
|---|---|
| The constant, 300 ms, with its rationale | `tui/update.go:1122-1134` |
| `Model.withinGrace(shownAt)` | `tui/update.go:1140` |
| Which keys are gated, permission | `isPermissionDecisionKey`, `tui/update.go:1148` |
| Which keys are gated, elicit | `isElicitCommitKey`, `tui/update.go:2945` |
| Stamp field, permission | `Model.permissionShownAt`, `model.go:221`, set at `update.go:974` |
| Stamp field, elicit | `Model.elicitShownAt`, `model.go:237`, set at `update.go:991` |
| Gate call site, permission | `tui/update.go:1274` |
| Gate call site, elicit | `tui/update.go:2837` |
| Requirement | R-PERM-8 **and** R-ELIC-4, `requirements.md` |

So: one constant and one predicate shared; two stamps, two
key-classifiers, two call sites and two requirements duplicated. The
duplication is small but it is the load-bearing kind — the
key-classifier is a hand-maintained list of which strokes are
dangerous, and it has already diverged from the handler it guards
(`isPermissionDecisionKey` includes `"v"` unconditionally while the
handler gates `"v"` on `Verb != ""`; that divergence is deliberate and
documented at `update.go:1146`, which is exactly the kind of thing
that stops being deliberate on the third copy).

**And every dialog on the overlay stack has no grace at all.** The
text input, the pickers, and `Model.pendingForm` (the huh pricing
form) are all openable from a slash command, which is operator-driven,
so today that is defensible. It stops being defensible the moment a
question can be opened *by the agent* — which is the entire point of
#164. An agent-driven yes/no with no grace window is issue #95 again
with a better widget.

### 9.2 Where it should live

In `Overlay`, once:

```go
// Ask stamps the question's open time. Overlay.Key ignores a
// keystroke that would produce an answer while the stamp is inside
// modalInputGrace, unless the question opts out.
//
// The gate is "would this keystroke answer the question", not "is
// this keystroke in a hand-maintained danger list": the overlay calls
// q.Key on a scratch copy? No — see below.
```

The honest mechanism matters here, because "would this keystroke
produce an answer" cannot be known without running the handler, and
running the handler speculatively is not safe for a stateful widget.
So the gate stays declarative, but it becomes *one* declaration on the
question rather than a package-level function per modal:

```go
type question interface {
	// ... as §6.1, plus:

	// Commits reports whether stroke would end the question, given
	// its current state. The overlay consults it during the grace
	// window and swallows the keystroke if true. A question opened by
	// the OPERATOR (a slash command) returns false always via the
	// embedded base; a question opened by the AGENT overrides it.
	Commits(stroke string) bool
}
```

with the base providing `func (questionBase) Commits(string) bool { return false }`,
and `Overlay.Ask` taking the origin:

```go
type askOrigin uint8

const (
	askOperator askOrigin = iota // opened by a keystroke; no grace needed
	askAgent                     // opened asynchronously; grace applies
)

func (o *Overlay) Ask(q question, origin askOrigin, r resolver)
```

Which is the real fix: the grace window's trigger stops being "is this
the permission modal or the elicit modal" and becomes "did the
operator ask for this surface, or did it appear underneath their
fingers". `esc` remains exempt at the overlay level, for the reason
R-PERM-8 already gives — it dismisses, which is the fail-safe
direction.

R-PERM-8 and R-ELIC-4 then collapse into one requirement about
agent-opened questions. That is a `requirements.md` edit, not an API
change.

---

## 10. API impact

The section the freeze cares about. Two postures; the recommendation
is A.

### 10.1 Posture A (recommended) — land it unexported

Every symbol in §5, §6 and §7 is unexported. `answer`, `dismissed`,
`chosen`, `selected`, `text`, `fields`, `decision`, `declined`,
`question`, `resolver`, `option`, `askOrigin`, the constructors, the
extension interfaces — all lowercase.

**Exported symbols added: none.**

**Exported symbols removed or changed:**

| Symbol | Declared | Change | `verify-apidiff` |
|---|---|---|---|
| `Dialog` | `dialog.go:39` | unchanged (viewers keep it) | not flagged |
| `DialogAction` | `dialog.go:80` | unchanged (viewers keep it) | not flagged |
| `KeyMsgDialog` | `dialog.go:70` | **removed** — `question.Key` takes `tea.KeyPressMsg` unconditionally, so the two-entry-point split has no purpose | **incompatible**; needs a `dev/api-breaks.txt` entry |
| `ScrollDialog` | `dialog_scroll.go:453` | unchanged for viewers; questions use `scrollQuestion` | not flagged |
| `Overlay.Open` | `dialog.go:106` | unchanged (viewers) | not flagged |
| `Overlay.HandleKey` | `dialog.go:170` | **removed** — stroke-string entry point; the only remaining caller is `HandleWheel`'s synthesized up/down, which moves to `keyMsgFromStroke` | **incompatible**; needs an entry |
| `NewTextInputDialog` | `dialog_textinput.go:144` | signature unchanged; `TextInputConfig.Submit` changes shape | see below |
| `TextInputConfig.Submit` | `dialog_textinput.go:94` | `func(string, *Model) DialogAction` → `func(string) error`, with the effects moving to the resolver at the open site | **incompatible**; needs an entry |
| `SessionInput.Submit` | `capabilities.go:120` | **unchanged** — it is already `func(string) (SwitchTarget, error)`, i.e. already effect-free and already the right shape. Worth noting: the one host-facing question callback in the API is the one that got this right. | not flagged |

Three acknowledged breaks, all in symbols `docs/api-surface.md` §3.2
already recommends unexporting outright, and none of which the
reference host references (core-agent uses none of `Dialog`,
`DialogAction`, `KeyMsgDialog`, `Overlay`, `NewTextInputDialog` or
`TextInputConfig` — verified against its two `coretui.Options`
construction sites). The blast radius is `examples/local`,
`examples/core-agent` and the in-repo tests.

`dev/api-breaks.txt` is currently empty ("no acknowledged breaks
against v0.20.0"), so this PR would be the first to write to it since
the tag.

**And the second-order effect, which is the argument for doing it now:
Posture A makes the §3.2 unexport sweep *possible*.** Today `Dialog`
cannot be unexported without also unexporting `Model` (it names
`*Model`), which is #115's blocked work. Remove `*Model` from the
question signatures and the question family is unexportable-by-
construction from day one; the remaining `Dialog` (viewers only) is a
smaller, later problem.

### 10.2 Posture B — export the question family as an extension seam

If the owner wants hosts to be able to *ask* questions — which is a
real want, see §10.3 — then a minimum of nineteen new exported
symbols:

`Answer`, `Dismissed`, `DismissReason` (+4 constants), `Declined`,
`Chosen`, `Selected`, `Text`, `Fields`, `Decision`, `Question`,
`Option`, `Resolver`, `Overlay.Ask`, `AskOrigin` (+2 constants),
`NewSelect`, `NewMultiSelect`, `NewConfirm`, `NewForm`.

All additions, so `verify-apidiff` reports them as **compatible** and
CI stays green. That is precisely why this posture needs a decision
rather than a diff: apidiff will not stop it, and the milestone rule
("read the milestone before proposing anything that widens the
exported surface — it is being narrowed, not grown") is the only gate.

**Recommendation: no.** Nineteen symbols to expose a seam whose
`Overlay` is still an unexported field of `Model`, so a host still
cannot reach it. Posture B is only coherent together with an
`Options`-level registration seam, which is a feature with no host
asking for it, and `docs/api-surface.md` §3.2 makes the general
argument better than this document can: *an exported interface with no
installation seam is a promise the library cannot keep.*

### 10.3 The one exported addition worth arguing for

There *is* a host asking, and it is not asking for widgets.

core-agent ships an `ask_user` tool whose whole contract is one
question (`pkg/tools/askuser.go:40`):

```go
Prompt(ctx context.Context, question string) (string, error)
```

Its production implementation, `tools.StdinPrompter`
(`pkg/tools/askuser.go:133`), reads the passed `io.Reader` — which
`resolveAskUserTool` (`cmd/core-agent/main.go:2548`) sets to the
process's stdin. Under the TUI that contends with Bubble Tea for the
terminal, and the `--ask=auto` path degrades to `tools.RefusePrompter`
whenever stdin is not a TTY. So the reference host has an agent-driven
question path today that **cannot be answered by the operator when the
TUI is running** — the one arrangement in which an operator is
demonstrably sitting there.

That is exactly `requirements.md` §3.19 R-PROMPT-1, which is marked
"⚠️ specified, not shipped as of v0.19.0 — neither `UserPrompter` nor
`NewUserPrompter()` exists in `package tui`. Whether to build it or
drop this requirement is part of the exported-surface audit, issue
#78." #164 is the issue that would build it.

The minimal shape, mirroring D5/D6 exactly:

```go
// Asker is the interface the TUI implements and the host wires into
// its agent's ask-the-user tool. The call blocks on the TUI's modal
// until the operator answers, declines, or ctx cancels. Mirrors
// PermissionPrompter (prompter.go:29) and Elicitor (elicitor.go:30).
type Asker interface {
	Ask(ctx context.Context, req AskRequest) (AskResult, error)
}

// AskKind picks the modal shape. The TUI declines a kind it cannot
// render rather than opening a broken modal (cf. supportedElicit).
type AskKind int

const (
	AskChoice      AskKind = iota // single-select over Choices
	AskMultiChoice                // multi-select over Choices
	AskConfirm                    // yes/no
	AskText                       // one free-text value
	AskLongText                   // editor-backed free text
)

type AskChoice struct {
	ID          string
	Label       string
	Description string
}

type AskRequest struct {
	Kind        AskKind
	Title       string
	Prompt      string
	Choices     []AskChoice // AskChoice / AskMultiChoice
	Placeholder string      // AskText / AskLongText
	Initial     string      // AskText / AskLongText
	// Source is the originating sub-agent name, empty for the
	// foreground agent. Same field, same meaning, as
	// PermissionRequest.Source.
	Source string
}

type AskAction int

const (
	AskAnswered AskAction = iota
	AskDeclined
	AskCancelled
)

type AskResult struct {
	Action    AskAction
	ChoiceIDs []string // AskChoice: len 1; AskMultiChoice: len 0..n
	Text      string   // AskText / AskLongText
}

// NewAsker constructs an Asker ready to be wired into the host's
// ask-the-user tool and the TUI's Options.
func NewAsker() Asker
```

Plus `Options.Asker Asker` — a field addition, non-breaking by
Go-module rules and by `docs/design.md` §8's own statement.

**Exported symbols added: 17** (`Asker`, `AskKind` + 5 constants,
`AskChoice`, `AskRequest`, `AskAction` + 3 constants, `AskResult`,
`NewAsker`, `Options.Asker`). All additions → `verify-apidiff`
reports **compatible**, no `api-breaks.txt` entry.

Note what this is *not*: it is not `Answer`. The host boundary keeps
the enum-plus-payload idiom that `ElicitResult` and
`PermissionDecision` already established. §5.3 argued for sealing
*inside* the package; sealing across the module boundary buys
materially less, because (a) Go gives the host no exhaustiveness
either way, and (b) the reference host demonstrably does not want it —
`translateDecision`, `translateElicitAction`, `decisionToWire` and
`DecisionFromWire` all end in a safe `default:`, which is a deliberate
forward-compatibility choice by a host that upgrades on its own
schedule. **Seal where the compiler and the linter can reach; use
enums where they cannot.** That asymmetry is the design's actual
thesis about "what a typed question is in the host contract".

---

## 11. Migration

### 11.1 For core-agent (the only gating host)

Posture A: **nothing.** core-agent references none of the changed
symbols. Its `PermissionPrompter` bridge
(`cmd/core-agent/coretui_enabled.go:1315-1340`), its elicit adapter
(`:1428-1457`), its schema translator (`:1464-1541`) and its
`Options` literal (`:162-242`) are all untouched. The permission
prompt looks different in one respect only — the six decisions render
as a select with accelerators rather than a footer hint — and behave
identically.

§10.3, if taken, is opt-in: an existing host that does not set
`Options.Asker` sees no change, and the TUI's `ask_user` path stays as
it is.

### 11.2 For a host that has wired an `ask_user`-style tool

Before, in the host:

```go
// resolveAskUserTool, main.go:2548 — reads the real terminal, so it
// fights the Bubble Tea input reader when the TUI owns the tty.
case "stdin":
	prompter = tools.StdinPrompter(in, out)
case "auto":
	if f, ok := in.(*os.File); ok && runner.IsTerminal(f) {
		prompter = tools.StdinPrompter(in, out)
	} else {
		prompter = tools.RefusePrompter("running unattended; ...")
	}
```

After:

```go
type tuiAskPrompter struct{ inner coretui.Asker }

func (p tuiAskPrompter) Prompt(ctx context.Context, question string) (string, error) {
	res, err := p.inner.Ask(ctx, coretui.AskRequest{
		Kind:   coretui.AskText,
		Title:  "The agent has a question",
		Prompt: question,
	})
	if err != nil {
		return "", err
	}
	switch res.Action {
	case coretui.AskAnswered:
		return res.Text, nil
	default:
		return "", errors.New("operator declined")
	}
}
```

and, at the two `Options` construction sites:

```go
 asker := coretui.NewAsker()
 opts := coretui.Options{
     Prompter: prompter,
     Elicitor: elicitor,
+    Asker:    asker,
 }
```

with `resolveAskUserTool` gaining a `"tui"` mode that wires
`tuiAskPrompter{inner: asker}` — and `"auto"` preferring it over
`tools.StdinPrompter` whenever the TUI is the front end. Roughly
twenty lines, the same shape as the existing `gatePrompterBridge`.

### 11.3 For an in-repo caller of the dialog surface

The three breaks in §10.1 affect `examples/local`,
`examples/core-agent` and the tests. `TextInputConfig.Submit` is the
only one with a non-mechanical fix; the recipe is "move the body of
your `Submit` closure into the resolver you pass to `Ask`, delete the
`DialogAction` construction, and return an error instead of appending
a `RoleError` row yourself." `MIGRATION.md` gets a §-entry with that
recipe.

---

## 12. Staged plan

Each stage is a PR. Stages 0-3 are internal and can land before the
freeze without an `api-breaks.txt` entry; stage 4 is the point of no
return.

**Stage 0 — the linter. Done, 2026-08-17.** Both linters are on in
`dev/tools/.golangci.yml`. `gochecksumtype` reported nothing, there
being no sealed interface yet — it is scaffolding for stage 1.
`exhaustive` reported eleven switches, and they were not a mess: every
one of them put the enum's *zero value* on a `default:` arm, which is
one deliberate idiom applied consistently rather than eleven
oversights. Each is now written as a total switch with the fallback
below it, so a member added later is a lint failure instead of a
silent route to the default. The one behaviour change is
`effectiveLayout`, which now normalizes an out-of-range `StatusLayout`
itself rather than leaving `View`'s `default:` arm to absorb it.
This answers §14 Q2: **sealed**, since the condition it was
contingent on now holds.

**Stage 1 — the answer type and the resolver, with one caller. Done,
2026-08-17.** All seven variants, `question`, `cursorQuestion`,
`resolver`, `Overlay.ask` and `Overlay.resolveAll` are in
`tui/question.go`; the theme picker is the one caller, in
`tui/question_themepicker.go`; the §8 criterion ships as
`tui/question_external_test.go`. No exported change — apidiff is
unmoved, and the questions ride the existing stack through an
`askedQuestion` adapter that implements `Dialog`, `KeyMsgDialog` and
`cursorDialog`, so routing, z-order, the esc cascade, the wheel and
the caret all keep working against the old contract while the two
shapes coexist.

Four things came out differently from the sketch, each recorded where
it belongs: `Body` takes the terminal height and `Width` joined the
seam (§6.1), `Ask` shipped as `ask` (§6.2), the live preview became a
scheduled message (§6.3), and the external test reaches the unexported
family through `export_test.go` (§8). Two deliberate omissions:
`scrollQuestion` is not declared, because an interface with no
implementor is a guess rather than a seam and the first scrolling
question arrives in stage 3; and the adapter does not implement
`ScrollDialog`, so a wheel tick over a question still steps the list
by one row instead of three.

**Stage 2 — the grace window moves to `Overlay`. Folded into stage 3,
2026-08-17.** Not landing as a standalone PR, for two reasons found
when it came up for work.

Its headline benefit is already delivered. The stranded-flow hole from
§1.4 — a session switch tearing down a permission prompt or an elicit
form with the host never told — was closed by PR #214 (`ff2e6fb`),
which dispatches `DecisionDeny` / `ElicitActionCancel` at
`tui/update.go:2623-2627`. Wiring `resolveAll` into `applySwitchTarget`
today would also contradict the deliberate comment there that the
overlay stack *survives* a session switch, so a picker can close itself
normally after returning the switch `Cmd`.

And the grace machinery (`askOrigin`, `Commits`) has no agent-opened
question to guard until the permission prompt and the elicit form
become questions, which is stage 3. Landing it first would be an
interface with no implementor — the same thing stage 1 declined to do
with `scrollQuestion`, for the same reason. Each piece now lands in the
stage-3 PR that creates its first user.

**Stage 3 — the family, and the two big migrations.**
`selectQuestion`, `confirmQuestion`, `formQuestion` (huh-backed),
`textQuestion` (from `textInputDialog`). Then move the model picker,
session picker, **permission prompt** and **elicit form** onto them.
This is the largest stage by far and should be at least four PRs:
one per widget, one per migrated modal, each with its own external
test. It also absorbs stage 2, above.

**Model picker: done, 2026-08-17.** `tui/dialog_modelpicker.go` is
gone; `tui/question_modelpicker.go` replaces it. It is the first
question whose answer does not come from a keystroke, and it brought
`Overlay.resolve` with it (§6.5). Four reaches into `*Model` left the
widget: the `ModelSwapper` type assertion, `displayModelName()` during
render, the `history.Append`, and building a host `Cmd` out of
`sessionGen` — the last two now sit in `modelPickerResolver` and in
`Update`'s `modelSwitchRequestedMsg` arm respectively. One behaviour
change: a **failed** switch now leaves the picker open on its list
rather than closing it, since the question was never answered and the
operator's next move is almost always the next model down. It deletes `handleElicitKey` (130 lines), the permission arm of
`handleKey` (50 lines), and five `Model` fields. Fixes the
unreachable-decline bug in §1.4 by construction, since `declined`
becomes an option row.

  **This is the stage that must land before the freeze**, because it
  is the one that removes `*Model` from the widget signatures and
  therefore the one that unblocks `docs/api-surface.md` §3.2's
  unexport sweep. Everything after it is additive.

**Stage 4 — the exported break.** Remove `KeyMsgDialog`,
`Overlay.HandleKey`; change `TextInputConfig.Submit`. Write the three
`dev/api-breaks.txt` entries and the `MIGRATION.md` recipe. **Point of
no return**: after this PR the pre-1.0 break has been spent, and
reverting stages 1-3 means a second break to undo it. Everything
before stage 4 is revertible with no external consequence.

**Stage 5 (optional, gated on §14 Q1) — `Asker`.** The 17 exported
additions from §10.3, `Options.Asker`, the `AskLongText` /
`editorQuestion` member, and a `examples/local` demonstration. Purely
additive; can land after the freeze if the answer to Q1 is "not yet",
at the cost of leaving core-agent's `ask_user` broken under the TUI
for another release.

**Stage 6 — `multiSelectQuestion` + R-PERM-4.** Gated on Q3.

---

## 13. Out of scope

- **Any part of #115.** No files move; no `internal/` packages are
  created; `Model` and `NewModel` are not touched.
- **A host-facing dialog registration seam.** `Overlay` stays an
  unexported field of `Model`. Posture B (§10.2) is described in order
  to be declined.
- **Rewriting the three viewers** (tool-call detail, subagent detail,
  `/btw` side answer). They keep `Dialog` and keep `*Model`. Folding
  them in is a follow-up whose only benefit is symmetry.
- **The slash palette and the `@`-file palette.** D26 already carves
  them out as autocomplete UIs, not forms. Unchanged.
- **`ElicitURLMode`.** No host produces it —
  `translateMCPSchemaToElicitRequest` in core-agent always sets
  `ElicitFormMode`, and the remote client wires no `Elicitor` at all.
  It migrates as a two-option `selectQuestion` and is otherwise left
  alone. Whether to delete it is a separate question for #78.
- **Changing `PermissionDecision`, `ElicitRequest`, `ElicitResult` or
  `ElicitField`.** The MCP schema translator drops `format`,
  `minLength`, `pattern`, `minimum`, `enumNames` and non-string enums
  because `ElicitField` has nowhere to put them; widening
  `ElicitField` is worthwhile and is not this issue.

---

## 14. Open questions

**Q1. Do we ship `Asker` (§10.3), and if so, before the freeze?**
17 exported additions, all compatible, in a milestone whose stated
posture is narrowing. Against: the surface is being narrowed. For: it
is the one piece of #164 a host is actually blocked on today —
core-agent's `ask_user` cannot be answered while the TUI owns the
terminal — and it discharges R-PROMPT-1, which `requirements.md`
currently carries as a ⚠️ marked "build it or drop it, per #78". A
requirement cannot stay in that state through a 1.0.
**Recommendation: yes, and before the freeze, as stage 5 —
but only after stage 3 has landed and proved the internal contract.**
If stage 3 slips, drop R-PROMPT-1 from `requirements.md` rather than
shipping `Asker` on an unproven base.

**Q2. Sealed interface, or enum-plus-payload struct, inside the
package?**
**Recommendation: sealed — conditional on stage 0.** If
`gochecksumtype` is not enabled, take the struct: an unchecked sealed
interface costs seven types and a marker method to buy what a struct
with a `Kind` field buys for free. The conditionality is the answer,
not a hedge. **Resolved 2026-08-17: sealed.** Stage 0 landed and
`gochecksumtype` is enabled, so the condition holds.

**Q3. Include `selected` (multi-select) in the initial variant set,
with no shipped caller?**
Every variant added later is a change to every switch. Every variant
added speculatively is an arm that returns "unreachable" forever.
**Recommendation: declare `selected` in stage 1, build
`multiSelectQuestion` only in stage 6 when R-PERM-4 is scheduled.**
The variant is cheap while the set is being written and expensive
afterwards; the widget is the reverse.

**Q4. Does the permission prompt become a `selectQuestion`, or stay
bespoke?**
Becoming one is what makes R-PERM-2's six keys data instead of a
`switch`, and it is what lets the "always" option's presence stop
depending on a nil callback (§7.2). Against: it is the highest-stakes
modal in the product and it currently works.
**Recommendation: yes, as the last item in stage 3, behind the full
existing test suite plus a golden-frame test** — the modal's rendered
frame is already covered by `tui/golden_test.go` and
`tui/frame_invariant_test.go`, so a regression in appearance is
catchable, and a regression in *decision routing* is catchable by the
existing prompter tests, which do not know how the modal is drawn.

**Q5. Amend D26's mapping table?**
D26 maps the permission modal and the `/permissions` picker to
`huh.NewSelect` / `huh.NewMultiSelect`. §7.7 argues the selects should
stay hand-rolled (filter row, two-line cells, live preview,
accelerators) and the *forms* should become huh.
**Recommendation: amend D26 with a dated note rather than rewriting
it** — "the select half of this table is superseded by
`design-question-dialogs.md` §7.7; the form half stands." D26's
reasoning was right about forms and was written before the pickers
grew filtering in #117.

**Q6. Is `package tui_test` + no `NewModel` an acceptable reading of
#164's acceptance criterion?**
The literal criterion needs a dialog package and is blocked on #115.
**Recommendation: yes, accept the §8 form, and amend #164's wording**
so a future reader does not treat the criterion as unmet. If the owner
wants the literal form, this issue is blocked on #115 and should be
moved out of the milestone rather than half-done.

**Q7. Fix the two bugs this investigation turned up separately, or as
part of the migration?**
The unreachable form-mode decline (§1.4) and the stranded blocked call
on session switch (§1.4) are both real today. Each is a small
standalone fix; each is also fixed by construction in stage 2/3.
**Recommendation: fix the stranded call now, as its own bug PR** —
it hangs a host goroutine and does not need this design. **Leave the
decline path to stage 3**, and in the meantime correct the lying
comment at `tui/elicitor.go:107`, since "form: n" describes behaviour
that does not exist.

---

## Decision log

- 2026-08-17 — design captured for #164. Not implemented. Blocking
  decision is Q1 + Q2; stage 0 can start regardless of either.
- 2026-08-17 — stage 0 landed: `exhaustive` and `gochecksumtype` are
  enabled and the eleven enum switches they found are total. Q2 is
  answered — sealed. Q1 (`Asker`) is still open and still gated on
  stage 3.
- 2026-08-17 — stage 1 landed: the sealed `answer`, the `question`
  seam, the `resolver`, and the theme picker as the first caller. The
  sealing is real — `answer` carries `//sumtype:decl`, so the resolver
  names all seven variants and a missing arm is a lint failure rather
  than a silent fallback. Four deviations from the sketch are recorded
  in §6.1, §6.2, §6.3 and §8. Q1 and Q3 are still open; neither gates
  stage 2 or 3.
- 2026-08-17 — stage 2 folded into stage 3 rather than landing on its
  own. PR #214 already closed the §1.4 hole it was justified by, and
  the grace machinery has no agent-opened question to guard until the
  permission prompt and the elicit form migrate. Recorded in §12.
- 2026-08-17 — stage 3 began with the model picker. It is the first
  question whose answer arrives from a host reply rather than from a
  keystroke, which is the shape the session picker, the permission
  prompt and the elicit form share, so it went first to establish it.
  The seam it added is `Overlay.resolve` (§6.5), together with the two
  rules that fall out of pop ordering: a resolver may not open a
  dialog, and a committed question is not an answered one.
