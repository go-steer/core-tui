// Copyright 2026 The go-steer team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// textareaMinHeight / textareaMaxHeight bound the auto-growing
// input box. The textarea starts at MinHeight and grows one row
// per visual line until it hits MaxHeight, after which the
// textarea's own internal scroll takes over. Layout reconciles
// on every height change so the viewport shrinks to make room
// (and re-scrolls if it was pinned to bottom).
const (
	textareaMinHeight = 3
	textareaMaxHeight = 15
)

// turnState is the high-level activity bit the spinner and input
// gating key off of.
type turnState int

const (
	stateIdle      turnState = iota // input enabled, no spinner
	stateStreaming                  // turn in flight: input disabled, spinner active
)

// Model is the Bubble Tea model that drives the TUI. Field set is the
// minimum needed for the v0 visual-preview slice; later slices add
// streaming state, modal forms, transcript persistence, etc.
type Model struct {
	opts    Options
	styles  Styles
	history History

	// viewport is the transcript's window — its size and its scroll
	// position, as a (row, line) pair rather than a flat line offset
	// into a concatenated blob. It holds no content: rows are
	// rendered on demand by the walk in chatlist.go, which is what
	// bounds a repaint by what is on screen instead of by how long
	// the session has been running (issue #161).
	viewport chatViewport
	input    composer

	// chatTail is the rendered live tail of the transcript — the
	// in-progress assistant block, the spinner line, or the
	// empty-state hint — as lines, separator included. Rebuilt by
	// refreshViewport (buildChatTail) rather than cached per item:
	// it changes on essentially every event, so there is nothing to
	// memoize, and rebuilding it there keeps chatView pure enough to
	// run under View's value receiver.
	chatTail []string

	// chatRule is the separator rule drawn above a user turn, and
	// chatRuleWidth is the width it was rendered at. Rebuilt by
	// refreshViewport as well: the rule belongs to the gap between
	// two rows rather than to either of them, so it is the one piece
	// of a row the per-item cache does not hold, and without a memo
	// every walk over the window would re-render one per user turn on
	// screen.
	chatRule      string
	chatRuleWidth int

	// follow is the operator's "keep me on the tail" intent for the
	// chat viewport. refreshViewport re-pins to the bottom after
	// every repaint while it is set.
	//
	// It is tracked rather than derived because viewport.AtBottom()
	// is a geometric fact about the CURRENT height — shrink the
	// viewport (terminal resize, the textarea growing a line, the
	// help panel opening) and a viewport that was pinned to the
	// bottom starts reporting false, silently dropping follow
	// mid-stream (issue #93). Intent has to survive geometry
	// changes, so only the scroll paths write it: syncFollow re-
	// derives it right after a user-driven scroll (while the
	// geometry is still the one the operator scrolled in), and the
	// operator-initiated jump paths set it outright.
	follow bool

	width  int
	height int

	// chrome is the row allocation the last resize() computed for
	// everything View stacks around the chat viewport (budget.go).
	// It is carried on the model because the two collapsible panels
	// have to see the ceiling they were given at render time, and
	// because syncInputHeight clamps the auto-grow to what the
	// budget can afford. The zero value means "never sized", which
	// every reader treats as "no ceiling".
	chrome chromeBudget

	statusLayout StatusLayout
	permMode     PermissionMode

	// themeName holds the operator's explicit named-theme pick
	// (seeded from Options.InitialThemeName, mutated by the
	// /theme picker via applyNamedTheme). Empty (zero value)
	// means "no explicit pick" — resolveStyles then falls through
	// to the auto / per-provider path. Resolved case-insensitively
	// via ThemeByName, so a stale persisted name never strands
	// the operator on a half-painted UI.
	themeName string

	// focus is the region that owns the keyboard: the composer (the
	// zero value, and what every path assumed before issue #151) or
	// the transcript. Move it with Model.setFocus rather than
	// assigning here — the textarea's own focus flag has to track
	// it, and that is what keeps stray text out of a prompt nobody
	// is looking at. See focus.go.
	focus focusTarget

	// selIdx is the history row the transcript's cursor sits on, and
	// collapsed holds the folded rows by Message.ID (issue #152).
	// Neither is a render input: the marker is prefixed at draw time
	// and the fold is applied to the cached lines on the way out, so
	// moving the cursor or folding a row re-renders nothing. See
	// select.go.
	//
	// selIdx has no "nothing selected" sentinel. Zero — the first
	// row, or a harmless index into an empty transcript — is right
	// for a model nobody has focused yet, because the marker is only
	// drawn while the transcript holds the keyboard, and
	// chatSeedSelection places the cursor properly the moment it
	// does.
	selIdx    int
	collapsed map[uint64]bool

	// chatX is how far right the transcript window is panned, in
	// cells (issue #154). Zero for all but the moments an operator is
	// reading a wide table or an unwrapped diff: it is dropped by a
	// selection move, a resize and a focus change, so it never
	// outlives the one thing it was opened for. Unlike selIdx it IS a
	// render input — chatView cuts every line at [chatX, chatX+width)
	// — but it changes nothing about the cache, since the cut happens
	// after the lookup.
	chatX int

	// copyNotice is what the last copy did, shown in place of the
	// focus legend until the next transcript keystroke (issue #153).
	// A copy changes nothing on screen, so it has to say so somewhere;
	// this is a string rather than a timed toast because a tick would
	// need a generation stamp to keep two copies from fighting, and
	// "it says so until you do the next thing" cannot describe the
	// wrong copy. Cleared in handleTranscriptKey and setFocus.
	copyNotice string

	// copyGen is the generation of copyNotice: bumped every time the
	// notice is set, replaced or cleared (issue #175). Options
	// .ClipboardWriter runs off the loop, so its verdict can land
	// after the operator has moved the cursor or copied something
	// else; the reply carries the generation it was issued under and
	// is dropped unless the notice it describes is still on screen.
	// Strictly stronger than sessionGen for this — a notice is
	// superseded many times within one session, and a session switch
	// clears it anyway by way of setFocus.
	//
	// Note this is a stamp on the NOTICE, not on the clipboard: the
	// write itself is never retracted, only the sentence describing
	// it. A superseded reply loses the right to speak, not the right
	// to have happened.
	copyGen uint64

	// helpOpen toggles the bottom-anchored stacked help panel
	// (`?` to open / page / close). When open, the chat viewport
	// shrinks to make room above the input.
	helpOpen bool

	// helpPage is the zero-based page of the help panel on screen.
	// The panel lays itself out to the row cap the chrome budget
	// gives it and paginates when the content does not fit; `?`
	// walks the pages and closes the panel after the last one
	// (help.go). Always 0 while the panel is closed, and clamped at
	// render time — the cap moves with the terminal, so a stored
	// page index can go stale under a resize.
	helpPage int

	// palette is the active slash / file palette overlay (R-PAL-1 /
	// R-PAL-2). Nil = no palette open. Triggered by typing `/` at
	// the start of the input or `@` anywhere.
	palette *palette

	// sideAnswer is the active /btw-style modal overlay (R-CMD-5).
	// Nil = no side-answer open. Carries the question, the agent's
	// answer (or err), and the Glamour render width. Dismissed with
	// Esc / Enter / Space.
	sideAnswer *SideAnswer

	// pendingPermission is the active PermissionRequest awaiting an
	// operator decision (R-PERM-1). Nil = no modal open. Key
	// handler dispatches back via opts.Prompter.dispatchDecision.
	//
	// permissionShownAt stamps when the modal appeared. Decision keys
	// are inert for modalInputGrace after that — a prompt arrives
	// asynchronously, and without the window whatever the operator
	// happened to be typing is consumed as their answer to a modal
	// they have not seen yet (issue #95).
	pendingPermission *PermissionRequest
	permissionShownAt time.Time

	// pendingElicit is the active ElicitRequest awaiting form
	// submission / decline / cancel (R-ELIC-1). Nil = no modal
	// open. Per-field cursor + values tracked in elicitFieldIdx +
	// elicitValues. Key handler dispatches back via
	// opts.Elicitor.dispatchResult.
	//
	// elicitShownAt is the permission modal's grace stamp for the
	// same reason: the keys that dispatch a result (submit / accept /
	// decline) stay inert for modalInputGrace so buffered input can't
	// answer the form before the operator has seen it.
	pendingElicit    *ElicitRequest
	pendingElicitSrv string         // server name for the title bar
	elicitFieldIdx   int            // currently-focused field (Tab/Shift+Tab nav)
	elicitValues     map[string]any // in-progress form values
	elicitShownAt    time.Time

	// modalScroll is the shared scroll offset for the modals that
	// live inline on the Model rather than on the Overlay stack —
	// the permission overlay, the elicit form, and the /btw side
	// answer. Only one of them renders at a time (View's precedence
	// cascade), so one offset is enough; each open resets it.
	//
	// It's a POINTER because View() has a value receiver: the render
	// path measures the body and writes the geometry back here so
	// the next keystroke can clamp without re-rendering. Use
	// Model.scroll() rather than touching the field — a zero-value
	// Model{} (tests) has nil here.
	modalScroll *scrollState

	// toast is a transient banner that renders between the input
	// box and the footer (R-WAKE-1). Cleared after toastTTL via
	// cullToast on the next render. Set by wakeMsg handling.
	toast      string
	toastSetAt time.Time

	// Streaming-turn state (R-CHAT-3 / R-CHAT-4 / R-CHAT-6).
	state      turnState
	cancelTurn context.CancelFunc // non-nil while state == stateStreaming

	// turnStarted is when the current spinner animation began, and
	// is what the thinking line's elapsed readout counts from
	// (issue #111). Stamped at exactly the two sites that bump
	// spinnerGen — submitTurn for the per-turn Run path, and
	// applyStreamChunk's spinnerActive false→true flip for the
	// LiveAgent path, which never calls submitTurn — and zeroed
	// wherever that animation stops. Invariant: non-zero iff a
	// spinner animation is live. Read via Model.turnElapsed, which
	// treats the zero value as "no turn" rather than as 1970.
	turnStarted time.Time

	// now is the Model's clock, defaulted to time.Now by NewModel.
	// Exists so tests can pin the elapsed readout instead of
	// goldening wall-clock output. Unexported and deliberately NOT
	// an Options field: this is test scaffolding, and Options is
	// under the stability promise in CHANGELOG.md. Access through
	// Model.nowFn, which tolerates the nil on a zero-value Model{}.
	now func() time.Time

	inProgressText string  // accumulator for streamed tokens
	currentUsage   *Usage  // most recent usage snapshot for this turn
	currentCost    float64 // most recent positive cost for this turn (USD)
	currentModel   string  // model name for the in-progress message

	// Push-mode state (issue #40 / spec v1.1.0). Populated by the
	// Update handlers for statusUpdateMsg / usageUpdateMsg, read
	// by displayProvider() + the /stats renderer when the host
	// adapter is feeding push events. Nil/zero values mean the
	// host hasn't pushed any state via this path yet (poll-mode
	// hosts never touch these — they keep flowing through the
	// existing StatusReporter / per-turn snapshot paths).
	pushedProvider   string       // most recent push-status provider tag
	pushedContextPct *int         // most recent push-status context-pct (pointer so 0 ≠ absent)
	sessionUsage     *UsageUpdate // most recent cumulative usage snapshot from a usage-update event

	// hostSnap caches the StatusReporter + UsageTracker reads that the
	// status header used to pull on every paint, refreshed off the event
	// loop (see host_snapshot.go). The render-path helpers read this
	// instead of calling the host so a slow/wedged host method can never
	// block View() and freeze the event loop. Zero value until the first
	// refresh lands; the helpers fall back to placeholders in that window.
	hostSnap hostSnapshot

	// AsyncSlashProvider in-flight state (issue #13). The TUI used
	// to dispatch /btw / /compact and sit silent for the entire 1-10s
	// model call; inFlightSlash carries the running slash's name +
	// start time so the toast surface and status-line segment can
	// surface a "/<name> running…" indicator throughout. cancelSlash
	// is the ctx cancel from invokeSlashAsync — fired from the Esc
	// handler when set, mirroring how cancelTurn works for streaming
	// turns. Nil/zero when no async slash is pending; reset on the
	// slashResultMsg handler.
	inFlightSlash *slashFlight
	cancelSlash   context.CancelFunc

	// listCache memoizes the styled-string render of each history
	// Message keyed by (Message.ID, viewport width, Message.Version).
	// Without it every refreshViewport re-Glamour-renders every
	// assistant message — visible as stutter on long sessions. See
	// listcache.go for the cache contract.
	listCache *listCache

	// statusCache memoizes the assembled status header keyed on the
	// values that feed it (issue #201). Behind a pointer for the same
	// reason listCache is: it is filled during a draw, and Model.View
	// has a value receiver. See statuscache.go for why the key is the
	// values rather than a version stamp, which is the one thing that
	// makes a shared pointer safe here.
	statusCache *statusCache

	// Incremental Glamour cache for the in-progress assistant
	// stream. inProgressStablePrefix holds the portion of
	// inProgressText up to the latest safe boundary (\n\n outside
	// an open code fence); inProgressStableRender holds its
	// Glamour render. On each chunk, only the trailing partial
	// is re-rendered + concatenated, avoiding a full re-parse of
	// the accumulated text per token. Both reset when:
	//   - turn finalizes (finalizeTurn)
	//   - tool call segments the stream (applyToolCall)
	//   - viewport width changes (ensureMarkdown rebuilds)
	inProgressStablePrefix string
	inProgressStableRender string
	toolActive             bool // true after a ToolCall; flips back on next Text
	seenToolIDs            map[string]bool

	// subagentTails tracks the live turn tail under each in-flight
	// sync-subagent tool row (issue #71), keyed by tool call ID.
	// Entries are created in applyToolCall and retired in
	// applyToolResult; a tool whose polls come back unresolved
	// gives up early and lands in subagentNotTail.
	subagentTails map[string]*subagentTail
	// subagentNotTail is the per-tool-NAME negative cache: once
	// `read_file` has been proven not to be a subagent, no later
	// `read_file` call pays for the polls again.
	subagentNotTail map[string]bool
	// spinnerFrame counts spinner ticks since the animation started.
	// It drives the Braille glyph directly and the verb pool through
	// spinnerFramesPerVerb — one counter at two rates, which is issue
	// #162. It used to be one counter at the slower of the two, so the
	// glyph advanced once every three seconds.
	spinnerFrame  int
	spinnerActive bool // gates spinner tick scheduling

	// bannerFrame is how far the startup wipe has got (issue #165).
	// Counts up to bannerFrames and stops; at bannerFrames the banner
	// is the settled wordmark and no tick is armed. NewModel seeds it
	// AT bannerFrames when the wipe is not going to run at all, so the
	// still frame is the default and the animation is the exception —
	// see initialBannerFrame.
	bannerFrame int

	// spinnerGen identifies the spinner tick chain that is allowed to
	// be live (issue #112). Bumped wherever a new animation starts —
	// submitTurn (per turn) and applyStreamChunk (per LiveAgent
	// stretch) — and stamped onto every spinnerTickMsg by armSpinner.
	// The handler drops a tick whose stamp is stale, which terminates
	// the superseded chain instead of letting it re-arm forever
	// alongside the current one. Deliberately NOT sessionGen: that
	// only bumps on session switch, so two overlapping turns inside
	// one session — the reported symptom — would share a generation.
	spinnerGen uint64

	// queue is the per-session prompt queue (R-CHAT-10). Each entry
	// transitions through Queued → InFlight → Done / Failed and
	// lingers in terminal state for cullTTL so the operator can see
	// the result. Drained one-at-a-time when finalizeTurn fires.
	queue []QueueEntry

	// consecutiveAutoContinues counts back-to-back auto-continue
	// turns (Options.MidTurnInjectionMode == AutoContinueFromInbox)
	// without an operator-initiated turn in between. Reset to 0
	// whenever the operator types a fresh prompt; incremented on
	// each auto-continue submission. The soft cap from
	// Options.AutoContinueCap (default DefaultAutoContinueCap)
	// halts the loop and logs a system note so the operator can
	// reclaim control.
	consecutiveAutoContinues int

	// LiveAgent capability state (issue #22). liveMode is set
	// once at NewModel time when opts.Agent satisfies LiveAgent;
	// cancelLiveStream is the cancel func from startLiveStream
	// (held so a future force-reconnect / shutdown path can fire
	// it). liveDisconnected flips true when the stream ends and
	// gates the "Disconnected" banner. liveReadOnlyNoted prevents
	// the read-only-view system note from logging on every
	// keystroke when a LiveAgent host doesn't implement Inject.
	liveMode          bool
	cancelLiveStream  context.CancelFunc
	liveDisconnected  bool
	liveReadOnlyNoted bool
	// liveLastPartialAt + liveLastCommitAt drive the spinner in
	// LiveAgent mode: spinner is active whenever the most recent
	// partial Text arrived AFTER the most recent commit Text
	// (tokens are in flight). Both stay zero in non-LiveAgent
	// runs — the existing m.state == stateStreaming gate
	// continues to drive the spinner there.
	liveLastPartialAt time.Time
	liveLastCommitAt  time.Time

	// eventCh is the bridge between the agent dispatch goroutine and
	// the Bubble Tea loop. eventListener drains it one message at a
	// time. Buffered so a fast agent can't stall on a slow Update.
	eventCh chan tea.Msg

	// lifeCtx / lifeCancel bound the lifetime of every listener Cmd
	// — the drain loops in agentcmd.go that park on a channel waiting
	// for the next permission request, elicit request, notice, wake
	// signal or agent event (issue #202).
	//
	// Those Cmds used to block on context.Background() or on a bare
	// channel receive with no second case, which meant the only thing
	// that could ever wake them was traffic that, at shutdown, never
	// comes. Bubble Tea does not wait for outstanding Cmd goroutines
	// — handleCommands spawns one per Cmd and deliberately leaks it
	// ("Don't wait on these goroutines, otherwise the shutdown
	// latency would get too large", tea.go) — so today the parked
	// goroutines simply die with the process. That is an accident of
	// the process model, not a lifecycle: a host that embeds the TUI
	// in a longer-lived process, or runs it twice, accumulates one
	// permanently parked goroutine per listener per run, each one
	// pinning the Model it captured.
	//
	// The context is created in NewModel and cancelled at the two
	// places that can observe shutdown: every Update path that
	// returns tea.Quit (via Model.quitCmd), and Run's defer, which
	// covers the paths Update cannot see at all — a cancelled
	// Options context, tea.Program.Kill, SIGINT. Bubble Tea v2 hands
	// the model nothing on those paths; its event loop intercepts
	// QuitMsg and returns before model.Update is called, and context
	// cancellation exits the loop without synthesising a message. So
	// "cancel where we return tea.Quit, and again once Run returns"
	// is the whole of the observable surface.
	//
	// Both fields are copied by value along with the rest of the
	// Model, and that is fine here in a way it is not for listCache
	// or modalScroll: nothing ever writes these fields after
	// construction. Every copy carries the same context.Context
	// interface value and the same cancel closure over the same
	// cancelCtx, so cancelling through any copy cancels the one
	// context all the listeners are parked on. Read them through
	// Model.listenerCtx / Model.endListeners, which tolerate the nil
	// a zero-value Model{} (tests) has.
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	// Viewport-refresh coalescing (perf: attach to long remote
	// sessions used to be O(N²) because each incoming event walked
	// the full history, concatenated every rendered message, and
	// called viewport.SetContent on the ever-growing buffer).
	// Event handlers now flip viewportDirty via markViewportDirty
	// instead of calling refreshViewport synchronously; a single
	// coalescedRefreshMsg tick then runs refreshViewport once for
	// every event that landed in the coalesce window. refreshPending
	// guards against scheduling redundant ticks while one is already
	// in flight. Both start zero; user-input and modal-open paths
	// still refresh synchronously for immediate visual commit.
	viewportDirty  bool
	refreshPending bool

	// Debounced resize reflow (issue #104 — see resize.go).
	// resizeGen stamps every width-changing WindowSizeMsg; the
	// settle / warm ticks carry the stamp they were scheduled with
	// and are dropped when it no longer matches, so a superseded
	// drag's callback can't clobber a newer resize. reflowPending
	// says a width change is still working its way through the
	// transcript; reflowCursor is how far the incremental warm walk
	// has got. Which rows are on screen comes from the transcript's
	// own scroll offset now (chatVisitWindow) rather than from
	// recorded line spans — an item-addressed window knows the row
	// it starts at, so it can re-wrap a row and measure the result
	// instead of estimating from measurements taken at the previous
	// width.
	// reflowMaxID is the highest Message.ID that existed when the
	// width changed — rows appended after it are committed at the
	// current width and must not be re-rendered.
	resizeGen     uint64
	reflowPending bool
	reflowCursor  int
	reflowMaxID   uint64

	// markdown is the lazily-built Glamour renderer; rebuilt when
	// dark/light or width changes. nil until first use.
	markdown *markdownRenderer

	// modalMarkdown is the same thing sized for a modal body (the
	// /btw side answer), kept apart so the narrower modal width
	// doesn't evict the chat renderer on every open. See
	// ensureModalMarkdown.
	modalMarkdown *markdownRenderer

	// quitting flips when Ctrl+C / Ctrl+D land, so the next Update
	// returns tea.Quit.
	quitting bool

	// pendingExit holds the warn-then-quit state for Ctrl+C while
	// idle: first press sets it (showing a system message), second
	// press within ctrlCExitTTL actually quits. Mirrors internal/tui
	// + Claude Code: prevents accidental drops on a single fat-finger.
	// Reset by any keystroke that isn't Ctrl+C and by the
	// pendingExitClearMsg fired after the TTL.
	pendingExit bool

	// confirmingClear is true between a /clear submission and the
	// operator's y/yes confirmation. While true the footer hint
	// changes and the next Enter is interpreted as the confirmation
	// answer (y/yes wipes history, anything else cancels).
	confirmingClear bool

	// promptHistory is the shell-style recall buffer: every
	// non-slash submitted user prompt is appended (deduped if it
	// matches the immediate previous entry). historyCursor walks
	// the buffer when the operator presses ↑/↓ on an empty input.
	// -1 = not navigating.
	promptHistory []string
	historyCursor int
	historyDraft  string // user's in-flight input saved before navigation

	// startedAt is the wall-clock time the TUI launched. Used by
	// the transcript-on-exit hook so saved files name themselves
	// with the session-start instant.
	startedAt time.Time

	// overlayStack is the dialog stack (agentic-tui skill §9) and
	// the only modal mechanism besides the inline pendingX fields.
	// Model picker, theme picker, session picker and the detail
	// overlays all ride this stack; permission / elicit /
	// sideAnswer still use their inline pendingX fields because
	// the channel lifecycle hasn't been decoupled yet.
	overlayStack Overlay

	// caps holds the env-sniffed terminal capability bag
	// (agentic-tui skill §18). Renderers consult this to gate
	// hyperlinks, clipboard sequences, etc. Detected once at
	// NewModel; hosts can override post-construction.
	caps terminalCapabilities

	// cwd is the working directory as the status header displays it,
	// already shortened against the home directory. Resolved once in
	// NewModel (issue #223) because View is not allowed to do I/O and
	// the header would otherwise run getcwd(2) twice per layout pass.
	// A process that chdir's mid-session would stop being tracked;
	// no host does that today, and the fallback if one turns up is to
	// refresh this on a turn boundary the way hostSnapshot is
	// refreshed — which is a one-field write, because everything that
	// reads it already goes through displayCwd and keys on it.
	cwd string

	// spinnerCache holds the pre-rendered Braille frame strings
	// for the thinking spinner (agentic-tui skill §7). Rebuilt
	// on theme change (primary / secondary color update).
	spinnerCache *spinnerFrameCache

	// pendingForm is an embedded huh.Form (agentic-tui skill §12).
	// When non-nil, Update routes every tea.Msg into the form
	// first, intercepting all keystrokes; render shows it as a
	// centered modal. Today only /pricing set populates it; a
	// future PR migrates elicit forms here too once the
	// channel-based dispatch is wrapped.
	//
	// Typed as *huh.Form (not tea.Model) because huh's Update
	// returns its own compat.Model interface — not Bubble Tea
	// v2's tea.Model — and View returns a string, not tea.View.
	pendingForm *huh.Form

	// newlineHint is the keystroke we display in the footer for
	// "insert newline." Seeded from the detected terminal program
	// (VS Code → alt+enter, kitty/wezterm/iTerm → shift+enter,
	// everything else → ctrl+j) and overwritten the first time
	// the operator uses one of the three accepted combos. Stops
	// hints from lying when the user's terminal can't deliver the
	// suggested key.
	newlineHint string

	// activeToolID is the Message.ID of the in-flight tool call:
	// the most recent RoleTool message that hasn't been followed
	// by any assistant text or another tool. 0 = no active tool.
	// Renderer uses it to swap the tool glyph (▶ active vs › done)
	// and brighten the row so the operator's eye lands on "what
	// the model is doing RIGHT NOW" instead of scanning back.
	activeToolID uint64

	// sessionGen is bumped by applySwitchTarget on every mid-run
	// Agent swap (issue #48 / #53). Async goroutines started under
	// the previous gen (startAgentTurn, startLiveStream, emitEvent)
	// stamp every msg they emit with the gen they captured; Update
	// guards its handlers with `if msg.gen != m.sessionGen { drop }`
	// so a terminal msg from the OUTGOING session can't leak an
	// "(interrupted)" row into the incoming session's transcript,
	// and a straggler stream chunk can't bleed into the new
	// session's assistant buffer during the race window.
	sessionGen uint64

	// paletteSeq identifies the CURRENT palette instance (issue
	// #114). Palette items are now fetched off the Update goroutine
	// — the @ directory walk and the host's SlashCommands() — and a
	// palette opens and closes many times within one sessionGen, so
	// sessionGen alone can't tell whether an arriving fileItemsMsg /
	// slashCommandsMsg still belongs to what is on screen. Bumped on
	// every open; the reply is dropped unless it matches
	// m.palette.seq.
	paletteSeq uint64

	// slashSeq identifies the CURRENT slash dispatch (issue #137).
	// Bumped by dispatchSlash on every submitted /cmd, built-in or
	// host. The two-stage paths that landed with #137 — `/switch <id>`
	// enumerate-then-switch, and the host match-then-invoke — stamp
	// their replies with it and Update drops any whose stamp has been
	// overtaken.
	//
	// sessionGen is too coarse for this: it turns over only on a
	// session switch, while slash commands are typed back to back
	// inside one session. The reply that most needs the guard is the
	// NEGATIVE one — "unknown command /foo" used to be written in the
	// same frame as the dispatch, and now that the name match is a
	// host call it can land after the output of whatever the operator
	// typed next.
	slashSeq uint64
}

