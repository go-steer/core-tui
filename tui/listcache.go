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
//   - version mismatch → invalidate that entry, render fresh
//   - cache hit       → reuse content, skip the render
//
// Without this, every refreshViewport (stream chunk, spinner tick,
// resize, slash dispatch) re-Glamour-rendered every assistant
// message in history. With 50+ messages and ~10ms per render, that
// scales O(turns × n_messages) and visibly stutters.
//
// Entries are keyed by (identity, width), not by identity alone
// (issue #104). The cache used to pin one width and drop EVERY
// entry the moment the viewport width changed, which made a
// terminal drag — a stream of width-changing WindowSizeMsg events
// — pay a fully cold reassembly per event, and pay it again on the
// way back when the operator dragged the pane to a width the cache
// had already rendered at. Keying by width means a message already
// rendered at the incoming width survives the resize; retainedWidths
// bounds the extra memory that buys.

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
	// approx marks content carried over from a DIFFERENT width
	// while a resize reflow is pending (issue #104). Rows off
	// screen during a drag are not worth re-assembling on every
	// event — their wrapping is deferred anyway — so the cache
	// parks the previous width's render under the new width and
	// flags it. get() refuses to serve an approximate entry, so a
	// row that scrolls into view re-renders exactly; the warm pass
	// retires the rest a slice at a time.
	approx bool
}

// listCacheKey pins an entry to one (item, width) pair. A drag
// across a handful of columns therefore accumulates one entry per
// visited width per message rather than invalidating everything.
type listCacheKey struct {
	id    uint64
	width int
}

// retainedWidths caps how many distinct viewport widths the cache
// keeps entries for. A drag walks through dozens of widths; keeping
// all of them would grow the memo without bound (and every stale
// width is dead weight the moment the drag settles). Three is
// enough to cover the settled width plus the last two the operator
// swept through — which is what a jittery drag actually revisits.
const retainedWidths = 3

// listCache is the per-Model render memo, keyed by (Item.Identity(),
// width). Per-entry invalidation happens on version mismatch;
// widths beyond retainedWidths are evicted least-recently-used.
type listCache struct {
	// width is the most recent width put/reset saw. Kept so
	// callers that want the pin (tests, diagnostics) can read it;
	// lookups no longer consult it.
	width int
	// widths is the MRU list of live widths, newest first, capped
	// at retainedWidths.
	widths  []int
	entries map[listCacheKey]listCacheEntry
}

// newListCache returns an empty cache. The width is recorded
// lazily on the first store so the cache starts oblivious to
// viewport size.
func newListCache() *listCache {
	return &listCache{entries: map[listCacheKey]listCacheEntry{}}
}

// get returns the cached render for item at width, or "" + false
// on miss / version mismatch. A width the cache has no entries for
// is simply a miss — it does NOT evict the other widths. On miss,
// the caller is expected to Render and store via put.
func (c *listCache) get(item Item, width int) (string, bool) {
	entry, ok := c.entries[listCacheKey{id: item.Identity(), width: width}]
	if !ok || entry.approx {
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
	c.touchWidth(width)
	c.entries[listCacheKey{id: item.Identity(), width: width}] = listCacheEntry{
		width:   width,
		version: item.Version(),
		frozen:  item.Finished(),
		content: content,
	}
}

// getStale returns the freshest cached render for item at ANY
// retained width, approximate entries included. Only the resize
// path calls it, and only for rows outside the visible window
// (issue #104): re-assembling a row the operator cannot see, at a
// width whose Glamour pass has been deferred anyway, is work that
// buys nothing. The version check still applies — a row whose
// CONTENT changed is never served from a stale entry.
func (c *listCache) getStale(item Item) (string, bool) {
	id := item.Identity()
	version := item.Version()
	// c.widths is MRU-ordered, so this finds the most recently
	// rendered width first.
	for _, w := range c.widths {
		if entry, ok := c.entries[listCacheKey{id: id, width: w}]; ok && entry.version == version {
			return entry.content, true
		}
	}
	return "", false
}

// putStale parks content under width flagged approximate. get()
// will not serve it; the caller owes an exact render before the
// resize reflow retires (warmReflowSlice → dropApprox).
func (c *listCache) putStale(item Item, width int, content string) {
	c.touchWidth(width)
	c.entries[listCacheKey{id: item.Identity(), width: width}] = listCacheEntry{
		width:   width,
		version: item.Version(),
		frozen:  item.Finished(),
		content: content,
		approx:  true,
	}
}

// dropApprox removes every approximate entry for id, at any
// retained width, and reports whether it removed any. The resize
// warm pass uses the return value to charge its per-slice budget:
// retiring an approximate entry means the next paint pays a real
// render for that row, so it counts as work.
func (c *listCache) dropApprox(id uint64) bool {
	dropped := false
	for _, w := range c.widths {
		key := listCacheKey{id: id, width: w}
		if entry, ok := c.entries[key]; ok && entry.approx {
			delete(c.entries, key)
			dropped = true
		}
	}
	return dropped
}

// touchWidth promotes width to the front of the MRU list and
// evicts every entry belonging to a width that falls off the end.
// Eviction is O(entries) but only runs when a drag crosses into a
// fourth width, and it walks a map the drag was about to shrink
// anyway.
func (c *listCache) touchWidth(width int) {
	c.width = width
	for i, w := range c.widths {
		if w == width {
			copy(c.widths[1:i+1], c.widths[:i])
			c.widths[0] = width
			return
		}
	}
	c.widths = append([]int{width}, c.widths...)
	if len(c.widths) <= retainedWidths {
		return
	}
	evicted := c.widths[retainedWidths:]
	c.widths = c.widths[:retainedWidths]
	for key := range c.entries {
		for _, w := range evicted {
			if key.width == w {
				delete(c.entries, key)
				break
			}
		}
	}
}

// reset clears every entry and re-pins to width. Called when the
// host explicitly invalidates every render (theme change rebuilding
// Glamour, /clear, transcript resume re-keying the ID space) — NOT
// on a plain width change, which is now handled per entry.
func (c *listCache) reset(width int) {
	c.width = width
	c.widths = []int{width}
	c.entries = map[listCacheKey]listCacheEntry{}
}

// drop removes an entry by identity at every retained width. Used
// when a specific item's source data changed in a way the version
// counter can't capture (e.g. style change on just that role).
func (c *listCache) drop(id uint64) {
	for _, w := range c.widths {
		delete(c.entries, listCacheKey{id: id, width: w})
	}
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
