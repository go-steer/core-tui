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
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestNewModel_SeedHistory pins that Options.SeedHistory is appended
// into the model's history before the first render.
func TestNewModel_SeedHistory(t *testing.T) {
	seed := []Message{
		{Role: RoleUser, Text: "hello"},
		{Role: RoleAssistant, Text: "hi"},
	}
	m := NewModel(Options{SeedHistory: seed})
	got := m.history.Snapshot()
	if len(got) != len(seed) {
		t.Fatalf("history length = %d, want %d", len(got), len(seed))
	}
	for i, w := range seed {
		if got[i].Role != w.Role || got[i].Text != w.Text {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestUpdate_BackgroundColor_RefreshesStyles pins that the styles
// bundle re-resolves when BackgroundColorMsg arrives.
func TestUpdate_BackgroundColor_RefreshesStyles(t *testing.T) {
	m := NewModel(Options{})
	if !m.styles.Dark {
		t.Fatalf("expected initial styles to be dark; got light")
	}
	// Light background message.
	out, _ := m.Update(tea.BackgroundColorMsg{Color: lightColor{}})
	got := out.(Model)
	if got.styles.Dark {
		t.Errorf("expected styles.Dark=false after light BackgroundColorMsg")
	}
}

// TestUpdate_PermissionMode_Cycles pins that Shift+Tab cycles through
// the four permission modes when the host wired the chip.
func TestUpdate_PermissionMode_Cycles(t *testing.T) {
	var lastSet PermissionMode = -1
	m := NewModel(Options{
		PermissionMode: PermissionModeWiring{
			Initial: PermissionModeDefault,
			Set:     func(mode PermissionMode) error { lastSet = mode; return nil },
		},
	})
	shiftTab := tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	out, cmd := m.Update(shiftTab)
	got := out.(Model)
	// The chip flips on the keystroke; the host callbacks ride the Cmd
	// (issue #137), so Set has not been called yet.
	if got.permMode != PermissionModeAcceptEdits {
		t.Errorf("permMode = %s, want acceptEdits", got.permMode)
	}
	if lastSet != -1 {
		t.Errorf("Set ran on the Update goroutine (got %s)", lastSet)
	}
	if cmd == nil {
		t.Fatal("shift+tab returned no Cmd — the host would never hear about the mode")
	}
	msg, ok := cmd().(permissionModeAppliedMsg)
	if !ok {
		t.Fatalf("shift+tab Cmd produced %T, want permissionModeAppliedMsg", msg)
	}
	if lastSet != PermissionModeAcceptEdits {
		t.Errorf("Set callback received %s, want acceptEdits", lastSet)
	}
	if msg.prev != PermissionModeDefault || msg.mode != PermissionModeAcceptEdits {
		t.Errorf("reply = prev %s / mode %s, want default / acceptEdits", msg.prev, msg.mode)
	}
}

// TestPermissionMode_Next_WrapsAtBypass pins the cycle wraps back to
// default after bypassPermissions.
func TestPermissionMode_Next_WrapsAtBypass(t *testing.T) {
	if PermissionModeBypass.Next() != PermissionModeDefault {
		t.Errorf("Bypass.Next() = %s, want default", PermissionModeBypass.Next())
	}
}

// lightColor is an image/color.Color stand-in whose RGB sums above the
// IsDark threshold so BackgroundColorMsg.IsDark() returns false. The
// exact threshold is private to bubbletea — pure white is the safe
// choice for "light".
type lightColor struct{}

func (lightColor) RGBA() (r, g, b, a uint32) {
	return 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF
}

func TestNewModel_ForceTheme_LightSeedsLightStyles(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeLight})
	if m.styles.Dark {
		t.Errorf("ForceTheme=light should seed light styles, got Dark=true")
	}
}

func TestNewModel_ForceTheme_DarkSeedsDarkStyles(t *testing.T) {
	m := NewModel(Options{ForceTheme: ThemeDark})
	if !m.styles.Dark {
		t.Errorf("ForceTheme=dark should seed dark styles, got Dark=false")
	}
}

func TestUpdate_BackgroundColor_IgnoredWhenForceTheme(t *testing.T) {
	// Operator forced dark; the terminal reports light. The handler
	// must NOT flip to light — explicit choice wins.
	m := NewModel(Options{ForceTheme: ThemeDark})
	out, _ := m.Update(tea.BackgroundColorMsg{Color: lightColor{}})
	got := out.(Model)
	if !got.styles.Dark {
		t.Errorf("ForceTheme=dark should ignore a light BackgroundColorMsg, got Dark=false")
	}
}

func TestUpdate_BackgroundColor_RespectedWhenAutoTheme(t *testing.T) {
	// Sanity check the opposite path: with ForceTheme="" (auto),
	// the handler must still update on BackgroundColorMsg.
	m := NewModel(Options{}) // ForceTheme zero = "auto"
	out, _ := m.Update(tea.BackgroundColorMsg{Color: lightColor{}})
	got := out.(Model)
	if got.styles.Dark {
		t.Errorf("ForceTheme=auto should honor light BackgroundColorMsg")
	}
}

func TestView_MouseOption_DefaultEnablesCellMotion(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(24)
	m.width, m.height = 80, 24
	v := m.View()
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("default Mouse should be MouseModeCellMotion, got %v", v.MouseMode)
	}
}

