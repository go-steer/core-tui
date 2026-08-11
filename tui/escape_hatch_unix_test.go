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

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix || zos
// +build darwin dragonfly freebsd linux netbsd openbsd solaris aix zos

package tui

import (
	"os"
	"strings"
	"testing"
)

// TestEmergencyRestoreTerminal_WritesResetSequences guards the core of
// the wedge-recovery escape hatch: the sequences that undo bubble-tea's
// alt-screen/raw-mode terminal state must actually be emitted. Without
// the "leave alternate screen" + "show cursor" resets, a SIGQUIT out of
// a wedged TUI leaves the operator with a garbled, cursor-less shell
// (the reported "bubble-tea didn't release" symptom).
func TestEmergencyRestoreTerminal_WritesResetSequences(t *testing.T) {
	var buf strings.Builder
	// nil orig => termios restore is skipped (GetState-failed path); the
	// ANSI screen reset must still run.
	emergencyRestoreTerminal(0, &buf, nil)

	got := buf.String()
	wants := []struct {
		seq, why string
	}{
		{"\x1b[?1049l", "leave alternate screen buffer (restore scrollback)"},
		{"\x1b[?25h", "show cursor"},
		{"\x1b[?2004l", "disable bracketed paste"},
		{"\x1b[?1006l", "disable SGR mouse encoding"},
		{"\x1b[0m", "reset colors/attributes"},
	}
	for _, w := range wants {
		if !strings.Contains(got, w.seq) {
			t.Errorf("restore output missing %q (%s)\ngot: %q", w.seq, w.why, got)
		}
	}
}

// TestInstallEscapeHatch_NoopWhenNotTerminal verifies the guard: with a
// non-terminal fd (bubble-tea would read from /dev/tty in that case, not
// this fd) the hatch installs nothing and hands back a safe stop func.
// A regular file fd is reliably not a terminal.
func TestInstallEscapeHatch_NoopWhenNotTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer f.Close()

	stop := installEscapeHatch(f.Fd(), f)
	if stop == nil {
		t.Fatal("installEscapeHatch returned a nil stop func")
	}
	// Must be safe to call, and idempotent — the no-op path returns a
	// bare closure, so a second call must not panic either.
	stop()
	stop()
}
