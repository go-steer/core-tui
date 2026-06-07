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
	"sync"
	"testing"
)

// TestNotifier_BasicFlow exercises the happy path: NewNotifier
// → Notify(text) → drain envelope from the channel → text
// matches + zero dropped.
func TestNotifier_BasicFlow(t *testing.T) {
	n := NewNotifier()
	n.Notify("hello")
	select {
	case env := <-n.ch:
		if env.text != "hello" {
			t.Errorf("envelope.text = %q, want %q", env.text, "hello")
		}
		if env.dropped != 0 {
			t.Errorf("envelope.dropped = %d, want 0", env.dropped)
		}
	default:
		t.Fatal("Notify did not enqueue (channel empty)")
	}
}

// TestNotifier_EmptyTextIgnored — Notify("") is a no-op. Guards
// the channel against empty rows that would render as a blank
// notice line in the chat.
func TestNotifier_EmptyTextIgnored(t *testing.T) {
	n := NewNotifier()
	n.Notify("")
	select {
	case env := <-n.ch:
		t.Errorf("empty Notify should be no-op, got envelope %+v", env)
	default:
		// expected — nothing enqueued
	}
}

// TestNotifier_DropAndCoalesce — fill the buffer past capacity,
// then drain one slot, and verify the next successful Notify
// carries the coalesced dropped count.
func TestNotifier_DropAndCoalesce(t *testing.T) {
	n := NewNotifier()
	// Fill the buffer with notifyBufferSize successful enqueues.
	for i := 0; i < notifyBufferSize; i++ {
		n.Notify("filler")
	}
	// 3 more should drop (buffer is full).
	n.Notify("dropped-1")
	n.Notify("dropped-2")
	n.Notify("dropped-3")

	// Drain one slot to make room.
	<-n.ch

	// The next Notify enqueues and should carry dropped=3.
	n.Notify("coalesced")
	// Drain all filler entries until we find our "coalesced"
	// envelope (or run out, which is a test failure).
	found := drainUntil(t, n, "coalesced")
	if found.dropped != 3 {
		t.Errorf("envelope.dropped = %d, want 3 (coalesced from 3 drops)", found.dropped)
	}

	// After a successful enqueue, the internal dropped counter must
	// reset — the next Notify carries dropped=0.
	n.Notify("after-coalesce")
	got := drainUntil(t, n, "after-coalesce")
	if got.dropped != 0 {
		t.Errorf("envelope.dropped after coalesce reset = %d, want 0", got.dropped)
	}
}

// drainUntil pulls envelopes from the notifier's channel until
// it finds one whose text matches `want`, or fails the test if
// it never appears. Used by TestNotifier_DropAndCoalesce to walk
// past filler envelopes to the assertion target.
func drainUntil(t *testing.T, n *Notifier, want string) noticeEnvelope {
	t.Helper()
	for i := 0; i < notifyBufferSize+1; i++ {
		select {
		case env := <-n.ch:
			if env.text == want {
				return env
			}
		default:
			t.Fatalf("never found envelope with text %q", want)
		}
	}
	t.Fatalf("never found envelope with text %q within %d reads", want, notifyBufferSize+1)
	return noticeEnvelope{}
}

// TestNotifier_CloseSilentDrop — after close(), Notify silently
// drops instead of panicking (which a closed-channel send would
// do without the guard).
func TestNotifier_CloseSilentDrop(t *testing.T) {
	n := NewNotifier()
	n.close()
	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Notify after close panicked: %v", r)
		}
	}()
	n.Notify("after-close")
}

// TestNotifier_CloseIdempotent — calling close() twice is safe
// (no panic on the second call from a closed channel).
func TestNotifier_CloseIdempotent(t *testing.T) {
	n := NewNotifier()
	n.close()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second close panicked: %v", r)
		}
	}()
	n.close()
}

// TestNotifier_ConcurrentNotify — slam Notify from many
// goroutines simultaneously. Should not panic, should not
// deadlock; the resulting envelope count must equal min(N, buffer).
func TestNotifier_ConcurrentNotify(t *testing.T) {
	n := NewNotifier()
	const fanout = 100
	var wg sync.WaitGroup
	wg.Add(fanout)
	for i := 0; i < fanout; i++ {
		go func() {
			defer wg.Done()
			n.Notify("burst")
		}()
	}
	wg.Wait()

	// Drain everything we can — expect exactly notifyBufferSize
	// envelopes (the rest dropped).
	drained := 0
	for {
		select {
		case <-n.ch:
			drained++
		default:
			if drained != notifyBufferSize {
				t.Errorf("drained %d envelopes after burst, want %d", drained, notifyBufferSize)
			}
			return
		}
	}
}

// TestRoleString_NoticeRoundtrip — RoleNotice serializes to
// "notice" + roundtrips through roleFromString without becoming
// RoleSystem (which would happen if the lookup missed).
func TestRoleString_NoticeRoundtrip(t *testing.T) {
	if got := roleString(RoleNotice); got != "notice" {
		t.Errorf("roleString(RoleNotice) = %q, want %q", got, "notice")
	}
	if got := roleFromString("notice"); got != RoleNotice {
		t.Errorf("roleFromString(\"notice\") = %v, want RoleNotice", got)
	}
}

// TestRenderMessage_NoticeUsesDiamondGlyph — the rendered
// RoleNotice row must contain the ◇ glyph (the operator-facing
// distinguisher from RoleSystem's ℹ).
func TestRenderMessage_NoticeUsesDiamondGlyph(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark})
	m.viewport.SetWidth(80)
	out := m.renderMessage(Message{Role: RoleNotice, Text: "hello-notice"})
	if !strings.Contains(out, "◇") {
		t.Errorf("RoleNotice render missing ◇ glyph; got: %q", out)
	}
	if !strings.Contains(out, "hello-notice") {
		t.Errorf("RoleNotice render missing text; got: %q", out)
	}
}