// NewModel constructs a Model from Options. SeedHistory entries are
// appended in order before the first render.
func NewModel(opts Options) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message and hit Enter. /help for commands."
	if opts.Branding.InputPlaceholder != "" {
		ta.Placeholder = opts.Branding.InputPlaceholder
	}
	// Prompt rail: a thin vertical bar to the left of every
	// input row, colored by the active theme's BorderActive
	// (see textareaStyles). Gives the input a persistent,
	// theme-aware focus marker — bubbles v2's textarea draws no
	// border of its own, so this rail is the operator's visual
	// anchor for "this is where input goes." Themes that have a
	// signature glyph (e.g. GKE's ⎈ helm) override via
	// Theme.PromptGlyph; refreshTheme applies the override on
	// theme swap.
	ta.Prompt = defaultPromptGlyph
	ta.ShowLineNumbers = false
	ta.SetHeight(textareaMinHeight)
	// Drive the TERMINAL's cursor rather than a painted one
	// (issue #105). bubbles defaults to a "virtual cursor" — a
	// reverse-video block drawn into the frame — which leaves the
	// real cursor wherever the last write left it: IME candidate
	// windows open in the wrong place, the operator's configured
	// cursor shape / blink is ignored, and assistive tech has
	// nothing to follow. With it off, textarea.Cursor() reports a
	// position and View forwards it as tea.View.Cursor (cursor.go).
	ta.SetVirtualCursor(false)
	// Focus the textarea so KeyPressMsg events route to it — and so
	// textarea.Cursor() reports a position at all; bubbles returns
	// nil for a blurred widget. Focus() returns a blink Cmd we drop:
	// with the virtual cursor off there is no painted block to
	// animate, the terminal blinks its own caret.
	_ = ta.Focus()

	// Start the textarea with a transparent CursorLine style —
	// bubbles v2 textarea.New() applies DefaultDarkStyles which
	// paints the cursor line solid black; that's invisible on
	// dark terminals and a screaming black block on light ones.
	// We don't yet know dark/light (BackgroundColorMsg comes
	// post-Init), so pick the "safer" no-tint default and let
	// the BackgroundColorMsg handler swap in the right variant.
	// Use defaultTheme(true) here; refreshTheme rebuilds with the
	// resolved theme once BackgroundColorMsg lands.
	ta.SetStyles(textareaStyles(true, defaultTheme(true)))

	// Seed styles.Dark from ForceTheme up front when the host has
	// chosen explicitly; otherwise start in dark mode (the most
	// common terminal default) and let BackgroundColorMsg overwrite
	// when the OSC-11 response arrives. The ForceTheme path skips
	// that overwrite — see the BackgroundColorMsg handler.
	initialDark := true
	switch opts.ForceTheme {
	case ThemeLight:
		initialDark = false
	case ThemeDark:
		initialDark = true
	}
	// Sniffed once and read three times below — the bag itself, the
	// newline hint, and the banner's starting frame. It reads the
	// environment, so calling it per use is both wasted work and a way
	// for the three readings to disagree.
	caps := detectCapabilities()
	// The listener lifetime starts here rather than in Init so that
	// it is non-nil for every Model a host can get its hands on —
	// Init runs on the Bubble Tea loop's goroutine, after
	// tea.NewProgram has already copied the Model, and a context
	// installed there would never reach the copy the program holds.
	// See the lifeCtx field comment for what cancels it.
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	m := Model{
		opts:            opts,
		lifeCtx:         lifeCtx,
		lifeCancel:      lifeCancel,
		styles:          NewStyles(initialDark, opts.Branding), // overwritten on BackgroundColorMsg unless ForceTheme is set
		input:           newComposer(ta),
		follow:          true, // start pinned to the tail
		statusLayout:    opts.StatusLayout,
		permMode:        opts.PermissionMode.Initial,
		themeName:       opts.InitialThemeName,
		eventCh:         make(chan tea.Msg, 32),
		seenToolIDs:     make(map[string]bool),
		subagentTails:   make(map[string]*subagentTail),
		subagentNotTail: make(map[string]bool),
		historyCursor:   -1,
		startedAt:       time.Now(),
		now:             time.Now,
		listCache:       newListCache(),
		statusCache:     &statusCache{},
		modalScroll:     &scrollState{},
		caps:            caps,
		cwd:             resolveDisplayCwd(),
		newlineHint:     defaultNewlineHint(caps.TermProgram),
		bannerFrame:     initialBannerFrame(caps, opts.Branding),
	}
	// LiveAgent precedence (issue #22): when the host satisfies
	// LiveAgent, the per-turn Run path is bypassed entirely and a
	// single drain goroutine pumps Events(ctx) through m.eventCh.
	// Init() spawns the goroutine after NewModel returns so the
	// channel is ready and Update can dispatch events as soon as
	// they arrive.
	if _, ok := opts.Agent.(LiveAgent); ok {
		m.liveMode = true
	}
	for _, msg := range opts.SeedHistory {
		m.history.Append(msg)
	}
	return m
}

