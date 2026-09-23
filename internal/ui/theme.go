// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/systheme"
	"github.com/usestring/gate-inbox/internal/termseq"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// Theme is the full token set every rendered pixel of the TUI resolves
// through. Colors are hex so terminals with truecolor get the exact
// palette; lipgloss degrades them to the closest 256 index elsewhere.
//
// OLED paints Bg across the frame; other themes let the terminal backdrop
// show through. Bg also supplies the ink on accent fills.
type Theme struct {
	Name string

	// Surfaces and structure.
	Bg      string // backdrop and ink on accent fills
	Surface string // selected row / chip fill
	Overlay string // raised fill for gauges tracks and gutters
	Border  string // idle panel border

	// Type.
	Bright string // emphasized text (selected names, values that must win)
	Text   string // body text
	Dim    string // secondary text
	Subtle string // tertiary text, rules, separators

	// Brand.
	Accent  string // primary: focus, keys, brand
	Accent2 string // secondary: groups, scopes

	// Agent states.
	Working  string
	Waiting  string
	Finished string
	Errored  string
	Idle     string
}

// themes is the built-in palette set, in picker order.
var themes = []Theme{
	{
		Name:    "classic",
		Bg:      "#0f1115",
		Surface: "#232830",
		Overlay: "#2d333d",
		Border:  "#2d333d",
		Bright:  "#eceff4",
		Text:    "#c6ccd6",
		Dim:     "#98a0ac",
		Subtle:  "#646c78",
		Accent:  "#6cb6a4",
		Accent2: "#6daaba",

		Working:  "#d08442",
		Waiting:  "#a78bd0",
		Finished: "#85b26f",
		Errored:  "#cc6a6a",
		Idle:     "#646c78",
	},
	{
		// True black background: on an OLED panel, a black pixel is a lit
		// pixel turned off rather than a dark gray one held on, so this is
		// the one theme that actually saves power rather than just looking
		// dark. Surface/Overlay/Border still lift a few steps off Bg so a
		// selected row and a gauge track stay visibly distinct from the
		// backdrop; everything else follows classic's palette so this
		// reads as "classic, but the blacks are really black."
		Name:    "oled",
		Bg:      "#000000",
		Surface: "#161616",
		Overlay: "#202020",
		Border:  "#202020",
		Bright:  "#ffffff",
		Text:    "#d0d0d0",
		Dim:     "#9a9aa4",
		Subtle:  "#666666",
		Accent:  "#6cb6a4",
		Accent2: "#6daaba",

		Working:  "#d08442",
		Waiting:  "#a78bd0",
		Finished: "#85b26f",
		Errored:  "#cc6a6a",
		Idle:     "#666666",
	},
	{
		Name:    "solarized dark",
		Bg:      "#002b36",
		Surface: "#073642",
		Overlay: "#0e4753",
		Border:  "#0e4753",
		Bright:  "#eee8d5",
		Text:    "#93a1a1",
		Dim:     "#839496",
		Subtle:  "#586e75",
		Accent:  "#268bd2",
		Accent2: "#2aa198",

		Working:  "#268bd2",
		Waiting:  "#b58900",
		Finished: "#859900",
		Errored:  "#dc322f",
		Idle:     "#586e75",
	},
	{
		Name:    "catppuccin mocha",
		Bg:      "#11111b",
		Surface: "#313244",
		Overlay: "#45475a",
		Border:  "#45475a",
		Bright:  "#f5f5ff",
		Text:    "#cdd6f4",
		Dim:     "#a6adc8",
		Subtle:  "#6c7086",
		Accent:  "#cba6f7",
		Accent2: "#94e2d5",

		Working:  "#fab387",
		Waiting:  "#f5c2e7",
		Finished: "#a6e3a1",
		Errored:  "#f38ba8",
		Idle:     "#7f849c",
	},
	{
		Name:    "tokyo night",
		Bg:      "#1a1b26",
		Surface: "#292e42",
		Overlay: "#3b4261",
		Border:  "#3b4261",
		Bright:  "#ffffff",
		Text:    "#c0caf5",
		Dim:     "#9aa5ce",
		Subtle:  "#565f89",
		Accent:  "#7aa2f7",
		Accent2: "#7dcfff",

		Working:  "#e0af68",
		Waiting:  "#bb9af7",
		Finished: "#9ece6a",
		Errored:  "#f7768e",
		Idle:     "#565f89",
	},
	{
		Name:    "gruvbox dark",
		Bg:      "#1d2021",
		Surface: "#3c3836",
		Overlay: "#504945",
		Border:  "#504945",
		Bright:  "#fbf1c7",
		Text:    "#ebdbb2",
		Dim:     "#bdae93",
		Subtle:  "#928374",
		Accent:  "#83a598",
		Accent2: "#8ec07c",

		Working:  "#fabd2f",
		Waiting:  "#d3869b",
		Finished: "#b8bb26",
		Errored:  "#fb4934",
		Idle:     "#928374",
	},
	{
		Name:    "nord",
		Bg:      "#2e3440",
		Surface: "#3b4252",
		Overlay: "#434c5e",
		Border:  "#434c5e",
		Bright:  "#eceff4",
		Text:    "#d8dee9",
		Dim:     "#aebacf",
		Subtle:  "#616e88",
		Accent:  "#88c0d0",
		Accent2: "#8fbcbb",

		Working:  "#ebcb8b",
		Waiting:  "#b48ead",
		Finished: "#a3be8c",
		Errored:  "#bf616a",
		Idle:     "#6b7689",
	},
	{
		Name:    "dracula",
		Bg:      "#21222c",
		Surface: "#343746",
		Overlay: "#44475a",
		Border:  "#44475a",
		Bright:  "#ffffff",
		Text:    "#f8f8f2",
		Dim:     "#c3c3d0",
		Subtle:  "#6272a4",
		Accent:  "#bd93f9",
		Accent2: "#8be9fd",

		Working:  "#ffb86c",
		Waiting:  "#ff79c6",
		Finished: "#50fa7b",
		Errored:  "#ff5555",
		Idle:     "#6272a4",
	},
	{
		Name:    "rosé pine",
		Bg:      "#191724",
		Surface: "#26233a",
		Overlay: "#403d52",
		Border:  "#403d52",
		Bright:  "#e0def4",
		Text:    "#e0def4",
		Dim:     "#908caa",
		Subtle:  "#6e6a86",
		Accent:  "#c4a7e7",
		Accent2: "#9ccfd8",

		Working:  "#f6c177",
		Waiting:  "#ebbcba",
		Finished: "#31748f",
		Errored:  "#eb6f92",
		Idle:     "#6e6a86",
	},
	{
		Name:    "monochrome",
		Bg:      "#0d0d0f",
		Surface: "#26262b",
		Overlay: "#3a3a41",
		Border:  "#3a3a41",
		Bright:  "#ffffff",
		Text:    "#d0d0d8",
		Dim:     "#9a9aa4",
		Subtle:  "#63636d",
		Accent:  "#d8d8e0",
		Accent2: "#a8a8b4",

		Working:  "#e8e8ef",
		Waiting:  "#c8c8d2",
		Finished: "#a8a8b4",
		Errored:  "#f0f0f6",
		Idle:     "#63636d",
	},
	{
		Name:    "solarized light",
		Bg:      "#fdf6e3",
		Surface: "#eee8d5",
		Overlay: "#e0d8bf",
		Border:  "#e0d8bf",
		Bright:  "#073642",
		Text:    "#586e75",
		Dim:     "#657b83",
		Subtle:  "#93a1a1",
		Accent:  "#268bd2",
		Accent2: "#218a80",

		Working:  "#268bd2",
		Waiting:  "#b58900",
		Finished: "#859900",
		Errored:  "#dc322f",
		Idle:     "#93a1a1",
	},
	{
		Name:    "catppuccin latte",
		Bg:      "#eff1f5",
		Surface: "#ccd0da",
		Overlay: "#bcc0cc",
		Border:  "#bcc0cc",
		Bright:  "#2c2f44",
		Text:    "#4c4f69",
		Dim:     "#6c6f85",
		Subtle:  "#9ca0b0",
		Accent:  "#8839ef",
		Accent2: "#179299",

		Working:  "#fe640b",
		Waiting:  "#ea76cb",
		Finished: "#40a02b",
		Errored:  "#d20f39",
		Idle:     "#8c8fa1",
	},
	{
		Name:    "tokyo night day",
		Bg:      "#e1e2e7",
		Surface: "#c4c8da",
		Overlay: "#a8aecb",
		Border:  "#a8aecb",
		Bright:  "#1a1b26",
		Text:    "#3760bf",
		Dim:     "#6172b0",
		Subtle:  "#848cb5",
		Accent:  "#2e7de9",
		Accent2: "#007197",

		Working:  "#8c6c3e",
		Waiting:  "#9854f1",
		Finished: "#587539",
		Errored:  "#f52a65",
		Idle:     "#848cb5",
	},
	{
		Name:    "gruvbox light",
		Bg:      "#fbf1c7",
		Surface: "#ebdbb2",
		Overlay: "#d5c4a1",
		Border:  "#d5c4a1",
		Bright:  "#282828",
		Text:    "#3c3836",
		Dim:     "#665c54",
		Subtle:  "#928374",
		Accent:  "#076678",
		Accent2: "#427b58",

		Working:  "#b57614",
		Waiting:  "#8f3f71",
		Finished: "#79740e",
		Errored:  "#9d0006",
		Idle:     "#928374",
	},
	{
		Name:    "rosé pine dawn",
		Bg:      "#faf4ed",
		Surface: "#dfdad9",
		Overlay: "#cecacd",
		Border:  "#cecacd",
		Bright:  "#575279",
		Text:    "#575279",
		Dim:     "#797593",
		Subtle:  "#9893a5",
		Accent:  "#907aa9",
		Accent2: "#56949f",

		Working:  "#ea9d34",
		Waiting:  "#d7827e",
		Finished: "#286983",
		Errored:  "#b4637a",
		Idle:     "#9893a5",
	},
	{
		Name:    "paper",
		Bg:      "#f7f7f5",
		Surface: "#e2e2df",
		Overlay: "#d0d0cc",
		Border:  "#d0d0cc",
		Bright:  "#111114",
		Text:    "#33333a",
		Dim:     "#5f5f68",
		Subtle:  "#9a9aa0",
		Accent:  "#2f2f38",
		Accent2: "#6a6a74",

		Working:  "#1c1c22",
		Waiting:  "#4a4a54",
		Finished: "#6a6a74",
		Errored:  "#0a0a0e",
		Idle:     "#9a9aa0",
	},
}

