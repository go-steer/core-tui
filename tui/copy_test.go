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
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Copying out of the transcript (issue #153). The contract under test
// is one sentence: what lands on the clipboard is the SOURCE of the
// selected item, not the frame it was drawn into.

// copySrc is the markdown of the assistant turn the fixture selects.
// One line long enough to be wrapped at any sane width, and two
// fenced blocks, because "just the code" has to mean both of them.
const copySrc = "Two things. " + "The first is that the loader resolves every path relative to the " +
	"working directory rather than to the config file, which is why a relative include " +
	"behaves differently under a cron run than it does in a shell.\n" +
	"\n" +
	"```yaml\n" +
	"include:\n" +
	"  - ./base.yaml\n" +
	"```\n" +
	"\n" +
	"Then run it:\n" +
	"\n" +
	"```sh\n" +
	"steer run --config ./app.yaml\n" +
	"```\n"

// copyModel is a transcript with one of each thing worth copying: a
// prompt, an assistant turn with code in it, and a tool call carrying
// structured args and a structured response.
func copyModel(t *testing.T, sel int) Model {
	t.Helper()
	m := NewModel(Options{Agent: &bareAgent{id: "copy"}})
	m.styles = NewStylesWithTheme(true, goldenTheme())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)

	m.history.Append(Message{Role: RoleUser, Text: "why is the include path wrong?", Rendered: "why is the include path wrong?"})
	m.history.Append(Message{Role: RoleAssistant, Text: copySrc, Rendered: m.ensureMarkdown().renderMarkdown(copySrc)})
	m.history.Append(Message{
		Role:            RoleTool,
		ToolName:        "read_file",
		ToolArgs:        "app.yaml",
		ToolArgsMap:     map[string]any{"path": "app.yaml"},
		ToolResponseMap: map[string]any{"bytes": 412},
		ToolPreview:     "\x1b[31mapp.yaml\x1b[m",
	})
	m.refreshViewport()
	m.setFocus(focusTranscript)
	m.selIdx = sel
	return m
}

// pressCmd feeds one stroke through the real Update path and keeps
// the Cmd, which is where a copy lives.
func pressCmd(m Model, stroke string) (Model, tea.Cmd) {
	out, cmd := m.Update(keyPress(stroke))
	return out.(Model), cmd
}

// clipboardText unwraps bubbletea's unexported OSC 52 message. Its
// concrete type is not reachable from here, but its kind is — and the
// type NAME is worth pinning too, since SetPrimaryClipboard produces
// an identically-shaped message that goes somewhere else.
func clipboardText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("the key produced no clipboard command")
	}
	msg := cmd()
	if name := fmt.Sprintf("%T", msg); !strings.Contains(name, "setClipboardMsg") {
		t.Fatalf("expected an OSC 52 clipboard write, got %s", name)
	}
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.String {
		t.Fatalf("clipboard payload is %s, not a string", v.Kind())
	}
	return v.String()
}

// The whole issue in one test: y copies what the host sent, not what
// the terminal drew.
func TestCopy_ItemCopiesTheSourceNotTheFrame(t *testing.T) {
	m, cmd := pressCmd(copyModel(t, 1), "y")
	got := clipboardText(t, cmd)

	if got != strings.TrimRight(copySrc, "\n") {
		t.Errorf("copied text is not the markdown source:\n%q", got)
	}
	if got != ansi.Strip(got) {
		t.Error("an ANSI escape reached the clipboard")
	}
	if strings.Contains(got, glyphSelectBar) {
		t.Error("the selection gutter reached the clipboard")
	}
	// The frame wraps that first paragraph over several rows; the
	// source is one line and has to stay one line.
	first := strings.SplitN(got, "\n", 2)[0]
	if ansi.StringWidth(first) <= m.viewport.Width() {
		t.Fatalf("setup: the first line (%d cells) is not wide enough to have been wrapped", ansi.StringWidth(first))
	}
}