// thinkingPhrases / workingPhrases return the rotated verb pools
// (R-CHAT-3). Falls back to internal/tui's pool when Options are
// not set — "Thinking..." anchors the first tick so the affordance
// is unambiguous before the rotator wanders into the AI / sci-fi /
// CS jokes.
//
// The trailing "..." on these entries is not what reaches the screen:
// renderSpinnerLine normalizes it away and appends GlyphTruncate, so
// the rendered line is "Thinking…" (issue #141). The entries stay
// punctuated because they read as prose here, and because a
// host-supplied pool gets the same normalization either way.
func (m Model) thinkingPhrases() []string {
	if len(m.opts.ThinkingPhrases) > 0 {
		return m.opts.ThinkingPhrases
	}
	return []string{
		"Thinking...",
		"Consulting the latent space...",
		"Sampling from the distribution...",
		"Reticulating splines...",
		"Computing the answer to the ultimate question...",
		"Spinning up the attention heads...",
		"Asking Stack Overflow nicely...",
		"Untangling pointer chains...",
		"Bargaining with the loss function...",
		"Compiling a thoughtful response...",
		"Defragmenting cache lines...",
		"Negotiating with the Vogons...",
		"Brewing a fresh stack frame...",
		"Plotting a hyperspace course...",
		"Resolving promises...",
		"Eval'ing your prompt...",
	}
}

