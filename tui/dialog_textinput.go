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

// Text-input dialog — the "type one value" primitive (issue #56).
// Every Dialog before this one was arrow-nav + Enter; pickers that
// need an operator-supplied string (the session picker's
// "+ Attach to endpoint…" row being the motivating case) had no
// modal to hand off to. huh.Form on Model.pendingForm covers
// multi-field forms but is a separate lane that doesn't ride the
// Overlay stack and doesn't wear the RenderContext chrome.
//
// This is deliberately single-line: a bubbles/v2 textinput inside
// standard dialog chrome, Enter validates + submits, Esc cancels.
// Nesting is the point — a picker Dialog can Open() one of these on
// top of itself and close both from the submit closure.

package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// textInputDialogID is the default Dialog ID when TextInputConfig
// leaves ID empty. Hosts that stack several text inputs (or want to
// Overlay.Close a specific one) supply their own.
const textInputDialogID = "text-input"

// defaultTextInputWidth matches the model picker so a text input
// opened from a picker doesn't visibly jump in size.
const defaultTextInputWidth = 64

// TextInputConfig configures a text-input Dialog. Only Submit is
// strictly required — everything else has a sane default:
//
//	ID     → "text-input"
//	Title  → "Enter a Value"
//	Width  → 64 columns (clamped to the terminal)
//
// The zero value is therefore usable for a throwaway prompt, but a
// Title + Prompt pair is what makes the modal self-explanatory.
type TextInputConfig struct {
	// ID is the Overlay identity (Overlay.Close / HasID). Empty
	// falls back to textInputDialogID.
	ID string

	// Title renders in the dialog's title bar.
	Title string

	// Prompt is the one-line question above the input box, e.g.
	// "Attach to endpoint (URL):". Empty renders no prompt line.
	Prompt string

	// Placeholder is the dim hint shown while the input is empty.
	Placeholder string

	// Initial pre-fills the input (cursor lands at end) — useful
	// for "edit this value" flows.
	Initial string

	// CharLimit caps the typed value. 0 = unlimited.
	CharLimit int

	// Width is the dialog width in columns. 0 = defaultTextInputWidth.
	// Always clamped to the terminal width at render time.
	Width int

	// Validate is called on Enter with the trimmed value. A
	// non-empty return is rendered as an inline error under the
	// input and the dialog STAYS OPEN so the operator can fix the
	// value. Nil = every value is accepted.
	Validate func(value string) string

	// Submit is called on Enter once Validate passes. It receives
	// the trimmed value and the live Model, and returns the
	// DialogAction the Overlay applies — so a submit closure can
	// close this dialog (Close: true), leave it open, emit a Cmd,
	// and/or Open another dialog on the stack. Nil Submit closes
	// the dialog without doing anything.
	Submit func(value string, m *Model) DialogAction

	// Footer overrides the default "enter submit · esc cancel"
	// hint line.
	Footer string
}

// textInputDialog is the Dialog implementation behind
// NewTextInputDialog. It implements KeyMsgDialog so the embedded
// textinput sees real tea.KeyPressMsg values (Key.Text, modifiers,
// synthesized pastes) instead of normalized stroke strings.
type textInputDialog struct {
	cfg   TextInputConfig
	input textinput.Model

	// errMsg holds the last Validate() failure. Rendered under the
	// input and cleared on the next edit so the error tracks the
	// value the operator is actually looking at.
	errMsg string

	// styledDark caches which dark/light variant the input's
	// styles were built for, so Render only rebuilds them on an
	// actual theme flip rather than every frame.
	styledDark  bool
	styledTheme string
	styled      bool
}

// NewTextInputDialog builds a single-line text-entry Dialog ready
// for Overlay.Open. Typical use from inside another Dialog's
// HandleKey — note the submit closure closing BOTH dialogs:
//
//	m.overlayStack.Open(NewTextInputDialog(TextInputConfig{
//	    Title:  "Attach to Endpoint",
//	    Prompt: "Daemon URL:",
//	    Validate: func(v string) string {
//	        if v == "" { return "endpoint is required" }
//	        return ""
//	    },
//	    Submit: func(v string, m *Model) DialogAction {
//	        tgt, err := dialTheThing(v)
//	        if err != nil {
//	            m.history.Append(Message{Role: RoleError, Text: err.Error()})
//	            m.refreshViewport()
//	            return DialogAction{Consumed: true, Close: true}
//	        }
//	        m.overlayStack.Close(sessionPickerDialogID) // close the parent
//	        return DialogAction{Consumed: true, Close: true, Cmd: m.applySwitchTarget(&tgt)}
//	    },
//	}))
func NewTextInputDialog(cfg TextInputConfig) Dialog {
	if cfg.ID == "" {
		cfg.ID = textInputDialogID
	}
	if cfg.Title == "" {
		cfg.Title = "Enter a Value"
	}
	if cfg.Width <= 0 {
		cfg.Width = defaultTextInputWidth
	}

	ti := textinput.New()
	// The dialog draws its own prompt LINE above the box, so the
	// widget's inline prompt would be a second, redundant marker.
	// Keep a thin rail instead — it marks where typing lands.
	ti.Prompt = "▎ "
	ti.Placeholder = cfg.Placeholder
	ti.CharLimit = cfg.CharLimit
	if cfg.Initial != "" {
		ti.SetValue(cfg.Initial)
		ti.CursorEnd()
	}
	// Real cursor, not a painted block (issue #105) — see the same
	// call in NewModel. Also retires the blink plumbing problem
	// noted in syncStyles: a real cursor blinks in the terminal, so
	// nothing has to route cursor.BlinkMsg back into the widget.
	ti.SetVirtualCursor(false)
	_ = ti.Focus()

	return &textInputDialog{cfg: cfg, input: ti}
}