const defaultTheme = "oled"
const themeSetting = "theme"
const backdropSetting = "theme_backdrop"

// The two backdrop modes. Inheriting, the terminal keeps whatever colours
// the operator gave it and the frame's derived tones are mixed from that
// background instead of from the theme's own. Syncing, the terminal is
// repainted to the theme's backdrop -- the only behaviour there used to be.
const (
	backdropInherit = "inherit"
	backdropSync    = "sync"
)

var (
	backdropMode = backdropInherit
	// terminalBg is the terminal's own background as "#rrggbb", resolved
	// once before the TUI owns the terminal, or empty where the terminal
	// did not answer. It is read whichever mode is live, so the mode can be
	// toggled from the settings screen without a second query racing Bubble
	// Tea for the tty.
	terminalBg string
	// backdropPainted records that the terminal is showing a background of
	// ours, so leaving for a theme that inherits knows there is something to
	// take back even where the terminal never told us its own.
	backdropPainted bool
)

// ResolveBackdrop asks the terminal for its own background, once, before
// anything has taken the terminal over. A terminal that does not answer
// leaves this empty and every derived tone falls back to the theme's own
// backdrop, which is what happened before there was a choice.
func ResolveBackdrop() {
	if hex, ok := systheme.Background(); ok {
		terminalBg = hex
	}
}