func (m Model) workingPhrases() []string {
	if len(m.opts.WorkingPhrases) > 0 {
		return m.opts.WorkingPhrases
	}
	return []string{
		"Working...",
		"Running tools...",
		"Reading the code...",
		"Searching the haystack...",
		"Editing in place...",
		"Tracing call sites...",
		"Cross-referencing...",
	}
}

// ensureMarkdown returns the cached markdown renderer, rebuilding it
// when dark/light or width has changed since the last call. A rebuild
// invalidates the incremental stream cache too, since cached prefix
// renders are width-pinned (re-rendering them with the new width is
// what makes resize keep the in-progress text readable).
func (m *Model) ensureMarkdown() *markdownRenderer {
	width := m.viewport.Width()
	if width <= 0 {
		width = 80
	}
	if m.markdown == nil || m.markdown.dark != m.styles.Dark || m.markdown.width != width {
		m.markdown = newMarkdownRenderer(m.styles.Theme, m.styles.Dark, width)
		m.inProgressStablePrefix = ""
		m.inProgressStableRender = ""
	}
	return m.markdown
}

// ensureModalMarkdown returns a markdown renderer word-wrapped to a
// MODAL body width rather than the chat column's.
//
// The /btw modal used to render its answer through ensureMarkdown,
// i.e. wrapped for the viewport — 20-plus columns wider than the
// modal frame it lands in. Every long line then got re-wrapped by
// the frame, which both doubled the modal's height and made the body
// impossible to measure for scrolling (source lines != screen rows).
// Cached separately from m.markdown so a modal never evicts the
// chat's renderer.
func (m *Model) ensureModalMarkdown(width int) *markdownRenderer {
	if width <= 0 {
		width = 80
	}
	if m.modalMarkdown == nil || m.modalMarkdown.dark != m.styles.Dark || m.modalMarkdown.width != width {
		m.modalMarkdown = newMarkdownRenderer(m.styles.Theme, m.styles.Dark, width)
	}
	return m.modalMarkdown
}

