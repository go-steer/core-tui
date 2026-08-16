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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The host clipboard path (issue #175). Two contracts: the host
// writer gets the same bytes the escape does, and the notice stops
// hedging exactly when — and only when — the copy has been confirmed.

// writerModel is copyModel with a host clipboard writer wired, and
// the channel the writer records what it was handed on. Buffered so
// the writer never blocks whether or not the test reads it.
func writerModel(t *testing.T, sel int, err error) (Model, chan string) {
	t.Helper()
	got := make(chan string, 4)
	m := copyModel(t, sel)
	m.opts.ClipboardWriter = func(text string) error {
		got <- text
		return err
	}
	return m, got
}

// runCmds executes a Cmd and returns every message it produced,
// flattening the batch a copy now returns. Order is not meaningful —
// tea.Batch makes no promises about it and neither does this.
func runCmds(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("the key produced no command at all")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, runCmds(t, c)...)
	}
	return out
}

// Both writes carry the same text. They go to different machines, so
// an operator who has both wired must not have to know which one
// their paste came from.
func TestClipboard_TheHostWriterGetsWhatTheEscapeGets(t *testing.T) {
	m, got := writerModel(t, 1, nil)
	_, cmd := pressCmd(m, "y")

	var escape string
	for _, msg := range runCmds(t, cmd) {
		if name := fmt.Sprintf("%T", msg); strings.Contains(name, "setClipboardMsg") {
			escape = reflect.ValueOf(msg).String()
		}
	}
	if escape == "" {
		t.Fatal("wiring a host writer replaced the OSC 52 write instead of joining it")
	}
	select {
	case hosted := <-got:
		if hosted != escape {
			t.Errorf("the host writer got:\n%q\nthe terminal got:\n%q", hosted, escape)
		}
	default:
		t.Fatal("the host writer was never called")
	}
}

// The notice is the only evidence a copy happened, and OSC 52 cannot
// be acknowledged — so it names the mechanism while that is all we
// have, and drops the qualifier only once a host write has confirmed
// the copy.
func TestClipboard_TheNoticeHedgesUntilSomethingConfirmsIt(t *testing.T) {
	t.Run("no host writer means the caveat stays", func(t *testing.T) {
		m, _ := pressCmd(copyModel(t, 1), "y")
		if !strings.HasSuffix(m.copyNotice, copyViaOSC52) {
			t.Errorf("notice %q does not name the mechanism it went out by", m.copyNotice)
		}
	})

	t.Run("a clean host write drops it", func(t *testing.T) {
		m, _ := writerModel(t, 1, nil)
		m, _ = pressCmd(m, "y")
		hedged := m.copyNotice
		m.applyClipboardResult(clipboardWrittenMsg{gen: m.copyGen})
		if strings.Contains(m.copyNotice, "osc52") {
			t.Errorf("notice %q still hedges after a confirmed write", m.copyNotice)
		}
		if want := strings.TrimSuffix(hedged, copyViaOSC52); m.copyNotice != want {
			t.Errorf("confirming the copy changed what it said it copied: %q, want %q", m.copyNotice, want)
		}
	})

	t.Run("a failed host write says so and quotes why", func(t *testing.T) {
		m, _ := writerModel(t, 1, errors.New("xclip: exit status 1"))
		m, _ = pressCmd(m, "y")
		m.applyClipboardResult(clipboardWrittenMsg{gen: m.copyGen, err: errors.New("xclip: exit status 1")})
		for _, want := range []string{"copied", copyOSC52Only, "xclip: exit status 1"} {
			if !strings.Contains(m.copyNotice, want) {
				t.Errorf("notice %q is missing %q", m.copyNotice, want)
			}
		}
		if !strings.Contains(m.footerHint(), copyOSC52Only) {
			t.Errorf("the failure never reached the legend: %q", m.footerHint())
		}
	})

	t.Run("a long error is cut to fit the legend", func(t *testing.T) {
		m, _ := writerModel(t, 1, nil)
		m, _ = pressCmd(m, "y")
		m.applyClipboardResult(clipboardWrittenMsg{gen: m.copyGen, err: errors.New(strings.Repeat("verbose ", 40))})
		if n := len(m.copyNotice); n > 128 {
			t.Errorf("a %d-character notice pushes the rest of the legend off the line: %q", n, m.copyNotice)
		}
	})
}

