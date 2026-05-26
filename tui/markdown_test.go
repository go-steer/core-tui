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

import "testing"

// TestSplitAtSafeBoundary pins the incremental-render boundary logic:
// stable prefix is everything up to the latest \n\n outside an open
// code fence; trailing is the in-flight chunk.
func TestSplitAtSafeBoundary(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantStable   string
		wantTrailing string
	}{
		{
			name:         "no boundary yet",
			input:        "Just one paragraph, no double-newline yet",
			wantStable:   "",
			wantTrailing: "Just one paragraph, no double-newline yet",
		},
		{
			name:         "one boundary",
			input:        "First para.\n\nSecond para in progress",
			wantStable:   "First para.\n\n",
			wantTrailing: "Second para in progress",
		},
		{
			name:         "multiple boundaries — picks latest",
			input:        "Para 1.\n\nPara 2.\n\nPara 3 streaming",
			wantStable:   "Para 1.\n\nPara 2.\n\n",
			wantTrailing: "Para 3 streaming",
		},
		{
			name:         "open code fence — boundary inside fence is unsafe",
			input:        "Header\n\n```go\nfunc x() {\n\nstill inside fence",
			wantStable:   "Header\n\n",
			wantTrailing: "```go\nfunc x() {\n\nstill inside fence",
		},
		{
			name:         "closed code fence — boundary after close is safe",
			input:        "Header\n\n```\ncode\n```\n\nNext para streaming",
			wantStable:   "Header\n\n```\ncode\n```\n\n",
			wantTrailing: "Next para streaming",
		},
		{
			name:         "only boundary is inside open fence — no safe split",
			input:        "```go\nfunc foo() {\n\nstill open",
			wantStable:   "",
			wantTrailing: "```go\nfunc foo() {\n\nstill open",
		},
		{
			name:         "boundary at end (trailing empty)",
			input:        "Para complete.\n\n",
			wantStable:   "Para complete.\n\n",
			wantTrailing: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStable, gotTrailing := splitAtSafeBoundary(tc.input)
			if gotStable != tc.wantStable {
				t.Errorf("stable = %q, want %q", gotStable, tc.wantStable)
			}
			if gotTrailing != tc.wantTrailing {
				t.Errorf("trailing = %q, want %q", gotTrailing, tc.wantTrailing)
			}
		})
	}
}
