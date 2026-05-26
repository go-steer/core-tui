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

// First Huh form integration (agentic-tui skill §12). Replaces
// the positional `/pricing set <model> <in> <out>` parse with a
// three-field huh.Form when the operator invokes /pricing-set
// (or /pricing set with no args). On submit the form dispatches
// PricingController.Set; on cancel it closes.
//
// The form is held as a top-level Model.pendingForm field rather
// than going through the Dialog overlay because huh.Form needs
// every tea.Msg (KeyPressMsg, WindowSizeMsg, ticks). Dialog's
// keystroke-only HandleKey can't carry that. A future PR will
// extend Dialog with a tea.Msg variant so huh forms ride the
// overlay stack like model picker does today.

package tui

import (
	"errors"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

const pricingFormKeyModel = "model"
const pricingFormKeyIn = "input"
const pricingFormKeyOut = "output"

// newPricingForm constructs the embedded huh.Form for /pricing
// set. Three Input fields with inline validation; Enter on the
// last field submits; Esc aborts.
//
// The form's theme is set to ThemeCharm — palette tweaks to
// match per-provider theming live in a future PR (would require
// translating Theme tokens into huh's separate theme struct).
func newPricingForm(initialModel string) *huh.Form {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key(pricingFormKeyModel).
				Title("Model ID").
				Placeholder("e.g. gemini-3.1-pro-preview").
				Value(&initialModel).
				Validate(func(s string) error {
					if s == "" {
						return errors.New("model ID required")
					}
					return nil
				}),
			huh.NewInput().
				Key(pricingFormKeyIn).
				Title("Input cost ($ per million tokens)").
				Placeholder("e.g. 1.25").
				Validate(validatePositiveFloat),
			huh.NewInput().
				Key(pricingFormKeyOut).
				Title("Output cost ($ per million tokens)").
				Placeholder("e.g. 5.00").
				Validate(validatePositiveFloat),
		),
	).WithShowHelp(false).WithTheme(huh.ThemeFunc(huh.ThemeCharm))
	return form
}

// validatePositiveFloat returns nil when s parses as a non-
// negative float, or an inline error message huh renders under
// the field. Empty is rejected — the field is required.
func validatePositiveFloat(s string) error {
	if s == "" {
		return errors.New("required")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return errors.New("must be a number (e.g. 1.25)")
	}
	if v < 0 {
		return errors.New("must be non-negative")
	}
	return nil
}

// updatePricingForm forwards msg to m.pendingForm and applies
// the result. On StateCompleted: dispatches PricingController.Set
// + closes; on StateAborted: closes silently. The returned Cmd
// is whatever huh.Form's Update emitted (typically a cursor
// blink tick).
func (m *Model) updatePricingForm(msg tea.Msg) tea.Cmd {
	if m.pendingForm == nil {
		return nil
	}
	updated, cmd := m.pendingForm.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		m.pendingForm = f
	} else {
		// updated may be compat.Model wrapping a *huh.Form (huh
		// v2's Update returns compat.Model, not tea.Model).
		// Re-pin our reference to the same *huh.Form so state
		// inspection on .State below works.
		m.pendingForm = updated.(interface{ Form() *huh.Form }).Form()
	}
	switch m.pendingForm.State {
	case huh.StateCompleted:
		modelID := m.pendingForm.GetString(pricingFormKeyModel)
		in, _ := strconv.ParseFloat(m.pendingForm.GetString(pricingFormKeyIn), 64)
		out, _ := strconv.ParseFloat(m.pendingForm.GetString(pricingFormKeyOut), 64)
		m.pendingForm = nil
		if ctrl, ok := m.opts.Agent.(PricingController); ok {
			summary, err := ctrl.Set(modelID, in, out)
			if err != nil {
				m.history.Append(Message{Role: RoleError, Text: "/pricing set: " + err.Error()})
			} else {
				m.history.Append(Message{Role: RoleSystem, Text: summary})
			}
		} else {
			m.history.Append(Message{Role: RoleSystem, Text: "/pricing: agent doesn't implement PricingController"})
		}
		m.resize()
		m.refreshAndScroll()
	case huh.StateAborted:
		m.pendingForm = nil
		m.history.Append(Message{Role: RoleSystem, Text: "/pricing set: cancelled"})
		m.resize()
		m.refreshAndScroll()
	}
	return cmd
}