// setBackdropMode switches the mode and drops the tones derived under the
// old one. It never queries: the answer was taken at startup.
func setBackdropMode(mode string) {
	if mode != backdropSync {
		mode = backdropInherit
	}
	backdropMode = mode
	applyTheme(current)
}

// backdropSyncing reports whether the terminal and the agent panes are
// repainted to the theme's backdrop. OLED always is, whatever the backdrop
// setting says: the theme exists so that every pixel is off, and inheriting
// would leave the window padding, the tones mixed from the backdrop and the
// agent panes on whatever colour the terminal already had.
func backdropSyncing() bool {
	return backdropMode == backdropSync || current.Name == "oled"
}

// backdropBase is the colour every derived tone -- the rail, the section
// blocks, the hairline -- is mixed from, and the colour the frame's
// half-block edges paint as "the backdrop showing through". Inheriting, it
// is the operator's own background, so the rail sits one step above what
// their terminal already draws rather than one step above a colour we
// imposed on it.
func backdropBase() string {
	if !backdropSyncing() && terminalBg != "" {
		return terminalBg
	}
	return current.Bg
}

// backdropIsLight classifies the backdrop the frame actually sits on, which
// is not the theme's own once a light terminal is hosting a dark palette.
func backdropIsLight() bool {
	return lightHex(backdropBase())
}

