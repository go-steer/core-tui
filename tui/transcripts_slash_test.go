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
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transcriptsModel builds a model with one saved transcript on disk
// and returns it alongside the file's base name.
func transcriptsModel(t *testing.T) (model, string) {
	t.Helper()
	dir := t.TempDir()
	path, err := saveTranscriptFile(dir, Transcript{
		Model:     "gemini-3.1-pro",
		StartedAt: time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC),
		Messages: []TranscriptMsg{
			{Role: "user", Text: "what broke the deploy"},
			{Role: "assistant", Text: "The **rollout** hit a quota."},
		},
	})
	if err != nil {
		t.Fatalf("saveTranscriptFile: %v", err)
	}
	m := newModel(Options{Agent: &noopAgent{}, AgentsDir: dir, ForceTheme: ThemeDark})
	m.width, m.height = 100, 40
	m.resize()
	return m, filepath.Base(path)
}

// TestTranscripts_ListsAndLoads is the command's whole job, under its
// new name (issue #268).
func TestTranscripts_ListsAndLoads(t *testing.T) {
	m, name := transcriptsModel(t)

	list := m.handleTranscripts("")
	if !strings.Contains(list, "Saved transcripts (1)") {
		t.Errorf("listing header wrong\n  output:\n%s", list)
	}
	if !strings.Contains(list, "/transcripts <name>") {
		t.Errorf("listing should name the load form\n  output:\n%s", list)
	}
	if !strings.Contains(list, name) {
		t.Errorf("listing missing %q\n  output:\n%s", name, list)
	}

	// A bare file name resolves against AgentsDir/sessions.
	loaded := m.handleTranscripts(name)
	if !strings.Contains(loaded, "/transcripts: loaded") || !strings.Contains(loaded, "2 messages") {
		t.Errorf("load line wrong\n  output:\n%s", loaded)
	}
	if got := m.history.Len(); got != 2 {
		t.Errorf("history has %d messages after a load, want 2", got)
	}
}

// TestTranscripts_NoAgentsDirSaysSo. The command needs no host
// capability, only a directory, so the miss has to name the option
// rather than blaming the agent.
func TestTranscripts_NoAgentsDirSaysSo(t *testing.T) {
	m := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})
	got := m.handleTranscripts("")
	if !strings.Contains(got, "no AgentsDir wired") {
		t.Errorf("missing-AgentsDir message wrong: %q", got)
	}
}

// TestTranscripts_EveryLineOwnsTheNewName guards the half-rename: the
// prefixes are hand-written on each branch, so one missed string
// leaves an operator staring at "/resume:" under a command they typed
// as /transcripts.
func TestTranscripts_EveryLineOwnsTheNewName(t *testing.T) {
	withDir, name := transcriptsModel(t)
	empty := newModel(Options{Agent: &noopAgent{}, AgentsDir: t.TempDir(), ForceTheme: ThemeDark})
	noDir := newModel(Options{Agent: &noopAgent{}, ForceTheme: ThemeDark})

	for _, got := range []string{
		withDir.handleTranscripts(""),
		withDir.handleTranscripts(name),
		withDir.handleTranscripts("no-such-file.json"),
		empty.handleTranscripts(""),
		noDir.handleTranscripts(""),
	} {
		if strings.Contains(got, "/resume") {
			t.Errorf("output still says /resume:\n%s", got)
		}
	}
}

// TestResumeAlias_WorksAndSaysWhatToTypeInstead. The old spelling
// keeps working until v1.0 — the point of the notice is to teach the
// new name, not to make the operator type the command twice.
func TestResumeAlias_WorksAndSaysWhatToTypeInstead(t *testing.T) {
	m, _ := transcriptsModel(t)

	handled, next, _ := m.dispatchBuiltinSlash("resume", "")
	if !handled {
		t.Fatal("/resume not handled")
	}
	got := lastText(next.(model))
	if !strings.Contains(got, "/resume is now /transcripts") {
		t.Errorf("alias did not name its replacement\n  output:\n%s", got)
	}
	// And it still did the work.
	if !strings.Contains(got, "Saved transcripts (1)") {
		t.Errorf("alias refused to run\n  output:\n%s", got)
	}

	// The new name carries no notice.
	handled, next, _ = m.dispatchBuiltinSlash("transcripts", "")
	if !handled {
		t.Fatal("/transcripts not handled")
	}
	if got := lastText(next.(model)); strings.Contains(got, "is now") {
		t.Errorf("/transcripts should not print the deprecation row\n  output:\n%s", got)
	}
}

// TestTranscripts_RefusedMidTurn is the bug the rename uncovered
// (issue #268). "resume" was in neither mid-turn bucket, so it fell to
// the default — queue as prompt text — and "/resume foo" typed during
// a turn reached the agent as the literal prose "resume foo". Loading
// a transcript replaces the history wholesale, which is the same race
// /clear is refused for, so it belongs in the refused set. Both
// spellings have to reach that verdict: the fold runs first.
func TestTranscripts_RefusedMidTurn(t *testing.T) {
	for _, line := range []string{"/transcripts", "/transcripts old.json", "/resume", "/resume old.json"} {
		if got := midTurnSlashDisposition(line); got != midTurnRefuse {
			t.Errorf("%q mid-turn = %v, want midTurnRefuse", line, got)
		}
	}
}
