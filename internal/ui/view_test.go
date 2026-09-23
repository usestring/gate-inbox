// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

func lipglossWidth(s string) int { return lipgloss.Width(s) }

func TestScrollWindow(t *testing.T) {
	cases := []struct {
		name                  string
		total, cursor, height int
		wantStart, wantEnd    int
	}{
		{"fits entirely", 5, 2, 10, 0, 5},
		{"cursor at top", 100, 0, 20, 0, 18},
		{"cursor centered", 100, 50, 20, 41, 59},
		{"cursor at bottom", 100, 99, 20, 82, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := scrollWindow(tc.total, tc.cursor, tc.height)
			if start != tc.wantStart || end != tc.wantEnd {
				t.Fatalf("scrollWindow(%d,%d,%d) = %d,%d want %d,%d",
					tc.total, tc.cursor, tc.height, start, end, tc.wantStart, tc.wantEnd)
			}
			if tc.cursor < start || tc.cursor >= end {
				t.Fatalf("cursor %d outside window [%d,%d)", tc.cursor, start, end)
			}
		})
	}
}

func TestPaneExactPreservesBlanks(t *testing.T) {
	pane := "one\n\n\ntwo\n"
	got := paneExact(pane, 10, 80)
	if len(got) != 4 || got[1] != "" || got[2] != "" {
		t.Fatalf("paneExact should keep blank rows: %q", got)
	}
	got = paneExact("a\nb\nc\nd", 2, 80)
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("paneExact oversize should take bottom lines: %q", got)
	}
}

func TestClampFrame(t *testing.T) {
	tall := "a\nb\nc\nd\ne"
	got := clampFrame(tall, 3)
	if got != "a\nb\nc" {
		t.Fatalf("clampFrame trim = %q", got)
	}
	short := "x"
	got = clampFrame(short, 3)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[0] != "x" {
		t.Fatalf("clampFrame pad = %q", got)
	}
	for _, line := range lines[1:] {
		// Padding rows wear the backdrop fill rather than being bare, so
		// a short frame cannot show the terminal's background through its
		// bottom rows.
		if !strings.Contains(line, " ") {
			t.Fatalf("padding row is bare: %q", line)
		}
	}
}

func TestPreviewLine(t *testing.T) {
	colored := "\x1b[38;5;42mgreen text\x1b[39m"
	got := previewLine(colored, 80)
	if !strings.Contains(got, "\x1b[38;5;42m") {
		t.Fatalf("color escapes should survive: %q", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("line with ANSI must reset SGR: %q", got)
	}
	erased := "abc\x1b[K\x1b[2Jdef"
	if got := ansi.Strip(previewLine(erased, 80)); strings.TrimRight(got, " ") != "abcdef" {
		t.Fatalf("erase sequences should be stripped: %q", got)
	}
	scrolled := "a\x1b[1Sb\x1bMc\x1b[2Td"
	if got := ansi.Strip(previewLine(scrolled, 80)); strings.TrimRight(got, " ") != "abcd" {
		t.Fatalf("scroll sequences should be stripped: %q", got)
	}

	control := "a\rb\bc"
	if got := ansi.Strip(previewLine(control, 80)); strings.TrimRight(got, " ") != "abc" {
		t.Fatalf("control chars should be dropped: %q", got)
	}

	wide := "\x1b[31m" + strings.Repeat("x", 100) + "\x1b[0m"
	clipped := previewLine(wide, 20)
	if w := lipglossWidth(clipped); w > 20 {
		t.Fatalf("clipped ANSI line renders %d cells, want <= 20", w)
	}

	plain := previewLine("plain", 80)
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("plain line should gain no escapes: %q", plain)
	}
	if w := ansi.StringWidth(plain); w != 80 {
		t.Fatalf("pane row should fill its column, width %d", w)
	}
	mixed := ansi.Strip(previewLine("קודינג\",\"slowly\":false", 80))
	if strings.TrimRight(mixed, " ") != "קודינג\",\"slowly\":false" {
		t.Fatalf("mixed-direction pane row lost its text: %q", mixed)
	}
	// An RTL row is painted like any other row: the same cells and no
	// direction marks, so every host is handed the same frame.
	hebrew := previewLine("  העצמון 25 חולון", 40)
	if strings.ContainsAny(hebrew, "\u200e\u2066\u2069") {
		t.Fatalf("pane row must carry no bidi controls: %q", hebrew)
	}
	if w := ansi.StringWidth(hebrew); w != 40 {
		t.Fatalf("RTL row must pad to column width, got %d", w)
	}
	if body := strings.TrimRight(ansi.Strip(hebrew), " "); body != "  העצמון 25 חולון" {
		t.Fatalf("RTL row text changed: %q", body)
	}
}