func (d *textInputDialog) ID() string { return d.cfg.ID }

// Value exposes the current (untrimmed) buffer. Handy for tests and
// for submit closures that captured the dialog.
func (d *textInputDialog) Value() string { return d.input.Value() }

// HandleKey satisfies Dialog for callers holding only a normalized
// stroke (Overlay.HandleKey). It synthesizes a KeyPressMsg and
// delegates so both entry points behave identically.
func (d *textInputDialog) HandleKey(stroke string, m *Model) DialogAction {
	return d.HandleKeyMsg(keyMsgFromStroke(stroke), m)
}

// HandleKeyMsg is the real key handler. Enter validates + submits;
// Esc closes without submitting; everything else feeds the input.
func (d *textInputDialog) HandleKeyMsg(msg tea.KeyPressMsg, m *Model) DialogAction {
	switch msg.String() {
	case "esc":
		return DialogAction{Consumed: true, Close: true}
	case "enter":
		value := strings.TrimSpace(d.input.Value())
		if d.cfg.Validate != nil {
			if e := d.cfg.Validate(value); e != "" {
				// Stay open — the operator gets the error inline
				// with their text still in the box.
				d.errMsg = e
				return DialogAction{Consumed: true}
			}
		}
		d.errMsg = ""
		if d.cfg.Submit == nil {
			return DialogAction{Consumed: true, Close: true}
		}
		return d.cfg.Submit(value, m)
	}
	// Any edit invalidates the previous validation failure.
	d.errMsg = ""
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	// Consume unconditionally: an open text input is exclusive, and
	// letting a stray key fall through would type into the chat
	// textarea behind the modal.
	return DialogAction{Consumed: true, Cmd: cmd}
}

func (d *textInputDialog) Render(totalWidth int, m *Model) string {
	width := d.cfg.Width
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 30 {
		width = 30
	}

	d.syncStyles(m.styles)
	// Inner width: dialog width minus RenderContext's 1-col padding
	// on each side, minus the widget's own prompt rail.
	d.input.SetWidth(nonNeg(width - 2 - lipgloss.Width(d.input.Prompt)))

	parts := make([]string, 0, 3)
	if d.cfg.Prompt != "" {
		parts = append(parts, m.styles.Muted.Render(d.cfg.Prompt))
	}
	parts = append(parts, d.input.View())
	if d.errMsg != "" {
		parts = append(parts, m.styles.ErrorText.Render(GlyphWarn+" "+d.errMsg))
	}

	footer := d.cfg.Footer
	if footer == "" {
		footer = "enter submit " + GlyphSeparator + " esc cancel"
	}
	return RenderContext{
		Title:  d.cfg.Title,
		Body:   lipgloss.JoinVertical(lipgloss.Left, parts...),
		Footer: footer,
		Width:  width,
		Styles: m.styles,
	}.Render()
}

// DialogCursor implements cursorDialog: it reports where the caret
// sits inside the input box, relative to the dialog block's own
// top-left cell. Overlay.Cursor adds the origin lipgloss.Place
// composited the dialog at (cursor.go).
//
// The offsets are RenderContext's chrome, which is fixed: one column
// of horizontal padding, and a title line plus a blank row above the
// body. Inside the body the input is the first part unless a prompt
// line was configured, in which case it is the second. The inline
// validation error renders BELOW the input and never moves it.
//
// The column comes from textInputCursor rather than straight from the
// widget: bubbles' own textinput.Cursor() mixes a rune index with a
// cell width and ignores its scroll offset (issue #125, cursor.go).
func (d *textInputDialog) DialogCursor(_ int, _ *Model) *tea.Cursor {
	c := textInputCursor(d.input, d.input.Prompt)
	if c == nil {
		return nil // blurred
	}
	c.X += 1 // RenderContext's Padding(0, 1)
	c.Y += 2 // title line + blank row
	if d.cfg.Prompt != "" {
		c.Y++
	}
	return c
}