func TestView_MouseOption_FalseDisablesCapture(t *testing.T) {
	off := false
	m := NewModel(Options{Mouse: &off})
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(24)
	m.width, m.height = 80, 24
	v := m.View()
	if v.MouseMode != tea.MouseModeNone {
		t.Errorf("Mouse=*false should be MouseModeNone, got %v", v.MouseMode)
	}
}

func TestSlashMouse_TogglesAndPropagatesToView(t *testing.T) {
	// Start with mouse on (zero value). /mouse should flip to off,
	// View()'s MouseMode should reflect it.
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(24)
	m.width, m.height = 80, 24

	out, _ := m.dispatchSlash("/mouse")
	m = out.(Model)
	v := m.View()
	if v.MouseMode != tea.MouseModeNone {
		t.Errorf("after /mouse from default-on, expected MouseModeNone, got %v", v.MouseMode)
	}

	// Second /mouse flips back to on.
	out2, _ := m.dispatchSlash("/mouse")
	m = out2.(Model)
	v = m.View()
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("after second /mouse, expected MouseModeCellMotion, got %v", v.MouseMode)
	}
}

// TestContextFillStyle_TracksActiveTheme pins that the three-tier
// context-fill ramp reads its colors from the active Theme's
// semantic tokens (Success / Warning / Error) rather than fixed hex
// literals. Runs one dark theme and two light ones so a palette
// whose green/amber/red differ from the house default — which is
// exactly the case a light terminal needs for contrast — is covered.
func TestContextFillStyle_TracksActiveTheme(t *testing.T) {
	themes := []struct {
		name  string
		dark  bool
		theme Theme
	}{
		{"default dark", true, defaultTheme(true)},
		{"christmas light", false, christmasTheme(false)},
		{"google light", false, googleTheme(false)},
	}
	tiers := []struct {
		name string
		used int
		size int
		want func(Theme) color.Color
		bold bool
	}{
		{"below 60% picks Success", 10, 100, func(th Theme) color.Color { return th.Success }, false},
		{"60-85% picks Warning", 70, 100, func(th Theme) color.Color { return th.Warning }, false},
		{"at or above 85% picks Error", 90, 100, func(th Theme) color.Color { return th.Error }, true},
	}
	for _, tc := range themes {
		for _, tier := range tiers {
			t.Run(tc.name+"/"+tier.name, func(t *testing.T) {
				m := Model{styles: NewStylesWithTheme(tc.dark, tc.theme)}
				got := m.contextFillStyle(tier.used, tier.size)
				want := tier.want(tc.theme)
				if got.GetForeground() != want {
					t.Errorf("contextFillStyle(%d, %d) foreground = %v, want theme token %v",
						tier.used, tier.size, got.GetForeground(), want)
				}
				if got.GetBold() != tier.bold {
					t.Errorf("contextFillStyle(%d, %d) bold = %v, want %v",
						tier.used, tier.size, got.GetBold(), tier.bold)
				}
			})
		}
	}
}

// TestContextFillStyle_DiffersAcrossThemes is the regression the
// hardcoded ramp would fail: every tier must actually change color
// when the operator swaps themes. Christmas (light) picks pine /
// gold / cardinal against the default's #5FD787 / #FFD75F / #FF5F5F,
// so all three tiers have to move.
func TestContextFillStyle_DiffersAcrossThemes(t *testing.T) {
	dark := Model{styles: NewStylesWithTheme(true, defaultTheme(true))}
	light := Model{styles: NewStylesWithTheme(false, christmasTheme(false))}
	tiers := []struct {
		name string
		used int
	}{
		{"success tier", 10},
		{"warning tier", 70},
		{"error tier", 90},
	}
	for _, tier := range tiers {
		d := dark.contextFillStyle(tier.used, 100).GetForeground()
		l := light.contextFillStyle(tier.used, 100).GetForeground()
		if d == l {
			t.Errorf("%s: default-dark and christmas-light both rendered %v — ramp is not theme-aware", tier.name, d)
		}
	}
}

// TestContextFillStyle_UnknownSizeIsMuted keeps the "context window
// size unknown" path on the muted chrome style instead of implying a
// usage tier.
func TestContextFillStyle_UnknownSizeIsMuted(t *testing.T) {
	m := Model{styles: NewStylesWithTheme(true, defaultTheme(true))}
	got := m.contextFillStyle(1000, 0)
	if got.GetForeground() != m.styles.Muted.GetForeground() {
		t.Errorf("contextFillStyle with size=0 foreground = %v, want Muted %v",
			got.GetForeground(), m.styles.Muted.GetForeground())
	}
}

