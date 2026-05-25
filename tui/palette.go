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
}

// palette is the active palette overlay state. Nil = no palette open.
type palette struct {
	kind  paletteKind
	items []paletteItem // all candidates (refreshed on open)

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

// builtinSlashItems returns the static list of built-in slash commands
// for the visual-preview slice. Real implementations will source the
// list from the command registry (built-ins + Options.Commands +
// SlashProvider). Items marked Available=false render dim — they exist
// in the catalog but the host hasn't wired their capability.
func builtinSlashItems() []paletteItem {
	return []paletteItem{
		{Name: "help", Display: "/help, /?", Description: "show command reference", Available: true},
		{Name: "clear", Description: "clear chat history", Available: true},
		{Name: "quit", Display: "/quit, /exit, /q", Description: "exit", Available: true},
		{Name: "memory", Description: "display loaded memory files", Available: true},
		{Name: "stats", Description: "per-turn + session usage totals", Available: true},
		{Name: "mcp", Description: "configured MCP servers", Available: true},
		{Name: "skills", Description: "loaded skill bundles", Available: true},
		{Name: "mouse", Description: "toggle mouse capture", Available: true},
		{Name: "tools", Description: "list tools (requires ToolLister)", Available: false},
		{Name: "model", Description: "switch model (requires ModelSwapper)", Available: false},
		{Name: "reload", Description: "rebuild agent (requires Reloader)", Available: false},
		{Name: "permissions", Description: "review session approvals (requires PermissionController)", Available: false},
		{Name: "pricing", Description: "manage pricing (requires PricingController)", Available: false},
		{Name: "interrupt", Display: "/interrupt, /int", Description: "cancel turn (requires Interruptible)", Available: false},
	}
}

// scanFileItems walks every root in scope and returns the eligible
// paths as paletteItems. Honors R-PAL-4 by skipping common noise
// directories (.git, node_modules, vendor, dist, build, target,
// .agents, .claude) and hidden dotfiles at every depth. Symlinks
// are not followed. Caps at maxFilePaletteItems to keep the
// palette snappy on big trees; the cap also defends against
// runaway scope misconfiguration.
//
// Empty scope returns an empty list — the @ palette renders a hint
// telling the operator no PathScope is wired.
func scanFileItems(scope PathScope) []paletteItem {
	const maxFilePaletteItems = 500
	if len(scope.Roots) == 0 {
		return nil
	}
	skipDirs := map[string]bool{
		".git": true, ".hg": true, ".svn": true,
		"node_modules": true, "vendor": true,
		"dist": true, "build": true, "target": true,
		".agents": true, ".claude": true,
	}
	out := make([]paletteItem, 0, 64)
	for _, root := range scope.Roots {
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

// newSlashPalette opens a / palette at trigger position pos in the
// current input. Cursor starts at 0.
func newSlashPalette(pos int) *palette {
	return &palette{
		kind:       paletteSlash,
		items:      builtinSlashItems(),
		triggerPos: pos,
	}
}

// newFilePalette opens an @ palette at trigger position pos. The
// items are sourced from scanning every PathScope root (R-PAL-4).
// Empty scope yields an empty palette — the renderer surfaces the
// "no PathScope configured" hint instead of a misleading file list.
func newFilePalette(pos int, scope PathScope) *palette {
	return &palette{
		kind:       paletteFile,
		items:      scanFileItems(scope),
		triggerPos: pos,
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

// filtered returns the subset of items matching filter, ranked:
// prefix matches first, then substring matches, both case-insensitive.
// Empty filter returns all items in their original order.
func (p *palette) filtered() []paletteItem {
	if p.filter == "" {
		return p.items
	}
	q := strings.ToLower(p.filter)
	var prefix, substr []paletteItem
	for _, item := range p.items {
		name := strings.ToLower(item.Name)
		switch {
		case strings.HasPrefix(name, q):
			prefix = append(prefix, item)
		case strings.Contains(name, q):
			substr = append(substr, item)
		}
	}
	// Stable-sort each bucket alphabetically for predictable ranking.
	sort.SliceStable(prefix, func(i, j int) bool { return prefix[i].Name < prefix[j].Name })
	sort.SliceStable(substr, func(i, j int) bool { return substr[i].Name < substr[j].Name })
	return append(prefix, substr...)
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
func (m Model) renderPalette(width int) string {
	if m.palette == nil || width <= 0 {
		return ""
	}
	items := m.palette.filtered()
	title := "Slash commands"
	if m.palette.kind == paletteFile {
		title = "Project files"
	}

	rule := m.styles.Rule.Render(strings.Repeat(GlyphRule, width))
	header := m.styles.Accent.Render(title) + "  " +
		m.styles.Muted.Render(fmt.Sprintf("(%d match%s)", len(items), pluralS(len(items))))

	lines := []string{rule, header}

	if len(items) == 0 {
		lines = append(lines, "  "+m.styles.SystemText.Render("no matches"))
	} else {
		visible := items
		if len(visible) > maxPaletteRows {
			visible = visible[:maxPaletteRows]
		}
		for i, it := range visible {
			lines = append(lines, m.renderPaletteRow(it, i == m.palette.cursor, width))
		}
		if len(items) > maxPaletteRows {
			lines = append(lines,
				"  "+m.styles.Muted.Render(fmt.Sprintf("%s and %d more — keep typing to narrow",
					GlyphTruncate, len(items)-maxPaletteRows)))
		}
	}

	lines = append(lines, rule)
	return strings.Join(lines, "\n")
}

// renderPaletteRow renders one row: `> Display              Description`.
// Selected row uses the accent color; unavailable items render dim.
func (m Model) renderPaletteRow(it paletteItem, selected bool, width int) string {
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
	row := marker + display + strings.Repeat(" ", pad) + it.Description
	switch {
	case !it.Available:
		return m.styles.Muted.Render(row)
	case selected:
		return m.styles.AssistantText.Bold(true).Render(row)
	default:
		return m.styles.AssistantText.Render(row)
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