// permissionModeWired reports whether the host configured the chip.
func (m Model) permissionModeWired() bool {
	return m.opts.PermissionMode.Set != nil
}

// wordmark returns the brand identity string for the status surface.
func (m Model) wordmark() string {
	if m.opts.Branding.Wordmark != "" {
		return m.opts.Branding.Wordmark
	}
	return "core-tui"
}

// defaultNewlineHint picks the most-likely-to-work newline
// keystroke for the given terminal program, used to seed the
// footer hint before the operator has actually pressed one.
//
//   - VS Code integrated terminal  → alt+enter (terminal-setup
//     binds Shift+Enter → \x1b\r, which bubbletea normalizes
//     to alt+enter)
//   - kitty / wezterm / iTerm2     → shift+enter (likely have the
//     keyboard-enhancement protocol enabled, so true shift+enter
//     reaches the app)
//   - everything else              → ctrl+j (ASCII LF, the most
//     portable; works unless something steals the binding)
func defaultNewlineHint(termProgram string) string {
	switch termProgram {
	case "vscode":
		return "alt+enter"
	case "kitty", "wezterm", "iterm.app", "iterm2", "ghostty":
		return "shift+enter"
	default:
		return "ctrl+j"
	}
}

// displayCwd returns the operator's cwd, shortened with `~/` when it
// sits under the home directory, for the status surface. Empty when
// the directory could not be resolved (no point displaying a stale or
// wrong path).
//
// This is a field read, not a lookup. It used to call os.Getwd and
// os.UserHomeDir on every call, and the status header calls it twice
// per layout pass at whatever rate the spinner ticks — I/O on a path
// documented as doing none, which is the same contract hostSnapshot
// exists to keep for the host (issue #223). The value is resolved once
// in NewModel by resolveDisplayCwd.
//
// It has to be a field resolved at construction rather than one filled
// lazily on first use: Model is copied by value throughout the package
// and View has a value receiver, so anything the render path writes to
// its receiver lands in a copy that is discarded on return. A lazily
// filled field would do the syscalls on every frame anyway and simply
// throw the answer away each time.
func (m Model) displayCwd() string {
	return m.cwd
}

