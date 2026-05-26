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

// Terminal capability sniffing (agentic-tui skill §18.A). Probe
// once at startup, store on Model, branch on the bits when a
// renderer wants a richer affordance (hyperlinks for `@`-ref
// targets, OSC 52 for "copy to clipboard" hints, Kitty graphics
// for inline images later).
//
// All sniffing is env-var based — no terminal queries (which
// would need the alt-screen torn down + a stdin write/read cycle
// that races bubbletea v2's input loop). Hosts that want
// authoritative detection can override Model.caps post-NewModel.

package tui

import (
	"os"
	"strings"
)

// TerminalCapabilities is the bag of optional terminal features
// the TUI knows how to exploit when present and degrade past when
// absent. Zero value = "assume nothing supported" (safe default).
type TerminalCapabilities struct {
	// TrueColor is true when the terminal advertises 24-bit color
	// via COLORTERM. Lipgloss v2 picks the best output path
	// regardless, but renderers that want to gate gradient/blend
	// effects on real truecolor support read this bit.
	TrueColor bool

	// Hyperlinks reports whether OSC 8 hyperlinks should render
	// as actual clickable terminal hyperlinks. Sniff by allowlist
	// because there's no portable query — common modern emulators
	// (kitty, iTerm2, wezterm, foot, alacritty 0.14+, vte 0.50+,
	// vscode integrated) support them.
	Hyperlinks bool

	// Clipboard reports whether OSC 52 "set clipboard" sequences
	// are likely honored. Mostly the same allowlist as Hyperlinks;
	// users still need to enable the feature in their term config.
	Clipboard bool

	// KittyGraphics reports whether the terminal supports the
	// Kitty graphics protocol for inline images. Reserved for
	// future image-rendering paths.
	KittyGraphics bool

	// TermProgram is the canonical name of the terminal program
	// when known (TERM_PROGRAM / KITTY_WINDOW_ID / WT_SESSION /
	// VSCODE_PID etc.). Used by other capability checks; surfaced
	// so the host can log it.
	TermProgram string
}

// DetectCapabilities probes the environment once and returns the
// best-guess capability bag. Called from NewModel; hosts can
// override on Model.caps after NewModel returns if they have a
// better signal.
func DetectCapabilities() TerminalCapabilities {
	colorterm := strings.ToLower(os.Getenv("COLORTERM"))
	term := strings.ToLower(os.Getenv("TERM"))
	prog := termProgram()
	caps := TerminalCapabilities{
		TermProgram: prog,
		TrueColor:   colorterm == "truecolor" || colorterm == "24bit" || strings.Contains(term, "direct"),
	}
	switch prog {
	case "kitty":
		caps.Hyperlinks = true
		caps.Clipboard = true
		caps.KittyGraphics = true
	case "iterm.app", "iterm2":
		caps.Hyperlinks = true
		caps.Clipboard = true
	case "wezterm":
		caps.Hyperlinks = true
		caps.Clipboard = true
		caps.KittyGraphics = true
	case "alacritty", "foot", "ghostty":
		caps.Hyperlinks = true
		caps.Clipboard = true
	case "vscode":
		caps.Hyperlinks = true
		// VS Code's integrated terminal supports OSC 52 only when
		// the user enables "terminal.integrated.enablePersistentSessions"
		// (disabled by default) — leave Clipboard off.
	case "tmux":
		// tmux passthrough varies wildly; leave both off and let
		// the host override.
	}
	return caps
}

// termProgram returns the lowercased canonical name of the
// terminal program, probing TERM_PROGRAM and a few well-known
// env vars. Returns "" when no signal is available.
func termProgram() string {
	if v := os.Getenv("TERM_PROGRAM"); v != "" {
		return strings.ToLower(v)
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return "kitty"
	}
	if os.Getenv("ALACRITTY_LOG") != "" || os.Getenv("ALACRITTY_WINDOW_ID") != "" {
		return "alacritty"
	}
	if os.Getenv("WEZTERM_PANE") != "" || os.Getenv("WEZTERM_UNIX_SOCKET") != "" {
		return "wezterm"
	}
	if os.Getenv("FOOT_SOCK") != "" {
		return "foot"
	}
	if os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return "ghostty"
	}
	if os.Getenv("VSCODE_PID") != "" || os.Getenv("VSCODE_INJECTION") != "" {
		return "vscode"
	}
	if os.Getenv("TMUX") != "" {
		return "tmux"
	}
	return ""
}

// Hyperlink renders s as an OSC 8 hyperlink to url when the
// capability is supported, otherwise returns s unchanged. Lets
// renderers always call Hyperlink without branching themselves.
func (c TerminalCapabilities) Hyperlink(url, s string) string {
	if !c.Hyperlinks || url == "" {
		return s
	}
	// OSC 8 syntax: ESC ] 8 ; ; URL ST text ESC ] 8 ; ; ST
	const esc = "\x1b]8;;"
	const st = "\x1b\\"
	return esc + url + st + s + esc + st
}