// themeIndex finds a theme by name, falling back to the default when the
// stored name is unknown (a theme removed between releases).
func themeIndex(name string) int {
	for i, t := range themes {
		if t.Name == name {
			return i
		}
	}
	return themeIndex(defaultTheme)
}

// applyTheme rebuilds every package-level style from a token set. The TUI
// runs one model per process, so the styles stay package-level and a theme
// switch simply repaints from the next frame on.
func applyTheme(t Theme) {
	// current stays the theme as written. The quiet transform is applied to
	// the copy the styles are built from, so turning it off has the original
	// tokens to hand back rather than a palette already flattened into
	// itself. See quiet.go.
	current = t
	if quietPalette {
		t = quietened(t)
	}

	colorBg = lipgloss.Color(t.Bg)
	colorBackdrop = lipgloss.Color(backdropBase())
	colorSurface = lipgloss.Color(t.Surface)
	colorOverlay = lipgloss.Color(t.Overlay)
	colorBorder = lipgloss.Color(t.Border)
	colorBright = lipgloss.Color(t.Bright)
	colorText = lipgloss.Color(t.Text)
	colorDim = lipgloss.Color(t.Dim)
	colorSubtle = lipgloss.Color(t.Subtle)
	colorAccent = lipgloss.Color(t.Accent)
	colorAccent2 = lipgloss.Color(t.Accent2)
	colorSelBg = colorSurface

	colorWorking = lipgloss.Color(t.Working)
	colorWaiting = lipgloss.Color(t.Waiting)
	colorFinished = lipgloss.Color(t.Finished)
	colorErrored = lipgloss.Color(t.Errored)
	colorIdle = lipgloss.Color(t.Idle)

	resetRenderCaches()
	rebuildStyles()
}

func (t Theme) lightBackdrop() bool {
	return lightHex(t.Bg)
}

// lightHex is the luminance split every backdrop classification runs
// through, so a theme's own polarity and the terminal's are decided by one
// threshold rather than two that can drift apart.
func lightHex(hex string) bool {
	r, g, b := hexRGB(hex)
	return 0.2126*float64(r)+0.7152*float64(g)+0.0722*float64(b) > 128
}

// agentPaneTheme is the backdrop an agent pane sits on: the same one the
// manager's own frame sits on, and the one its capture is drawn over.
// Agents that auto-detect follow it, so they resolve to the side the
// manager is drawing rather than guessing.
func agentPaneTheme() tmux.PaneTheme {
	fgbg := "15;0"
	if backdropIsLight() {
		fgbg = "0;15"
	}
	// Inheriting, the agent panes keep the terminal's own background as
	// well: an empty Background is what tells the driver to unset the pane
	// style rather than write one of ours.
	bg := ""
	if backdropSyncing() {
		bg = current.Bg
	}
	return tmux.PaneTheme{Background: bg, ColorFgBg: fgbg}
}

// current is the live token set; renderers that need a raw SGR sequence
// (rather than a lipgloss style) read their hex from here.
var current = themes[themeIndex(defaultTheme)]

