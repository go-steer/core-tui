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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// paletteKind distinguishes the trigger character + filter source for
// an open palette overlay (R-PAL-1 / R-PAL-2).
type paletteKind int

const (
	paletteSlash paletteKind = iota
	paletteFile
)

// paletteItem is a single candidate row in the palette.
type paletteItem struct {
	// Name is the unique key (e.g. "help" for /help; "cmd/foo/main.go"
	// for an @file). The filter matches against Name.
	Name string

	// Display is the visible row text, possibly with aliases
	// (e.g. "/help, /?"). Falls back to Name when empty.
	Display string

	// Description is the dim right-aligned subtitle for the row.
	// Often the command's purpose or, for files, the file size.
	Description string

	// Insert is the literal that replaces the trigger-prefixed token
	// on selection. Empty means use the conventional form
	// ("/" + Name for slash, "@" + Name for file).
	Insert string

	// Available reports whether the item's underlying capability is
	// wired in this host. Unavailable items render dim and are
	// skipped by Enter / Tab (selecting one is a no-op + a system
	// message).
	Available bool

	// IsDir flags directory entries in the file palette. Enter on
	// a directory drills into it (palette re-walks with the dir
	// path as the new filter prefix) instead of closing.
	IsDir bool

	// NoAutoSubmit blocks the slash-palette's insert+submit
	// behavior on Enter — the item is inserted, palette closes,
	// but dispatchSlash is NOT called. Use for compound commands
	// that need the operator to type more (e.g. "/allow bundle:"
	// needs the bundle name appended; submitting bare would
	// error). Tab still inserts-without-submit either way.
	NoAutoSubmit bool
}

// palette is the active palette overlay state. Nil = no palette open.
type palette struct {
	kind  paletteKind
	items []paletteItem // all candidates (refreshed on open)

	// seq is the model.paletteSeq value stamped when this palette
	// opened. Off-loop item fetches (the @ directory walk, the host's
	// SlashCommands) carry it back so a reply for a palette the
	// operator has already dismissed — or replaced by re-triggering —
	// is dropped instead of repopulating the current one.
	seq uint64

	// loading is true while an off-loop fetch is still populating
	// items (issue #114). The renderer shows a "scanning…" line
	// instead of "no matches" so an empty panel on a big tree reads
	// as work in progress rather than an empty project.
	loading bool

	// filter is the typed text AFTER the trigger char. Updated on
	// every keystroke while the palette is open.
	filter string

	// cursor indexes into filtered() (clamped on each render).
	cursor int

	// triggerPos is the byte index in the textarea content where the
	// trigger char (`/` or `@`) was typed. On Enter/Tab, the input
	// from triggerPos to the cursor is replaced with the selected
	// item's Insert form.
	triggerPos int
}

