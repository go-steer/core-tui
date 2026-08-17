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

// Subagent detail overlay: `/subagents <name>`.
//
// Closes two issues at once. #70 — the roster list truncates
// LastReport to 60 columns and there was nowhere to read the rest;
// the report renders here in full, wrapped, at the top. #71 — a
// subagent's turns never reach the parent's event stream, so the
// only thing an operator could see was "running"; the turn log
// below the report shows what it actually did, and keeps tailing
// while the overlay is open so a running subagent scrolls live.
//
// Modeled on dialog_toolcall.go: same chrome, same scroll keys.
// The difference is that this overlay's content arrives
// asynchronously, so it also owns a loading state, an error state,
// and a poll cursor.

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

const subagentDialogID = "subagent-detail"

// subagentDialogPreferredWidth matches the tool-call overlay so the
// two read as the same surface. Turn lines are transcript-shaped and
// benefit from the room.
const subagentDialogPreferredWidth = 96

// subagentBodyHeight is how many log rows fit at the current terminal
// height. Unlike the tool-call overlay this dialog puts nothing extra
// inside Body, so its chrome is exactly RenderContext's own.
//
// Shared with the tool-call overlay until issue #149; the two have
// different chrome, and the helper they shared knew neither of them.
func subagentBodyHeight(termHeight int) int {
	return modalBodyHeight(termHeight, modalChromeRows)
}

// subagentDialog is one open drill-down. Everything the host sent
// lives here rather than on the Model: the overlay is the only
// consumer, and closing it should drop the accumulated log.
type subagentDialog struct {
	name string

	scroll int
	// pinned keeps the view stuck to the newest turn as the tail
	// appends. Scrolling up releases the pin — an operator reading
	// history shouldn't get yanked to the bottom every second —
	// and End/G takes it back.
	pinned bool

	events    []SubagentEvent
	since     int64
	truncated bool

	loading bool
	err     error
	// available carries the roster names to offer when the host
	// says it has never heard of this one.
	available []string

	info    SubagentInfo
	hasInfo bool

	// lastBody caches the rendered line count so HandleKey can clamp
	// scrolling without re-rendering. Set on every Render.
	lastBody int
}

func newSubagentDialog(name string) *subagentDialog {
	return &subagentDialog{name: name, pinned: true, loading: true}
}

func (d *subagentDialog) ID() string { return subagentDialogID }

func (d *subagentDialog) HandleKey(stroke string, m *Model) DialogAction {
	viewport := subagentBodyHeight(m.height)
	maxScroll := nonNeg(d.lastBody - viewport)
	switch stroke {
	case "esc":
		return DialogAction{Consumed: true, Close: true}
	case "up", "k":
		if d.scroll > 0 {
			d.scroll--
		}
		d.pinned = false
		return DialogAction{Consumed: true}
	case "down", "j":
		if d.scroll < maxScroll {
			d.scroll++
		}
		d.pinned = d.scroll >= maxScroll
		return DialogAction{Consumed: true}
	case "pgup":
		d.scroll = nonNeg(d.scroll - viewport)
		d.pinned = false
		return DialogAction{Consumed: true}
	case "pgdown", "pgdn":
		d.scroll = min(maxScroll, d.scroll+viewport)
		d.pinned = d.scroll >= maxScroll
		return DialogAction{Consumed: true}
	case "home", "g":
		d.scroll = 0
		d.pinned = false
		return DialogAction{Consumed: true}
	case "end", "G":
		d.scroll = maxScroll
		d.pinned = true
		return DialogAction{Consumed: true}
	}
	// Unhandled key — consume so it doesn't leak to the composer
	// behind the modal, but don't close.
	return DialogAction{Consumed: true}
}

// ScrollBy implements ScrollDialog: mouse-wheel ticks move the log,
// and scrolling up off the bottom releases the follow-the-tail pin
// exactly as the arrow keys do.
func (d *subagentDialog) ScrollBy(delta int, m *Model) {
	maxScroll := nonNeg(d.lastBody - subagentBodyHeight(m.height))
	d.scroll = min(nonNeg(d.scroll+delta), maxScroll)
	d.pinned = d.scroll >= maxScroll
}

// apply folds a fetched page into the overlay. Returns false when the
// message is for a different subagent (a stale reply from a previous
// open), which the caller drops.
func (d *subagentDialog) apply(msg subagentEventsMsg) bool {
	if msg.name != d.name {
		return false
	}
	d.loading = false
	if msg.err != nil {
		// A not-found is a distinct, actionable state — show the
		// roster instead of a raw error string.
		if nf, ok := asSubagentNotFound(msg.err); ok {
			d.err, d.available = nil, nf.Available
			if len(d.events) > 0 {
				// It answered before and doesn't now: the host
				// restarted or pruned. Keep what we have rather
				// than blanking a log the operator is reading.
				d.available = nil
			}
			return true
		}
		d.err = msg.err
		return true
	}
	d.err, d.available = nil, nil
	d.events = mergeSubagentEvents(d.events, msg.page.Events)
	if msg.page.NextSince > d.since {
		d.since = msg.page.NextSince
	}
	d.truncated = d.truncated || msg.page.Truncated
	if msg.hasInfo {
		d.info, d.hasInfo = msg.info, true
	}
	return true
}

