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
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

// Run constructs the Model and runs the Bubble Tea program until exit.
// Blocks until the user quits or ctx is cancelled. Returns the first
// error encountered by tea.Program.Run, if any.
//
// On clean exit (when Options.AgentsDir is non-empty) writes a
// JSON transcript of the session to
// <AgentsDir>/sessions/<RFC3339-timestamp>.json. Failures are
// non-fatal — surfaced to stderr after the alt-screen tears down
// so the operator can see them.
//
// Mouse cell-motion is enabled so the wheel scrolls the viewport;
// operators who want native terminal text-select hold Shift to
// bypass capture. Hosts can rely on this default rather than
// passing tea.WithMouseCellMotion themselves.
func Run(ctx context.Context, opts Options) error {
	if opts.Agent == nil {
		return fmt.Errorf("tui.Run: Options.Agent is required")
	}

	m := NewModel(opts)
	// Mouse mode is set declaratively on the View (see view.go); no
	// Program-level option needed in bubbletea v2.
	p := tea.NewProgram(m, tea.WithContext(ctx))
	finalModel, err := p.Run()

	// Persist transcript on exit. Done after p.Run() returns so the
	// alt-screen is torn down and stderr is visible again. We project
	// the final model rather than holding onto our pre-Run handle so
	// the snapshot reflects every history mutation up to quit.
	if opts.AgentsDir != "" {
		if fm, ok := finalModel.(Model); ok {
			t := buildTranscript(&fm)
			if path, terr := saveTranscriptFile(opts.AgentsDir, t); terr != nil {
				fmt.Fprintf(os.Stderr, "core-tui: transcript save: %v\n", terr)
			} else if path != "" {
				fmt.Fprintf(os.Stderr, "core-tui: transcript saved to %s\n", path)
			}
		}
	}

	return err
}