// builtinSlashItems returns the catalog of built-in slash commands.
// Layout: three "essentials" pinned at the top (help / clear / quit
// — the ones operators reach for reflexively), followed by the rest
// in alphabetical order. Real dispatch happens in dispatchBuiltinSlash;
// items here describe the palette UI only.
//
// Every built-in is Available=true: dispatchBuiltinSlash + the
// underlying capability assertions handle the host-doesn't-implement
// case at runtime (with a "agent doesn't implement X" system message)
// rather than dimming the palette row — operators can still see what
// commands exist and learn they need to wire X.
func builtinSlashItems() []paletteItem {
	essentials := []paletteItem{
		{Name: "help", Display: "/help, /?", Description: "show command reference", Available: true},
		{Name: "clear", Description: "clear chat history", Available: true},
		{Name: "quit", Display: "/quit, /exit, /q", Description: "exit", Available: true},
	}
	rest := []paletteItem{
		{Name: "allow", Description: "add allow pattern (e.g. /allow bash:git *)", Available: true},
		{Name: "abandon", Description: "resume from a hold and drop the interrupted work", Available: true},
		{Name: "allow bundle:", Display: "/allow bundle:<name>", Insert: "/allow bundle:", Description: "enable a built-in allow bundle (e.g. dev_tools)", Available: true, NoAutoSubmit: true},
		{Name: "continue", Display: "/continue, /cont", Description: "resume from a hold and carry on", Available: true},
		{Name: "deny", Description: "add deny pattern", Available: true},
		{Name: "interrupt", Display: "/interrupt, /int", Description: "cancel the in-flight turn and hold the agent", Available: true},
		{Name: "keys", Description: "show terminal + newline-keystroke diagnostic", Available: true},
		{Name: "mcp", Description: "configured MCP servers and tools", Available: true},
		{Name: "memory", Description: "display loaded memory files", Available: true},
		{Name: "model", Description: "open model picker / switch model", Available: true},
		{Name: "mouse", Description: "toggle mouse capture (placeholder)", Available: true},
		{Name: "pause", Description: "hold the agent — no new turn starts until you resume", Available: true},
		{Name: "permissions", Description: "review session approvals", Available: true},
		{Name: "pricing", Description: "manage pricing (refresh / set)", Available: true},
		{Name: "pricing refresh", Display: "/pricing refresh", Insert: "/pricing refresh", Description: "re-pull the upstream price table", Available: true},
		{Name: "pricing set", Display: "/pricing set", Insert: "/pricing set", Description: "open form to override per-model rates", Available: true},
		{Name: "reload", Description: "rebuild agent from disk", Available: true},
		{Name: "resume", Description: "list / load a saved session transcript", Available: true},
		{Name: "skills", Description: "loaded skill bundles", Available: true},
		{Name: "stats", Description: "per-turn + session usage totals", Available: true},
		{Name: "subagents", Display: "/subagents [<name>]", Description: "list subagents / open one's report + turn log", Available: true},
		{Name: "switch", Display: "/switch, /sess", Description: "open session picker / attach to <id> in place", Available: true},
		{Name: "theme", Description: "open theme picker / switch theme", Available: true},
		{Name: "tools", Description: "list tools and gate state", Available: true},
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	return append(essentials, rest...)
}

// scanFileItems walks every root in scope and returns the eligible
// paths as paletteItems. Honors R-PAL-4 by skipping common noise
// directories (.git, node_modules, vendor, dist, build, target,
// .agents, .claude) and hidden dotfiles at every depth. Symlinks
// are not followed. Caps at maxFilePaletteItems to keep the
// palette snappy on big trees; the cap also defends against
// runaway scope misconfiguration.
//
// Empty scope falls back to the current working directory so the
// @ palette has a useful default — most hosts don't configure
// PathScope at all and operators expect the project tree to be
// in scope by default (matches internal/tui's projectRoot=cwd
// behavior).
func scanFileItems(scope PathScope) []paletteItem {
	const maxFilePaletteItems = 500
	roots := scope.Roots
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			roots = []string{cwd}
		} else {
			return nil
		}
	}
	skipDirs := map[string]bool{
		".git": true, ".hg": true, ".svn": true,
		"node_modules": true, "vendor": true,
		"dist": true, "build": true, "target": true,
		".agents": true, ".claude": true,
		// Expanded set from internal/tui/files.go:20-34: language
		// build/cache dirs that are almost never the right thing
		// to reference.
		".next": true, ".cache": true, ".venv": true,
		"__pycache__": true, ".idea": true, ".vscode": true,
		".terraform": true,
	}
	out := make([]paletteItem, 0, 64)
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable subtrees rather than aborting
			}
			name := d.Name()
			if d.IsDir() {
				if path != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				if path == root {
					return nil // don't add the root itself
				}
				rel, rerr := filepath.Rel(root, path)
				if rerr != nil {
					rel = path
				}
				rel = filepath.ToSlash(rel)
				out = append(out, paletteItem{
					Name:      rel + "/",
					IsDir:     true,
					Available: true,
				})
				if len(out) >= maxFilePaletteItems {
					return filepath.SkipAll
				}
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return nil
			}
			if len(out) >= maxFilePaletteItems {
				return filepath.SkipAll
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)
			size := ""
			if info, ierr := d.Info(); ierr == nil {
				size = formatFileSize(info.Size())
			}
			out = append(out, paletteItem{
				Name:        rel,
				Description: size,
				Available:   true,
			})
			return nil
		})
		if len(out) >= maxFilePaletteItems {
			break
		}
	}
	return out
}

// formatFileSize renders bytes in compact human form for the @
// palette description column. 0 falls back to empty so the column
// doesn't render a noisy "0".
func formatFileSize(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1fG", float64(n)/(1024*1024*1024))
	}
}

// slashProviderItems converts a host's SlashCommands() specs into
// palette rows. Runs inside slashCommandsCmd's goroutine (off the
// event loop) — SlashCommands() is a host method like any other and
// gets no free pass just because it looks like a constant lookup.
func slashProviderItems(provider SlashProvider) []paletteItem {
	specs := provider.SlashCommands()
	items := make([]paletteItem, 0, len(specs))
	for _, spec := range specs {
		display := "/" + spec.Name
		for _, a := range spec.Aliases {
			display += ", /" + a
		}
		items = append(items, paletteItem{
			Name:        spec.Name,
			Display:     display,
			Description: spec.Description,
			Available:   true,
		})
	}
	return items
}

