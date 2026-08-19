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
// Every dialog before this one was arrow-nav + Enter; pickers that
// need an operator-supplied string (the session picker's
// "+ Attach to endpoint…" row being the motivating case) had no
// modal to hand off to. huh.Form on model.pendingForm covers
// multi-field forms but is a separate lane that doesn't ride the
// overlay stack and doesn't wear the renderContext chrome.
//
// This is deliberately single-line: a bubbles/v2 textinput inside
// standard dialog chrome, Enter validates + submits, Esc cancels.
// Nesting is the point — a picker dialog can Open() one of these on
// top of itself and close both from the submit closure.

package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// textInputDialogID is the default dialog ID when textInputConfig
// leaves ID empty. Callers that stack several text inputs (or want to
// overlay.close a specific one) supply their own.
const textInputDialogID = "text-input"

// defaultTextInputWidth matches the model picker so a text input
// opened from a picker doesn't visibly jump in size.
const defaultTextInputWidth = 64

// textInputConfig configures a text-input dialog. Only Submit is
// strictly required — everything else has a sane default:
//
//	ID     → "text-input"
//	Title  → "Enter a Value"
//	Width  → 64 columns (clamped to the terminal)
//
// The zero value is therefore usable for a throwaway prompt, but a
// Title + Prompt pair is what makes the modal self-explanatory.
//
// Unexported in #254, with the NewTextInputDialog constructor that
// took it. Both were exported for a host that never had a way to reach
// them: opening one needs an overlay, and the only overlay in
// existence is an unexported field of model. Submit's signature is
// unchanged — it still takes the live model and returns a
// dialogAction, because the one caller that wraps this primitive needs
// exactly that. See the field's own comment.
type textInputConfig struct {
	// ID is the overlay identity (overlay.close / hasID). Empty
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
	// the trimmed value and the live model, and returns the
	// dialogAction the overlay applies — so a submit closure can
	// close this dialog (Close: true), leave it open, emit a Cmd,
	// and/or Open another dialog on the stack. Nil Submit closes
	// the dialog without doing anything.
	//
	// #164 §10.1 planned to narrow this to func(value string) error
	// on the way out of the exported surface, on the assumption that
	// the text input would become a question and its effects would
	// move to a resolver. It did not — §13 keeps this a dialog — and
	// the shape does not fit its one caller: the action row's
	// in-flight commit (dialog_sessioninput.go) has to return a Cmd
	// and has to stay OPEN while the host's dial is out, and an error
	// return can say neither.
	Submit func(value string, m *model) dialogAction

	// Footer overrides the default "enter submit · esc cancel"
	// hint line.
	//
	// It is rendered verbatim. The built-in legends bind each key to
	// its action with U+00A0 so a narrow modal cannot wrap between
	// them (issue #230), and a host footer gets none of that: this
	// side cannot tell which of an arbitrary string's spaces hold a
	// pair together and which merely separate two. A host that wants
	// the same protection puts a literal U+00A0 between each key and
	// its action, and leaves the spaces around the separators
	// ordinary so the legend still wraps between pairs.
	Footer string
}

