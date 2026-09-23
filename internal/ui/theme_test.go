// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/termseq"
)

func relativeLuminance(hex string) float64 {
	red, green, blue := hexRGB(hex)
	channel := func(value int) float64 {
		scaled := float64(value) / 255
		if scaled <= 0.03928 {
			return scaled / 12.92
		}
		return math.Pow((scaled+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(red) + 0.7152*channel(green) + 0.0722*channel(blue)
}

func contrastRatio(a, b string) float64 {
	lumA := relativeLuminance(a)
	lumB := relativeLuminance(b)
	if lumA < lumB {
		lumA, lumB = lumB, lumA
	}
	return (lumA + 0.05) / (lumB + 0.05)
}

// lightThemeNames mirrors TestLightThemesPresent: the themes whose backdrop
// is light, which the luminance split must classify exactly.
var lightThemeNames = map[string]bool{
	"solarized light":  true,
	"catppuccin latte": true,
	"tokyo night day":  true,
	"gruvbox light":    true,
	"rosé pine dawn":   true,
	"paper":            true,
}

func TestLightBackdropClassification(t *testing.T) {
	for _, theme := range themes {
		if got, want := theme.lightBackdrop(), lightThemeNames[theme.Name]; got != want {
			t.Errorf("%s: lightBackdrop() = %v, want %v (Bg %s)", theme.Name, got, want, theme.Bg)
		}
	}
}

func TestLightThemesPresent(t *testing.T) {
	for _, name := range []string{
		"solarized light",
		"catppuccin latte",
		"tokyo night day",
		"gruvbox light",
		"rosé pine dawn",
		"paper",
	} {
		if themes[themeIndex(name)].Name != name {
			t.Errorf("theme %q missing from the built-in set", name)
		}
	}
}

func TestThemeTextContrast(t *testing.T) {
	for _, theme := range themes {
		checks := []struct {
			token   string
			hex     string
			minimum float64
		}{
			{"Bright", theme.Bright, 4.5},
			{"Text", theme.Text, 4.0},
			{"Dim", theme.Dim, 2.8},
			{"Subtle", theme.Subtle, 2.0},
		}
		for _, check := range checks {
			if ratio := contrastRatio(check.hex, theme.Bg); ratio < check.minimum {
				t.Errorf("%s: %s %s on Bg %s contrast %.2f, want >= %.1f",
					theme.Name, check.token, check.hex, theme.Bg, ratio, check.minimum)
			}
		}
		if theme.Surface == theme.Bg {
			t.Errorf("%s: Surface equals Bg, selected rows would be invisible", theme.Name)
		}
		// Badges paint Bg as bold ink on an accent fill, so both accents
		// have to clear the large-text floor against the backdrop tone.
		for token, accent := range map[string]string{"Accent": theme.Accent, "Accent2": theme.Accent2} {
			if ratio := contrastRatio(theme.Bg, accent); ratio < 3.0 {
				t.Errorf("%s: Bg %s on %s fill %s contrast %.2f, want >= 3.0",
					theme.Name, theme.Bg, token, accent, ratio)
			}
		}
	}
}

func globalWindowStyle(t *testing.T) string {
	t.Helper()
	out, err := tmuxCmd("show-options", "-gv", "window-style").CombinedOutput()
	if err != nil {
		t.Fatalf("show-options window-style: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// syncBackdrop puts the package into the repainting mode for one test and
// restores the inherited default afterwards, since inherit is what a fresh
// process starts in and every other test reads that.
func syncBackdrop(t *testing.T) {
	t.Helper()
	mode, bg, painted := backdropMode, terminalBg, backdropPainted
	t.Cleanup(func() {
		backdropMode, terminalBg, backdropPainted = mode, bg, painted
		applyTheme(current)
	})
	backdropPainted = false
	terminalBg = ""
	setBackdropMode(backdropSync)
}

// inheritBackdrop puts the package into the inheriting mode with a known
// terminal background standing in for the OSC 11 answer.
func inheritBackdrop(t *testing.T, hex string) {
	t.Helper()
	mode, bg, painted := backdropMode, terminalBg, backdropPainted
	t.Cleanup(func() {
		backdropMode, terminalBg, backdropPainted = mode, bg, painted
		applyTheme(current)
	})
	backdropPainted = false
	terminalBg = hex
	setBackdropMode(backdropInherit)
}

// The pane carries the theme's own backdrop, on both sides of the split, so
// an agent that follows it renders on the side the manager is drawing.
func TestAgentPaneTheme(t *testing.T) {
	syncBackdrop(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	for _, tt := range []struct {
		theme string
		fgbg  string
	}{
		{"catppuccin latte", "0;15"},
		{"tokyo night", "15;0"},
	} {
		want := themes[themeIndex(tt.theme)]
		applyTheme(want)
		got := agentPaneTheme()
		if got.Background != want.Bg {
			t.Errorf("%s: pane background = %q, want %q", tt.theme, got.Background, want.Bg)
		}
		if got.ColorFgBg != tt.fgbg {
			t.Errorf("%s: COLORFGBG = %q, want %q", tt.theme, got.ColorFgBg, tt.fgbg)
		}
	}
}

// Agents resolve their own palette against the pane background, so a theme
// switch has to reach the tmux server as well as the manager's own frame.
func TestThemeSwitchPushesPaneBackground(t *testing.T) {
	m := buildModel(t)
	syncBackdrop(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	// OLED paints its panes whatever the row says, so this runs on a theme
	// that honours inherit.
	applyTheme(themes[themeIndex("classic")])
	// Global options only stick while a server is up, and a server with no
	// sessions exits at once, so one session holds it open.
	if err := m.tmux.Create("panebg", "/tmp", "", nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { m.tmux.Kill("panebg") })
	t.Cleanup(func() { tmuxCmd("set-option", "-gu", "window-style").Run() })

	m.openSettings()
	m.settings.field = settingsFieldTheme

	light := themes[themeIndex("solarized light")]
	m.settings.themeIndex = themeIndex("solarized light") - 1
	if cmd := m.cycleSetting(1); cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	if m.errBar.text != "" {
		t.Fatalf("pane theme push reported %q", m.errBar.text)
	}
	if got, want := globalWindowStyle(t), "bg="+light.Bg; got != want {
		t.Errorf("light theme pushed window-style %q, want its own backdrop %q", got, want)
	}

	m.settings.themeIndex = themeIndex("nord") - 1
	if cmd := m.cycleSetting(1); cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	if got, want := globalWindowStyle(t), "bg="+themes[themeIndex("nord")].Bg; got != want {
		t.Errorf("dark theme pushed window-style %q, want %q", got, want)
	}
}

// Inheriting, the tones the frame derives are mixed from the terminal's own
// background rather than the theme's, so the rail lands one step above what
// the operator's terminal already draws instead of one step above a colour
// the manager imposed on it.
func TestDerivedTonesFollowTheTerminalBackdrop(t *testing.T) {
	applyTheme(themes[themeIndex("classic")])
	syncBackdrop(t)
	fromTheme := panelHex()
	if want := mix(current.Bg, current.Surface, 0.55); fromTheme != want {
		t.Fatalf("syncing: panel = %q, want the theme's own %q", fromTheme, want)
	}

	const terminal = "#241f2b"
	inheritBackdrop(t, terminal)
	if got, want := panelHex(), mix(terminal, current.Surface, 0.55); got != want {
		t.Errorf("inheriting: panel = %q, want %q mixed from the terminal", got, want)
	}
	if got, want := ruleHex(), mix(terminal, current.Text, 0.22); got != want {
		t.Errorf("inheriting: rule = %q, want %q", got, want)
	}
	if panelHex() == fromTheme {
		t.Error("inheriting left the panel tone on the theme's backdrop")
	}
}

// A light terminal hosting a dark palette is classified by the backdrop the
// frame is actually sitting on, not by the palette, so the value handed to
// an agent for its own auto-detection matches what it will render over.
func TestBackdropPolarityFollowsTheTerminal(t *testing.T) {
	applyTheme(themes[themeIndex("classic")])
	inheritBackdrop(t, "#fdf6e3")
	if !backdropIsLight() {
		t.Error("a light terminal under a dark theme classified as dark")
	}
	if got := agentPaneTheme().ColorFgBg; got != "0;15" {
		t.Errorf("COLORFGBG = %q, want the light pair %q", got, "0;15")
	}
}

// Inheriting, the panes get no background of ours: an empty Background is
// what makes the driver unset window-style instead of writing one.
func TestAgentPaneThemeInheritsTheTerminal(t *testing.T) {
	applyTheme(themes[themeIndex("tokyo night")])
	inheritBackdrop(t, "#0a0a0a")
	if got := agentPaneTheme().Background; got != "" {
		t.Errorf("pane background = %q, want none while inheriting", got)
	}
}

// The terminal write is the theme's backdrop only while syncing. Inheriting,
// it puts back the colour the terminal already had — a no-op at startup and
// the repair after an editor or an attached agent repainted the window — and
// a terminal that never told us its background is left entirely alone.
func TestSyncTerminalBackgroundHonoursTheMode(t *testing.T) {
	applyTheme(themes[themeIndex("classic")])
	emitted := func(t *testing.T) string {
		t.Helper()
		var buf strings.Builder
		out := termseq.Out
		t.Cleanup(func() { termseq.Out = out })
		termseq.Out = &buf
		SyncTerminalBackground()
		return buf.String()
	}

	syncBackdrop(t)
	if got, want := emitted(t), "\x1b]11;"+current.Bg+"\x07"; !strings.Contains(got, want) {
		t.Errorf("syncing emitted %q, want it to carry the theme's backdrop %q", got, want)
	}

	inheritBackdrop(t, "#241f2b")
	if got, want := emitted(t), "\x1b]11;#241f2b\x07"; !strings.Contains(got, want) {
		t.Errorf("inheriting emitted %q, want the terminal's own %q", got, want)
	}
	if strings.Contains(emitted(t), current.Bg) {
		t.Error("inheriting wrote the theme's backdrop to the terminal")
	}

	inheritBackdrop(t, "")
	if got := emitted(t); got != "" {
		t.Errorf("an unanswered terminal was written to: %q", got)
	}
}

// Exiting undoes only what was imposed. Inheriting, OSC 111 would drop a
// background the operator set for their own shell back to the profile's
// default, so nothing goes out. OLED always imposes, so this runs on classic.
func TestResetTerminalBackgroundHonoursTheMode(t *testing.T) {
	t.Cleanup(func() { applyTheme(themes[0]) })
	applyTheme(themes[themeIndex("classic")])
	emitted := func(t *testing.T) string {
		t.Helper()
		var buf strings.Builder
		out := termseq.Out
		t.Cleanup(func() { termseq.Out = out })
		termseq.Out = &buf
		ResetTerminalBackground()
		return buf.String()
	}

	syncBackdrop(t)
	if got := emitted(t); !strings.Contains(got, "\x1b]111\x07") {
		t.Errorf("syncing emitted %q, want the OSC 111 restore", got)
	}
	inheritBackdrop(t, "#241f2b")
	if got := emitted(t); got != "" {
		t.Errorf("inheriting emitted %q, want nothing", got)
	}
}

// window-style outlives the process that wrote it, so a session painted by
// an earlier run must be cleared rather than merely left alone: turning the
// backdrop back to inherit unsets the option on the server.
func TestInheritedBackdropClearsPaneStyle(t *testing.T) {
	m := buildModel(t)
	syncBackdrop(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	// OLED paints its panes whatever the row says, so this runs on a theme
	// that honours inherit.
	applyTheme(themes[themeIndex("classic")])
	// Global options only stick while a server is up, and a server with no
	// sessions exits at once, so one session holds it open.
	if err := m.tmux.Create("panebg-inherit", "/tmp", "", nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { m.tmux.Kill("panebg-inherit") })
	t.Cleanup(func() { tmuxCmd("set-option", "-gu", "window-style").Run() })

	m.openSettings()
	m.settings.backdropSync = true
	m.settings.field = settingsFieldBackdrop

	// Arrive at the state an earlier painting run leaves behind.
	if cmd := m.syncPaneTheme(); cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	if got, want := globalWindowStyle(t), "bg="+current.Bg; got != want {
		t.Fatalf("setup pushed window-style %q, want %q", got, want)
	}

	// Toggling the row to inherit is what has to clear it.
	if cmd := m.cycleSetting(1); cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	if m.errBar.text != "" {
		t.Fatalf("pane theme push reported %q", m.errBar.text)
	}
	if m.settings.backdropSync {
		t.Fatal("the row did not toggle to inherit")
	}
	if got := globalWindowStyle(t); got != "default" {
		t.Errorf("window-style = %q, want it unset back to %q", got, "default")
	}
}

func TestOLEDIsDefaultTheme(t *testing.T) {
	for _, name := range []string{"", "removed-theme"} {
		if got := themes[themeIndex(name)].Name; got != "oled" {
			t.Fatalf("theme %q resolved to %q, want oled", name, got)
		}
	}
	for _, theme := range themes {
		if got := themes[themeIndex(theme.Name)].Name; got != theme.Name {
			t.Fatalf("saved theme %q resolved to %q", theme.Name, got)
		}
	}
	m := buildModel(t)
	if current.Name != "oled" {
		t.Fatalf("fresh startup theme = %q, want oled", current.Name)
	}
	m.loadDeviceTheme("device:new")
	if current.Name != "oled" {
		t.Fatalf("new device theme = %q, want oled", current.Name)
	}
}

// OLED is black end to end even with the backdrop left on inherit, the
// default: the terminal, the tones mixed from the backdrop and the agent
// panes all take #000000 rather than the grey the terminal came up in. Leaving
// OLED hands the terminal its own colour back.
func TestOLEDOverridesAnInheritedBackdrop(t *testing.T) {
	emitted := func(t *testing.T) string {
		t.Helper()
		var buf strings.Builder
		out := termseq.Out
		t.Cleanup(func() { termseq.Out = out })
		termseq.Out = &buf
		SyncTerminalBackground()
		return buf.String()
	}
	t.Cleanup(func() { applyTheme(themes[0]) })

	inheritBackdrop(t, "#3a3f4b")
	applyTheme(themes[themeIndex("oled")])
	if got := backdropBase(); got != "#000000" {
		t.Errorf("backdropBase = %s, want #000000", got)
	}
	if got := agentPaneTheme().Background; got != "#000000" {
		t.Errorf("agent pane background = %q, want #000000", got)
	}
	if got := emitted(t); !strings.Contains(got, "]11;#000000") {
		t.Errorf("OLED emitted %q, want OSC 11 to #000000", got)
	}
	applyTheme(themes[themeIndex("classic")])
	if got := emitted(t); !strings.Contains(got, "]11;#3a3f4b") {
		t.Errorf("leaving OLED emitted %q, want the terminal's own #3a3f4b back", got)
	}

	// A terminal that never answered still gets its own colour back, by
	// OSC 111, and only when something of ours was up.
	terminalBg = ""
	applyTheme(themes[themeIndex("oled")])
	emitted(t)
	applyTheme(themes[themeIndex("classic")])
	if got := emitted(t); !strings.Contains(got, "]111") {
		t.Errorf("leaving OLED on an unanswered terminal emitted %q, want OSC 111", got)
	}
	if got := emitted(t); got != "" {
		t.Errorf("a second sync emitted %q, want nothing", got)
	}
}

// Every cell of an OLED frame carries an explicit background, the preview of
// a pane whose output drops back to the default background included, so
// nothing of the terminal's own colour shows through.
func TestOLEDFrameHasNoDefaultBackground(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	inheritBackdrop(t, "#3a3f4b")
	applyTheme(themes[themeIndex("oled")])
	createSession(t, m, "blackout", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	m.preview = "\x1b[41mred\x1b[49m plain \x1b[1m\x1b[32mgreen\x1b[0m tail\x1b[44m blue\x1b[49m end\n"
	m.lastFrame = ""

	for row, line := range strings.Split(m.frame(), "\n") {
		for col, bg := range cellBackgrounds(line) {
			if bg == "" {
				t.Fatalf("row %d col %d has the terminal's default background:\n%q", row, col, line)
			}
		}
	}
}

// cellBackgrounds walks one rendered row and reports each printed cell's
// background SGR: empty where the terminal's default would show.
func cellBackgrounds(line string) []string {
	var cells []string
	bg := ""
	for i := 0; i < len(line); {
		if strings.HasPrefix(line[i:], "\x1b[") {
			end := strings.IndexByte(line[i:], 'm')
			if end < 0 {
				break
			}
			params := strings.Split(line[i+2:i+end], ";")
			for p := 0; p < len(params); p++ {
				switch v := params[p]; {
				case v == "" || v == "0" || v == "49":
					bg = ""
				case v == "38" || v == "58":
					if p+1 < len(params) && params[p+1] == "5" {
						p += 2
					} else {
						p += 4
					}
				case v == "48":
					n := 4
					if p+1 < len(params) && params[p+1] == "5" {
						n = 2
					}
					bg = strings.Join(params[p:min(len(params), p+n+1)], ";")
					p += n
				case len(v) == 2 && v[0] == '4' || len(v) == 3 && v[:2] == "10":
					bg = v
				}
			}
			i += end + 1
			continue
		}
		if line[i] == 0x1b {
			// Any other escape (OSC hyperlinks) prints nothing.
			end := strings.Index(line[i+1:], "\x1b\\")
			if end < 0 {
				break
			}
			i += end + 3
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		for range max(1, ansi.StringWidth(string(r))) {
			cells = append(cells, bg)
		}
		i += size
	}
	return cells
}