// newSlashPalette opens a / palette at trigger position pos in the
// current input, seeded with the TUI built-ins (builtinSlashItems),
// which are a compile-time constant and cost nothing on the keypress.
//
// Agent-provided commands from SlashProvider — /btw, /subagent, other
// host extensions — are merged later by the slashCommandsMsg handler
// so the panel appears on the `/` keystroke rather than after the
// host has answered (issue #114). pendingHost marks that a merge is
// expected so the panel doesn't briefly claim to be complete.
func newSlashPalette(pos int, seq uint64, pendingHost bool) *palette {
	return &palette{
		kind:       paletteSlash,
		items:      builtinSlashItems(),
		seq:        seq,
		loading:    pendingHost,
		triggerPos: pos,
	}
}

// newFilePalette opens an @ palette at trigger position pos, EMPTY
// and loading. The directory walk that fills it (scanFileItems, one
// filepath.WalkDir per PathScope root, defaulting to the whole cwd)
// runs in scanFileItemsCmd off the Update goroutine; the panel opens
// on the keystroke and populates when fileItemsMsg lands.
func newFilePalette(pos int, seq uint64) *palette {
	return &palette{
		kind:       paletteFile,
		seq:        seq,
		loading:    true,
		triggerPos: pos,
	}
}

// applyItems merges an off-loop fetch into an open palette. Slash
// palettes APPEND (built-ins were already there); file palettes
// REPLACE (they opened empty). Either way the load flag clears and
// the cursor is re-clamped against the newly-filtered list.
func (p *palette) applyItems(items []paletteItem) {
	if p.kind == paletteFile {
		p.items = items
	} else {
		p.items = append(p.items, items...)
	}
	p.loading = false
	if n := len(p.filtered()); p.cursor >= n {
		if n > 0 {
			p.cursor = n - 1
		} else {
			p.cursor = 0
		}
	}
}

// triggerRune returns the literal character the user typed to open
// this palette ("/" or "@").
func (p *palette) triggerRune() string {
	if p.kind == paletteFile {
		return "@"
	}
	return "/"
}

// filtered returns the subset of items matching filter, ranked by
// the shared 4-tier classifier in rank.go — exact basename, basename
// prefix, whole path segment, then substring, tiebroken by shorter
// name. Empty filter returns items in original order (the same slice,
// not a copy). All matching is case-insensitive.
//
// The ranking itself used to live here. It moved to rankNames so the
// model / session / theme pickers could type-to-filter with the same
// ordering (issue #117); this is now the adapter that maps indices
// back onto paletteItems. Behaviour is unchanged and pinned by
// TestPalette_RankingOrderIsPinned.
func (p *palette) filtered() []paletteItem {
	if p.filter == "" {
		return p.items
	}
	idx := rankNames(paletteNames(p.items), p.filter)
	out := make([]paletteItem, len(idx))
	for i, at := range idx {
		out[i] = p.items[at]
	}
	return out
}

// paletteNames projects the field the ranker matches on.
func paletteNames(items []paletteItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

// moveCursor advances the cursor by delta with wrap-around.
func (p *palette) moveCursor(delta int) {
	n := len(p.filtered())
	if n == 0 {
		p.cursor = 0
		return
	}
	p.cursor = (p.cursor + delta + n) % n
}

// selected returns the currently highlighted item, or false if the
// filtered list is empty.
func (p *palette) selected() (paletteItem, bool) {
	items := p.filtered()
	if len(items) == 0 {
		return paletteItem{}, false
	}
	if p.cursor >= len(items) {
		p.cursor = len(items) - 1
	}
	return items[p.cursor], true
}

// completion returns the longest common prefix of all currently-matched
// item names that extends the filter. Used by Tab. Empty when no
// extension is possible (filter is already the full prefix, or no
// matches).
func (p *palette) completion() string {
	items := p.filtered()
	if len(items) == 0 {
		return ""
	}
	prefix := items[0].Name
	for _, it := range items[1:] {
		prefix = commonPrefix(prefix, it.Name)
		if prefix == "" {
			return ""
		}
	}
	if len(prefix) <= len(p.filter) {
		return ""
	}
	return prefix
}

// insertText returns the text that should replace the trigger token
// in the input on Enter. Slash commands become "/<name>", file
// references become "@<name>"; Insert overrides both when set.
func (it paletteItem) insertText(kind paletteKind) string {
	if it.Insert != "" {
		return it.Insert
	}
	if kind == paletteFile {
		return "@" + it.Name
	}
	return "/" + it.Name
}

// commonPrefix returns the longest case-insensitive common prefix of
// a and b, preserving a's case.
func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if strings.EqualFold(string(a[i]), string(b[i])) {
			continue
		}
		return a[:i]
	}
	return a[:n]
}

// maxPaletteRows is the R-PAL-3 cap on visible rows.
const maxPaletteRows = 8