// A row `go test` wrote carries literal tabs, which the frame measures as
// nothing and the host paints out to its own stops, pushing the preview,
// footer and stat rows off their positions.
func TestPaneExactExpandsTabs(t *testing.T) {
	const width = 60
	rows := paneExact("ok  \tgithub.com/usestring/gate-inbox/internal/ui\t73.163s\n", 4, width)
	if len(rows) != 1 || strings.ContainsRune(rows[0], '\t') {
		t.Fatalf("tab reached the frame: %q", rows)
	}
	if !strings.HasPrefix(rows[0], "ok      github.com/") {
		t.Fatalf("tab should reach the pane's next eight-column stop: %q", rows[0])
	}
	if w := ansi.StringWidth(previewLine(rows[0], width)); w != width {
		t.Fatalf("tabbed row paints %d cells, want %d", w, width)
	}

	// A tab close to the right edge stops on the row's last cell, where
	// tmux leaves it, so the cell painted after it stays in the preview.
	// Measured on tmux 3.7b: in a 62-column pane, 58 columns then a tab
	// leaves the cursor at column 61.
	edge := paneExact(strings.Repeat("a", 58)+"\tX", 1, 62)
	if want := strings.Repeat("a", 58) + "   X"; edge[0] != want {
		t.Fatalf("tab at the edge expanded to %q, want %q", edge[0], want)
	}

	// A capture taken before a resize is wider than the box it lands in,
	// and a tab past the right edge covers no cells at all.
	stale := paneExact(strings.Repeat("a", 40)+"\tX", 1, 20)
	if want := strings.Repeat("a", 40) + "X"; stale[0] != want {
		t.Fatalf("tab past the edge expanded to %q, want %q", stale[0], want)
	}
}

// The capture keeps the agent's own colors on every theme: the pane is
// painted the theme's backdrop, so nothing has to be recolored on the way in.
func TestPreviewLineKeepsAgentColors(t *testing.T) {
	for _, name := range []string{"classic", "solarized light"} {
		applyTheme(themes[themeIndex(name)])
		colored := "\x1b[38;5;42mhi\x1b[39m"
		got := previewLine(colored, 4)
		if !strings.Contains(got, "\x1b[38;5;42m") {
			t.Errorf("%s: agent color dropped: %q", name, got)
		}
		if body := strings.TrimRight(ansi.Strip(got), " "); body != "hi" {
			t.Errorf("%s: preview line rewritten: %q", name, got)
		}
	}
	applyTheme(themes[0])
}

func TestTruncateRuneSafe(t *testing.T) {
	hebrew := "/home/dev/פרויקטים/gate-inbox"
	tail := truncateTail(hebrew, 10)
	if !strings.HasPrefix(tail, "…") || len([]rune(tail)) != 10 {
		t.Fatalf("truncateTail broken: %q (%d runes)", tail, len([]rune(tail)))
	}
	if truncateTail("short", 10) != "short" {
		t.Fatal("short strings should pass through")
	}
	if got := truncateTail("abcdef", 1); got != "…" {
		t.Fatalf("max 1 = %q, want ellipsis", got)
	}
	if got := truncateTail("abcdef", 0); got != "" {
		t.Fatalf("max 0 = %q, want empty", got)
	}
}

