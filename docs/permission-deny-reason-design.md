# Permission prompt: deny with a reason

**Status:** implemented ([#344](https://github.com/go-steer/core-tui/issues/344)).
Requirement: R-PERM-9 in [`requirements.md`](./requirements.md). API:
[`design.md`](./design.md) §3.5.

## Problem

core-agent's attach protocol 1.15.0
([go-steer/core-agent#1165](https://github.com/go-steer/core-agent/issues/1165))
lets a deny carry the operator's reason: `POST /perms/respond` takes an
optional `reason`, and the model reads it in the refused call's result.
Without one the model guesses what the operator objected to, and
usually re-issues a near-identical call. The TUI's permission prompt
resolved a deny as a bare decision, with nowhere to type.

## Shape

```go
type PermissionOutcome struct {
    Decision PermissionDecision
    Reason   string // non-empty only on DecisionDeny, only when typed
}

func (p *Prompter) AskApprovalDetailed(ctx context.Context, req PermissionRequest) (PermissionOutcome, error)
```

`AskApproval` is now a thin wrapper returning `.Decision`. On the
prompt, a request that came in through `AskApprovalDetailed` gains one
key: `r` "deny with reason…", listed next to `n`. It opens a one-line
input inside the prompt (both layouts). `enter` submits the deny with
the trimmed text; `esc` goes back to the choices, undecided.

## Settled decisions (do not relitigate)

1. **No host capability. The reason is a return value.** The issue
   proposed a `PermissionDenyReasoner` capability for hosts to
   implement. Hosts do not implement permission handling: they call
   `tui.Prompter.AskApproval` from their gate (core-agent's in-process
   `gatePrompterBridge`, and `coretuiremote.StartRemotePrompter` in
   attach mode) and relay the decision to their backend. A reason
   travelling the other way would need a second channel from the TUI
   into the host, keyed by a prompt id the TUI does not have. Returning
   it next to the decision puts it exactly where the host is already
   holding the decision. One exported type and one method; no new
   interface, no `Options` field.
2. **Opt-in per request, by which method the host called.** A request
   through `AskApprovalDetailed` offers `r`; one through `AskApproval`
   does not, and the key is inert there. The TUI cannot know whether a
   host would forward a reason, and collecting text that is then
   silently dropped tells the operator the agent heard something it
   never did. The caller choosing the method that can return a reason
   is the only honest signal. The flag rides on the internal
   `permissionFlow` to the prompt, and the dispatch path drops a
   reason on any flow that did not opt in.
3. **500-byte cap, measured in bytes, refused rather than truncated.**
   500 bytes is the server's limit, and it is bytes of UTF-8, so the
   counter reads `n/500 bytes` and 200 × "é" (400 bytes) is allowed
   where 300 × "é" (600) is not. The counter turns warning-coloured
   past the cap, and `enter` over it is refused with an inline note,
   the text left in place. Truncating would send a sentence the
   operator did not write; passing it through would earn the host a
   400. The TUI measures the trimmed text without collapsing inner
   whitespace runs the way the server does, so it can only ever be
   stricter than the server, never looser.
4. **Deny only; no dismissal carries a reason.** The server rejects a
   reason on any other decision, and an allow carrying text reads as
   conditions on what it authorized. Esc at the choices, a superseded
   or shut-down prompt and a cancelled context are all plain denies,
   even if text had been typed: a reason is only ever something the
   operator typed and submitted.
5. **The single-keypress deny stays.** `n` and esc at the choices
   deny exactly as before. `r` is a second path, not a detour on the
   first.
6. **While the input is open, letters are text.** `y`, `s`, `t`, `a`
   type into the input and decide nothing. Esc means "back", and both
   the prompt's legend and the app footer say `esc back` while the
   input is open, so the fail-safe key is never misdescribed.
7. **Grace window.** `r` is held by the input grace like the decision
   keys, because it is the first stroke of a deny and a buffered `r`
   would feed the operator's next keystrokes into the reason. Inside
   the input only `enter` is held.

## Out of scope

- **core-agent's side.** Its two hosts switching their `AskApproval`
  call to `AskApprovalDetailed` and forwarding a non-empty `Reason`
  with the deny — `/perms/respond`'s `reason` on protocol ≥ 1.15.0,
  via `attachclient.Client.DenyPrompt` in attach mode and the
  in-process gate locally — lands with core-agent's pin bump.
- **Reasons on allows.** The server rejects them, and there is no
  proposal to change that.
- **Multi-line reasons or an editor.** One line, bounded at 500 bytes,
  is the shape the server takes.
