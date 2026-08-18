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

// The permission prompt, as a question (issue #164, stage 3).
//
// The last modal to move, and it went last on purpose
// (docs/design-question-dialogs.md §14 Q4): it is the highest-stakes
// surface in the product and it already worked, so it waited until the
// seam had been proved by four other migrations and until the corpus
// had a byte-level capture of both its layouts to be held against
// (golden_permission_test.go).
//
// What the move buys, beyond the two Model fields and the fifty-line
// arm of handleKey it deletes. R-PERM-2's six decision keys were a
// switch whose arms each named a PermissionDecision, duplicated once
// as a legend builder that had to list the same keys in the same order
// and once more as isPermissionDecisionKey, which decided which of
// them the grace window held. Three lists, agreeing by inspection. The
// verb key's conditional presence was spelled out in all three. They
// are one []permissionOption here: the switch is a lookup, the legend
// is a Join, and the grace window's answer is "is this stroke one of
// my options".
//
// One extension comes with it, narrow and with exactly one
// implementor, which is the bar §12 set: inlineQuestion, because this
// modal has a second renderer. The DEFAULT layout is not a modal at all
// — it is a gutter block in the transcript flow, drawn where the
// spinner would be, so the operator reads what is being approved next
// to the text that asked for it. A question that draws itself elsewhere
// still wants everything else the seam gives it (key routing, the grace
// window, the resolver, teardown on a session switch), so the layout is
// a property of the question rather than a second place for the prompt
// to live.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const permissionDialogID = "permission"

// permissionModalWidth is the centered layout's preferred total width,
// before the terminal-relative clamp in Width.
const permissionModalWidth = 80

// permissionOption is one decision the operator can take, and it is
// the single list the key switch, the legend and the grace window all
// read.
//
// It carries a PermissionDecision rather than an option ID, which is
// the same argument the decision answer variant is declared with
// (question.go): the mapping from a string back to a decision needs a
// fallback for the unrecognised case, the only safe fallback is
// DecisionDeny, and a silent security downgrade is not a thing to put
// inside the component whose job is to be right about permissions.
type permissionOption struct {
	key   string
	label string
	value PermissionDecision
}

// permissionQuestion is R-PERM-1 / R-PERM-2: one tool call, six
// decision keys, and a payload the operator has to be able to read
// before answering.
type permissionQuestion struct {
	req    PermissionRequest
	opts   []permissionOption
	inline bool
	sc     scrollState

	// md caches the Glamour renderer the DetailDiff path needs, keyed
	// on exactly what Model.ensureMarkdown keys its own on. A question
	// cannot reach the app's cached renderer — Body is handed a Styles
	// and a width and nothing else — and building one per frame is not
	// free, so the question keeps its own for as long as it is open.
	md      *markdownRenderer
	mdWidth int
	mdDark  bool
}

// newPermissionQuestion builds the prompt for req under layout.
func newPermissionQuestion(req PermissionRequest, layout PermissionLayout) *permissionQuestion {
	opts := []permissionOption{
		{"y", "allow once", DecisionAllowOnce},
		{"n", "deny", DecisionDeny},
		{"s", "allow session", DecisionAllowSession},
	}
	// The verb-scoped grant exists only when the payload had a verb to
	// scope it to. Conditional in ONE place now: the legend and the key
	// switch both read this slice, so a prompt with no verb cannot
	// advertise a key that does nothing or answer one it never showed.
	if req.Verb != "" {
		opts = append(opts, permissionOption{"v", "allow verb", DecisionAllowSessionVerb})
	}
	opts = append(opts,
		permissionOption{"t", "allow tool", DecisionAllowSessionTool},
		permissionOption{"a", "allow always", DecisionAllowAlways},
	)
	return &permissionQuestion{
		req:    req,
		opts:   opts,
		inline: layout != PermissionOverlay,
	}
}

// permissionResolver turns the operator's answer into the dispatch
// back to the blocked AskApproval call, and re-arms the listener.
//
// It closes over the question because the effects need the request —
// the AlwaysAllow callback is handed the whole PermissionRequest so
// the host knows what scope to persist, and the transcript echo names
// the tool. Closing over the question is safe in a way closing over a
// *Model is not: the question is heap-allocated and outlives every
// per-Update copy of the model, which is the reason resolver takes its
// *Model as a parameter in the first place.
func permissionResolver(q *permissionQuestion) resolver {
	return func(a answer, m *Model) tea.Cmd {
		switch a := a.(type) {
		case decision:
			m.dispatchPermission(a.Value, q.req)
			return m.promptListener()
		case dismissed:
			// Every way this modal dies is a deny. Nothing may proceed
			// on the operator's behalf when the operator did not say
			// yes, and a host left waiting on a channel with no writer
			// is worse than a refusal it can act on.
			m.dispatchPermission(DecisionDeny, q.req)
			switch a.Reason {
			case dismissEscape, dismissUnrenderable:
				return m.promptListener()
			case dismissSuperseded, dismissShutdown:
				// A session switch already re-arms the listener for the
				// new session (applySwitchTarget step 8), and a second
				// armed listener is a second consumer of one channel:
				// prompts would land on alternate turns with nothing
				// anywhere to explain the ones that vanished.
				return nil
			}
			return nil
		case declined, chosen, selected, text, fields:
			// Shapes this question never produces. Listed rather than
			// defaulted because gochecksumtype does not accept a
			// default arm as covering a variant, which is what stops
			// an eighth answer from landing here silently.
			return nil
		}
		return nil
	}
}

