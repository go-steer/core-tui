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
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
)

// overlay identifies which modal, if any, is currently visible.
type overlay int

const (
	overlayNone overlay = iota
	overlayModelPicker
	overlayPermission
	overlayElicit
)

// Model is the Bubble Tea model that drives the TUI. Field set is the
// minimum needed for the v0 visual-preview slice; later slices add
// streaming state, modal forms, transcript persistence, etc.
type Model struct {
	opts    Options
	styles  Styles
	history History

	viewport viewport.Model
	input    textarea.Model

	width  int
	height int

	statusLayout StatusLayout
	permMode     PermissionMode
	overlay      overlay

	// helpOpen toggles the bottom-anchored stacked help panel
	// (`?` to open / close). When open, the chat viewport shrinks
	// to make room above the input.
	helpOpen bool

	// palette is the active slash / file palette overlay (R-PAL-1 /
	// R-PAL-2). Nil = no palette open. Triggered by typing `/` at
	// the start of the input or `@` anywhere.
	palette *palette

	// quitting flips when Ctrl+C / Ctrl+D land, so the next Update
	// returns tea.Quit.
	quitting bool
}

// NewModel constructs a Model from Options. SeedHistory entries are
// appended in order before the first render.
func NewModel(opts Options) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message and hit Enter. /help for commands."
	if opts.Branding.InputPlaceholder != "" {
		ta.Placeholder = opts.Branding.InputPlaceholder
	}
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	// Focus the textarea so KeyPressMsg events route to it. Focus()
	// returns a blink Cmd we deliberately drop here — Init below
	// returns textarea.Blink directly to start the cursor animation.
	_ = ta.Focus()

	vp := viewport.New()

	m := Model{
		opts:         opts,
		styles:       NewStyles(true, opts.Branding), // overwritten on BackgroundColorMsg
		viewport:     vp,
		input:        ta,
		statusLayout: opts.StatusLayout,
		permMode:     opts.PermissionMode.Initial,
	}
	for _, msg := range opts.SeedHistory {
		m.history.Append(msg)
	}
	return m
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
