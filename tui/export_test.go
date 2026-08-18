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

// Test-only exports for the external question test (issue #164).
//
// docs/design-question-dialogs.md §8 sets the decoupling bar as: a
// test in package tui_test constructs each question, drives it with
// tea.KeyPressMsg values and asserts on the returned answer, with no
// NewModel, no tea.NewProgram, and the zero Styles passed to Body.
//
// The question family is unexported and stays that way — exporting it
// is posture B in the design and is not stage 1's to decide — so the
// external test reaches it through the standard export_test.go
// bridge. The go tool compiles this file only under `go test`, so
// none of it is API: apidiff does not see it and a host cannot import
// it.
//
// Aliases rather than wrappers, deliberately. An alias makes the
// external test assert against the very same types the package
// switches over, so a variant renamed or a field moved fails to
// compile here instead of being quietly shimmed by a bridge that
// still builds.

package tui

type (
	Answer         = answer
	Question       = question
	CursorQuestion = cursorQuestion
	Resolver       = resolver

	Dismissed = dismissed
	Declined  = declined
	Chosen    = chosen
	Selected  = selected
	Text      = text
	Fields    = fields
	Decision  = decision

	DismissReason = dismissReason

	ThemePreviewMsg = themePreviewMsg

	// Styles keeps the name §8 uses for the zero value the external
	// test hands to Body. The type itself is styleSet now (#257);
	// the alias is spelled for the caller rather than for the
	// declaration, because a question taking a style bundle it did
	// not fill in is the property under test and "the zero Styles"
	// is how the design states it.
	Styles = styleSet

	ModelPickerQuestion     = modelPickerQuestion
	ModelSwitchRequestedMsg = modelSwitchRequestedMsg

	SessionPickerQuestion     = sessionPickerQuestion
	SessionSwitchRequestedMsg = sessionSwitchRequestedMsg
	SessionInputRequestedMsg  = sessionInputRequestedMsg
)

const (
	DismissEscape       = dismissEscape
	DismissSuperseded   = dismissSuperseded
	DismissShutdown     = dismissShutdown
	DismissUnrenderable = dismissUnrenderable
)

var (
	NewThemePickerQuestion = newThemePickerQuestion
	NewModelPickerQuestion = newModelPickerQuestion

	// LoadModels is applyModels as a plain function — a method
	// expression rather than a hand-written wrapper, for the same
	// reason the types above are aliases: the external test calls the
	// very method the Update loop does, so a signature change fails to
	// compile here too.
	//
	// The theme picker takes its list in its constructor and needs no
	// equivalent. This one's list is a host snapshot that arrives
	// asynchronously (#114), which is exactly the property that makes
	// the picker worth having an external test for.
	LoadModels = (*modelPickerQuestion).applyModels

	NewSessionPickerQuestion = newSessionPickerQuestion

	// LoadSessions is applySessions the same way, and the difference
	// from LoadModels is the point of testing both: this one seeds no
	// cursor. A session list has no "the one you are on" to open on —
	// the attached row is where you already are, and the row worth
	// landing on is the one you are leaving it for.
	LoadSessions = (*sessionPickerQuestion).applySessions
)
