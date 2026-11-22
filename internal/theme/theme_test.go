package theme

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestResolveFollowsAppearance(t *testing.T) {
	s := Settings{Appearance: Automatic, LightCanvas: PaperWhite, DarkCanvas: Black, Accent: "Pink"}
	if p := Resolve(s, true, 1); p.Name != Black || !p.Dark {
		t.Fatalf("automatic+system dark = %s", p.Name)
	}
	if p := Resolve(s, false, 1); p.Name != PaperWhite || p.Dark {
		t.Fatalf("automatic+system light = %s", p.Name)
	}
	s.Appearance = Dark
	if p := Resolve(s, false, 1); p.Name != Black {
		t.Fatalf("forced dark = %s", p.Name)
	}
	s.Appearance = Light
	if p := Resolve(s, true, 1); p.Name != PaperWhite {
		t.Fatalf("forced light = %s", p.Name)
	}
}

func TestResolveRejectsMismatchedCanvas(t *testing.T) {
	// A light canvas stored in the dark slot (a hand-edited file) falls back.
	p := Resolve(Settings{Appearance: Dark, DarkCanvas: White}, false, 1)
	if p.Name != Midnight {
		t.Fatalf("got %s", p.Name)
	}
	p = Resolve(Settings{Appearance: Light, LightCanvas: "nope"}, false, 1)
	if p.Name != White {
		t.Fatalf("got %s", p.Name)
	}
}

// The derived accent colours match the design's renderVals formulas.
func TestAccentDerivations(t *testing.T) {
	p := Resolve(Settings{Appearance: Dark, DarkCanvas: Midnight, Accent: "Pink"}, false, 1)
	if p.Accent != ui.Hex("#EE8AB8") || p.AccentText != p.Accent {
		t.Fatalf("dark accent text %v", p.AccentText)
	}
	// lum(#EE8AB8) = (0.2126*238 + 0.7152*138 + 0.0722*184)/255 ≈ 0.638 > 0.55
	if p.OnAccent != ui.Hex("#14131F") {
		t.Fatalf("on pink should be dark ink, got %v", p.OnAccent)
	}
	if p.AccentSoft.A < 32 || p.AccentSoft.A > 34 { // 0.13*255 ≈ 33
		t.Fatalf("accentSoft alpha %d", p.AccentSoft.A)
	}

	l := Resolve(Settings{Appearance: Light, LightCanvas: White, Accent: "Blue"}, false, 1)
	// shade(0.55) of #5B7BF5 = round(91*.55)=50, round(123*.55)=68, round(245*.55)=135
	if l.AccentText != ui.RGB(50, 68, 135) {
		t.Fatalf("light accent text %v", l.AccentText)
	}
	if l.OnAccent != ui.Hex("#FFFFFF") {
		t.Fatalf("on blue should be white, got %v", l.OnAccent)
	}
	// tint(0.55) of #EE8AB8: 238+17*.55=247.35→247, 138+117*.55=202.35→202, 184+71*.55=223.05→223
	if p.AccentTint != ui.RGB(247, 202, 223) {
		t.Fatalf("tint %v", p.AccentTint)
	}
}

func TestTextSize(t *testing.T) {
	if p := Resolve(Settings{}, true, 1.25); p.BodySize != 17.5 {
		t.Fatalf("system step should follow text scale: %v", p.BodySize)
	}
	if p := Resolve(Settings{TextSize: 3}, true, 2); p.BodySize != 17 {
		t.Fatalf("step 3 = 14+3: %v", p.BodySize)
	}
	if p := Resolve(Settings{TextSize: 99}, true, 1); p.BodySize != 14+MaxTextSize {
		t.Fatalf("clamped: %v", p.BodySize)
	}
	p := Resolve(Settings{TextSize: 2}, true, 1) // 16px body
	if got := p.Scale(17); got != 19.5 {         // 17*16/14 = 19.43 → 19.5
		t.Fatalf("scale %v", got)
	}
}

func TestUnknownAccentFallsBackToPink(t *testing.T) {
	if a := AccentNamed("Chartreuse"); a.Name != "Pink" {
		t.Fatal(a.Name)
	}
	if len(Accents) != 10 || len(Canvases) != 5 {
		t.Fatal("design has 10 accents and 5 canvases")
	}
}

func TestUIThemeMapsPalette(t *testing.T) {
	p := Resolve(Settings{Appearance: Dark, DarkCanvas: Gray, Accent: "Teal", Font: FontMono}, false, 1)
	ut := p.UITheme()
	if !ut.Dark || ut.Background != p.Bg || ut.Accent != p.Accent || ut.Text != p.Text || ut.Font != MonoFamily {
		t.Fatalf("ui theme does not follow palette: %+v", ut)
	}
}

// The bundled fonts register, and text in them renders in a headless window.
func TestFontsRegisterAndRender(t *testing.T) {
	if err := RegisterFonts(); err != nil {
		t.Fatal(err)
	}
	if err := RegisterFonts(); err != nil { // idempotent
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range ui.FontFamilies() {
		if f.Registered {
			found[f.Name] = true
		}
	}
	if !found["Nunito"] || !found["JetBrains Mono"] {
		t.Fatalf("registered families: %v", found)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Text(c, "Connect to Hermes").Font("Nunito").FontWeight(800)
		ui.Text(c, "hermes sessions list").Font(MonoFamily)
	}, 400, 100)
	if !tt.HasText("Connect to Hermes") || !tt.HasText("hermes sessions list") {
		t.Fatalf("texts: %q", tt.Texts())
	}
}