// A prompt is its own source; a tool row has none, and is reassembled
// from the fields the renderer was handed.
func TestCopy_RowsWithoutMarkdownStillCopySource(t *testing.T) {
	t.Run("prompt", func(t *testing.T) {
		_, cmd := pressCmd(copyModel(t, 0), "y")
		if got := clipboardText(t, cmd); got != "why is the include path wrong?" {
			t.Errorf("copied %q", got)
		}
	})
	t.Run("tool call", func(t *testing.T) {
		_, cmd := pressCmd(copyModel(t, 2), "y")
		got := clipboardText(t, cmd)
		for _, want := range []string{"read_file", `"path": "app.yaml"`, `"bytes": 412`} {
			if !strings.Contains(got, want) {
				t.Errorf("copied tool row is missing %q:\n%s", want, got)
			}
		}
		if got != ansi.Strip(got) {
			t.Errorf("the pre-rendered preview's ANSI reached the clipboard:\n%q", got)
		}
	})
}

// c is the narrower half: the code out of the turn, all of it, and
// none of the prose or the fences that delimited it.
func TestCopy_CodeTakesEveryBlockAndNothingElse(t *testing.T) {
	m, cmd := pressCmd(copyModel(t, 1), "c")
	got := clipboardText(t, cmd)

	want := "include:\n  - ./base.yaml\n\nsteer run --config ./app.yaml"
	if got != want {
		t.Errorf("copied code blocks:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "```") {
		t.Error("the fences came with the code")
	}
	if strings.Contains(got, "Then run it") {
		t.Error("the prose between the blocks came with the code")
	}
	if !strings.Contains(m.copyNotice, "2 code blocks") {
		t.Errorf("the notice does not say what was copied: %q", m.copyNotice)
	}
}

// A key that copies something else when it cannot do what it says is
// worse than one that declines: the clipboard is not inspectable from
// here, so the operator would find out on paste.
func TestCopy_CodeDeclinesWhenThereIsNone(t *testing.T) {
	m, cmd := pressCmd(copyModel(t, 0), "c")
	if cmd != nil {
		t.Errorf("a prompt with no code in it still wrote %q to the clipboard", clipboardText(t, cmd))
	}
	if !strings.Contains(m.copyNotice, "no code block") {
		t.Errorf("the operator was not told why nothing happened: %q", m.copyNotice)
	}
}

// A copy leaves the frame exactly as it found it, so the notice is
// the only evidence it happened -- and it describes the last copy or
// nothing at all, never the one before it.
func TestCopy_TheNoticeIsInTheLegendAndClearsOnTheNextKey(t *testing.T) {
	m, _ := pressCmd(copyModel(t, 1), "y")
	if !strings.Contains(m.footerHint(), m.copyNotice) || m.copyNotice == "" {
		t.Fatalf("the legend %q does not carry the copy notice %q", m.footerHint(), m.copyNotice)
	}
	if !strings.Contains(m.copyNotice, "copied") {
		t.Errorf("notice does not say a copy happened: %q", m.copyNotice)
	}

	m = press(m, "down")
	if m.copyNotice != "" {
		t.Errorf("the notice outlived the copy it described: %q", m.copyNotice)
	}
	if !strings.Contains(m.footerHint(), "↑↓ select") {
		t.Errorf("the legend did not come back: %q", m.footerHint())
	}

	m, _ = pressCmd(m, "y")
	m.setFocus(focusInput)
	if m.copyNotice != "" {
		t.Errorf("the notice followed the keyboard back to the composer: %q", m.copyNotice)
	}
}

// The scanner is small on purpose, so the cases it has to get right
// are worth naming.
func TestCopy_FencedCodeBlocks(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"none", "just prose\nover two lines", nil},
		{"info string", "```go\nx := 1\n```", []string{"x := 1"}},
		{"tildes", "~~~\nplain\n~~~", []string{"plain"}},
		{"indented fence", "  ```\n  body\n  ```", []string{"  body"}},
		{"trailing space on the close", "```\nbody\n```   ", []string{"body"}},
		{"unterminated is still code", "```sh\nmake test", []string{"make test"}},
		{"a fence with an info string cannot close one", "~~~\na\n```go\nb\n~~~", []string{"a\n```go\nb"}},
		{"blank block", "```\n```", []string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fencedCodeBlocks(tc.src)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d blocks %q, want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("block %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
