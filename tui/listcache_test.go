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

// fakeItem implements Item for cache exercising.
type fakeItem struct {
	id       uint64
	version  uint64
	finished bool
	content  string
}

func (f *fakeItem) Identity() uint64              { return f.id }
func (f *fakeItem) Version() uint64               { return f.version }
func (f *fakeItem) Finished() bool                { return f.finished }
func (f *fakeItem) Render(_ *Model, _ int) string { return f.content }

func TestListCache_HitMissInvalidation(t *testing.T) {
	c := newListCache()
	a := &fakeItem{id: 1, version: 1, finished: true, content: "first"}

	// First lookup: miss.
	if _, ok := c.get(a, 80); ok {
		t.Fatalf("expected miss on first get")
	}
	c.put(a, 80, "rendered-A-80")

	// Same width + version: hit.
	got, ok := c.get(a, 80)
	if !ok || got != "rendered-A-80" {
		t.Fatalf("expected hit, got (%q, %v)", got, ok)
	}

	// Width change: drop everything.
	if _, ok := c.get(a, 100); ok {
		t.Fatalf("expected miss on width change")
	}
	c.put(a, 100, "rendered-A-100")
	if _, ok := c.get(a, 80); ok {
		t.Fatalf("expected miss back at width=80 — cache should have been reset")
	}

	// Version bump invalidates that entry but not the cache.
	c.put(a, 100, "rendered-A-100")
	a.version = 2
	if _, ok := c.get(a, 100); ok {
		t.Fatalf("expected miss after version bump")
	}
	c.put(a, 100, "rendered-A-100-v2")
	got, _ = c.get(a, 100)
	if got != "rendered-A-100-v2" {
		t.Fatalf("expected refreshed content, got %q", got)
	}
}

func TestListCache_FrozenSkipsRender(t *testing.T) {
	c := newListCache()
	f := &fakeItem{id: 7, version: 1, finished: true, content: "x"}
	c.put(f, 60, "frozen-content")

	// Multiple gets at same version should keep returning the
	// cached content (frozen entries don't expire on read).
	for i := 0; i < 5; i++ {
		got, ok := c.get(f, 60)
		if !ok || got != "frozen-content" {
			t.Fatalf("get #%d: expected (frozen-content, true), got (%q, %v)", i, got, ok)
		}
	}

	// Bumping version on a frozen entry implicitly unfreezes.
	f.version = 99
	if _, ok := c.get(f, 60); ok {
		t.Fatalf("frozen entry should still invalidate on version bump")
	}
}

func TestListCache_DropAndReset(t *testing.T) {
	c := newListCache()
	a := &fakeItem{id: 1, version: 1, finished: true}
	b := &fakeItem{id: 2, version: 1, finished: true}
	c.put(a, 80, "A")
	c.put(b, 80, "B")
	c.drop(1)
	if _, ok := c.get(a, 80); ok {
		t.Fatalf("expected miss on dropped entry")
	}
	if _, ok := c.get(b, 80); !ok {
		t.Fatalf("non-dropped entry should still hit")
	}
	c.reset(80)
	if _, ok := c.get(b, 80); ok {
		t.Fatalf("expected miss after explicit reset")
	}
}
