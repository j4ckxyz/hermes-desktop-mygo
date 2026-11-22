// Package theme holds the design tokens of Hermes Desktop, taken from the
// design export (design/*.dc.html): the five canvases, the ten accents, the
// colours derived from an accent, the fonts and the text sizes. It turns the
// user's Look & Feel settings into a Palette for the views and a ui.Theme
// for MyGo's widgets.
package theme

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// Canvas is the paper the app is printed on: the colours of one
// light or dark mode (design/Main.dc.html, renderVals C).
type Canvas struct {
	Name     string
	Dark     bool
	Bg       ui.Color // window background
	Side     ui.Color // sidebar
	Surf     ui.Color // cards, buttons, bubbles
	Surf2    ui.Color // raised surfaces: tiles, icon wells
	Line     ui.Color // hairlines
	Line2    ui.Color // stronger lines: the session tree
	Text     ui.Color
	Muted    ui.Color
	Pill     ui.Color // tool-call pill background
	PillLine ui.Color // tool-call pill border
	OK       ui.Color // connected dot, success
}

// Canvas names, as the settings store them.
const (
	Midnight   = "Midnight"
	Black      = "Black"
	Gray       = "Gray"
	White      = "White"
	PaperWhite = "Paper White"
)

// Canvases lists every canvas, the dark ones first, in the settings' order.
var Canvases = []Canvas{
	{Name: Midnight, Dark: true, Bg: ui.Hex("#030320"), Side: ui.Hex("#06062B"), Surf: ui.Hex("#1C1A36"), Surf2: ui.Hex("#272545"),
		Line: ui.RGBA(255, 255, 255, 0.08), Line2: ui.RGBA(255, 255, 255, 0.16), Text: ui.Hex("#F4F3FA"), Muted: ui.Hex("#A2A0BE"),
		Pill: ui.Hex("#132036"), PillLine: ui.RGBA(70, 211, 138, 0.22), OK: ui.Hex("#46D38A")},
	{Name: Black, Dark: true, Bg: ui.Hex("#000000"), Side: ui.Hex("#0A0A0B"), Surf: ui.Hex("#1A1A1C"), Surf2: ui.Hex("#252528"),
		Line: ui.RGBA(255, 255, 255, 0.09), Line2: ui.RGBA(255, 255, 255, 0.17), Text: ui.Hex("#F5F5F7"), Muted: ui.Hex("#A3A3AA"),
		Pill: ui.Hex("#0F1B17"), PillLine: ui.RGBA(70, 211, 138, 0.22), OK: ui.Hex("#46D38A")},
	{Name: Gray, Dark: true, Bg: ui.Hex("#1C1C1E"), Side: ui.Hex("#161618"), Surf: ui.Hex("#2A2A2D"), Surf2: ui.Hex("#353539"),
		Line: ui.RGBA(255, 255, 255, 0.09), Line2: ui.RGBA(255, 255, 255, 0.18), Text: ui.Hex("#F5F5F7"), Muted: ui.Hex("#AEAEB5"),
		Pill: ui.Hex("#1F2A26"), PillLine: ui.RGBA(70, 211, 138, 0.24), OK: ui.Hex("#4FD892")},
	{Name: White, Dark: false, Bg: ui.Hex("#FFFFFF"), Side: ui.Hex("#F6F6F9"), Surf: ui.Hex("#F1F0F5"), Surf2: ui.Hex("#E9E8EF"),
		Line: ui.RGBA(0, 0, 0, 0.08), Line2: ui.RGBA(0, 0, 0, 0.15), Text: ui.Hex("#15141F"), Muted: ui.Hex("#5E5C70"),
		Pill: ui.Hex("#EEF6F1"), PillLine: ui.RGBA(23, 120, 74, 0.22), OK: ui.Hex("#17784A")},
	{Name: PaperWhite, Dark: false, Bg: ui.Hex("#FAF6EE"), Side: ui.Hex("#F3EEE3"), Surf: ui.Hex("#EFE9DC"), Surf2: ui.Hex("#E7E0D1"),
		Line: ui.RGBA(60, 40, 10, 0.09), Line2: ui.RGBA(60, 40, 10, 0.17), Text: ui.Hex("#1F1B14"), Muted: ui.Hex("#655D50"),
		Pill: ui.Hex("#ECF1E6"), PillLine: ui.RGBA(23, 120, 74, 0.22), OK: ui.Hex("#17784A")},
}

