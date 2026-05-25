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

// Package testagent provides an in-process stand-in for a real
// tui.Agent. The current implementation is the "idle" variant used by
// the visual-preview slice: Run yields no events so the TUI sits in
// its idle state. Later slices will add a scripted variant that
// replays recorded event streams on a timer.
package testagent

import (
	"context"
	"iter"

	"github.com/go-steer/core-tui/tui"
)

// New returns an Agent that produces no events on Run. The TUI
// renders its idle state (any SeedHistory shows, the input box is
// active, the spinner stays still).
func New() tui.Agent { return idle{} }

type idle struct{}

func (idle) Run(_ context.Context, _ string) iter.Seq2[tui.Event, error] {
	return func(yield func(tui.Event, error) bool) {
		// no-op: yield nothing.
	}
}
