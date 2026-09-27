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
	"os"
	"testing"
)

// terminalIdentityEnv is every variable termProgram() reads. The
// suite runs with all of them cleared so it sees the same terminal
// in CI as in a developer's shell: without this, running `go test`
// from VS Code's integrated terminal flips per-terminal behavior
// (the capture hint's modifier, the newline hint, capability
// detection) out from under tests written against the no-signal
// case. A test
// that wants a terminal passes the program name to the function
// under test rather than setting these.
var terminalIdentityEnv = []string{
	"TERM_PROGRAM",
	"KITTY_WINDOW_ID",
	"ALACRITTY_LOG",
	"ALACRITTY_WINDOW_ID",
	"WEZTERM_PANE",
	"WEZTERM_UNIX_SOCKET",
	"FOOT_SOCK",
	"GHOSTTY_RESOURCES_DIR",
	"VSCODE_PID",
	"VSCODE_INJECTION",
	"TMUX",
}

func TestMain(m *testing.M) {
	for _, k := range terminalIdentityEnv {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
