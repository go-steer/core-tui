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
)

const (
	DismissEscape       = dismissEscape
	DismissSuperseded   = dismissSuperseded
	DismissShutdown     = dismissShutdown
	DismissUnrenderable = dismissUnrenderable
)

var NewThemePickerQuestion = newThemePickerQuestion
