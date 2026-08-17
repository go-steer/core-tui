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
	def := defaultTheme(true)
	google := googleTheme(true)
	gopher := gopherTheme(true)
	if google.Primary == def.Primary {
		t.Error("googleTheme(true).Primary equals defaultTheme.Primary — theme is not tinted")
	}
	if gopher.Primary == def.Primary {
		t.Error("gopherTheme(true).Primary equals defaultTheme.Primary — theme is not tinted")
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
	if g := googleTheme(true); len(g.WordmarkSequence) != 6 {
		t.Errorf("googleTheme WordmarkSequence: want 6 entries (B-R-Y-B-G-R), got %d", len(g.WordmarkSequence))
	}
}

// TestRenderWordmark_NoSequenceFallsBackToWordmarkStyle — when
// the theme has no WordmarkSequence, RenderWordmark must produce
// the same output as the existing Wordmark style. Guards against
// a regression where the new path overtakes themes that opted
// out.
func TestRenderWordmark_NoSequenceFallsBackToWordmarkStyle(t *testing.T) {
	s := NewStylesWithTheme(true, defaultTheme(true))
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
	s := NewStylesWithTheme(true, googleTheme(true))
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
	g := gkeTheme(true)
	if len(g.WordmarkSequence) != 4 {
		t.Fatalf("gkeTheme WordmarkSequence: want 4 entries (R-B-G-Y), got %d", len(g.WordmarkSequence))
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
			t.Errorf("gkeTheme WordmarkSequence[%d] = %v, want %v", i, c, want[i])
		}
	}
	if g.PromptGlyph != "⎈ " {
		t.Errorf("gkeTheme PromptGlyph = %q, want %q", g.PromptGlyph, "⎈ ")
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

// --- theme derivation (issue #116) -----------------------------

// TestNewStylesWithTheme_NormalizesBareThemeLiteral is the reason
// the normalizer sits in NewStylesWithTheme rather than in
// defaultTheme.
//
// Two of the four ways a Theme comes into existence never touch
// defaultTheme: a host handing a composite literal to the exported
// NewStylesWithTheme, and the golden corpus's goldenTheme(). Put
// the derivation in defaultTheme and both of those get a zero
// ChromaStyleName, styles.Get("") silently falls back, and OnPrimary
// stays nil for the one surface that paints on top of Primary.
func TestNewStylesWithTheme_NormalizesBareThemeLiteral(t *testing.T) {
	bare := Theme{Primary: lipgloss.Color("#101010")}
	if bare.OnPrimary != nil || bare.ChromaStyleName != "" {
		t.Fatal("test setup: the literal must start with both derived tokens unset")
	}
	got := NewStylesWithTheme(true, bare).Theme
	if got.ChromaStyleName != defaultChromaStyleName {
		t.Errorf("ChromaStyleName = %q, want %q", got.ChromaStyleName, defaultChromaStyleName)
	}
	if got.OnPrimary == nil {
		t.Error("OnPrimary was not derived — text on a Primary fill has no color")
	}
	// The caller's own value must survive untouched.
	if got.Primary != bare.Primary {
		t.Errorf("Primary = %v, want the caller's %v", got.Primary, bare.Primary)
	}
}

// TestNormalizeTheme_OnPrimaryFollowsLuminance — the derived
// foreground has to flip with the brightness of Primary, or the H1
// banner is unreadable for half the palettes.
func TestNormalizeTheme_OnPrimaryFollowsLuminance(t *testing.T) {
	onDark := NewStylesWithTheme(true, Theme{Primary: lipgloss.Color("#101010")}).Theme.OnPrimary
	onLight := NewStylesWithTheme(true, Theme{Primary: lipgloss.Color("#F5F5DC")}).Theme.OnPrimary
	if onDark == onLight {
		t.Fatalf("OnPrimary did not flip between a near-black and a near-white Primary (both %v)", onDark)
	}
	if relativeLuminance(onDark) < 0.5 {
		t.Errorf("OnPrimary over a near-black Primary = %v, want a light foreground", onDark)
	}
	if relativeLuminance(onLight) > 0.5 {
		t.Errorf("OnPrimary over a near-white Primary = %v, want a dark foreground", onLight)
	}
}

// TestNormalizeTheme_KeepsExplicitValues — "derive, do not require"
// means the derivation is a fallback, never an override. A host that
// spells either token out keeps it.
func TestNormalizeTheme_KeepsExplicitValues(t *testing.T) {
	want := lipgloss.Color("#123456")
	got := NewStylesWithTheme(true, Theme{
		Primary:         lipgloss.Color("#101010"),
		OnPrimary:       want,
		ChromaStyleName: "monokai",
	}).Theme
	if got.OnPrimary != want {
		t.Errorf("OnPrimary = %v, want the explicit %v", got.OnPrimary, want)
	}
	if got.ChromaStyleName != "monokai" {
		t.Errorf("ChromaStyleName = %q, want the explicit %q", got.ChromaStyleName, "monokai")
	}
}

// TestBranding_RederivesOnPrimary — both Branding override sites
// mutate Primary and then call NewStylesWithTheme, so the derived
// foreground must follow the override rather than the base theme's
// Primary. This is the property that let the normalizer live at the
// Styles boundary with no extra call site.
func TestBranding_RederivesOnPrimary(t *testing.T) {
	base := NewStyles(true, Branding{}).Theme.OnPrimary
	// A near-white brand accent has to flip the derived foreground
	// dark; the house violet does not.
	over := NewStyles(true, Branding{AccentColor: "#FAFAFA"}).Theme.OnPrimary
	if base == over {
		t.Fatalf("OnPrimary was not re-derived after a Branding override (both %v)", base)
	}
	if relativeLuminance(over) > 0.5 {
		t.Errorf("OnPrimary over a near-white branded Primary = %v, want a dark foreground", over)
	}
}

// TestBuiltinThemes_ChromaStyleNamesResolve — a typo in a theme's
// ChromaStyleName does not fail loudly (Chroma hands back a fallback
// style), so it has to fail here instead. Both polarities are
// checked: every builtin that names a dark style also has to name
// something legible for a light terminal.
func TestBuiltinThemes_ChromaStyleNamesResolve(t *testing.T) {
	for _, bt := range BuiltinThemes() {
		for _, dark := range []bool{true, false} {
			name := NewStylesWithTheme(dark, bt.Build(dark)).Theme.ChromaStyleName
			if name == "" {
				t.Errorf("%s (dark=%v): ChromaStyleName empty after normalization", bt.Name, dark)
				continue
			}
			if got := chromaStyleByName(name); got == nil || got.Name != name {
				t.Errorf("%s (dark=%v): ChromaStyleName %q is not a registered Chroma style", bt.Name, dark, name)
			}
		}
	}
}

// TestRefreshTheme_InvalidatesBothMarkdownRenderers — refreshTheme
// cleared m.markdown but not m.modalMarkdown. That was invisible
// while Glamour styles were theme-independent; now that markdown is
// built from Theme tokens, a /theme swap with the /btw modal open
// would keep the modal body on the previous palette until its width
// happened to change.
func TestRefreshTheme_InvalidatesBothMarkdownRenderers(t *testing.T) {
	m := NewModel(Options{Agent: &bareAgent{id: "theme"}})
	m.themeName = "matrix"
	m.refreshTheme()
	before := m.ensureModalMarkdown(60)
	m.ensureMarkdown()

	m.themeName = "christmas"
	m.refreshTheme()
	if m.markdown != nil {
		t.Error("refreshTheme left the chat markdown renderer in place")
	}
	if m.modalMarkdown != nil {
		t.Error("refreshTheme left the modal markdown renderer in place — /btw would keep the old palette")
	}

	const doc = "## Section\n\nBody.\n"
	after := m.ensureModalMarkdown(60)
	if before.renderMarkdown(doc) == after.renderMarkdown(doc) {
		t.Error("the rebuilt modal renderer produced identical bytes across a theme swap")
	}
}