// SyncTerminalBackground puts the terminal's background where the frame
// expects it (OSC 11). The frame can only paint its cell grid; any window
// padding the terminal draws around that grid keeps the terminal's color,
// so the two must agree for the frame's edges to look exact.
//
// Inheriting, agreeing means the colour the terminal already had: the write
// is a no-op at startup and a repair afterwards, when an editor or an
// attached agent has repainted the window for itself. A terminal that never
// told us its background gets nothing at all, since there is no colour to
// put back and imposing one is the thing being avoided -- unless one of ours
// is still up, which OSC 111 takes back to the terminal's own. Syncing, it is
// the theme's own backdrop. Terminals without OSC 11 ignore it either way.
func SyncTerminalBackground() {
	if backdropSyncing() {
		backdropPainted = true
		emitToTerminal("\x1b]11;" + current.Bg + "\x07")
		return
	}
	switch {
	case terminalBg != "":
		emitToTerminal("\x1b]11;" + terminalBg + "\x07")
	case backdropPainted:
		emitToTerminal("\x1b]111\x07")
	}
	backdropPainted = false
}

// syncPaneTheme hands the tmux server the background agent panes render on,
// so an agent that auto-detects its palette resolves to the same side the
// manager is drawing. Sessions that are already running keep whatever they
// resolved at startup; the theme reaches them on their next launch. The
// chosen theme is recorded synchronously so a session created right after
// still opens on it; the push to a running server shells out to tmux, so it
// runs as a command off the update path and surfaces a failure through errMsg.
func (m *Model) syncPaneTheme() tea.Cmd {
	m.tmux.PublishPaneTheme(agentPaneTheme())
	return func() tea.Msg {
		if err := m.tmux.PushPaneTheme(); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// ResetTerminalBackground restores the terminal's own background (OSC 111)
// when the manager exits. Inheriting, with no backdrop of ours still up,
// there is nothing to undo, and OSC 111 would drop a background the operator set for their own shell back
// to the terminal profile's default.
func ResetTerminalBackground() {
	if !backdropSyncing() && !backdropPainted {
		return
	}
	emitToTerminal("\x1b]111\x07")
}

// emitToTerminal sends a control sequence to whatever is actually drawing
// the window. Run under tmux, only the passthrough envelope goes out: a
// plain OSC would make tmux recolor our pane's default background, and a
// recolored pane paints as explicit color while the terminal's padding
// keeps blending its own — the exact ring the backdrop scheme exists to
// avoid. EnableTerminalPassthrough must have opened the envelope first.
func emitToTerminal(seq string) {
	_ = termseq.Emit(seq)
}

// EnableTerminalPassthrough opens the tmux passthrough envelope the backdrop
// sync and the clipboard fallback both write through.
func EnableTerminalPassthrough() {
	termseq.EnablePassthrough()
}

// bgSeq is the raw "set background" SGR for a hex color, for the few spots
// that paint a background around content lipgloss already styled. It emits
// truecolor because v2 dropped the render-time color profile: Bubble Tea's
// writer downsamples the whole frame on the way out, this sequence with it,
// so a 256-color terminal still ends up with an indexed sequence.
func bgSeq(hex string) string {
	if out, ok := bgSeqCache[hex]; ok {
		return out
	}
	out := ansi.Style{}.BackgroundColor(lipgloss.Color(hex)).String()
	bgSeqCache[hex] = out
	return out
}

// fgSeq is the raw "set foreground" SGR for a hex color.
func fgSeq(hex string) string {
	return ansi.Style{}.ForegroundColor(lipgloss.Color(hex)).String()
}

// hexRGB parses "#rrggbb"; an unparseable value reads as black, which is
// visible enough in a diff to be caught rather than silently mistinted.
func hexRGB(hex string) (int, int, int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	val, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(val>>16) & 0xff, int(val>>8) & 0xff, int(val) & 0xff
}

// mix blends two hex colors, ratio 0 returning a and 1 returning b. Used
// for derived tones (row gutters, gauge tracks) so a theme only has to
// declare its anchors.
func mix(a, b string, ratio float64) string {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	ar, ag, ab := hexRGB(a)
	br, bg, bb := hexRGB(b)
	blend := func(x, y int) int {
		return int(float64(x)*(1-ratio) + float64(y)*ratio + 0.5)
	}
	return fmt.Sprintf("#%02x%02x%02x", blend(ar, br), blend(ag, bg), blend(ab, bb))
}
