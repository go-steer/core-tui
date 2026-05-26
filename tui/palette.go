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
		{Name: "deny", Description: "add deny pattern", Available: true},
		{Name: "interrupt", Display: "/interrupt, /int", Description: "cancel the in-flight turn", Available: true},
		{Name: "mcp", Description: "configured MCP servers and tools", Available: true},
		{Name: "memory", Description: "display loaded memory files", Available: true},
		{Name: "model", Description: "open model picker / switch model", Available: true},
		{Name: "mouse", Description: "toggle mouse capture (placeholder)", Available: true},
		{Name: "permissions", Description: "review session approvals", Available: true},
		{Name: "pricing", Description: "manage pricing (refresh / set)", Available: true},
		{Name: "reload", Description: "rebuild agent from disk", Available: true},
		{Name: "resume", Description: "list / load a saved session transcript", Available: true},
		{Name: "skills", Description: "loaded skill bundles", Available: true},
		{Name: "stats", Description: "per-turn + session usage totals", Available: true},
		{Name: "subagents", Description: "list background subagents", Available: true},
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

// filtered returns the subset of items matching filter, ranked
// across four tiers (agentic-tui skill §8.B):
//
//	1. exact basename match           ("main" → "main.go")
//	2. basename prefix match          ("main" → "main_test.go")
//	3. path-segment exact match       ("main" → "cmd/main/run.go")
//	4. fuzzy substring                ("main" → "models/main_factory.go")
//
// Ties are broken by shorter path (prefer items closer to repo
// root). Empty filter returns items in original order. All matches
// are case-insensitive.
func (p *palette) filtered() []paletteItem {
	if p.filter == "" {
		return p.items
	}
	q := strings.ToLower(p.filter)

	type ranked struct {
		item paletteItem
		tier int
		path string // lowercased name for tiebreak
	}
	rs := make([]ranked, 0, len(p.items))
	for _, item := range p.items {
		name := strings.ToLower(item.Name)
		// Treat the last path segment (after the final '/') as the
		// basename. Slash commands have no '/' so the basename is
		// the whole name.
		base := name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			base = name[i+1:]
		}
		switch {
		case base == q:
			rs = append(rs, ranked{item, 1, name})
		case strings.HasPrefix(base, q):
			rs = append(rs, ranked{item, 2, name})
		case segmentEquals(name, q):
			rs = append(rs, ranked{item, 3, name})
		case strings.Contains(name, q):
			rs = append(rs, ranked{item, 4, name})
		}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].tier != rs[j].tier {
			return rs[i].tier < rs[j].tier
		}
		// Tiebreak: shorter path wins (closer to repo root /
		// fewer typed chars to confirm).
		if len(rs[i].path) != len(rs[j].path) {
			return len(rs[i].path) < len(rs[j].path)
		}
		return rs[i].path < rs[j].path
	})
	out := make([]paletteItem, len(rs))
	for i, r := range rs {
		out[i] = r.item
	}
	return out
}

// segmentEquals reports whether q appears as a full
// slash-delimited segment anywhere in path. "main" matches
// "cmd/main/run.go" (the middle segment) but NOT
// "cmd/maintain/run.go" (segment "maintain" contains but doesn't
// equal q).
func segmentEquals(path, q string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == q {
			return true
		}
	}
	return false
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
		// Scrolling window: the visible slice tracks the cursor so
		// ↑/↓ can step past the maxPaletteRows boundary. When the
		// cursor is below the window we slide it down; above, up.
		start := 0
		if m.palette.cursor >= maxPaletteRows {
			start = m.palette.cursor - maxPaletteRows + 1
		}
		end := start + maxPaletteRows
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
