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
	"sort"
	"strings"
	"time"
)

// TranscriptSchemaVersion is the on-disk schema version. Bump when
// the JSON shape changes in a non-backwards-compatible way.
//
// v2 (2026-06-09): TranscriptMsg gained optional tool-call fields
// (tool_name, tool_args, tool_preview, tool_call_id). Backwards-
// compatible: v1 files load fine (the new fields default to empty)
// and v2 files written by newer code load fine in older readers
// (json.Unmarshal silently drops unknown fields). The version bump
// is a signal to consumers that tool rows now carry their structured
// data instead of serializing as {role: "tool", text: ""}.
const TranscriptSchemaVersion = 2

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
// string form ("user" / "assistant" / "system" / "error" / "tool" /
// "notice") so consumers don't have to import the package's enum.
//
// Tool-call fields (ToolName, ToolArgs, ToolPreview, ToolCallID) are
// populated only when Role == "tool". For other roles they're empty
// and omitted from the JSON via omitempty. Text is intentionally
// empty for tool rows — the in-memory renderer assembles the visible
// row from ToolName/ToolArgs/ToolPreview rather than a single string,
// and that structure is preserved here.
type TranscriptMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`

	// ToolName is the tool's canonical name (e.g. "read_file",
	// "mcp.gke.list_clusters"). Populated when Role == "tool".
	ToolName string `json:"tool_name,omitempty"`

	// ToolArgs is the JSON-serialized call arguments (or a
	// human-readable rendering when JSON serialization isn't
	// available). Populated when Role == "tool".
	ToolArgs string `json:"tool_args,omitempty"`

	// ToolPreview is the pre-rendered multi-line block the renderer
	// shows under the tool row — a unified diff for edit_file, a
	// read-scope summary, a result excerpt, etc. Populated when
	// Role == "tool"; empty when the tool call has no preview yet
	// (call-only row, before the result arrived).
	ToolPreview string `json:"tool_preview,omitempty"`

	// ToolCallID is the wire-level identifier the host emitted for
	// this call (e.g. genai.FunctionCall.ID). Populated when
	// Role == "tool" and the host supplied an ID. Useful for
	// cross-referencing the transcript against the host's audit log.
	ToolCallID string `json:"tool_call_id,omitempty"`
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
//
// Tool rows: Message.Text is intentionally empty in-memory (the
// renderer assembles the visible row from ToolName/ToolArgs/
// ToolPreview), so prior to v2 schema tool rows persisted as
// {role: "tool", text: ""} — useless for post-mortems. v2 preserves
// the structured fields alongside the empty Text so post-mortem
// readers can see what each tool was actually called with and what
// it returned.
func buildTranscript(m *Model) Transcript {
	msgs := m.history.Snapshot()
	out := make([]TranscriptMsg, 0, len(msgs))
	for _, msg := range msgs {
		entry := TranscriptMsg{Role: roleString(msg.Role), Text: msg.Text}
		if msg.Role == RoleTool {
			entry.ToolName = msg.ToolName
			entry.ToolArgs = msg.ToolArgs
			entry.ToolPreview = msg.ToolPreview
			entry.ToolCallID = msg.ToolCallID
		}
		out = append(out, entry)
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
	case RoleNotice:
		return "notice"
	}
	return "unknown"
}

// roleFromString is the inverse of roleString — used by
// LoadTranscript to rebuild Message.Role from the on-disk
// lowercase tag. Unknown tags become RoleSystem so they at
// least render as something muted.
func roleFromString(s string) Role {
	switch s {
	case "user":
		return RoleUser
	case "assistant":
		return RoleAssistant
	case "error":
		return RoleError
	case "tool":
		return RoleTool
	case "notice":
		return RoleNotice
	default:
		return RoleSystem
	}
}

// LoadTranscript reads a transcript JSON file from disk. Returns
// the decoded Transcript ready for ApplyTranscript. Errors
// propagate as-is so the caller (slash dispatcher) can surface
// them inline.
func LoadTranscript(path string) (Transcript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Transcript{}, err
	}
	var t Transcript
	if err := json.Unmarshal(data, &t); err != nil {
		return Transcript{}, fmt.Errorf("transcript: parse %s: %w", path, err)
	}
	return t, nil
}

// ListTranscripts returns every transcript file under
// <agentsDir>/sessions, most-recent first by modification time.
// Empty agentsDir or missing dir returns ([], nil) — no error,
// just no sessions to surface.
func ListTranscripts(agentsDir string) ([]TranscriptInfo, error) {
	if agentsDir == "" {
		return nil, nil
	}
	dir := filepath.Join(agentsDir, transcriptSessionsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]TranscriptInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		abs, _ := filepath.Abs(filepath.Join(dir, e.Name()))
		out = append(out, TranscriptInfo{
			Path:    abs,
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// TranscriptInfo is one entry in the /resume picker.
type TranscriptInfo struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
}

// ApplyTranscript replaces the model's history with the loaded
// transcript's messages and re-renders any assistant markdown at
// the current viewport width so wrapping is correct. The list
// cache is reset so the next refreshViewport regenerates every
// row from the new identities.
//
// Doesn't restore the in-flight turn, queue, or modal state — a
// resumed session starts idle.
func (m *Model) ApplyTranscript(t Transcript) {
	m.history.Reset()
	mr := m.ensureMarkdown()
	for _, msg := range t.Messages {
		role := roleFromString(msg.Role)
		entry := Message{Role: role, Text: msg.Text}
		if role == RoleAssistant && mr != nil && msg.Text != "" {
			entry.Rendered = mr.renderMarkdown(msg.Text)
		}
		if role == RoleTool {
			// Restore the structured tool fields written by v2
			// schema. v1 transcripts will have these empty; tool
			// rows from a v1 file will render as bare "tool" entries
			// (lossy, matching the information that was actually
			// persisted at v1).
			entry.ToolName = msg.ToolName
			entry.ToolArgs = msg.ToolArgs
			entry.ToolPreview = msg.ToolPreview
			entry.ToolCallID = msg.ToolCallID
		}
		m.history.Append(entry)
	}
	// Width-keyed caches must drop so the new ID space gets
	// fresh entries (old entries are pinned to pre-resume IDs).
	if m.listCache != nil {
		m.listCache.reset(m.viewport.Width())
	}
}
