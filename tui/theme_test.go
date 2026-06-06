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
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestBuiltinThemes_AllPopulated guards against a future contributor
// adding a registry entry but forgetting the Build closure or the
// Name slug — both would crash the picker.
func TestBuiltinThemes_AllPopulated(t *testing.T) {
	themes := BuiltinThemes()
	if len(themes) == 0 {
		t.Fatal("BuiltinThemes returned empty list")
	}
	seen := map[string]bool{}
	for i, bt := range themes {
		if bt.Name == "" {
			t.Errorf("BuiltinThemes[%d]: empty Name", i)
		}
		if bt.Build == nil {
			t.Errorf("BuiltinThemes[%d] (%s): nil Build", i, bt.Name)
			continue
		}
		if seen[bt.Name] {
			t.Errorf("BuiltinThemes: duplicate Name %q", bt.Name)
		}
		seen[bt.Name] = true
		built := bt.Build(true)
		if built.Primary == nil || built.Secondary == nil || built.Accent == nil {
			t.Errorf("BuiltinThemes[%s].Build(true): missing brand slot (Primary=%v Secondary=%v Accent=%v)",
				bt.Name, built.Primary, built.Secondary, built.Accent)
		}
	}
}

// TestThemeByName_CaseInsensitive verifies the lookup matches
// against the registry without caring about case — operators
// don't have to remember whether `/theme Google` or `/theme google`.
func TestThemeByName_CaseInsensitive(t *testing.T) {
	for _, name := range []string{"google", "Google", "GOOGLE", "GoOgLe"} {
		got := ThemeByName(name, true)
		if got.Name != "google" {
			t.Errorf("ThemeByName(%q, true).Name = %q, want %q", name, got.Name, "google")
		}
	}
}

// TestThemeByName_UnknownFallsBackToDefault — a stale persisted
// name or a typo in /theme <name> must not strand the operator
// on a half-painted UI.
func TestThemeByName_UnknownFallsBackToDefault(t *testing.T) {
	got := ThemeByName("does-not-exist", true)
	if got.Name != "default" {
		t.Errorf("ThemeByName(\"does-not-exist\", true).Name = %q, want %q", got.Name, "default")
	}
	// Same in light mode — make sure the dark argument is forwarded.
	gotLight := ThemeByName("nope", false)
	if gotLight.Name != "default" {
		t.Errorf("ThemeByName(\"nope\", false).Name = %q, want %q", gotLight.Name, "default")
	}
}

// TestThemeByName_EmptyFallsBackToDefault — zero value of
// Options.InitialThemeName / m.themeName routes through ThemeByName
// (when called) and must not panic.
func TestThemeByName_EmptyFallsBackToDefault(t *testing.T) {
	got := ThemeByName("", true)
	if got.Name != "default" {
		t.Errorf("ThemeByName(\"\", true).Name = %q, want %q", got.Name, "default")
	}
}

// TestGoogleAndGopherTinted asserts the two new themes actually
// override the brand slots — guards against an accidental
// regression that would make /theme google indistinguishable
// from /theme default.
func TestGoogleAndGopherTinted(t *testing.T) {
	def := DefaultTheme(true)
	google := GoogleTheme(true)
	gopher := GopherTheme(true)
	if google.Primary == def.Primary {
		t.Error("GoogleTheme(true).Primary equals DefaultTheme.Primary — theme is not tinted")
	}
	if gopher.Primary == def.Primary {
		t.Error("GopherTheme(true).Primary equals DefaultTheme.Primary — theme is not tinted")
	}
	if google.Primary == gopher.Primary {
		t.Error("Google + Gopher have identical Primary — palettes weren't differentiated")
	}
}

