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

// Transcript persistence (R-TR-1). On TUI exit, when Options.AgentsDir
// is non-empty, the session is written to
// <AgentsDir>/sessions/<RFC3339-timestamp>.json with the chat history
// + final usage totals. Atomic write (temp + rename) so a partial
// disk write doesn't leave a corrupt file. Schema is versioned so
// downstream readers can evolve safely.
//
// Lifted from internal/tui/transcript.go (which itself was lifted
// from cogo's internal/session/transcript.go) so the on-disk format
// stays compatible across both hosts.

package tui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TranscriptSchemaVersion is the on-disk schema version. Bump when
// the JSON shape changes in a non-backwards-compatible way.
const TranscriptSchemaVersion = 1

// Transcript is the on-disk session record.
type Transcript struct {
	Version   int             `json:"version"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   time.Time       `json:"ended_at"`
	Model     string          `json:"model"`
	Messages  []TranscriptMsg `json:"messages"`
	Usage     TranscriptUsage `json:"usage"`
}

// TranscriptMsg is one entry in the chat. Role uses the lowercase
// string form ("user" / "assistant" / "system" / "error" / "tool")
// so consumers don't have to import the package's enum.
type TranscriptMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// TranscriptUsage mirrors the host UsageTracker's session totals.
type TranscriptUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

const transcriptSessionsDir = "sessions"

// saveTranscriptFile writes t to <agentsDir>/sessions/<timestamp>.json
// atomically. Returns the absolute path of the new file (or "" when
// skipped because agentsDir was empty).
func saveTranscriptFile(agentsDir string, t Transcript) (string, error) {
	if agentsDir == "" {
		return "", nil
	}
	if t.Version == 0 {
		t.Version = TranscriptSchemaVersion
	}
	if t.EndedAt.IsZero() {
		t.EndedAt = time.Now()
	}
	dir := filepath.Join(agentsDir, transcriptSessionsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("transcript: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, transcriptFileName(t.StartedAt))
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return "", fmt.Errorf("transcript: marshal: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteTranscript(path, data, 0o644); err != nil {
		return "", err
	}
	abs, _ := filepath.Abs(path)
	return abs, nil
}

// transcriptFileName returns a filesystem-safe sortable filename.
// ':' is illegal on Windows so it's swapped for '-' so transcripts
// from any host land cleanly.
func transcriptFileName(started time.Time) string {
	if started.IsZero() {
		started = time.Now()
	}
	return strings.ReplaceAll(started.UTC().Format(time.RFC3339), ":", "-") + ".json"
}

// atomicWriteTranscript writes data via tempfile-in-same-dir +
// rename so a crash mid-write doesn't leave a half-written
// transcript on disk.
func atomicWriteTranscript(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".core-tui-transcript-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("transcript: rename: %w", err)
	}
	return nil
}

// buildTranscript projects the model's chat history + usage totals
// into the on-disk shape. Pure / side-effect-free so the caller
// can decide whether (and where) to persist.
func buildTranscript(m *Model) Transcript {
	msgs := m.history.Snapshot()
	out := make([]TranscriptMsg, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, TranscriptMsg{Role: roleString(msg.Role), Text: msg.Text})
	}
	tot := TranscriptUsage{}
	if m.opts.UsageTracker != nil {
		u := m.opts.UsageTracker.SessionTotals()
		tot = TranscriptUsage{
			InputTokens:  u.InputTokens,
			OutputTokens: u.OutputTokens,
			CostUSD:      m.opts.UsageTracker.SessionCostUSD(),
		}
	}
	return Transcript{
		StartedAt: m.startedAt,
		Model:     m.displayModelName(),
		Messages:  out,
		Usage:     tot,
	}
}

// roleString maps the Role enum to its on-disk string form.
func roleString(r Role) string {
	switch r {
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "assistant"
	case RoleSystem:
		return "system"
	case RoleError:
		return "error"
	case RoleTool:
		return "tool"
	}
	return "unknown"
}