func (q *permissionQuestion) ID() string { return permissionDialogID }

func (q *permissionQuestion) Title() string {
	return "Permission required: " + q.req.ToolName
}

// legend is the key hint WITHOUT the scroll prefix — what Body
// measures its chrome against, and what the inline block and the app
// footer both show.
func (q *permissionQuestion) legend() string {
	keys := make([]string, 0, len(q.opts)+1)
	for _, o := range q.opts {
		keys = append(keys, o.key+" "+o.label)
	}
	// Esc is not an option row: it produces a dismissal rather than a
	// decision, and the resolver is what turns that into a deny. It is
	// in the legend because the operator needs to know it works.
	keys = append(keys, "esc deny")
	return keyLegend(keys...)
}

func (q *permissionQuestion) Footer() string {
	if q.sc.overflows() {
		return scrollHint(true) + " " + GlyphSeparator + " " + q.legend()
	}
	return q.legend()
}

func (q *permissionQuestion) Width(avail int) int {
	width := permissionModalWidth
	if avail > 0 && width > avail-4 {
		width = avail - 4
	}
	if width < 30 {
		width = 30
	}
	return width
}

// Commits reports which strokes the grace window holds: every decision
// key, and only those. Esc is exempt at the seam (askedQuestion.held),
// which is what keeps the fail-safe direction live from the first
// frame.
func (q *permissionQuestion) Commits(msg tea.KeyPressMsg) bool {
	return q.option(msg.String()) != nil
}

// ScrollBy windows the body of the centered layout.
//
// The inline layout never gets here: the block renders inside the chat
// viewport, so the wheel keeps scrolling the chat — that IS the surface
// showing the prompt, and taking the tick would freeze the transcript
// under a block the operator is trying to read past. Overlay.HandleWheel
// bounces it on Inline() before this is reached.
func (q *permissionQuestion) ScrollBy(delta int) { q.sc.by(delta) }

// Inline reports whether this prompt draws itself in the transcript
// flow rather than in the modal frame.
func (q *permissionQuestion) Inline() bool { return q.inline }

func (q *permissionQuestion) option(stroke string) *permissionOption {
	for i := range q.opts {
		if q.opts[i].key == stroke {
			return &q.opts[i]
		}
	}
	return nil
}

func (q *permissionQuestion) Key(msg tea.KeyPressMsg) (answer, tea.Cmd) {
	stroke := msg.String()
	if stroke == "esc" {
		return dismissed{Reason: dismissEscape}, nil
	}
	if o := q.option(stroke); o != nil {
		return decision{Value: o.value}, nil
	}
	// Arrow / page keys window the body — a 200-line diff in the
	// centered overlay was readable only down to whatever fit on
	// screen. The inline layout needs nothing here: it renders in the
	// chat viewport, which already scrolls.
	if !q.inline {
		q.sc.applyStroke(stroke)
	}
	// Everything else is swallowed. An open modal is exclusive, and
	// this is the modal where a stroke leaking through to the composer
	// while a grant is pending would be worst.
	return nil, nil
}

// Body renders the centered layout's content: the provenance rows, the
// payload, windowed to whatever the terminal left after the chrome.
func (q *permissionQuestion) Body(width, termHeight int, st Styles) string {
	inner := modalInnerWidth(width)
	bodyWidth := modalBodyWidth(width)

	var lines []string
	if q.req.Source != "" {
		lines = append(lines, st.Muted.Render("from sub-agent: "+q.req.Source))
	}
	if q.req.Verb != "" {
		lines = append(lines, st.Muted.Render("verb: "+q.req.Verb))
	}
	if q.req.Detail != "" {
		lines = append(lines, q.detail(bodyWidth, st))
	}

	// Re-split rather than window `lines`: the payload is one entry
	// carrying many rows, and a window measured in entries would call a
	// forty-row diff one row and never scroll.
	bodyLines := strings.Split(strings.Join(lines, "\n"), "\n")
	// The legend wraps on narrow terminals, so measure it rather than
	// assuming one row.
	chrome := modalChromeRows - 1 + wrappedRows(q.legend(), inner)
	view := modalBodyHeight(termHeight, chrome)
	q.sc.measure(len(bodyLines), view)
	return strings.Join(scrollView(st, bodyLines, bodyWidth, view, q.sc.offset), "\n")
}