// resolveDisplayCwd does the two lookups displayCwd used to do per
// call, once, off the render path. Called from NewModel.
//
// HOME is read here rather than memoized in a package var: a process
// var would be filled by whichever Model was constructed first and
// then be wrong for every later one, and the golden corpus constructs
// models under a synthetic HOME (see pinCwd) precisely so the captured
// frames do not carry the checkout path of whoever ran -update.
func resolveDisplayCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	home, herr := os.UserHomeDir()
	if herr != nil {
		home = ""
	}
	return abbreviateHome(cwd, home)
}

// abbreviateHome shortens dir to `~/…` when it sits under home,
// and returns it verbatim otherwise. Split out from resolveDisplayCwd
// so the abbreviation is testable without moving the process.
//
// The match is textual, not path-aware, which is what the golden
// corpus's synthetic HOME is symlink-resolved to line up with.
func abbreviateHome(dir, home string) string {
	if home == "" || !strings.HasPrefix(dir, home) {
		return dir
	}
	return "~" + dir[len(home):]
}

// displayProvider extracts the provider tag from the host. Push-
// mode (issue #40) takes precedence — when a status-update event
// has populated m.pushedProvider, that wins over the cached
// StatusReporter read. Otherwise falls back to the host snapshot
// (see host_snapshot.go), or empty when neither path has surfaced a
// provider. Reads only cached state so it's safe from View().
func (m Model) displayProvider() string {
	if m.pushedProvider != "" {
		return m.pushedProvider
	}
	return m.hostSnap.provider
}

