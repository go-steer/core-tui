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
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/charmbracelet/x/term"
)

// installEscapeHatch wires a SIGQUIT (Ctrl-\) handler that recovers a
// wedged TUI, returning a stop function the caller defers.
//
// Why this exists: bubble-tea runs Update + View on a single event-loop
// goroutine and catches SIGINT/SIGTERM by turning them into messages
// that loop must process. When a blocking host call in View() or a
// stalled PTY write has wedged the loop, those messages are never read —
// keys and Ctrl+C go dead and the TUI is stuck. SIGQUIT is normally left
// to the Go runtime, whose default handler dumps every goroutine's stack
// (useful) but then exits WITHOUT restoring the terminal, leaving it in
// raw + alternate-screen mode: no echo, garbled prompt, the "bubble-tea
// didn't release" symptom that forces a manual `reset`.
//
// We install our own SIGQUIT handler in a dedicated goroutine. Signal
// delivery is independent of the event loop, so it fires even when the
// loop is fully wedged. The handler (1) prints the goroutine dump itself,
// preserving the debug signal the default handler gave; (2) restores the
// terminal directly — bubble-tea's own restore only runs after the event
// loop unwinds, which a wedge prevents; and (3) exits. Net effect:
// Ctrl-\ becomes a reliable "unwedge and hand my shell back" key.
//
// No-op when fd isn't a terminal (piped/redirected stdin) — there's no
// raw mode to undo and bubble-tea would be reading from /dev/tty instead.
func installEscapeHatch(fd uintptr, out io.Writer) func() {
	if !term.IsTerminal(fd) {
		return func() {}
	}
	// Capture the pre-raw ("cooked") termios now, before bubble-tea's
	// p.Run() calls MakeRaw, so we can put the terminal back exactly as
	// the operator's shell left it. A nil orig (GetState failed) just
	// means we skip the termios step and still do the ANSI reset.
	orig, _ := term.GetState(fd)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
		case <-done:
			return
		}
		// 1. Preserve the debug payload the runtime's default SIGQUIT
		//    handler would have printed (we've overridden it).
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		_, _ = os.Stderr.Write(buf[:n])
		// 2. Hand the terminal back to the shell.
		emergencyRestoreTerminal(fd, out, orig)
		// 3. Exit with the conventional 128+signal status.
		os.Exit(128 + int(syscall.SIGQUIT))
	}()

	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

// emergencyRestoreTerminal undoes the terminal modes bubble-tea sets
// (raw input, alternate screen, hidden cursor, mouse tracking) without
// going through the Program, so it's safe to call from the signal
// goroutine while the event loop is wedged. Best-effort: each step is
// independent so a failure in one doesn't skip the rest.
func emergencyRestoreTerminal(fd uintptr, out io.Writer, orig *term.State) {
	if orig != nil {
		// raw -> cooked: line-buffered input + echo back. This is the
		// step that fixes "can't type / nothing echoes".
		_ = term.Restore(fd, orig)
	}
	// Reset the screen state the alt-screen renderer left behind. These
	// are plain bytes to the terminal, independent of termios.
	_, _ = io.WriteString(out,
		"\x1b[?1049l"+ // leave alternate screen buffer (restores scrollback)
			"\x1b[?25h"+ // show cursor
			"\x1b[?2004l"+ // disable bracketed paste
			"\x1b[?1000l"+ // disable X10 mouse reporting
			"\x1b[?1002l"+ // disable button-event mouse reporting
			"\x1b[?1003l"+ // disable any-event mouse reporting
			"\x1b[?1006l"+ // disable SGR mouse encoding
			"\x1b[0m", // reset colors + attributes
	)
}
