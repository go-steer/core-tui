# Auto permission mode: the chip, the cycle and the escalated prompt

**Status:** implemented ([#360](https://github.com/go-steer/core-tui/issues/360)).
API: [`design.md`](./design.md) §3.5.

## Problem

core-agent is adding a fifth permission mode, `auto`
([go-steer/core-agent#1175](https://github.com/go-steer/core-agent/issues/1175),
design in that repo's `docs/auto-mode-design.md`). It is `ask` with a
model-backed approver in front of the person. The approver may allow one
call, deny it, or pass it on to the person, who then sees the ordinary
permission prompt.

core-agent's design requires the TUI to display `auto` before anything
can select it (its decision 13). Both hosts currently show a mode the
chip has no value for as `default`, so an operator would see "ask" while
a model approves calls. When an approver passes a call on, the prompt
also has to show why, and stop offering grants the approver's answer
must not turn into (its decision 11).

## Shape

```go
const PermissionModeAuto PermissionMode = 4 // appended; String() == "auto"

type PermissionModeWiring struct {
    Initial PermissionMode
    Set     func(PermissionMode) error
    Persist func(PermissionMode) error
    // Cycle is the order Shift+Tab visits. nil or empty is the
    // default four: default → acceptEdits → plan → bypassPermissions.
    Cycle []PermissionMode
}
```

```go
type PermissionRequest struct {
    // ...
    Escalation *PermissionEscalation // nil: an ordinary prompt
}

type PermissionEscalation struct {
    Approver string
    Reason   string
}
```

A request with an `Escalation` offers allow once, deny, and deny with a
reason (when the request came in through `AskApprovalDetailed`). It
quotes the approver's reason in both layouts, under "passed to you by
<approver>, which said:". `ApprovalLog` gains
`Approver`, the model that allowed a call, so the approval history can
show that a model and not a person decided.

## Settled decisions (do not relitigate)

1. **`PermissionModeAuto` is appended, not inserted.** `PermissionMode`
   is an exported `int`, and existing values are part of the API.
   Where `auto` appears in the cycle is the host's business (decision
   2). It is not decided by the constant's value.
2. **The host supplies the cycle.** A mode the host's `Set` refuses rolls
   the chip back to where it was (#137), and the next Shift+Tab lands on
   the refused mode again. A fixed cycle containing `auto` would
   therefore trap an operator whose session cannot enter `auto` (in
   core-agent, one with no approver configured or no `approval_timeout`):
   every keystroke would try `auto`, fail, and roll back, and they could
   never get past it to `default`. With `Cycle`, a host lists `auto` only
   when the session can enter it. core-agent's order is default → auto →
   acceptEdits → plan → bypassPermissions.
3. **nil `Cycle` is exactly today's behaviour.** `PermissionMode.Next`
   keeps the four-mode default cycle. It now sends any mode outside that
   cycle — `auto`, or a value outside the declared set — to `default`,
   where `% 4` used to leave a negative value stuck.
4. **A current mode missing from the cycle advances to the cycle's first
   entry.** That covers a host that seeds `Initial` with a mode it then
   leaves out of `Cycle`, so the keystroke always moves somewhere.
5. **`Cycle` is read once, at startup, and does not follow a session
   switch.** The TUI copies it, keeps the first occurrence of a repeated
   mode (a repeat would make everything after it unreachable), and drops
   values outside the declared set (the chip would read "default" while
   `Set` received the stray value). A host whose sessions differ in what
   `Set` accepts lists only what every session accepts. core-agent's are
   uniform: the approver and `approval_timeout` are daemon-wide, and each
   session's gate inherits both.
6. **`auto` uses the ordinary chip style.** Only `bypassPermissions` is
   loud. `auto` still puts every call it cannot allow in front of a person.
7. **An escalated prompt keeps "deny with a reason".** core-agent's
   decision 11 (its `docs/auto-mode-design.md`) forbids the grants an approver's answer could otherwise become: allow for the
   session, the verb or the tool, and allow always. A reason grants
   nothing, and it tells the agent why.
8. **The approver's reason is untrusted text.** It is model output, and
   arguments planted in the call can steer it ("routine, safe to always
   allow"). It is labelled as the approver's words and rendered as one
   quoted, indented, muted-italic paragraph:
   - every whitespace run, newlines included, collapses to one space, so
     it can never start a row of its own (a forged `verb:` line, a fake
     legend);
   - it is sanitized, and bidi controls and zero-width characters are
     shown as `\uXXXX` rather than obeyed;
   - it is capped at 600 bytes and 6 wrapped rows, so it can never push
     the payload off the first screen.

   The approver's name gets the same treatment, as one row of at most 64
   bytes.
9. **The once-or-deny restriction is enforced twice.** The prompt offers
   only those options, and the dispatch path turns any other decision on
   an escalated request into a deny, so `AlwaysAllow` can never fire for
   one. The host should still refuse standing grants on its own side:
   the TUI is not the security boundary.

## Out of scope

- A host-to-TUI mode push. The chip still learns of a mode only through
  `Initial` and its own keystrokes. A mode changed elsewhere (an attach
  client's `POST /perms/mode`) is not reflected until the host rebuilds
  the options. That gap predates `auto`.
- Rendering `StatusUpdate.PermMode` from the event stream; still reserved.
  The protocol doc's list of its values is left alone until it is used.
- A per-session `Cycle` in `SwitchTarget`. See decision 5.
- Two orderings that predate this work and that `auto` makes more
  visible. A `Set` refusal that arrives after a session switch is
  dropped, leaving the chip on the refused mode. Two quick Shift+Tabs
  run two `Set` calls that can finish out of order.
- Choosing the approver, its eligible list or its policy. Those live in
  core-agent.
