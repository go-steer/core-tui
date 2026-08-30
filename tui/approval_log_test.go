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
	"strings"
	"testing"
)

// TestApprovalLog_AttributedRowNamesTheApprover pins the new half of
// issue #277: a host that fills By gets it in the line.
func TestApprovalLog_AttributedRowNamesTheApprover(t *testing.T) {
	got := renderApprovalLog([]ApprovalLog{
		{Tool: "bash", Key: "kubectl rollout restart deploy/api", Decision: "allow-once", By: "ops@example.com"},
	})
	want := "/permissions: 1 decision(s) this session\n" +
		"  • bash — kubectl rollout restart deploy/api [allow-once] by ops@example.com"
	if got != want {
		t.Errorf("renderApprovalLog:\n got %q\nwant %q", got, want)
	}
}

// TestApprovalLog_UnattributedRowIsByteIdentical is the half that
// matters more. An empty By means the gate verified nobody, and the
// row has to read as it did before the field existed — not "by
// <unknown>", not a trailing "by". A placeholder in an audit line is
// indistinguishable from a name somebody checked.
func TestApprovalLog_UnattributedRowIsByteIdentical(t *testing.T) {
	got := renderApprovalLog([]ApprovalLog{
		{Tool: "edit", Key: "internal/auth/session.go", Decision: "allow-session"},
	})
	want := "/permissions: 1 decision(s) this session\n" +
		"  • edit — internal/auth/session.go [allow-session]"
	if got != want {
		t.Errorf("renderApprovalLog:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "by") {
		t.Errorf("unattributed row leaked an attribution: %q", got)
	}
}

// TestApprovalLog_MixedLogKeepsRowsIndependent walks the shape a real
// multi-operator session produces: some rows answered by a verified
// caller, some by whoever was at the keyboard.
func TestApprovalLog_MixedLogKeepsRowsIndependent(t *testing.T) {
	got := renderApprovalLog([]ApprovalLog{
		{Tool: "bash", Key: "go test ./...", Decision: "allow-session", By: "ops@example.com"},
		{Tool: "edit", Key: "internal/auth/session.go", Decision: "allow-once"},
		{Tool: "fetch", Key: "https://internal.example/admin", Decision: "deny", By: "sre-oncall"},
	})
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header plus 3 rows, got %d lines: %q", len(lines), got)
	}
	for i, want := range []string{
		"  • bash — go test ./... [allow-session] by ops@example.com",
		"  • edit — internal/auth/session.go [allow-once]",
		"  • fetch — https://internal.example/admin [deny] by sre-oncall",
	} {
		if lines[i+1] != want {
			t.Errorf("row %d:\n got %q\nwant %q", i, lines[i+1], want)
		}
	}
}

// TestApprovalLog_EmptyIsUnchanged guards the no-rows branch, which
// has no field to render either way.
func TestApprovalLog_EmptyIsUnchanged(t *testing.T) {
	if got, want := renderApprovalLog(nil), "/permissions: no approvals recorded this session"; got != want {
		t.Errorf("renderApprovalLog(nil) = %q, want %q", got, want)
	}
}