// DarkCanvases and LightCanvases are the names the settings offer for each mode.
var (
	DarkCanvases  = []string{Midnight, Black, Gray}
	LightCanvases = []string{White, PaperWhite}
)

// CanvasNamed returns the canvas called name, or Midnight.
func CanvasNamed(name string) Canvas {
	for _, c := range Canvases {
		if c.Name == name {
			return c
		}
	}
	return Canvases[0]
}

// Accent is one of the accent colours (design/Settings.dc.html, ACC).
type Accent struct {
	Name string
	Hex  string
}

// Accents in the settings' order. Rainbow's own colour is its swatch's
// centre; its swatch is a conic gradient of the others (RainbowStops).
var Accents = []Accent{
	{"Blue", "#5B7BF5"}, {"Sky", "#3B82F6"}, {"Indigo", "#7C7FE8"}, {"Teal", "#5EC8C6"}, {"Green", "#7ED58B"},
	{"Yellow", "#E8CF60"}, {"Orange", "#EFA35A"}, {"Coral", "#EE736E"}, {"Pink", "#EE8AB8"}, {"Rainbow", "#B98AF0"},
}

// DefaultAccent is Pink, the design's accent.
const DefaultAccent = "Pink"

// RainbowStops are the colours of the Rainbow swatch, around the circle.
var RainbowStops = []string{"#5B7BF5", "#5EC8C6", "#7ED58B", "#E8CF60", "#EFA35A", "#EE736E", "#EE8AB8", "#7C7FE8", "#5B7BF5"}

// AccentNamed returns the accent called name, or Pink.
func AccentNamed(name string) Accent {
	for _, a := range Accents {
		if a.Name == name {
			return a
		}
	}
	return Accents[8]
}

// Appearance chooses between the light and the dark canvas.
type Appearance string

const (
	Automatic Appearance = "Automatic" // follow the system
	Light     Appearance = "Light"
	Dark      Appearance = "Dark"
)

// Font choices (design/Settings.dc.html, FONTS).
const (
	FontRounded = "Rounded"
	FontSystem  = "System"
	FontSerif   = "Serif"
	FontMono    = "Mono"
)

// Fonts lists the font choices in the settings' order.
var Fonts = []string{FontRounded, FontSystem, FontSerif, FontMono}

// FontLabel is how the font picker names each choice.
func FontLabel(f string) string {
	switch f {
	case FontSystem:
		return "System"
	case FontSerif:
		return "Serif"
	case FontMono:
		return "Mono"
	}
	return "Rounded · Default"
}

// Family returns the CSS-like family list MyGo's Font takes for a choice.
// Nunito and JetBrains Mono are bundled (assets/fonts), so the rounded
// and mono looks are the same on every platform; on macOS the system's own
// rounded face comes first.
func Family(f string) string {
	switch f {
	case FontSystem:
		return "" // the system's UI font
	case FontSerif:
		return "New York, Newsreader, Georgia, Noto Serif, DejaVu Serif, serif"
	case FontMono:
		return MonoFamily
	}
	return "SF Pro Rounded, Nunito"
}

// MonoFamily is the family of code, commands and tool arguments.
const MonoFamily = "JetBrains Mono, monospace"

// MaxTextSize is the largest text-size step; step 0 follows the system.
const MaxTextSize = 6

// Settings is the subset of the Look & Feel settings the palette depends on.
type Settings struct {
	Appearance  Appearance
	LightCanvas string
	DarkCanvas  string
	Accent      string
	Font        string
	TextSize    int // 0 = system, 1..MaxTextSize = +1px each
}

// Palette is everything the views paint with: a canvas, an accent and the
// colours derived from them, plus the text metrics.
type Palette struct {
	Canvas
	AccentName string
	Accent     ui.Color
	AccentText ui.Color // links and accent-coloured text, readable on Bg
	OnAccent   ui.Color // text and icons on an accent fill
	AccentSoft ui.Color // tinted backgrounds (selected chips)
	AccentLine ui.Color // tinted borders
	AccentTint ui.Color // the accent mixed halfway to white (sphere loaders)
	Font       string   // family list for Font()
	Mono       string
	// BodySize is the size of message text; the others scale from it.
	BodySize float32
}

