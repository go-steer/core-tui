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
	"testing"
	"time"
)

// TestStatusCache_StaysFresh is the assertion the header memo is worth
// having only because of.
//
// The failure a keyed cache invites is not a slow header, it is a
// header that has quietly stopped describing the session — the model
// chip still naming the model the operator switched away from, the
// spend block frozen at the figures from four turns ago. That happens
// the moment renderStatusLine reads a value statusKey does not carry,
// and nothing about either declaration makes that visible: they are
// two lists in two files that have to agree.
//
// So the assertion is differential rather than expected-value. Move
// one input, then compare the memoized header against the same Model
// rendered with the cache switched off. A key missing a field shows up
// on the step that moves that field, naming it. Adding a segment to
// the status line and forgetting to key on it is caught here as long
// as a step moves it, which is the reason the table below is written
// as one step per key field rather than as a plausible session.
//
// It cannot catch a field nobody ever moves, and until issue #223 the
// working directory was exactly that: displayCwd read it back out of
// the process on every call, which a test has no business changing, so
// the cwd leg of the key was asserted by inspection rather than here.
// It is a plain Model field now, resolved once in NewModel, so the
// steps below move it like any other input and the carve-out is gone.
func TestStatusCache_StaysFresh(t *testing.T) {
	m := benchModel(t, 3, 100, 40)

	steps := []struct {
		name string
		do   func(m *Model)
	}{
		{"initial", func(*Model) {}},
		{"narrow", func(m *Model) { *m = resizeModel(*m, 60, 24) }},
		{"widen", func(m *Model) { *m = resizeModel(*m, 140, 40) }},
		{"theme swap", func(m *Model) { m.applyNamedTheme(BuiltinThemes()[1].Name) }},
		{"theme swap back", func(m *Model) { m.applyNamedTheme(BuiltinThemes()[0].Name) }},
		{"light mode", func(m *Model) { m.styles = NewStylesWithTheme(false, m.styles.Theme) }},
		{"wordmark", func(m *Model) { m.opts.Branding.Wordmark = "acme" }},
		{"identity", func(m *Model) { m.opts.Branding.AgentIdentity = "acme-agent" }},
		{"model name", func(m *Model) { m.currentModel = "a-model" }},
		{"model name again", func(m *Model) { m.currentModel = "another-model" }},
		{"model from the host snapshot", func(m *Model) { m.hostSnap.modelName = "snapshot-model" }},
		{"provider", func(m *Model) { m.pushedProvider = "a-provider" }},
		{"provider again", func(m *Model) { m.pushedProvider = "another-provider" }},
		{"cwd", func(m *Model) { m.cwd = "~/projects/core-tui" }},
		{"cwd again", func(m *Model) { m.cwd = "/srv/build/core-tui" }},
		{"cwd unresolvable", func(m *Model) { m.cwd = "" }},
		// The chip is only drawn when the host wired a setter, so the
		// wiring and the mode are two separate inputs and both are keyed.
		{"permission chip wired", func(m *Model) {
			m.opts.PermissionMode.Set = func(PermissionMode) error { return nil }
		}},
		{"permission mode", func(m *Model) { m.permMode = PermissionModeAcceptEdits }},
		{"permission mode again", func(m *Model) { m.permMode = PermissionModePlan }},
		{"usage wired", func(m *Model) {
			m.opts.UsageTracker = &bareTracker{totals: Usage{InputTokens: 1200, OutputTokens: 340}, cost: 0.0123}
			m.hostSnap.hasUsage = true
			m.hostSnap.totals = Usage{InputTokens: 1200, OutputTokens: 340}
			m.hostSnap.cost = 0.0123
		}},
		{"usage moves", func(m *Model) {
			m.hostSnap.totals = Usage{InputTokens: 9400, OutputTokens: 2100}
			m.hostSnap.cost = 0.4567
		}},
		{"context window appears", func(m *Model) {
			m.hostSnap.winUsed, m.hostSnap.winSize = 12_000, 200_000
		}},
		{"context window fills", func(m *Model) {
			m.hostSnap.winUsed = 190_000
		}},
		{"slash in flight", func(m *Model) {
			m.inFlightSlash = &slashFlight{name: "compact", startedAt: time.Unix(0, 0)}
		}},
		{"a different slash in flight", func(m *Model) {
			m.inFlightSlash = &slashFlight{name: "reload", startedAt: time.Unix(0, 0)}
		}},
		{"slash lands", func(m *Model) { m.inFlightSlash = nil }},
	}

	for _, step := range steps {
		step.do(&m)
		if got, want := m.renderHeader(), uncachedHeader(m); got != want {
			t.Fatalf("after %q the memoized header is stale\n memoized: %q\n    fresh: %q",
				step.name, got, want)
		}
	}
}

// TestStatusCache_HitsOnASettledFrame is the cost claim, from the
// other side: a header drawn twice with nothing moving must build
// once. Without it the key could grow a field that changes every
// frame — a timestamp, a spinner phase — and every assertion above
// would stay green while the cache never hit again.
func TestStatusCache_HitsOnASettledFrame(t *testing.T) {
	m := benchModel(t, 3, 100, 40)
	first := m.renderHeader()

	before := *m.statusCache
	for i := range 20 {
		if got := m.renderHeader(); got != first {
			t.Fatalf("draw %d returned a different header from draw 0", i)
		}
	}
	if m.statusCache.rendered != before.rendered || m.statusCache.key != before.key {
		t.Error("a settled frame rebuilt the header; the key is moving when the session is not")
	}
}

// uncachedHeader renders the header the long way. renderHeader has a
// value receiver, so blanking the cache pointer on the copy is enough
// to force the miss without disturbing the caller's model.
func uncachedHeader(m Model) string {
	m.statusCache = nil
	return m.renderHeader()
}
