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

	tea "charm.land/bubbletea/v2"
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
	out, _ := m.Update(shiftTab)
	got := out.(Model)
	if got.permMode != PermissionModeAcceptEdits {
		t.Errorf("permMode = %s, want acceptEdits", got.permMode)
	}
	if lastSet != PermissionModeAcceptEdits {
		t.Errorf("Set callback received %s, want acceptEdits", lastSet)
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