// Resolve builds the palette for the settings, with systemDark the
// system's appearance (followed when Appearance is Automatic) and
// textScale the system's text scale (followed by text-size step 0).
func Resolve(s Settings, systemDark bool, textScale float32) Palette {
	dark := systemDark
	switch s.Appearance {
	case Light:
		dark = false
	case Dark:
		dark = true
	}
	name := s.LightCanvas
	if dark {
		name = s.DarkCanvas
	}
	cv := CanvasNamed(name)
	if cv.Dark != dark { // a light name in the dark slot, or a bad value
		if dark {
			cv = CanvasNamed(Midnight)
		} else {
			cv = CanvasNamed(White)
		}
	}
	acc := AccentNamed(s.Accent)
	a := ui.Hex(acc.Hex)
	p := Palette{Canvas: cv, AccentName: acc.Name, Accent: a, Font: Family(s.Font), Mono: MonoFamily}
	if cv.Dark {
		p.AccentText = a
	} else {
		p.AccentText = Shade(a, 0.55)
	}
	if Luminance(a) > 0.55 {
		p.OnAccent = ui.Hex("#14131F")
	} else {
		p.OnAccent = ui.Hex("#FFFFFF")
	}
	soft := float32(0.10)
	if cv.Dark {
		soft = 0.13
	}
	p.AccentSoft = a.Alpha(soft)
	p.AccentLine = a.Alpha(0.4)
	p.AccentTint = Tint(a, 0.55)

	size := float32(14)
	if s.TextSize <= 0 {
		if textScale > 0 {
			size *= textScale
		}
	} else {
		size += float32(min(s.TextSize, MaxTextSize))
	}
	p.BodySize = size
	return p
}

// Scale returns a size from the design (at a 14px body) scaled to the
// palette's text size, so the whole UI follows the text-size setting.
func (p *Palette) Scale(px float32) float32 {
	return float32(math.Round(float64(px*p.BodySize/14)*2) / 2)
}

// Shade darkens c towards black, keeping k of each channel.
func Shade(c ui.Color, k float64) ui.Color {
	f := func(v uint8) uint8 { return uint8(math.Round(float64(v) * k)) }
	return ui.Color{R: f(c.R), G: f(c.G), B: f(c.B), A: c.A}
}

// Tint lightens c towards white by k.
func Tint(c ui.Color, k float64) ui.Color {
	f := func(v uint8) uint8 { return uint8(math.Round(float64(v) + (255-float64(v))*k)) }
	return ui.Color{R: f(c.R), G: f(c.G), B: f(c.B), A: c.A}
}

// Luminance is the design's luma (Rec. 709 weights on sRGB bytes), 0..1.
func Luminance(c ui.Color) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

// UITheme returns the MyGo theme that makes MyGo's own widgets (inputs,
// menus drawn in-window, scroll bars, switches, tooltips) match the palette.
func (p *Palette) UITheme() *ui.Theme {
	var t *ui.Theme
	if p.Dark {
		t = ui.DarkTheme()
	} else {
		t = ui.LightTheme()
	}
	t = t.WithAccent(p.Accent)
	t.Background = p.Bg
	t.Surface = p.Surf
	t.SurfaceHover = p.Surf2
	t.SurfacePressed = p.Surf2.Mix(p.Text, 0.06)
	t.Border = p.Line2
	t.Text = p.Text
	t.TextMuted = p.Muted
	t.AccentText = p.OnAccent
	t.InactiveAccent = p.Accent
	t.Focus = p.Accent.Alpha(0.6)
	t.Selection = p.Accent.Alpha(0.35)
	t.Success = p.OK
	t.Scrollbar = p.Line2
	t.Radius = 12
	t.FontSize = p.BodySize
	t.Font = p.Font
	t.Inverse, t.InverseText = p.Surf2, p.Text
	return t
}

// CanvasAccent is the colour of the accent called name.
func CanvasAccent(name string) ui.Color { return ui.Hex(AccentNamed(name).Hex) }