func TestTruncatePathCutsAtSeparator(t *testing.T) {
	path := "internal/api/handlers/sessions.go"
	got := truncatePath(path, 16)
	if !strings.HasPrefix(got, "…/") || !strings.HasSuffix(got, "sessions.go") {
		t.Fatalf("truncatePath = %q, want a slash-cut tail ending in sessions.go", got)
	}
	if strings.Contains(got, "ternal") {
		t.Fatalf("cut mid-segment: %q", got)
	}
	if truncatePath("short.go", 20) != "short.go" {
		t.Fatal("short paths should pass through")
	}
	if got := truncatePath(path, 1); got != "…" {
		t.Fatalf("limit 1 = %q, want ellipsis", got)
	}
	if got := truncatePath(path, 0); got != "" {
		t.Fatalf("limit 0 = %q, want empty", got)
	}
}

// TestZZShot renders a full frame to disk for visual review. Skipped
// unless GATE_INBOX_SHOT names an output path.
func TestZZShot(t *testing.T) {
	out := os.Getenv("GATE_INBOX_SHOT")
	if out == "" {
		t.Skip("set GATE_INBOX_SHOT to render a frame")
	}
	if name := os.Getenv("GATE_INBOX_SHOT_THEME"); name != "" {
		applyTheme(themes[themeIndex(name)])
		t.Cleanup(func() { applyTheme(themes[0]) })
	}

	m := shotModel()
	switch os.Getenv("GATE_INBOX_SHOT_MODE") {
	case "settings":
		m.mode = modeSettings
		m.settings = settingsState{
			toolNames: []string{"claude", "codex", "grok", "opencode"},
			toolIndex: 0, themeIndex: 1, field: settingsFieldTheme,
		}
		m.version = "0.9.2"
	case "help":
		m.mode = modeHelp
	case "agentpick":
		// The box as n opens it: prefilled with the CLI the last spawn used,
		// the rest of them beside it. GATE_INBOX_SHOT_FILTER types over the prefill,
		// which is what a shot of the filtering state needs.
		m.mode = modeAgentPick
		// A card is all this mode draws, so the frame is sized to it rather
		// than left as a board-sized sheet of backdrop around a small box.
		m.width, m.height = 96, 13
		input := textinput.New()
		input.Prompt = ""
		input.CharLimit = 40
		input.Focus()
		m.agentPick = agentPick{
			input: input,
			names: []string{"claude", "codex", "grok", "opencode"},
			fresh: true,
		}
		m.setAgentPick("claude")
		if filter := os.Getenv("GATE_INBOX_SHOT_FILTER"); filter != "" {
			m.agentPick.input.SetValue(filter)
			m.agentPick.input.SetCursor(len(filter))
			m.agentPick.fresh = false
			m.snapAgentPick()
		}
	}
	if err := os.WriteFile(out, []byte(m.frame()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func shotModel() *Model {
	now := time.Now()
	sess := func(name, group, tool, st string, age time.Duration) store.Session {
		return store.Session{
			ID: name, Name: name, Group: group, Tool: tool, Status: st,
			Cwd: "/Users/someone/dev/api", CreatedAt: now.Add(-24 * time.Hour),
			LastStatusAt: now.Add(-age),
		}
	}
	sessions := []store.Session{
		sess("db-migrations", "", "opencode", status.Waiting, 3*time.Minute),
		sess("notes", "", "grok", status.Idle, 12*time.Minute),
		sess("auth-refresh", "backend", "claude", status.Finished, 2*time.Minute),
		sess("add-rate-limiting", "backend", "claude", status.Working, 41*time.Second),
		sess("ui-polish", "backend/web", "codex", status.Working, 6*time.Minute),
		sess("flaky-e2e", "backend/web", "claude", status.Errored, 22*time.Minute),
	}
	rows := []treeRow{
		{sess: sessions[0]},
		{sess: sessions[1]},
		{isGroup: true, group: "backend"},
		{depth: 1, sess: sessions[2]},
		{depth: 1, sess: sessions[3]},
		{isGroup: true, group: "backend/web", depth: 1},
		{depth: 2, sess: sessions[4]},
		{depth: 2, sess: sessions[5]},
	}
	m := &Model{
		width: 120, height: 34, mode: modeList, cursor: 4,
		sessions: sessions, rows: rows, collapsed: map[string]bool{},
		groupPaths: map[string]string{"backend": "/Users/someone/dev/api"},
		split:      splitState{ratio: defaultSplitRatio},
		agents:     agentStats{count: 4, cpu: 12, ram: 9, rss: 1_530_000_000},
		net:        netStats{rates: true, down: 9_400_000, up: 2_100_000},
		snap: sysstat.Snapshot{
			CPUOK: true, CPUPercent: 22,
			MemOK: true, MemPercent: 75, MemUsed: 12_100_000_000, MemTotal: 16_000_000_000,
			SwapOK: true, SwapPercent: 43, SwapUsed: 4_500_000_000, SwapTotal: 8_000_000_000, SwapCeiling: 8_000_000_000,
			DiskOK: true, DiskPercent: 88, DiskUsed: 400_000_000_000, DiskFree: 100_000_000_000, DiskTotal: 500_000_000_000,
			CPUTempOK: true, CPUTemp: 61, GPUTempOK: true, GPUTemp: 55,
		},
		preview: previewSample,
		proc:    sysstat.ProcStat{OK: true, CPUPercent: 4.2, RamPercent: 3.6, RSS: 612_000_000},
		procFor: "add-rate-limiting",
	}
	return m
}

const previewSample = "\x1b[38;5;110m◆\x1b[0m claude \x1b[38;5;240m·\x1b[0m add-rate-limiting\n" +
	"\n" +
	"\x1b[38;5;250m❯ Add a token bucket limiter to the public API\x1b[0m\n" +
	"\n" +
	"\x1b[38;5;240m●\x1b[0m Read(internal/api/router.go)\n" +
	"  \x1b[38;5;240m└\x1b[0m 214 lines\n" +
	"\n" +
	"\x1b[38;5;240m●\x1b[0m Edit(internal/api/limiter.go)\n" +
	"  \x1b[38;5;240m└\x1b[0m +48 −3\n" +
	"\n" +
	"\x1b[38;5;214m✳\x1b[0m Running tests… (14s · esc to interrupt)\n"

// A frame line wider than the terminal wraps, which pushes the whole
// layout down a row and tears the panels. A frame with more rows than the
// terminal loses its footer to the clamp. Both have to hold at every size
// the layout still makes sense at, including the width where the header
// swaps between the wordmark and its one-line form.
func TestFrameFitsTerminal(t *testing.T) {
	for _, width := range []int{80, 100, 120, 160, 200} {
		for _, height := range []int{24, 34, 50} {
			m := shotModel()
			m.width, m.height = width, height
			// View clamps and pads, which would hide a frame that is a row
			// short or a row long, so the raw frame is what gets measured.
			raw := strings.Split(m.viewListFrame(), "\n")
			if len(raw) != height {
				t.Errorf("%dx%d: frame paints %d rows", width, height, len(raw))
			}
			lines := strings.Split(m.frame(), "\n")
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("%dx%d: line %d is %d wide: %q", width, height, i, got, ansi.Strip(line))
				}
			}
		}
	}
}

func TestFocusedPaneHasRightBorder(t *testing.T) {
	for _, width := range []int{80, 120, 200} {
		for _, height := range []int{12, 24, 50} {
			for _, preview := range []string{"hello", strings.Repeat("hello\n", height)} {
				m := shotModel()
				m.width, m.height, m.mode = width, height, modeFocus
				m.preview = preview
				if preview == "hello" {
					m.preview = strings.Repeat("x", m.previewPaneWidth()-1) + "Z"
				}
				rows := strings.Split(ansi.Strip(m.frame()), "\n")
				if m.pane.box.width != m.previewPaneWidth() {
					t.Fatalf("painted width %d differs from tmux width %d", m.pane.box.width, m.previewPaneWidth())
				}
				top, bottom := m.pane.box.y-1, m.listChromeRows()+m.listBodyHeight()
				if preview == "hello" && []rune(rows[m.pane.box.y])[width-2] != 'Z' {
					t.Fatal("border overwrote the last column of pane content")
				}
				for y := top; y <= bottom; y++ {
					want := '│'
					if y == top {
						want = '╮'
					}
					if y == bottom {
						want = '╯'
					}
					if got := []rune(rows[y])[width-1]; got != want {
						t.Fatalf("%dx%d row %d: right border = %q, want %q", width, height, y, got, want)
					}
				}
				for y, row := range rows {
					if got := ansi.StringWidth(row); got > width {
						t.Fatalf("row %d width %d exceeds %d", y, got, width)
					}
				}
				focusedWidth := m.previewPaneWidth()
				m.mode = modeList
				if m.previewPaneWidth() != focusedWidth {
					t.Fatal("unfocus changes tmux width")
				}
				if strings.Contains(ansi.Strip(m.frame()), "╮") {
					t.Fatal("focus border remains after unfocus")
				}
			}
		}
	}
}

// The pane's soft edges: the opening row bleeds down by half a cell, the
// closing row bleeds up by half, and every body row ends with the ▐
// half-block edge one column past the seam. The end cells of the edge rows
// are the corners: the first is a foreground block so the window margin
// cannot inherit pane tone and smear past the corner, and the last is
// narrowed to the bleed's half width so the fill closes at the corner
// instead of jutting right. Both rows are drawn in block elements, the
// vocabulary of the rest of the frame; TestFrameStaysInWidelyDrawnGlyphs
// is what holds the seam out of Symbols for Legacy Computing.
func TestPaneSoftEdges(t *testing.T) {
	m := shotModel()
	rows := strings.Split(m.frame(), "\n")
	leftWidth, _ := m.splitWidths()

	top := []rune(ansi.Strip(rows[(m.listChromeRows() - 1)]))
	bottom := []rune(ansi.Strip(rows[(m.listChromeRows()-1)+1+m.listBodyHeight()]))
	if top[0] != '▄' || bottom[0] != '▀' {
		t.Fatalf("edge rows should open with foreground blocks, got %q and %q",
			string(top[0]), string(bottom[0]))
	}
	for col := 1; col <= leftWidth; col++ {
		if top[col] != '▀' {
			t.Fatalf("top edge col %d is %q, want ▀:\n%s", col, string(top[col]), string(top))
		}
		if bottom[col] != '▄' {
			t.Fatalf("bottom edge col %d is %q, want ▄:\n%s", col, string(bottom[col]), string(bottom))
		}
	}
	if top[leftWidth+1] != '▜' || bottom[leftWidth+1] != '▟' {
		t.Fatalf("edge rows should close on the bleed's half width, got %q and %q",
			string(top[leftWidth+1]), string(bottom[leftWidth+1]))
	}
	if top[leftWidth+2] == '▀' || bottom[leftWidth+2] == '▄' {
		t.Fatalf("the bleed should stop after the seam's edge column")
	}
	for i := (m.listChromeRows() - 1) + 1; i < (m.listChromeRows()-1)+1+m.listBodyHeight(); i++ {
		row := []rune(ansi.Strip(rows[i]))
		if cell := row[0]; cell != '█' {
			t.Fatalf("row %d: first column is %q, want █:\n%s", i, string(cell), string(row))
		}
		if cell := row[leftWidth+1]; cell != '▐' && cell != '─' {
			t.Fatalf("row %d: edge column is %q, want ▐:\n%s", i, string(cell), string(row))
		}
		// The seam is fill, not a drawn line: the glyphs of the old line
		// seam appearing here mean a stale seam renderer shipped again.
		if cell := row[leftWidth]; cell != ' ' && cell != '─' {
			t.Fatalf("row %d: seam column is %q, want pane fill:\n%s", i, string(cell), string(row))
		}
	}
}

// The rendered half of the font-coverage guard: every rune the corpus puts
// on screen has to be one widelyDrawnRunes vouches for. It began as a ban on
// Symbols for Legacy Computing alone, after the rail's opening seam — drawn
// in sextants — came back on the phone and on iPad as a band of replacement
// glyphs under the wordmark. One block was too narrow a rule: the inbox
// badge broke next, on a dingbat. Run over the golden corpus rather than one
// shot, because a narrow board takes a different path through the same rules.
func TestFrameStaysInWidelyDrawnGlyphs(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			// The preview panel is a capture of an agent's pane. Its glyphs
			// are that agent's, so they are exempted here rather than
			// vouched for; see assertWidelyDrawnExcept.
			foreign := map[rune]bool{}
			for _, r := range ansi.Strip(m.preview) {
				foreign[r] = true
			}
			for i, line := range strings.Split(m.frame(), "\n") {
				assertWidelyDrawnExcept(t, fmt.Sprintf("row %d", i), line, foreign)
			}
		})
	}
}

