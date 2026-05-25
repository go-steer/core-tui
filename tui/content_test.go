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
	"context"
	"iter"
	"testing"
)

// TestContentRunner_FeatureDetectedByAssertion pins R-CHAT-12: a
// host whose agent satisfies the ContentRunner interface is
// detectable via type assertion the same way every other capability
// is. The TUI never reaches into this in the default submit flow —
// the assertion + invocation are the host's job until a future TUI
// affordance triggers RunWithContents.
func TestContentRunner_FeatureDetectedByAssertion(t *testing.T) {
	var withContents Agent = &contentAgent{}
	var withoutContents Agent = stubAgent{}

	if _, ok := withContents.(ContentRunner); !ok {
		t.Errorf("contentAgent should satisfy ContentRunner")
	}
	if _, ok := withoutContents.(ContentRunner); ok {
		t.Errorf("stubAgent should NOT satisfy ContentRunner")
	}
}

// TestContentRunner_RoundTripsThroughInterface pins that an agent
// implementing ContentRunner can be invoked through the interface
// (sanity check that the iter.Seq2 signature isn't mistyped).
func TestContentRunner_RoundTripsThroughInterface(t *testing.T) {
	agent := &contentAgent{}
	contents := []Content{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "hi", Parts: []ContentPart{
			{Kind: "tool_call", Data: map[string]any{"name": "Read"}},
		}},
	}

	var got int
	for ev, err := range ContentRunner(agent).RunWithContents(context.Background(), contents) {
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		got++
		_ = ev
	}
	if got != len(contents) {
		t.Errorf("yielded %d events, want %d", got, len(contents))
	}
	if agent.lastLen != len(contents) {
		t.Errorf("agent saw %d contents, want %d", agent.lastLen, len(contents))
	}
}

// contentAgent satisfies both Agent and ContentRunner — yields one
// Text event per Content fragment for the test to count.
type contentAgent struct {
	lastLen int
}

func (a *contentAgent) Run(_ context.Context, _ string) iter.Seq2[Event, error] {
	return func(_ func(Event, error) bool) {}
}
func (a *contentAgent) RunWithContents(_ context.Context, contents []Content) iter.Seq2[Event, error] {
	a.lastLen = len(contents)
	return func(yield func(Event, error) bool) {
		for _, c := range contents {
			if !yield(Event{Text: c.Text}, nil) {
				return
			}
		}
	}
}