// refreshTheme re-resolves Styles (picking up the active provider
// when AutoProviderTheme is on), invalidates BOTH Glamour renderers
// + the list cache so the next render uses the new palette, and
// rebuilds the textarea styles for the current dark/light mode.
// Called after any event that could change which theme applies:
// /model swap, dark/light flip, explicit theme reset.
//
// Both renderers have to be dropped, not just the chat one. The
// modal renderer was left behind here for as long as Glamour styles
// were theme-independent, which made the omission invisible; now
// that markdown is built from Theme tokens, a /theme swap with the
// /btw modal open would otherwise keep painting the modal body from
// the previous palette until its width changed.
func (m *Model) refreshTheme() {
	m.styles = m.resolveStyles(m.styles.Dark)
	m.markdown = nil
	m.modalMarkdown = nil
	if m.listCache != nil {
		m.listCache.reset(m.viewport.Width())
	}
	m.input.SetStyles(textareaStyles(m.styles.Dark, m.styles.Theme))
	// Apply the theme's prompt glyph (or fall back to the house
	// default). Themes that don't customize the glyph leave
	// Theme.PromptGlyph empty.
	if glyph := m.styles.Theme.PromptGlyph; glyph != "" {
		m.input.SetPrompt(glyph)
	} else {
		m.input.SetPrompt(defaultPromptGlyph)
	}
}

// applyNamedTheme switches the active theme to the named entry
// and re-renders. Called by the /theme picker dialog on cursor
// (live preview), restore-on-cancel (esc), and commit (enter),
// and by the `/theme <name>` slash form. Unknown names fall
// through to defaultTheme via ThemeByName so a typo is safe.
func (m *Model) applyNamedTheme(name string) {
	m.themeName = name
	m.refreshTheme()
	m.refreshViewport()
}

