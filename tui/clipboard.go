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

// The second half of a copy (issue #175).
//
// copy.go decides WHAT goes on the clipboard; this file is about
// getting it onto the right machine's clipboard, and about admitting
// when we could not.
//
// # Why OSC 52 is not enough on its own
//
// The escape stays the default, and #153's reasoning for it holds: it
// is the only mechanism that reaches the clipboard of the machine the
// operator is SITTING at rather than the one the process runs on,
// which is the whole LiveAgent-over-SSH case. What #153 did not
// reckon with is how many terminals decline it. Terminal.app has
// never implemented it; iTerm2 has it behind a preference that is off
// by default; several remote/web terminals strip it in the relay. On
// those the key does nothing whatsoever, and because the protocol has
// no acknowledgement, the footer said "copied 24 lines" regardless.
// Claiming a result we cannot observe is the actual defect — worse
// than the missing feature, because it sends the operator looking for
// the bug in the wrong place.
//
// # The shape of the fix
//
// Options.ClipboardWriter is host code, called IN ADDITION to the
// escape rather than instead of it. Both writes go out on every copy:
// they target different machines, so neither is a fallback for the
// other, and a host that is local wants the native one while a host
// on the far side of SSH wants the escape. The host write is the only
// one that can report anything, so it is what turns the notice from a
// claim into an observation (see copyToClipboard).
//
// It is a func field rather than a §3.3 capability interface because
// it is a property of where the PROCESS runs, not of what the agent
// can do — the same agent implementation wants a different answer in
// a desktop binary and in a remote daemon.
//
// # Why core-tui does not just take a clipboard dependency
//
// The obvious libraries work, and a host that wants one should use
// one. But core-tui is a library: a dependency here lands in every
// host's module graph and go.sum, whether or not that host ever
// copies anything, and the clipboard ones pull windowing-system
// bindings behind them. SystemClipboardWriter buys most of the same
// coverage out of os/exec, and the hosts that want more can wire it
// in one line.
//
// # Nil is the honest answer on a headless box
//
// SystemClipboardWriter resolves a helper ONCE, at construction, and
// returns nil when there isn't one. That nil is load-bearing: a
// remote box with no display has no clipboard to write to, so the
// only truthful thing to do is decline the native path and let the
// escape try to reach the operator's real terminal. The alternative —
// a writer that exists and fails on every copy — would report a
// failure per keystroke for a machine that was never going to have a
// clipboard.

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// clipboardWriteTimeout bounds one helper invocation. The helpers are
// local processes that either take the text immediately or are
// wedged; five seconds is far past "slow" and well short of the
// operator concluding the key did nothing.
const clipboardWriteTimeout = 5 * time.Second

// clipboardWriteCmd runs Options.ClipboardWriter off the Update
// goroutine, per host_async.go's rules: the func is resolved before
// the closure, the closure touches no model state, and a nil hook
// yields a nil Cmd so the caller keeps its "not wired" behaviour.
//
// A host writer is not obliged to be quick — an exec, an X11 round
// trip, or an RPC to a companion process are all plausible — and
// running it inline would freeze the loop for the length of a
// keystroke's worth of I/O.
//
// gen is the copy notice's generation, not the session's: the reply
// exists only to correct a notice, and the notice can be superseded
// several times within one session (see model.copyGen).
func clipboardWriteCmd(write func(string) error, gen uint64, text string) tea.Cmd {
	if write == nil {
		return nil
	}
	return func() tea.Msg {
		return clipboardWrittenMsg{gen: gen, err: write(text)}
	}
}

// SystemClipboardWriter returns a value for Options.ClipboardWriter
// backed by whichever command-line clipboard helper this machine has,
// or nil when it has none — including, deliberately, a Unix box with
// no display, where the helper may be installed but has nothing to
// talk to. Assigning a nil hook is exactly the same as not assigning
// one, so the call site needs no branch:
//
//	opts.ClipboardWriter = tui.SystemClipboardWriter()
//
// The helper is resolved once, here, and the resolved absolute path
// is captured — a copy does not re-scan PATH, and cannot be
// redirected by a PATH change made after startup.
//
// Hosts wanting something else pass their own func: a clipboard
// library, or, on a remote box where no clipboard exists to write to,
// a sink the operator can reach from the machine they are sitting at
// — a scratch file open in an editor works, because the editor is
// local even when the process is not:
//
//	opts.ClipboardWriter = func(text string) error {
//	    return os.WriteFile(scratch, []byte(text), 0o600)
//	}
func SystemClipboardWriter() func(text string) error {
	argv := findClipboardHelper()
	if argv == nil {
		return nil
	}
	return func(text string) error { return runClipboardHelper(argv, text) }
}

// findClipboardHelper returns the argv of the first usable helper, or
// nil. Order within a platform is preference order.
func findClipboardHelper() []string {
	for _, argv := range clipboardHelpers(runtime.GOOS, os.Getenv) {
		path, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		return append([]string{path}, argv[1:]...)
	}
	return nil
}

// clipboardHelpers lists the candidates for a platform, most specific
// first. goos and getenv are parameters so the selection is testable
// without the test having to BE on the platform it is checking.
//
// The display checks on Unix are the point of the whole function: a
// remote shell routinely has xclip installed and no X server to reach
// with it, and treating "the binary exists" as "there is a clipboard"
// is how you get a failure notice on every copy for the rest of the
// session.
//
// clip.exe is listed for Unix as well as Windows. Under WSL it is on
// PATH and writes the Windows clipboard, which is the one attached to
// the screen the operator is looking at; anywhere else the LookPath
// simply misses.
func clipboardHelpers(goos string, getenv func(string) string) [][]string {
	switch goos {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"clip.exe"}}
	}
	var out [][]string
	if getenv("WAYLAND_DISPLAY") != "" {
		out = append(out, []string{"wl-copy"})
	}
	if getenv("DISPLAY") != "" {
		out = append(out,
			[]string{"xclip", "-selection", "clipboard"},
			[]string{"xsel", "--clipboard", "--input"},
		)
	}
	return append(out, []string{"clip.exe"})
}

// runClipboardHelper feeds text to a helper on stdin.
//
// Stdout and stderr are left nil — connected to os.DevNull rather
// than to pipes — and that is a correctness requirement, not tidiness.
// xclip and wl-copy have to outlive the command that started them:
// on X11 and Wayland the clipboard is served by its owner, so they
// fork a resident process that inherits the descriptors. A pipe here
// would keep that inherited end open and Wait would block on it until
// the operator's next copy replaced the selection. The cost is that
// the helper's own diagnostics are dropped and the exit status is all
// we report, which for "is there a clipboard" is enough.
func runClipboardHelper(argv []string, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardWriteTimeout)
	defer cancel()
	// #nosec G204 -- argv is not input. Every element comes from the
	// literal table in clipboardHelpers, with argv[0] replaced by
	// whatever exec.LookPath resolved that literal to at startup; no
	// caller can add to it or reorder it. The one untrusted value in
	// this function is the copied text, and it goes on stdin, which
	// is where it stays.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}
