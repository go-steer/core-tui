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

// statusKey is every value renderStatusLine and renderHeader read.
// Two frames with the same key produce the same header, so the header
// only has to be built when the key moves.
//
// The fields are the raw inputs, not the styled output: the header's
// cost is not in finding out what to say, it is in saying it.
// Assembling this struct calls the same accessors the renderer does
// and costs 5 allocations; the render it replaces costs 61, almost
// all of them lipgloss styling one short segment at a time and then
// wrapping the join. That ratio is the whole justification for the
// cache — a key that were as expensive as the render would leave
// nothing on the table.
//
// It is a comparable struct rather than a hash or a version counter
// on purpose. A version counter would have to be bumped by every
// place that can move one of these values, which is most of the
// package, and a missed bump shows up as a header that has quietly
// stopped agreeing with the session it describes. A key that IS the
// values cannot go stale: if it compares equal, the inputs are the
// same, and if the inputs are the same the render is too.
type statusKey struct {
	width int

	// The palette. Styles is rebuilt by resolveStyles from exactly
	// these two plus Branding, and Branding is fixed for the life of
	// a Model, so naming the theme and the mode pins the styling.
	theme string
	dark  bool

	wordmark  string
	identity  string
	model     string
	provider  string
	cwd       string
	permWired bool
	permMode  PermissionMode
	usage     string
	slash     string
}

// statusCache holds the last header and the key that produced it.
//
// It lives behind a pointer because Model.View has a value receiver:
// anything View writes to its receiver lands in a copy that is
// discarded on return, so a cache filled during a draw has to be
// reachable through a field that survives the copy. That is only safe
// because the key above is content-derived. A pointer cache keyed on
// a version stamp would be a hazard here — two divergent copies of a
// Model can hold the same stamp with different content, and the
// second one to draw would be handed the first one's header. Keyed on
// the values, divergent copies simply miss and re-render.
type statusCache struct {
	key      statusKey
	rendered string
	valid    bool
}

// statusLineKey collects the current inputs. Kept next to statusKey
// rather than in view.go so that adding a segment to the status line
// and forgetting to key on it is a one-file mistake rather than a
// two-file one.
func (m Model) statusLineKey() statusKey {
	k := statusKey{
		width:     m.width,
		theme:     m.styles.Theme.Name,
		dark:      m.styles.Dark,
		wordmark:  m.wordmark(),
		identity:  m.opts.Branding.AgentIdentity,
		model:     m.displayModelName(),
		provider:  m.displayProvider(),
		cwd:       m.displayCwd(),
		permWired: m.permissionModeWired(),
		permMode:  m.permMode,
		usage:     m.usageSummaryOneLine(),
	}
	if m.inFlightSlash != nil {
		k.slash = m.inFlightSlash.name
	}
	return k
}