// The writer runs off the loop, so its verdict can arrive after the
// operator has moved on. It may correct the notice it belongs to and
// nothing else — a reply that repaints a superseded copy is worse
// than one that is dropped, because it describes a clipboard state
// that is no longer current.
func TestClipboard_AStaleReplyStaysQuiet(t *testing.T) {
	cases := map[string]func(m Model) Model{
		"the operator moved the cursor": func(m Model) Model { return press(m, "down") },
		"the operator left focus mode": func(m Model) Model {
			m.setFocus(focusInput)
			return m
		},
		"a second copy superseded it": func(m Model) Model {
			m, _ = pressCmd(m, "c")
			return m
		},
		"a declining key superseded it": func(m Model) Model {
			m.selIdx = 0
			m, _ = pressCmd(m, "c") // a prompt has no code block
			return m
		},
	}
	for name, moveOn := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := writerModel(t, 1, nil)
			m, _ = pressCmd(m, "y")
			stale := clipboardWrittenMsg{gen: m.copyGen, err: errors.New("too late")}

			m = moveOn(m)
			before := m.copyNotice
			m.applyClipboardResult(stale)
			if m.copyNotice != before {
				t.Errorf("a retired reply rewrote the notice to %q, want %q", m.copyNotice, before)
			}
		})
	}
}

// A nil hook has to be indistinguishable from no hook, because
// SystemClipboardWriter returns nil on every machine without a
// clipboard and hosts are told to assign it unconditionally.
func TestClipboard_ANilWriterAddsNothing(t *testing.T) {
	m := copyModel(t, 1)
	m.opts.ClipboardWriter = nil
	_, cmd := pressCmd(m, "y")

	msgs := runCmds(t, cmd)
	if len(msgs) != 1 {
		t.Fatalf("an unwired host writer produced %d messages, want just the escape", len(msgs))
	}
	if name := fmt.Sprintf("%T", msgs[0]); !strings.Contains(name, "setClipboardMsg") {
		t.Errorf("the one message is %s, not the OSC 52 write", name)
	}
	if clipboardWriteCmd(nil, 1, "x") != nil {
		t.Error("clipboardWriteCmd invented a command for a nil writer")
	}
}

// Which helper gets picked is mostly about what is NOT picked: a
// remote shell with xclip installed and no X server to reach has no
// clipboard, and treating the binary's presence as an answer would
// report a failure on every copy for the rest of the session.
func TestClipboard_HelperCandidates(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want []string // first token of each candidate, in order
	}{
		{"macos is pbcopy and only pbcopy", "darwin", nil, []string{"pbcopy"}},
		{"windows is clip.exe", "windows", nil, []string{"clip.exe"}},
		{"a headless unix box offers nothing local", "linux", nil, []string{"clip.exe"}},
		{"x11", "linux", map[string]string{"DISPLAY": ":0"}, []string{"xclip", "xsel", "clip.exe"}},
		{"wayland comes first where both are set", "linux", map[string]string{
			"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0",
		}, []string{"wl-copy", "xclip", "xsel", "clip.exe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clipboardHelpers(tc.goos, func(k string) string { return tc.env[k] })
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i][0] != tc.want[i] {
					t.Errorf("candidate %d is %q, want %q", i, got[i][0], tc.want[i])
				}
			}
		})
	}
}

// The helper is fed on stdin and judged by its exit status — the two
// halves of runClipboardHelper that a wrong argv or a swallowed pipe
// would break.
func TestClipboard_RunHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probes are POSIX shell")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to stand in for a clipboard helper")
	}

	t.Run("the text arrives on stdin", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip")
		const text = "two\nlines"
		if err := runClipboardHelper([]string{sh, "-c", "cat > " + out}, text); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != text {
			t.Errorf("the helper received %q, want %q", got, text)
		}
	})

	t.Run("a refusal is an error naming the helper", func(t *testing.T) {
		err := runClipboardHelper([]string{sh, "-c", "exit 3"}, "x")
		if err == nil {
			t.Fatal("a helper that exited 3 was reported as a successful copy")
		}
		if !strings.Contains(err.Error(), sh) {
			t.Errorf("error %q does not say which helper failed", err)
		}
	})
}