// InlineBody renders the default layout: the same content as Body,
// without the centered frame, behind a left-rule gutter that sets it
// apart from the chat around it while leaving it in the natural scroll
// position under the tool call that triggered it.
//
// width is the chat column, not a modal width — the block is part of
// the transcript flow and there is no frame to fit inside.
func (q *permissionQuestion) InlineBody(width int, st Styles) string {
	if width <= 0 {
		width = 80
	}
	const gutter = "│ "
	bodyWidth := width - lipgloss.Width(gutter) - 1
	if bodyWidth < 20 {
		bodyWidth = 20
	}

	var lines []string
	lines = append(lines, st.Accent.Render("⚠ Permission required: "+q.req.ToolName))
	if q.req.Source != "" {
		lines = append(lines, st.Muted.Render("from sub-agent: "+q.req.Source))
	}
	if q.req.Verb != "" {
		lines = append(lines, st.Muted.Render("verb: "+q.req.Verb))
	}
	if q.req.Detail != "" {
		lines = append(lines, "", q.detail(bodyWidth, st))
	}
	lines = append(lines, "", st.Muted.Render(q.legend()))

	// Prefix each line with the gutter, in accent so the block reads as
	// a focused affordance rather than as a quiet quote.
	rule := st.Accent.Render("│ ")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		// Wrap each source line at bodyWidth so long shell commands
		// fold cleanly under the gutter.
		wrapped := strings.Split(wordWrap(line, bodyWidth), "\n")
		for j, wl := range wrapped {
			if j > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(rule)
			b.WriteString(wl)
		}
	}
	return b.String()
}

// detail renders the payload styled per DetailKind, at width.
//
// Shell and JSON go through plain bordered blocks: Glamour adds
// document margins and code-fence frames that do not compose inside a
// modal frame — the closing bar ends up indented and pushed off the
// right. A diff still rides Glamour, because unified-diff syntax
// highlighting is the whole point of the diff fence.
//
// Every kind now renders at the width it was handed, which the diff
// arm did not do before the migration: it reached for the model's
// chat-column renderer, so a diff inside an 80-column modal arrived
// 100 columns wide and took the box with it. That is the same defect
// ensureModalMarkdown was added for on the /btw modal, and the fix is
// the same one.
func (q *permissionQuestion) detail(width int, st Styles) string {
	if q.req.Detail == "" {
		return ""
	}
	switch q.req.DetailKind {
	case DetailDiff:
		return strings.TrimSpace(q.markdown(width, st).renderMarkdown("```diff\n" + q.req.Detail + "\n```"))
	case DetailShell:
		return renderShellDetail(q.req.Detail, width, st)
	case DetailHTTP:
		return renderShellDetail(q.req.Detail, width, st)
	case DetailArgs:
		return renderArgsDetail(q.req.Detail, width, st)
	case DetailPlain:
	}
	// DetailPlain, and any kind outside the declared set, is wrapped
	// verbatim.
	return wordWrap(q.req.Detail, width)
}

// markdown returns a Glamour renderer for width, rebuilding it when
// the width or the light/dark polarity moves under it — the same three
// keys Model.ensureMarkdown compares, for the same reason.
func (q *permissionQuestion) markdown(width int, st Styles) *markdownRenderer {
	if width <= 0 {
		width = 80
	}
	if q.md == nil || q.mdWidth != width || q.mdDark != st.Dark {
		q.md = newMarkdownRenderer(st.Theme, st.Dark, width)
		q.mdWidth, q.mdDark = width, st.Dark
	}
	return q.md
}

// openPermission returns the permission prompt currently on the
// overlay stack, or nil. It replaces the `m.pendingPermission != nil`
// test the renderers, the footer legend and the session switch used to
// spell out.
func (m *Model) openPermission() *permissionQuestion {
	aq := m.overlayStack.asked(permissionDialogID)
	if aq == nil {
		return nil
	}
	q, _ := aq.q.(*permissionQuestion)
	return q
}

var (
	_ question       = (*permissionQuestion)(nil)
	_ gracedQuestion = (*permissionQuestion)(nil)
	_ scrollQuestion = (*permissionQuestion)(nil)
	_ inlineQuestion = (*permissionQuestion)(nil)
)