// textInputDialog is the dialog implementation behind
// newTextInputDialog. It implements keyMsgDialog so the embedded
// textinput sees real tea.KeyPressMsg values (Key.Text, modifiers,
// synthesized pastes) instead of normalized stroke strings.
type textInputDialog struct {
	cfg   textInputConfig
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

// newTextInputDialog builds a single-line text-entry dialog ready
// for overlay.open. Typical use from inside another dialog's
// HandleKey — note the submit closure closing BOTH dialogs:
//
//	m.overlayStack.Open(newTextInputDialog(textInputConfig{
//	    Title:  "Attach to Endpoint",
//	    Prompt: "Daemon URL:",
//	    Validate: func(v string) string {
//	        if v == "" { return "endpoint is required" }
//	        return ""
//	    },
//	    Submit: func(v string, m *model) dialogAction {
//	        tgt, err := dialTheThing(v)
//	        if err != nil {
//	            m.history.Append(Message{Role: RoleError, Text: err.Error()})
//	            m.refreshViewport()
//	            return dialogAction{Consumed: true, Close: true}
//	        }
//	        m.overlayStack.Close(sessionPickerDialogID) // close the parent
//	        return dialogAction{Consumed: true, Close: true, Cmd: m.applySwitchTarget(&tgt)}
//	    },
//	}))
//
// It returns the concrete type rather than a dialog because the one
// caller that WRAPS the primitive rather than merely opening it needs
// the struct: the session picker's action-row dialog embeds it to add
// an in-flight state around an async Submit (issue #194), and an
// interface value would promote only the three dialog methods and hide
// HandleKeyMsg and DialogCursor — exactly the two optional extensions
// the wrapper has to keep forwarding for the box to stay typeable and
// keep its caret. There was an interface-returning NewTextInputDialog
// alongside it until #254; nothing outside the package could open what
// it built.
func newTextInputDialog(cfg textInputConfig) *textInputDialog {
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

// HandleKey satisfies dialog for callers holding only a normalized
// stroke. It synthesizes a KeyPressMsg and delegates so both entry
// points behave identically.
func (d *textInputDialog) HandleKey(stroke string, m *model) dialogAction {
	return d.HandleKeyMsg(keyMsgFromStroke(stroke), m)
}

// HandleKeyMsg is the real key handler. Enter validates + submits;
// Esc closes without submitting; everything else feeds the input.
func (d *textInputDialog) HandleKeyMsg(msg tea.KeyPressMsg, m *model) dialogAction {
	switch msg.String() {
	case "esc":
		return dialogAction{Consumed: true, Close: true}
	case "enter":
		value := strings.TrimSpace(d.input.Value())
		if d.cfg.Validate != nil {
			if e := d.cfg.Validate(value); e != "" {
				// Stay open — the operator gets the error inline
				// with their text still in the box.
				d.errMsg = e
				return dialogAction{Consumed: true}
			}
		}
		d.errMsg = ""
		if d.cfg.Submit == nil {
			return dialogAction{Consumed: true, Close: true}
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
	return dialogAction{Consumed: true, Cmd: cmd}
}

// dialogWidth resolves the configured width against the terminal:
// clamped to leave the modal's own margin, floored so the box stays
// usable on a narrow pane. Split out of Render because a wrapper that
// substitutes its own body for a frame — the action-row dialog's
// in-flight state (issue #194) — has to compose it at exactly the
// width the typed frame would have used, or the modal visibly jumps
// the moment the operator presses Enter.
func (d *textInputDialog) dialogWidth(totalWidth int) int {
	width := d.cfg.Width
	if totalWidth > 0 && width > totalWidth-4 {
		width = totalWidth - 4
	}
	if width < 30 {
		width = 30
	}
	return width
}

func (d *textInputDialog) Render(totalWidth int, m *model) string {
	width := d.dialogWidth(totalWidth)

	d.syncStyles(m.styles)
	// Inner width: the modal's content column (box edge plus one
	// column of padding on each side) minus the widget's own prompt
	// rail.
	d.input.SetWidth(nonNeg(modalInnerWidth(width) - lipgloss.Width(d.input.Prompt)))

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
		footer = keyLegend("enter submit", "esc cancel")
	}
	return renderContext{
		Title:  d.cfg.Title,
		Body:   lipgloss.JoinVertical(lipgloss.Left, parts...),
		Footer: footer,
		Width:  width,
		Height: m.height,
		Styles: m.styles,
	}.render()
}

// DialogCursor implements cursorDialog: it reports where the caret
// sits inside the input box, relative to the dialog block's own
// top-left cell. overlay.cursor adds the origin lipgloss.Place
// composited the dialog at (cursor.go).
//
// The offsets are renderContext's chrome, which is fixed: the box
// edge plus one column of horizontal padding, and a title line plus a
// blank row above the body. Inside the body the input is the first
// part unless a prompt
// line was configured, in which case it is the second. The inline
// validation error renders BELOW the input and never moves it.
//
// The column comes from textInputCursor rather than straight from the
// widget: bubbles' own textinput.Cursor() mixes a rune index with a
// cell width and ignores its scroll offset (issue #125, cursor.go).
func (d *textInputDialog) DialogCursor(_ int, _ *model) *tea.Cursor {
	c := textInputCursor(d.input, d.input.Prompt)
	if c == nil {
		return nil // blurred
	}
	c.X += modalContentX
	c.Y += modalBodyTop
	if d.cfg.Prompt != "" {
		c.Y++
	}
	return c
}

// syncStyles rebuilds the textinput palette from the active theme.
// Called per-render (cheap, guarded) because /theme can swap the
// palette while this dialog is open — the theme picker applies on
// cursor move, and a host could stack the two.
func (d *textInputDialog) syncStyles(s styleSet) {
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
func textInputStyles(s styleSet) textinput.Styles {
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
	// the widget and the dialog contract is keystrokes-only — no msg
	// pipe. That constraint died with the virtual cursor.
	ts.Cursor.Blink = true
	return ts
}

// keyMsgFromStroke rebuilds a tea.KeyPressMsg from the normalized
// stroke string that dialog.HandleKey deals in. Only the keys a
// single-line input cares about are mapped; anything unrecognized
// comes back as a zero Key, which the textinput ignores.
//
// This exists so the stroke-string and KeyPressMsg entry points
// stay behaviorally identical — see keyMsgDialog's godoc.
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
// keyMsgDialog's input widget can insert it. bubbletea delivers a
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