// renderPalette renders the palette as a bordered panel sized to
// width. Returns empty when no palette is open. Follows style.md §6
// modal patterns but anchored to the chat-column bottom rather than
// centered.
//
// R-PAL-3's maxPaletteRows is the cap in a terminal with room for it.
// On a short one the chrome budget lowers it (m.chrome.paletteCap,
// budget.go) so the panel scrolls a smaller window instead of pushing
// the input box out of the frame — the palette is the third of the
// three variable-height elements that could do that (issue #121).
func (m model) renderPalette(width int) string {
	if m.palette == nil || width <= 0 {
		return ""
	}
	items := m.palette.filtered()
	title := "Slash commands"
	if m.palette.kind == paletteFile {
		title = "Project files"
	}

	rule := m.styles.Rule.Render(strings.Repeat(GlyphRule, width))
	// While the off-loop fetch is in flight a match count would be a
	// running tally presented as an answer, so say what is actually
	// happening instead. Also keeps the header no wider than before,
	// which the narrow-terminal frame invariants care about.
	count := fmt.Sprintf("(%d match%s)", len(items), pluralS(len(items)))
	if m.palette.loading {
		count = "scanning…"
	}
	header := m.styles.Accent.Render(title) + "  " + m.styles.Muted.Render(count)

	lines := []string{rule, header}

	if len(items) == 0 {
		empty := "no matches"
		if m.palette.loading {
			empty = "scanning…"
		}
		lines = append(lines, "  "+m.styles.SystemText.Render(empty))
	} else {
		// Scrolling window: the visible slice tracks the cursor so
		// ↑/↓ can step past the window boundary. When the cursor is
		// below the window we slide it down; above, up.
		window := m.paletteWindow()
		start := 0
		if m.palette.cursor >= window {
			start = m.palette.cursor - window + 1
		}
		end := start + window
		if end > len(items) {
			end = len(items)
		}
		visible := items[start:end]
		if start > 0 {
			lines = append(lines,
				"  "+m.styles.Muted.Render(fmt.Sprintf("%s %d above", GlyphTruncate, start)))
		}
		for i, it := range visible {
			lines = append(lines, m.renderPaletteRow(it, start+i == m.palette.cursor, width))
		}
		if end < len(items) {
			lines = append(lines,
				"  "+m.styles.Muted.Render(fmt.Sprintf("%s %d more below", GlyphTruncate, len(items)-end)))
		}
	}

	lines = append(lines, rule)
	return m.joinPanelRows(lines, m.chrome.paletteCap, width)
}

// paletteWindow is how many item rows the palette may show at once:
// R-PAL-3's maxPaletteRows, lowered to whatever the chrome budget
// left it (budget.go) once the panel's own chrome — the two rules and
// the header — is paid for. Never below one row; a palette with no
// visible item would be unusable, and the budget charges the
// shortfall to overflow instead.
//
// This is the palette shrinking itself to the ceiling. joinPanelRows
// still elides at the end as a backstop, because the two scroll
// indicators are conditional and can each add a row after the window
// has been chosen.
func (m model) paletteWindow() int {
	window := maxPaletteRows
	if ceiling := m.chrome.paletteCap; ceiling > 0 {
		const panelChrome = 3 // top rule + header + bottom rule
		if avail := ceiling - panelChrome; avail < window {
			window = avail
		}
	}
	return atLeast(window, 1)
}

// renderPaletteRow renders one row: `> Display              Description`.
// Selected row uses the accent color; unavailable items render dim.
func (m model) renderPaletteRow(it paletteItem, selected bool, width int) string {
	display := it.Display
	if display == "" {
		display = m.palette.triggerRune() + it.Name
	}

	marker := "  "
	if selected {
		marker = m.styles.Accent.Render("> ")
	}

	const descGutter = 4
	pad := width - 2 - lipgloss.Width(display) - lipgloss.Width(it.Description) - descGutter
	if pad < 1 {
		pad = 1
	}
	switch {
	case !it.Available:
		row := marker + display + strings.Repeat(" ", pad) + it.Description
		return m.styles.Muted.Render(row)
	case selected:
		// Selected row: tinted background across the full width;
		// command name in accent-bold (the FOCUS color) so the
		// active row reads at a glance, description muted on the
		// same tint so it stays subordinate.
		hi := lipgloss.NewStyle().Background(m.styles.Theme.BgElevated)
		nameStyle := hi.Foreground(m.styles.Theme.Accent).Bold(true)
		descStyle := hi.Foreground(m.styles.Theme.FgMuted)
		return nameStyle.Render(marker+display) +
			descStyle.Render(strings.Repeat(" ", pad)+it.Description)
	default:
		// Unselected: command name in bold pink (same identity as
		// tool names in /tools) so the eye can scan straight down
		// the command column without parsing the descriptions.
		// Description stays muted to keep visual hierarchy.
		return marker +
			m.itemNameStyle().Render(display) +
			m.styles.Muted.Render(strings.Repeat(" ", pad)+it.Description)
	}
}

// pluralS returns "es" for zero/many and "" for one. Used for the
// "(N match…)" header subtitle.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
