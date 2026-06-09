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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTranscript_ToolFields_SaveLoadRoundTrip covers the v2 schema's
// load-bearing property: a tool row written by buildTranscript-equivalent
// code and then read back via LoadTranscript preserves the structured
// tool fields. Regression-guards against the v1 bug where every tool
// row serialized as {role: "tool", text: ""} and the rich data was
// silently dropped.
func TestTranscript_ToolFields_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := Transcript{
		Version: TranscriptSchemaVersion,
		Model:   "claude-opus-4-7",
		Messages: []TranscriptMsg{
			{Role: "user", Text: "list the GKE clusters"},
			{
				Role:        "tool",
				ToolName:    "mcp.gke.list_clusters",
				ToolArgs:    `{"parent":"projects/demo/locations/-"}`,
				ToolPreview: "13 clusters returned\n- demo-1 RUNNING\n- demo-2 RUNNING",
				ToolCallID:  "call_abc123",
			},
			{
				Role:     "tool",
				ToolName: "read_file",
				ToolArgs: `{"path":"main.go"}`,
				// ToolPreview deliberately empty — covers the
				// "call-only row, result not yet arrived" case.
				ToolCallID: "call_def456",
			},
			{Role: "assistant", Text: "Here are your 13 clusters: …"},
		},
	}

	path, err := saveTranscriptFile(dir, original)
	if err != nil {
		t.Fatalf("saveTranscriptFile: %v", err)
	}

	got, err := LoadTranscript(path)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}

	if got.Version != TranscriptSchemaVersion {
		t.Errorf("Version = %d, want %d", got.Version, TranscriptSchemaVersion)
	}
	if len(got.Messages) != len(original.Messages) {
		t.Fatalf("Messages len = %d, want %d", len(got.Messages), len(original.Messages))
	}

	gotTool1 := got.Messages[1]
	if gotTool1.Role != "tool" {
		t.Errorf("messages[1].Role = %q, want %q", gotTool1.Role, "tool")
	}
	if gotTool1.ToolName != "mcp.gke.list_clusters" {
		t.Errorf("messages[1].ToolName = %q, want %q", gotTool1.ToolName, "mcp.gke.list_clusters")
	}
	if gotTool1.ToolArgs != `{"parent":"projects/demo/locations/-"}` {
		t.Errorf("messages[1].ToolArgs = %q, want preserved JSON args", gotTool1.ToolArgs)
	}
	if !strings.Contains(gotTool1.ToolPreview, "13 clusters returned") {
		t.Errorf("messages[1].ToolPreview = %q, want preserved preview text", gotTool1.ToolPreview)
	}
	if gotTool1.ToolCallID != "call_abc123" {
		t.Errorf("messages[1].ToolCallID = %q, want %q", gotTool1.ToolCallID, "call_abc123")
	}

	// Call-only row (no preview yet): name + args + ID round-trip,
	// preview stays empty.
	gotTool2 := got.Messages[2]
	if gotTool2.ToolName != "read_file" || gotTool2.ToolArgs == "" || gotTool2.ToolCallID == "" {
		t.Errorf("messages[2] tool fields not preserved: %+v", gotTool2)
	}
	if gotTool2.ToolPreview != "" {
		t.Errorf("messages[2].ToolPreview = %q, want empty (call-only row)", gotTool2.ToolPreview)
	}
}

// TestTranscript_OmitemptyOnNonToolRows ensures the new fields don't
// pollute the JSON for non-tool rows (where they'd be empty anyway
// but still hurt readability and bloat file size).
func TestTranscript_OmitemptyOnNonToolRows(t *testing.T) {
	dir := t.TempDir()
	tx := Transcript{
		Version: TranscriptSchemaVersion,
		Model:   "test",
		Messages: []TranscriptMsg{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "hi"},
		},
	}
	path, err := saveTranscriptFile(dir, tx)
	if err != nil {
		t.Fatalf("saveTranscriptFile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	body := string(raw)
	for _, field := range []string{"tool_name", "tool_args", "tool_preview", "tool_call_id"} {
		if strings.Contains(body, field) {
			t.Errorf("non-tool transcript should not contain %q in JSON, got:\n%s", field, body)
		}
	}
}

// TestTranscript_LoadV1File covers backwards compatibility: a v1 file
// (no tool fields, version=1) loads cleanly under the v2 schema. Tool
// rows in such files predate the schema bump and stay lossy — there's
// no data to recover — but the load itself must not error.
func TestTranscript_LoadV1File(t *testing.T) {
	dir := t.TempDir()
	v1JSON := `{
  "version": 1,
  "started_at": "2026-06-08T10:00:00Z",
  "ended_at": "2026-06-08T10:05:00Z",
  "model": "gemini-3.5-flash",
  "messages": [
    {"role": "user", "text": "find the bug"},
    {"role": "tool", "text": ""},
    {"role": "assistant", "text": "Found it."}
  ],
  "usage": {"input_tokens": 100, "output_tokens": 20, "cost_usd": 0.01}
}
`
	path := filepath.Join(dir, "v1.json")
	if err := os.WriteFile(path, []byte(v1JSON), 0o644); err != nil {
		t.Fatalf("write v1 file: %v", err)
	}
	got, err := LoadTranscript(path)
	if err != nil {
		t.Fatalf("LoadTranscript on v1 file: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1 (preserved from v1 file)", got.Version)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("Messages len = %d, want 3", len(got.Messages))
	}
	tool := got.Messages[1]
	if tool.Role != "tool" {
		t.Errorf("messages[1].Role = %q, want %q", tool.Role, "tool")
	}
	// All tool fields default to empty when absent — that's the
	// v1-can-still-load contract.
	if tool.ToolName != "" || tool.ToolArgs != "" || tool.ToolPreview != "" || tool.ToolCallID != "" {
		t.Errorf("messages[1] tool fields should be empty for v1 file, got %+v", tool)
	}
}

// TestTranscript_NewerReaderHandlesV2 confirms that a freshly-written
// v2 transcript with tool fields encodes and decodes through pure JSON
// (no schema-version-specific code path), so a future reader written
// against any v2+ schema can consume it.
func TestTranscript_NewerReaderHandlesV2(t *testing.T) {
	tx := Transcript{
		Version: TranscriptSchemaVersion,
		Messages: []TranscriptMsg{
			{Role: "tool", ToolName: "grep", ToolArgs: `{"pattern":"foo"}`, ToolCallID: "x"},
		},
	}
	data, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Transcript
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Messages[0].ToolName != "grep" {
		t.Errorf("ToolName round-trip failed: got %q", got.Messages[0].ToolName)
	}
}