func (d *subagentDialog) Render(totalWidth int, m *Model) string {
	width := subagentDialogPreferredWidth
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 40 {
		width = 40
	}
	// modalBodyWidth already reserves the scrollbar column and its
	// gutter so the content doesn't reflow the moment the log outgrows
	// the viewport; this overlay keeps two further columns of slack it
	// has always had.
	content := nonNeg(modalBodyWidth(width) - 2)

	bodyLines := d.bodyLines(m.styles, content)
	d.lastBody = len(bodyLines)

	viewport := subagentBodyHeight(m.height)
	maxScroll := nonNeg(len(bodyLines) - viewport)
	if d.pinned {
		d.scroll = maxScroll
	}
	d.scroll = min(nonNeg(d.scroll), maxScroll)

	visible := scrollView(m.styles, bodyLines, content, viewport, d.scroll)

	return RenderContext{
		Title:  "Subagent " + GlyphSeparator + " " + d.name,
		Body:   strings.Join(visible, "\n"),
		Footer: subagentDialogFooter(len(bodyLines), viewport, d.pinned),
		Width:  width,
		Height: m.height,
		Styles: m.styles,
	}.Render()
}

// bodyLines builds the whole scrollable body: status header, the
// untruncated report, then the turn log.
func (d *subagentDialog) bodyLines(styles Styles, width int) []string {
	var out []string
	out = append(out, d.headerLine(styles))

	if len(d.available) > 0 {
		out = append(out, "",
			styles.ErrorText.Render(fmt.Sprintf("No turns recorded for %q in this session.", d.name)),
			styles.Muted.Render("available: "+strings.Join(d.available, ", ")))
		return out
	}
	if d.err != nil {
		out = append(out, "", styles.ErrorText.Render(GlyphWarn+" "+collapseWhitespace(d.err.Error())))
	}

	if report := strings.TrimSpace(d.info.LastReport); report != "" {
		out = append(out, "", sectionRule("report", styles, width))
		out = append(out, strings.Split(wordWrap(report, width), "\n")...)
	}

	out = append(out, "", sectionRule("turns", styles, width))
	if d.truncated {
		// The host dropped the head of the log to satisfy the page
		// limit. Say so — an operator counting turns deserves to
		// know the top isn't the beginning.
		out = append(out, styles.Muted.Render("(older turns truncated by the host)"))
	}
	switch {
	case len(d.events) > 0:
		out = append(out, renderSubagentTurns(d.events, styles, width)...)
	case d.loading:
		out = append(out, styles.Muted.Render("loading…"))
	case d.err != nil:
		// Error already rendered above; don't also claim there are
		// no turns when we simply failed to ask.
	default:
		out = append(out, styles.Muted.Render("(no turns recorded yet)"))
	}
	return out
}

// headerLine is the status banner: state, uptime, and what the tail
// has counted so far.
func (d *subagentDialog) headerLine(styles Styles) string {
	var parts []string
	if d.hasInfo && d.info.Status != "" {
		parts = append(parts, subagentStatusChip(d.info.Status, styles))
	}
	if d.hasInfo && !d.info.StartedAt.IsZero() {
		parts = append(parts, styles.Muted.Render("started "+d.info.StartedAt.Format("15:04:05")))
	}
	if n := len(d.events); n > 0 {
		parts = append(parts, styles.Muted.Render(fmt.Sprintf("%d turn%s", n, plural(n))))
		if t := countSubagentTools(d.events); t > 0 {
			parts = append(parts, styles.Muted.Render(fmt.Sprintf("%d tool%s", t, plural(t))))
		}
	}
	if len(parts) == 0 {
		return styles.Muted.Render("(no roster entry — reading the log directly)")
	}
	return strings.Join(parts, "  "+styles.Muted.Render(GlyphSeparator)+"  ")
}

// subagentStatusChip colors the state word: running reads as active,
// failed as an error, everything else stays muted.
func subagentStatusChip(status string, styles Styles) string {
	switch strings.ToLower(status) {
	case "running":
		return styles.Accent.Render(GlyphToolActive + " running")
	case "failed", "error":
		return styles.ErrorText.Render(GlyphToolFail + " " + status)
	case "done", "completed":
		return styles.Muted.Render(GlyphToolDone + " " + status)
	}
	return styles.Muted.Render(status)
}

// sectionRule renders a `── report ──────` divider.
func sectionRule(label string, styles Styles, width int) string {
	head := GlyphRule + GlyphRule + " " + label + " "
	return styles.Muted.Render(head + strings.Repeat(GlyphRule, nonNeg(width-lipgloss.Width(head))))
}

func subagentDialogFooter(bodyLines, viewport int, pinned bool) string {
	parts := []string{}
	if bodyLines > viewport {
		parts = append(parts, "↑↓ scroll")
		if pinned {
			parts = append(parts, "following")
		} else {
			parts = append(parts, "G follow")
		}
	}
	parts = append(parts, "esc close")
	return keyLegend(parts...)
}