// syncStyles rebuilds the textinput palette from the active theme.
// Called per-render (cheap, guarded) because /theme can swap the
// palette while this dialog is open — the theme picker applies on
// cursor move, and a host could stack the two.
func (d *textInputDialog) syncStyles(s Styles) {
	if d.styled && d.styledDark == s.Dark && d.styledTheme == s.Theme.Name {
		return
	}
	d.styled = true
	d.styledDark = s.Dark
	d.styledTheme = s.Theme.Name
	d.input.SetStyles(textInputStyles(s))
}

// textInputStyles maps the active theme onto a bubbles textinput
// palette. Shared with the pickers' filter row (dialog_filter.go) so
// the two typed surfaces cannot drift apart.
func textInputStyles(s Styles) textinput.Styles {
	ts := textinput.DefaultStyles(s.Dark)
	ts.Focused.Prompt = lipgloss.NewStyle().Foreground(s.Theme.BorderActive)
	ts.Blurred.Prompt = lipgloss.NewStyle().Foreground(s.Theme.FgMuted)
	ts.Focused.Text = lipgloss.NewStyle().Foreground(s.Theme.FgBase)
	ts.Focused.Placeholder = lipgloss.NewStyle().Foreground(s.Theme.FgSubtle)
	ts.Blurred.Placeholder = ts.Focused.Placeholder
	ts.Cursor.Color = s.Theme.Primary
	// Blink is on: the TERMINAL owns the caret now (issue #105), so
	// blinking costs no plumbing. It used to be forced off because a
	// blinking VIRTUAL cursor needs cursor.BlinkMsg routed back into
	// the widget and the Dialog contract is keystrokes-only — no msg
	// pipe. That constraint died with the virtual cursor.
	ts.Cursor.Blink = true
	return ts
}

// keyMsgFromStroke rebuilds a tea.KeyPressMsg from the normalized
// stroke string that Dialog.HandleKey deals in. Only the keys a
// single-line input cares about are mapped; anything unrecognized
// comes back as a zero Key, which the textinput ignores.
//
// This exists so the stroke-string and KeyPressMsg entry points
// stay behaviorally identical — see KeyMsgDialog's godoc.
func keyMsgFromStroke(stroke string) tea.KeyPressMsg {
	if named, ok := namedKeyStrokes[stroke]; ok {
		return tea.KeyPressMsg(named)
	}
	// ctrl+<x> / alt+<x> — reconstruct the modifier + base key so
	// the widget's KeyMap (ctrl+u, ctrl+w, ctrl+a, alt+b, …) matches.
	mod := tea.KeyMod(0)
	rest := stroke
	if r, found := strings.CutPrefix(rest, "ctrl+"); found {
		mod, rest = mod|tea.ModCtrl, r
	}
	if r, found := strings.CutPrefix(rest, "alt+"); found {
		mod, rest = mod|tea.ModAlt, r
	}
	if mod != 0 {
		if named, ok := namedKeyStrokes[rest]; ok {
			named.Mod, named.Text = mod, ""
			return tea.KeyPressMsg(named)
		}
		if r := []rune(rest); len(r) == 1 {
			return tea.KeyPressMsg{Code: r[0], Mod: mod}
		}
		return tea.KeyPressMsg{}
	}
	// Printable single grapheme — Text is what gets inserted.
	if r := []rune(stroke); len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: stroke}
	}
	return tea.KeyPressMsg{}
}

// namedKeyStrokes maps the normalized names bubbletea's
// Key.String() produces back to their Key form.
var namedKeyStrokes = map[string]tea.Key{
	"space":     {Code: tea.KeySpace, Text: " "},
	"enter":     {Code: tea.KeyEnter},
	"esc":       {Code: tea.KeyEscape},
	"tab":       {Code: tea.KeyTab},
	"backspace": {Code: tea.KeyBackspace},
	"delete":    {Code: tea.KeyDelete},
	"left":      {Code: tea.KeyLeft},
	"right":     {Code: tea.KeyRight},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"home":      {Code: tea.KeyHome},
	"end":       {Code: tea.KeyEnd},
}

// pasteKeyMsg wraps bracketed-paste content in a KeyPressMsg so a
// KeyMsgDialog's input widget can insert it. bubbletea delivers a
// paste as tea.PasteMsg, which never reaches handleKey — without
// this the pasted text would land in the chat textarea BEHIND the
// modal, which is the single most likely thing an operator does
// with an "Attach to endpoint (URL)" prompt.
//
// Key.Text carries the full pasted run (the widget inserts every
// rune of it); Code is set to the first rune so String() reports a
// plain printable key and can't collide with a KeyMap binding.
func pasteKeyMsg(content string) (tea.KeyPressMsg, bool) {
	r := []rune(content)
	if len(r) == 0 {
		return tea.KeyPressMsg{}, false
	}
	return tea.KeyPressMsg{Code: r[0], Text: content}, true
}