// Focused, the keyboard belongs to the agent: one tier with the keys the
// manager keeps, and the app-wide keys — which would go to the agent, not
// the manager — stay out.
func TestFooterInFocusMode(t *testing.T) {
	m := buildModel(t)
	m.mode = modeFocus
	footer := ansi.Strip(m.viewFooter())
	if !strings.Contains(footer, "Focused") {
		t.Fatalf("the tier should name the mode it describes:\n%s", footer)
	}
	if !strings.Contains(footer, "back to manager") || !strings.Contains(footer, "goes to the agent") {
		t.Fatalf("focus footer should carry the reserved keys:\n%s", footer)
	}
	if strings.Contains(footer, "navigate") || strings.Contains(footer, "View") {
		t.Fatalf("app-wide keys go to the agent while focused, so the tier must go:\n%s", footer)
	}
	// Blank rows below hold the list footer's height so focusing never
	// resizes the pane.
	if lines := strings.Split(strings.TrimRight(footer, "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("focus footer should be one row of keys, got %d:\n%s", len(lines), footer)
	}
	if strings.Contains(footer, "agent UI") {
		t.Fatalf("a plain focused pane should not offer mouse pass-through:\n%s", footer)
	}

	m.pane.mouse = true
	if footer := ansi.Strip(m.viewFooter()); !strings.Contains(footer, "click / "+keymap.Display("alt+drag")) || !strings.Contains(footer, "agent UI") {
		t.Fatalf("a mouse-tracking pane should advertise pass-through:\n%s", footer)
	}
}

// The quick bar takes its rows from the painted preview alone: resizing the
// pane for it would make an agent drawing on the normal screen redraw its
// whole transcript, so the pane stays pinned and the view crops instead.
func TestQuickBarKeepsPaneHeight(t *testing.T) {
	m := buildModel(t)
	m.conversation = nil
	createSession(t, m, "sizer", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	sess := m.rows[m.cursor].sess
	pinned := m.previewPaneHeight()

	rows := make([]string, pinned+10)
	for i := range rows {
		rows[i] = fmt.Sprintf("line%03d", i+1)
	}
	m.preview = strings.Join(rows, "\n") + "\n"
	listed := paintedPreview(m)

	m.openQuickMode()
	if got := m.previewPaneHeight(); got != pinned {
		t.Fatalf("box height with the quick bar open = %d, want %d", got, pinned)
	}
	painted := paintedPreview(m)
	// The bar's rows come off the top of the view; the pane's live end stays.
	if oldest := rows[len(rows)-pinned]; !strings.Contains(listed, oldest) || strings.Contains(painted, oldest) {
		t.Fatalf("the quick bar should have cropped %s off the top:\n%s", oldest, painted)
	}
	if newest := rows[len(rows)-1]; !strings.Contains(painted, newest) {
		t.Fatalf("the quick bar hid the pane's live end (%s):\n%s", newest, painted)
	}
	m.applyCmd(t, m.refreshCmd())
	if got := windowHeight(t, sess.ID); got != pinned {
		t.Fatalf("pane height with the quick bar open = %d, want %d", got, pinned)
	}

	m.quick.active = false
	m.applyCmd(t, m.refreshCmd())
	if got := windowHeight(t, sess.ID); got != pinned {
		t.Fatalf("pane height after closing the quick bar = %d, want %d", got, pinned)
	}
}

func paintedPreview(m *Model) string {
	_, rightWidth := m.splitWidths()
	var painted strings.Builder
	for _, row := range m.contentLines(rightWidth, m.listBodyHeight()) {
		painted.WriteString(ansi.Strip(row.text) + "\n")
	}
	return painted.String()
}

// Every transient tier holds the list footer's height: the footer sets the
// preview box, and a box that moves resizes every session's pane, which
// costs an agent drawing on the normal screen a full transcript redraw.
func TestTransientFootersKeepListHeight(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "sizer", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	listed := lipgloss.Height(m.viewFooter())

	for _, tier := range []struct {
		name string
		open func()
	}{
		{"prompt", func() { m.openQuickMode() }},
		{"resize", func() { m.split.resizeMode = true }},
		{"rename", func() { m.mode = modeRename }},
		{"focus", func() { m.mode = modeFocus }},
	} {
		tier.open()
		if got := lipgloss.Height(m.viewFooter()); got != listed {
			t.Errorf("%s footer = %d rows, want %d", tier.name, got, listed)
		}
		m.quick.active, m.split.resizeMode, m.mode = false, false, modeList
	}
}

// With nothing under the cursor there is nothing to act on, so the footer
// carries only the app-wide tier.
func TestFooterWithoutASelectedRow(t *testing.T) {
	m := buildModel(t)
	m.rows = nil
	footer := ansi.Strip(m.viewFooter())
	if strings.Contains(footer, "Session") || strings.Contains(footer, "Group") {
		t.Fatalf("no row selected, no row tier:\n%s", footer)
	}
	if !strings.Contains(footer, "View") {
		t.Fatalf("the app-wide tier should stay:\n%s", footer)
	}
}

func TestFooterTierFollowsTheCursor(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "legend", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())

	for i, row := range m.rows {
		if !row.isGroup {
			m.cursor = i
			break
		}
	}
	if footer := ansi.Strip(m.viewFooter()); !strings.Contains(footer, "Session") {
		t.Fatalf("a session under the cursor should title the tier Session:\n%s", footer)
	}

	for i, row := range m.rows {
		if row.isGroup {
			m.cursor = i
			break
		}
	}
	footer := ansi.Strip(m.viewFooter())
	if !strings.Contains(footer, "Group") {
		t.Fatalf("a group under the cursor should title the tier Group:\n%s", footer)
	}
	if strings.Contains(footer, "fork") {
		t.Fatalf("a group cannot be forked, so the key should not be offered:\n%s", footer)
	}
}

// A toggle's label names what the key will do next, not the state it is
// already in, so the footer reads as an instruction.
func TestFooterTogglesNameTheNextAction(t *testing.T) {
	m := buildModel(t)
	// Wide enough that the row budget keeps every app-wide binding.
	m.width = 260
	dir := t.TempDir()
	if err := m.store.AddGroup("work", dir); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "toggles", dir, "work")
	m.applyCmd(t, m.refreshCmd())

	if footer := m.peekLegend(m.listBodyHeight()); !strings.Contains(footer, keyCapQuiet("F", "fold all")) {
		t.Fatalf("an open tree should offer folding:\n%s", ansi.Strip(footer))
	}
	m.toggleCollapseAll()
	if footer := m.peekLegend(m.listBodyHeight()); !strings.Contains(footer, keyCapQuiet("F", "unfold all")) {
		t.Fatalf("a folded tree should offer unfolding:\n%s", ansi.Strip(footer))
	}

	group := -1
	for i, row := range m.rows {
		if row.isGroup && !row.isRoot() {
			group = i
			break
		}
	}
	if group < 0 {
		t.Fatalf("the fixture should list a group to fold, rows: %v", m.groupRowPaths())
	}
	m.cursor = group
	if footer := m.viewFooter(); !strings.Contains(footer, keyCap("↵", "unfold")) {
		t.Fatalf("a collapsed group should offer unfolding:\n%s", ansi.Strip(footer))
	}

	m.showArchived = true
	if footer := m.viewFooter(); !strings.Contains(footer, keyCapQuiet("t", "back to active")) {
		t.Fatalf("the archived view should offer the way back:\n%s", ansi.Strip(footer))
	}
}
