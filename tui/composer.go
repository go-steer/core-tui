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
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// composer is the chat input: a bubbles textarea that keeps its own
// rendered block, so a frame that did not touch the input does not
// pay to draw it again.
//
// The reason it exists is that the textarea is the most expensive
// thing in a frame and the least likely to have changed. It re-wraps
// and re-styles itself from scratch on every View — 826 allocations
// of a 1,022-allocation repaint, four fifths of the whole frame — and
// the cost is HIGHEST when the box is empty, because the placeholder
// path rebuilds the placeholder and its styling every time. That is
// the state the composer is in for the entire duration of a turn,
// while the spinner ticks the frame ten times a second (issue #162).
// So the library was spending most of its per-frame budget drawing an
// empty box that had not moved since the last one.
//
// # Why the render is eager rather than lazy
//
// The obvious shape — a cache consulted by View — is not available
// here. model.View has a value receiver, so anything it writes lands
// in a copy that is thrown away when it returns; a lazy cache would
// have to live behind a pointer shared between every copy of the
// model, and two copies that diverged would then read each other's
// entries. The listCache can afford that because its key carries a
// message identity and version that pin the content exactly. A
// textarea has no such identity, and the failure mode is the worst
// one available to a text editor: a composer that shows something
// other than what the operator typed.
//
// So the block is rendered at mutation time instead of at draw time,
// by every method below that can change what the textarea looks like.
// The rendered string is an ordinary field, copied with the value
// like any other, and View is a field read that cannot be stale
// because nothing can reach the textarea without going through this
// type. Rendering eagerly is not more work: bubbletea calls View
// after every Update, so the old code rendered once per mutation
// anyway. What it stops paying for is every frame that was NOT a
// mutation, which during a streaming turn is all of them.
//
// # Why the textarea is a field and not embedded
//
// Embedding would promote the textarea's own methods, and a caller
// that reached one of the mutating ones directly would change the
// widget without changing the string this type hands out. Holding it
// in an unexported field and forwarding by hand makes that
// unrepresentable rather than merely discouraged: the set of methods
// below IS the set of ways the composer can be touched, and every
// mutating one ends in a re-render. Adding a forwarder later means
// deciding, at that moment, whether it belongs in that set —
// which is exactly the decision that would otherwise be silently
// skipped.
type composer struct {
	ta textarea.Model

	// rendered is ta.View() as of the last mutation. Kept in the
	// value rather than behind a pointer so it travels with the
	// model copy that produced it.
	rendered string

	// ready distinguishes a composer that newComposer built from the
	// zero value one gets from a bare `model{}` literal. bubbles
	// panics rendering an unconstructed textarea — LineInfo indexes
	// into a line slice that textarea.New would have seeded with one
	// empty entry — and a good number of tests build a model by
	// literal to exercise one dialog without standing up a whole
	// widget tree. Those models are never drawn, so there is no
	// block to keep fresh; rendering them was never possible and is
	// not being made possible here. Without this the mere act of
	// moving the render earlier would turn every such test into a
	// panic, which is the cache changing behaviour rather than cost.
	ready bool
}

// newComposer wraps an already-configured textarea. The caller
// configures the widget first — placeholder, prompt, styles, virtual
// cursor — because those are construction concerns and this type has
// no opinion about them; it only has to be the last thing to touch it.
func newComposer(ta textarea.Model) composer {
	c := composer{ta: ta, ready: true}
	c.render()
	return c
}

// render refreshes the cached block. Every mutating method ends in a
// call to this, which is the whole contract of the type.
func (c *composer) render() {
	if !c.ready {
		return
	}
	c.rendered = c.ta.View()
}

// View returns the composer's block. It is a field read: the string
// was produced by the mutation that last changed the widget.
func (c composer) View() string { return c.rendered }

// Update forwards a message to the textarea. Value receiver and
// returned-by-value to match the bubbles convention the call sites
// already use.
func (c composer) Update(msg tea.Msg) (composer, tea.Cmd) {
	ta, cmd := c.ta.Update(msg)
	c.ta = ta
	c.render()
	return c, cmd
}

func (c *composer) SetValue(s string) {
	c.ta.SetValue(s)
	c.render()
}

func (c *composer) Reset() {
	c.ta.Reset()
	c.render()
}

func (c *composer) SetWidth(w int) {
	c.ta.SetWidth(w)
	c.render()
}

func (c *composer) SetHeight(h int) {
	c.ta.SetHeight(h)
	c.render()
}

func (c *composer) SetStyles(s textarea.Styles) {
	c.ta.SetStyles(s)
	c.render()
}

// SetPrompt replaces the prompt rail. bubbles exposes Prompt as a
// plain field and documents that SetWidth has to be called after it
// changes; going through a method is what lets that happen here
// instead of at each of the two call sites, and what gets the block
// re-rendered at all.
func (c *composer) SetPrompt(p string) {
	c.ta.Prompt = p
	if !c.ready {
		return
	}
	// bubbles documents that Prompt changes do not take effect in the
	// wrap arithmetic until SetWidth runs again, and the two call
	// sites — both in refreshTheme, applying a theme's prompt glyph —
	// did not do it. A glyph of a different width than the one it
	// replaced therefore left the textarea wrapping to the old
	// content column until the next resize happened to correct it.
	// Re-applying the current width is the documented fix and belongs
	// here rather than at each call site, which is half the reason
	// the field became a method.
	c.ta.SetWidth(c.ta.Width())
	c.render()
}

// Focus focuses the widget and returns the blink command bubbles
// hands back. The block is re-rendered because focus is visible in
// it — the placeholder and the cursor line are styled differently
// when the widget does not hold the keyboard.
func (c *composer) Focus() tea.Cmd {
	cmd := c.ta.Focus()
	c.render()
	return cmd
}

func (c *composer) Blur() {
	c.ta.Blur()
	c.render()
}

// The read-only forwarders below cannot change what the widget looks
// like, so none of them re-renders.
//
// LineCount is the one to be careful about: bubbles declares it on a
// pointer receiver, so it may touch the widget's internal wrap state.
// It does not change the rendered block — TestComposer_StaysFresh
// drives it alongside everything else and compares against a fresh
// render — but the pointer receiver is why it is grouped here with a
// note rather than left to look like an ordinary accessor.

func (c composer) Value() string       { return c.ta.Value() }
func (c composer) Height() int         { return c.ta.Height() }
func (c composer) Width() int          { return c.ta.Width() }
func (c composer) Focused() bool       { return c.ta.Focused() }
func (c composer) Cursor() *tea.Cursor { return c.ta.Cursor() }
func (c *composer) LineCount() int     { return c.ta.LineCount() }