// resolveStyles builds the Styles bundle for the current dark/
// light mode. Precedence is:
//
//  1. m.themeName (set by /theme picker or Options.InitialThemeName)
//     — wins over everything else; the operator's explicit pick.
//  2. Options.AutoProviderTheme — StatusReporter's Provider tag
//     picks the per-provider theme (Anthropic clay / Gemini blue /
//     OpenAI green).
//  3. defaultTheme — brand stays consistent regardless of model.
//
// Branding overrides still apply on top of whichever theme was
// picked. Called from BackgroundColorMsg (first-paint dark/light
// detect) and any time the active provider could have changed
// (post-/model swap) or the operator switched themes.
func (m Model) resolveStyles(dark bool) Styles {
	var theme Theme
	switch {
	case m.themeName != "":
		theme = ThemeByName(m.themeName, dark)
	case m.opts.AutoProviderTheme:
		theme = themeForProvider(m.displayProvider(), dark)
	default:
		theme = defaultTheme(dark)
	}
	if m.opts.Branding.AccentColor != "" {
		c := lipgloss.Color(m.opts.Branding.AccentColor)
		theme.Primary = c
		theme.Accent = c
		theme.BorderActive = c
	}
	if m.opts.Branding.SecondaryColor != "" {
		theme.Secondary = lipgloss.Color(m.opts.Branding.SecondaryColor)
	}
	return NewStylesWithTheme(dark, theme)
}

// displayModelName picks the best model identifier to surface on the
// status header/sidebar. Order:
//
//  1. hostSnap.modelName  — the cached StatusReporter read of the live
//     model (preferred; refreshed off-loop, updates on /model swap).
//  2. m.currentModel      — set per-turn from streamed Event.Model when
//     the host populates it; empty before any turn.
//  3. "(model not set)"   — placeholder so the chip isn't blank when
//     neither source has fired yet.
//
// Reads only cached state so it never calls the host from View().
func (m Model) displayModelName() string {
	if m.hostSnap.modelName != "" {
		return m.hostSnap.modelName
	}
	if m.currentModel != "" {
		return m.currentModel
	}
	return "(model not set)"
}

// usageSummaryOneLine returns the compact "Nk in · Nk out · $X · used/size"
// spend block for the status header. Empty when no UsageTracker is
// wired (the header just drops the trailing segment rather than
// rendering placeholder zeros that look like real data).
func (m Model) usageSummaryOneLine() string {
	if m.opts.UsageTracker == nil || !m.hostSnap.hasUsage {
		return ""
	}
	t := m.hostSnap.totals
	cost := m.hostSnap.cost
	used := m.hostSnap.winUsed
	size := m.hostSnap.winSize
	sep := " " + GlyphSeparator + " "
	out := formatKTokens(t.InputTokens) + " in" + sep + formatKTokens(t.OutputTokens) + " out" + sep + fmt.Sprintf("$%.4f", cost)
	if size > 0 {
		out += sep + m.contextFillStyle(used, size).Render(
			formatKTokens(used)+" / "+formatKTokens(size),
		)
	}
	return out
}

// usageSummaryStacked returns the sidebar's two-line spend block.
// First line: "Nk in · Nk out"; second line: "$X · used / size" (or
// just "$X" when context window is unknown). Empty pair when no
// UsageTracker is wired.
func (m Model) usageSummaryStacked() (string, string) {
	if m.opts.UsageTracker == nil || !m.hostSnap.hasUsage {
		return "", ""
	}
	t := m.hostSnap.totals
	cost := m.hostSnap.cost
	used := m.hostSnap.winUsed
	size := m.hostSnap.winSize
	sep := " " + GlyphSeparator + " "
	line1 := formatKTokens(t.InputTokens) + " in" + sep + formatKTokens(t.OutputTokens) + " out"
	line2 := fmt.Sprintf("$%.4f", cost)
	if size > 0 {
		line2 += sep + m.contextFillStyle(used, size).Render(
			formatKTokens(used)+" / "+formatKTokens(size),
		)
	}
	return line1, line2
}

// contextFillStyle picks a fg style for the "<used> / <size>"
// segment based on a 3-tier color ramp: Theme.Success below 60%,
// Theme.Warning 60-85%, Theme.Error above 85% (per agentic-tui
// skill §17.C). Lets the operator see overflow risk before it
// bites. The tiers read from the active Theme rather than fixed
// hex so the ramp keeps its contrast on light themes too.
func (m Model) contextFillStyle(used, size int) lipgloss.Style {
	if size <= 0 {
		return m.styles.Muted
	}
	theme := m.styles.Theme
	pct := (used * 100) / size
	switch {
	case pct >= 85:
		return lipgloss.NewStyle().Foreground(theme.Error).Bold(true)
	case pct >= 60:
		return lipgloss.NewStyle().Foreground(theme.Warning)
	default:
		return lipgloss.NewStyle().Foreground(theme.Success)
	}
}

// formatKTokens renders an integer token count in compact human form
// — "1.5K" for 1500, "23K" for 23000, plain "850" for sub-1K. Mirrors
// the format the per-turn footer uses (R-USE-1).
func formatKTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%dK", n/1000)
}

// subagentSummary renders the sidebar's subagent rows from the host
// snapshot's SubagentReporter read. Returns ("none") when the capability
// is unwired or the list is empty so the section reads consistently.
//
// Reads hostSnap rather than calling Subagents() directly: this runs
// from renderSidebar, i.e. from View(), which host_snapshot.go
// guarantees never blocks on a host method. The roster refreshes on
// the same hostSnapshotInterval tick as the header figures.
func (m Model) subagentSummary() []string {
	if _, ok := m.opts.Agent.(SubagentReporter); !ok {
		return []string{"none (no SubagentReporter)"}
	}
	if !m.hostSnap.valid {
		// Wired, but the first off-loop pull hasn't landed yet.
		return []string{"…"}
	}
	subs := m.hostSnap.subagents
	if len(subs) == 0 {
		return []string{"none"}
	}
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		row := s.Name + " [" + s.Status + "]"
		out = append(out, row)
	}
	return out
}
