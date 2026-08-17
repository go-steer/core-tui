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

import "time"

// ModalInputGraceForTest re-exports modalInputGrace to the external
// `tui_test` package, where the headless program smoke tests live
// (program_smoke_test.go).
//
// Those tests drive the permission modal through a real tea.Program,
// so they have to wait out the no-commit window before a decision
// keystroke counts — and the wait has to be derived from the
// production constant rather than copied. A copy would silently rot
// the day someone retunes modalInputGrace: the smoke tests would
// start pressing "y" into the textarea and failing on an assertion
// that says nothing about the cause.
const ModalInputGraceForTest = modalInputGrace

// Compile-time assurance that the re-export keeps its type — a
// plain untyped constant would still compile at the call site but
// would stop tracking a change from Duration to something else.
var _ time.Duration = ModalInputGraceForTest