// TestWordmarkSequencePresence — every theme that's supposed to
// have a multicolor wordmark must define one, and every theme
// that's NOT supposed to must leave it nil (so a contributor
// touching one theme can't accidentally regress others). Update
// `wantSeq` when adding/removing wordmark themes.
func TestWordmarkSequencePresence(t *testing.T) {
	wantSeq := map[string]bool{
		"google":    true,
		"gke":       true,
		"gopher":    true,
		"matrix":    true,
		"pride":     true,
		"cyberpunk": true,
		"vaporwave": true,
		"christmas": true,
	}
	for _, bt := range BuiltinThemes() {
		built := bt.Build(true)
		hasSeq := built.WordmarkSequence != nil
		want := wantSeq[bt.Name]
		if hasSeq != want {
			t.Errorf("BuiltinThemes[%s]: WordmarkSequence presence = %v, want %v", bt.Name, hasSeq, want)
		}
	}
	// Google specifically: 6 entries for the iconic B-R-Y-B-G-R
	// logo sequence. Other multicolor themes pick their own
	// length (Cyberpunk = 3, Christmas = 2, etc. — those are
	// design calls, not invariants).
	if g := GoogleTheme(true); len(g.WordmarkSequence) != 6 {
		t.Errorf("GoogleTheme WordmarkSequence: want 6 entries (B-R-Y-B-G-R), got %d", len(g.WordmarkSequence))
	}
}

// TestRenderWordmark_NoSequenceFallsBackToWordmarkStyle — when
// the theme has no WordmarkSequence, RenderWordmark must produce
// the same output as the existing Wordmark style. Guards against
// a regression where the new path overtakes themes that opted
// out.
func TestRenderWordmark_NoSequenceFallsBackToWordmarkStyle(t *testing.T) {
	s := NewStylesWithTheme(true, DefaultTheme(true))
	want := s.Wordmark.Render("core-tui")
	got := s.RenderWordmark("core-tui")
	if got != want {
		t.Errorf("RenderWordmark without sequence: want single-color path output\n  want %q\n  got  %q", want, got)
	}
}

// TestRenderWordmark_SequenceProducesDifferentOutput — sanity
// check that the multicolor path actually differs from the
// fallback. Doesn't assert on exact bytes (ANSI sequences are
// brittle); just that the two paths diverge.
func TestRenderWordmark_SequenceProducesDifferentOutput(t *testing.T) {
	s := NewStylesWithTheme(true, GoogleTheme(true))
	multi := s.RenderWordmark("core-tui")
	single := s.Wordmark.Render("core-tui")
	if multi == single {
		t.Error("RenderWordmark with sequence produced same output as Wordmark.Render — multicolor path didn't activate")
	}
}

// TestGKESignature — guards the two GKE-specific signatures: the
// R-B-G-Y wordmark sequence (mirrors the GKE hexagonal icon's
// clockwise quadrant order from top) and the ⎈ helm prompt glyph
// (Unicode K8s logo character). If a contributor reorders the
// sequence or drops the helm, GKE stops being identifiable.
func TestGKESignature(t *testing.T) {
	g := GKETheme(true)
	if len(g.WordmarkSequence) != 4 {
		t.Fatalf("GKETheme WordmarkSequence: want 4 entries (R-B-G-Y), got %d", len(g.WordmarkSequence))
	}
	// Order assertion: R-B-G-Y from the GKE icon's clockwise
	// quadrants (top-red, right-blue, bottom-green, left-yellow).
	// Direct interface comparison matches the pattern in
	// TestGoogleAndGopherTinted; lipgloss.Color values
	// constructed with the same hex literal compare equal as
	// color.Color interfaces.
	want := []color.Color{
		lipgloss.Color("#EA4335"), // R — top
		lipgloss.Color("#4285F4"), // B — right
		lipgloss.Color("#34A853"), // G — bottom
		lipgloss.Color("#FBBC04"), // Y — left
	}
	for i, c := range g.WordmarkSequence {
		if c != want[i] {
			t.Errorf("GKETheme WordmarkSequence[%d] = %v, want %v", i, c, want[i])
		}
	}
	if g.PromptGlyph != "⎈ " {
		t.Errorf("GKETheme PromptGlyph = %q, want %q", g.PromptGlyph, "⎈ ")
	}
}

// TestBuiltinThemes_IncludesNewThemes — the registry order is
// the picker's display order; the user-facing themes should be
// near the top (after "default") so they're scannable.
func TestBuiltinThemes_IncludesNewThemes(t *testing.T) {
	// First 4 positions are the "serious" head: default, the
	// Google family (google + gke), then gopher. Keep these
	// scannable at the top of the picker.
	want := []string{"default", "google", "gke", "gopher"}
	got := BuiltinThemes()
	if len(got) < len(want) {
		t.Fatalf("BuiltinThemes returned %d entries, want at least %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("BuiltinThemes[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}
}
