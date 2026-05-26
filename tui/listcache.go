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

// Lazy list caching (agentic-tui skill §4). Each history message
// has a stable identity (Message.ID, assigned at Append) and a
// monotonic version (Message.Version, bumped on each mutation
// that changes rendered output — currently SetRendered on resize).
//
// refreshViewport consults the cache before rendering each
// message:
//
//   - cache miss      → render via renderMessage, store entry
//   - width mismatch  → drop the whole cache, render fresh
//   - version mismatch → invalidate that entry, render fresh
//   - cache hit       → reuse content, skip the render
//
// Without this, every refreshViewport (stream chunk, spinner tick,
// resize, slash dispatch) re-Glamour-rendered every assistant
// message in history. With 50+ messages and ~10ms per render, that
// scales O(turns × n_messages) and visibly stutters.

package tui

// Item is the contract for any history entry that can be cached
// by the renderItem cache. The current implementation has only
// one concrete impl (messageItem wrapping a Message), but the
// interface is exposed so future surfaces (search results, code-
// review rows) can opt into the same caching path.
type Item interface {
	// Identity returns the stable opaque key the cache uses to
	// look up the item across refreshes. Two items with the same
	// Identity are considered the same logical entry.
	Identity() uint64

	// Version returns the monotonic mutation counter; cache
	// entries with a different version are invalidated.
	Version() uint64

	// Finished reports whether the item has reached a terminal
	// state. Cache marks finished entries as frozen — even a
	// width-keyed re-render skips work for these unless the
	// content was explicitly invalidated.
	Finished() bool

	// Render returns the styled string for the given viewport
	// width. Called only on cache miss / version bump.
	Render(m *Model, width int) string
}

// Optional capability interfaces — type-assert at use site for
// graceful degradation. Items don't need to implement these to
// participate in the cache; they're hooks for richer behaviors
// the list can layer on (per skill §4.D).

// RawRenderable lets clipboard / transcript paths grab unstyled
// text without ANSI escapes. Falls back to ansi.Strip(Render(...))
// when not implemented.
type RawRenderable interface {
	RawRender(width int) string
}

// Focusable receives focus state from the list (the selected
// row sets it before render). Items use the bit to apply hover
// / selection styling without inline `if focused` branches in
// every Render method.
type Focusable interface {
	SetFocused(bool)
}

// listCacheEntry holds one memoized render. width pins the entry
// to a specific viewport width; version pins it to a specific
// item mutation generation; frozen marks Finished() == true so
// the cache layer can skip the version comparison for entries
// known to be terminal.
type listCacheEntry struct {
	width   int
	version uint64
	frozen  bool
	content string
}

// listCache is the per-Model render memo. Keyed by Item.Identity().
// Dropped wholesale on width change so wrapping is correct; per-
// entry invalidation happens on version mismatch.
type listCache struct {
	width   int
	entries map[uint64]listCacheEntry
}

// newListCache returns an empty cache. The width is recorded
// lazily on the first lookup so the cache starts oblivious to
// viewport size.
func newListCache() *listCache {
	return &listCache{entries: map[uint64]listCacheEntry{}}
}

// get returns the cached render for item at width, or "" + false
// on miss / width mismatch / version mismatch. On miss, the
// caller is expected to Render and store via put.
func (c *listCache) get(item Item, width int) (string, bool) {
	if c.width != width {
		// Width changed — the cache is dead for the new layout.
		// Drop everything and reset the width pin.
		c.reset(width)
		return "", false
	}
	entry, ok := c.entries[item.Identity()]
	if !ok {
		return "", false
	}
	if entry.frozen {
		// Finished items are immutable once cached — version
		// bump on a frozen entry implicitly unfreezes via the
		// version comparison below.
		if entry.version == item.Version() {
			return entry.content, true
		}
	}
	if entry.version != item.Version() {
		return "", false
	}
	return entry.content, true
}

// put stores rendered content for item at width. Marks frozen
// when the item reports Finished — subsequent gets for that
// entry skip straight to the content (until version bumps).
func (c *listCache) put(item Item, width int, content string) {
	if c.width != width {
		c.reset(width)
	}
	c.entries[item.Identity()] = listCacheEntry{
		width:   width,
		version: item.Version(),
		frozen:  item.Finished(),
		content: content,
	}
}

// reset clears every entry and re-pins to width. Called when
// width changes or when the host explicitly invalidates (e.g.
// theme change rebuilding Glamour).
func (c *listCache) reset(width int) {
	c.width = width
	c.entries = map[uint64]listCacheEntry{}
}

// drop removes a single entry by identity. Used when a specific
// item's source data changed in a way the version counter can't
// capture (e.g. style change on just that role).
func (c *listCache) drop(id uint64) {
	delete(c.entries, id)
}

// messageItem wraps a history Message + its position index so it
// can participate in the listCache. Identity is the Message ID
// assigned at Append time; Version is the mutation counter;
// Finished is always true (history messages don't stream — the
// in-progress text renders via the stable-prefix path in
// renderInProgress, never through this cache).
//
// The index is needed so Render can ask the parent Model whether
// the previous message was RoleUser (controls whether the
// separator rule renders above this entry — see refreshViewport).
type messageItem struct {
	msg   Message
	idx   int
	total int
}

func (mi messageItem) Identity() uint64 { return mi.msg.ID }
func (mi messageItem) Version() uint64  { return mi.msg.Version }
func (mi messageItem) Finished() bool   { return true }

// Render delegates to the Model's renderMessage. The cache calls
// this only on miss, so the per-message rendering cost (Glamour,
// word-wrap, lipgloss styling) is paid at most once per
// (message, width) pair.
func (mi messageItem) Render(m *Model, width int) string {
	return m.renderMessage(mi.msg)
}

// RawRender returns the unstyled Text for clipboard / transcript
// paths. RoleAssistant uses Text (not Rendered) so the consumer
// gets clean markdown source instead of ANSI-styled output.
func (mi messageItem) RawRender(_ int) string { return mi.msg.Text }