// TestDisplayCwd_ResolvedAtConstruction is issue #223.
//
// displayCwd used to call os.Getwd and os.UserHomeDir every time the
// status header asked for it, which is twice per layout pass at
// whatever rate the spinner ticks — I/O on a path documented as doing
// none. The fix is to resolve it once in NewModel, and the only way to
// tell a resolved-once value from a re-read one is to move the process
// out from under a Model that has already been built and check that
// the header does not follow.
//
// Moving the process is exactly what the golden corpus does to pin the
// header (pinCwd), so the mechanism is the house one; this test just
// moves it a second time, after construction.
func TestDisplayCwd_ResolvedAtConstruction(t *testing.T) {
	pinCwd(t)
	want := "~" + string(filepath.Separator) + "core-tui"

	m := NewModel(Options{Agent: &bareAgent{id: "cwd"}})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = out.(Model)

	if got := m.displayCwd(); got != want {
		t.Fatalf("displayCwd() at construction = %q, want %q", got, want)
	}
	if frame := ansi.Strip(m.View().Content); !strings.Contains(frame, want) {
		t.Fatalf("precondition: the first frame does not carry %q in the status header", want)
	}

	// Move the process. Anything that re-reads the working directory
	// per call answers with this directory from here on.
	moved := filepath.Join(filepath.Dir(mustGetwd(t)), "elsewhere")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatalf("creating the second directory: %v", err)
	}
	t.Chdir(moved)
	if now := mustGetwd(t); now == want {
		t.Fatalf("precondition: the process did not move, still at %q", now)
	}

	for frame := range 3 {
		if got := m.displayCwd(); got != want {
			t.Fatalf("frame %d: displayCwd() = %q, want %q — the value moved after "+
				"construction, so it is still being read back from the process on the "+
				"render path", frame, got, want)
		}
		if got := ansi.Strip(m.renderHeader()); !strings.Contains(got, want) {
			t.Fatalf("frame %d: the status header stopped showing %q after the process "+
				"chdir'd; the header is tracking the live working directory\n%s",
				frame, want, got)
		}
		if got := ansi.Strip(m.View().Content); !strings.Contains(got, want) {
			t.Fatalf("frame %d: the composed frame stopped showing %q after the process "+
				"chdir'd", frame, want)
		}
	}
}

// mustGetwd is os.Getwd with the error turned into a test failure —
// the tests above move the process deliberately and a Getwd that fails
// there means the fixture is broken, not the code.
func mustGetwd(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return dir
}

// TestAbbreviateHome covers the shortening displayCwd used to do
// inline. It is a pure function of two strings precisely so this can
// be a table rather than a sequence of chdirs: the interesting cases
// are a directory that is not under the home directory at all and a
// home directory that could not be resolved, and neither is reachable
// by moving the process around a temp dir.
func TestAbbreviateHome(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		home string
		want string
	}{
		{
			name: "under home",
			dir:  "/home/op/projects/core-tui",
			home: "/home/op",
			want: "~/projects/core-tui",
		},
		{
			name: "home itself",
			dir:  "/home/op",
			home: "/home/op",
			want: "~",
		},
		{
			name: "outside home",
			dir:  "/srv/build/core-tui",
			home: "/home/op",
			want: "/srv/build/core-tui",
		},
		{
			// os.UserHomeDir errors when HOME is unset. The empty
			// string must not match everything as a prefix and turn
			// every path into "~"+path.
			name: "home unresolved",
			dir:  "/srv/build/core-tui",
			home: "",
			want: "/srv/build/core-tui",
		},
		{
			name: "root",
			dir:  "/",
			home: "/home/op",
			want: "/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := abbreviateHome(tc.dir, tc.home); got != tc.want {
				t.Errorf("abbreviateHome(%q, %q) = %q, want %q",
					tc.dir, tc.home, got, tc.want)
			}
		})
	}
}

// TestResolveDisplayCwd_ReadsHomePerCall guards the half of #223 that
// says HOME is read once rather than per displayCwd call — once, not
// never. Memoizing it in a package var would be filled by whichever
// Model was constructed first in the process and stay wrong for every
// later one, and the golden corpus depends on the opposite: pinCwd
// installs a synthetic HOME per test so the captured frames do not
// carry the checkout path of whoever ran -update.
func TestResolveDisplayCwd_ReadsHomePerCall(t *testing.T) {
	pinCwd(t)
	want := "~" + string(filepath.Separator) + "core-tui"
	if got := resolveDisplayCwd(); got != want {
		t.Fatalf("resolveDisplayCwd() = %q, want %q", got, want)
	}

	// Same process, same directory, a HOME that no longer contains it.
	t.Setenv("HOME", filepath.Join(mustGetwd(t), "not-a-parent"))
	t.Setenv("USERPROFILE", filepath.Join(mustGetwd(t), "not-a-parent"))
	if got, dir := resolveDisplayCwd(), mustGetwd(t); got != dir {
		t.Errorf("resolveDisplayCwd() = %q, want the verbatim %q — HOME was cached "+
			"across calls, so a later Model would abbreviate against an earlier "+
			"Model's home directory", got, dir)
	}
}
