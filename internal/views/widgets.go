package views

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/theme"
)

// ---- agent avatars ----

// AvatarStyle is how an agent is drawn.
type AvatarStyle struct {
	Color ui.Color // the orb's body colour
	Bot   bool     // a rounded "face" instead of an orb (helix in the design)
}

// orbStops is the design's orb gradient for a body colour: a highlight, the
// colour at 40% and a dark rim at 80% (radial-gradient(circle at 32% 28%, …)).
func orbStops(col ui.Color) []ui.GradientStop {
	return []ui.GradientStop{
		{Color: theme.Tint(col, 0.78), At: 0},
		{Color: col, At: 0.42},
		{Color: theme.Shade(col, 0.32), At: 0.81},
	}
}

// breathe is the design's 3.2 s ease-in-out scale 1 → 1.07 → 1.
func breathe(now time.Time, period time.Duration) float32 {
	ph := float64(now.UnixNano()%int64(period)) / float64(period)
	return 1 + 0.035*float32(1-math.Cos(2*math.Pi*ph))
}

// Avatar draws an agent. With animate it breathes; working, it also bobs
// ("move about while an agent is working"). It paints only itself each
// frame: the view does not rebuild.
func Avatar(c *ui.Context, st AvatarStyle, w, h float32, animate, working bool) ui.Element {
	reduce := c.Preferences().ReduceMotion
	e := ui.Box(c).Size(w, h).Shrink(0)
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		s, dy := float32(1), float32(0)
		if animate && !reduce {
			period := 3200 * time.Millisecond
			if working {
				period = 1600 * time.Millisecond
				t := float64(p.Now().UnixNano()%int64(1200*time.Millisecond)) / float64(1200*time.Millisecond)
				dy = float32(math.Sin(2*math.Pi*t)) * r.H * 0.06
			}
			s = breathe(p.Now(), period)
			p.AnimationFrameRate(30)
		}
		cw, ch := r.W/1.07*s, r.H/1.07*s
		box := ui.Rect{X: r.X + (r.W-cw)/2, Y: r.Y + (r.H-ch)/2 + dy, W: cw, H: ch}
		if st.Bot {
			rad := box.H * 0.3
			p.Fill(box, st.Color, rad)
			ink := theme.Shade(st.Color, 0.15)
			ew, eh := box.W*0.16, box.H*0.1
			gap := box.W * 0.1
			y := box.Y + box.H/2 - eh/2
			if working && !reduce { // blink
				if (p.Now().UnixMilli()/180)%14 == 0 {
					eh *= 0.3
				}
			}
			p.Fill(ui.Rect{X: box.X + box.W/2 - gap/2 - ew, Y: y, W: ew, H: eh}, ink, eh/2)
			p.Fill(ui.Rect{X: box.X + box.W/2 + gap/2, Y: y, W: ew, H: eh}, ink, eh/2)
			return
		}
		p.FillRadialGradient(box, ui.RadialGradient{Stops: orbStops(st.Color), Center: ui.Point{X: 0.32, Y: 0.28}}, box.W/2)
		// The inset shadow (inset -4px -6px 12px rgba(0,0,0,.35)).
		p.FillRadialGradient(box, ui.RadialGradient{Stops: []ui.GradientStop{
			{Color: ui.RGBA(0, 0, 0, 0), At: 0.55},
			{Color: ui.RGBA(0, 0, 0, 0.35), At: 1},
		}, Center: ui.Point{X: 0.42, Y: 0.38}}, box.W/2)
	})
	return e
}

// ---- loaders ----

// gridPhase returns each loader's marching order: when cell (col,row)
// lights, as a fraction of the 1.2 s cycle.
func gridPhase(id string, col, row int) float64 {
	switch id {
	case "g1":
		return float64(col) * 0.15
	case "g2":
		return float64(6-col-row) * 0.1
	case "g4":
		order := [4][4]int{{0, 1, 2, 3}, {11, 12, 13, 4}, {10, 15, 14, 5}, {9, 8, 7, 6}}
		return float64(order[row][col]) * 0.075
	case "g5":
		return float64((col+row)%2) * 0.5
	}
	return float64(col+row) * 0.12 // g3: the design's diagonal march
}

// gridLit is the design's still pattern of each grid (PATTERNS).
func gridLit(id string, col, row int) bool {
	switch id {
	case "g1":
		return col >= 2
	case "g2":
		return col+row >= 4
	case "g4":
		return !(col == 0 && row < 2)
	case "g5":
		return (col+row)%2 == 0
	case "summary": // the finished turn's group pill (gridStatic)
		return row == 3 || (row == 2 && col > 1)
	}
	return row == 3 // g3
}

// hgrid is the keyframes 0%,100% {opacity:.18} 50% {opacity:1} with ease-in-out.
func hgrid(t float64) float32 {
	t -= math.Floor(t)
	x := 1 - math.Abs(2*t-1) // 0→1→0
	e := x * x * (3 - 2*x)   // ease-in-out
	return float32(0.18 + 0.82*e)
}

// GridLoader draws a 4×4 marching grid of cell-px squares gap apart. With
// animate the cells march; still, the grid shows its pattern.
func GridLoader(c *ui.Context, id string, col ui.Color, cell, gap float32, animate bool) ui.Element {
	size := 4*cell + 3*gap
	reduce := c.Preferences().ReduceMotion
	e := ui.Box(c).Size(size, size).Shrink(0)
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		t := float64(p.Now().UnixNano()%int64(time.Hour)) / float64(1200*time.Millisecond)
		rad := float32(1)
		if cell > 3 {
			rad = 1.5
		}
		if cell >= 5 {
			rad = 2
		}
		for row := range 4 {
			for cl := range 4 {
				var a float32
				switch {
				case animate && !reduce:
					a = hgrid(t - gridPhase(id, cl, row))
				case gridLit(id, cl, row):
					a = 1
				default:
					a = 0.24
				}
				x := r.X + float32(cl)*(cell+gap)
				y := r.Y + float32(row)*(cell+gap)
				p.Fill(ui.Rect{X: x, Y: y, W: cell, H: cell}, col.Alpha(a*float32(col.A)/255), rad)
			}
		}
		if animate && !reduce {
			p.AnimationFrameRate(30)
		}
	})
	return e
}

// SphereLoader draws one of the spheres of light (l1..l5) or of mist
// (m1..m5) in a size-px box.
func SphereLoader(c *ui.Context, id string, pal *theme.Palette, size float32, animate bool) ui.Element {
	reduce := c.Preferences().ReduceMotion
	accent, soft := pal.Accent, pal.AccentTint
	white := ui.RGB(255, 255, 255)
	clear := func(col ui.Color) ui.Color { return col.Alpha(0) }
	bg := pal.Surf2
	e := ui.Box(c).Size(size, size).Shrink(0)
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		now := p.Now()
		s := float32(1)
		spin := float32(0)
		if animate && !reduce {
			s = breathe(now, 3200*time.Millisecond)
			spin = float32(float64(now.UnixNano()%int64(4*time.Second)) / float64(4*time.Second) * 360)
			p.AnimationFrameRate(30)
		}
		d := r.W / 1.07 * s
		box := ui.Rect{X: r.X + (r.W-d)/2, Y: r.Y + (r.H-d)/2, W: d, H: d}
		cx, cy := box.X+d/2, box.Y+d/2
		radial := func(stops []ui.GradientStop, center ui.Point) {
			p.FillRadialGradient(box, ui.RadialGradient{Stops: stops, Center: center, Radius: d / 2}, d/2)
		}
		switch id {
		case "think": // "Thinking…": radial-gradient(circle at 35% 30%, #fff, accent 45%, transparent 75%)
			radial([]ui.GradientStop{gs(white, 0), gs(accent, 0.6), gs(clear(accent), 1)}, ui.Point{X: 0.35, Y: 0.3})
		case "l1":
			radial([]ui.GradientStop{gs(white, 0), gs(soft, 0.6), gs(clear(soft), 1)}, ui.Point{})
		case "l2":
			radial([]ui.GradientStop{gs(soft, 0), gs(accent, 0.7), gs(clear(accent), 1)}, ui.Point{})
			p.Clip(box, d/2, func() {
				for y := box.Y; y < box.Y+d; y += d / 6 {
					p.Fill(ui.Rect{X: box.X, Y: y, W: d, H: d / 14}, white.Alpha(0.5), 0)
				}
			})
		case "l3":
			// circle at 30% 30%, reaching the far side: a lit sphere.
			p.FillRadialGradient(box, ui.RadialGradient{Stops: []ui.GradientStop{
				gs(white, 0), gs(soft, 0.3), gs(accent, 0.62), gs(theme.Shade(accent, 0.7), 1),
			}, Center: ui.Point{X: 0.3, Y: 0.3}, Radius: d * 0.95}, d/2)
		case "l4":
			radial([]ui.GradientStop{gs(clear(white), 0.47), gs(white, 0.61), gs(soft, 0.78), gs(clear(soft), 1)}, ui.Point{})
		case "l5":
			radial([]ui.GradientStop{gs(white, 0), gs(soft, 0.62), gs(clear(soft), 1)}, ui.Point{})
			for a := float32(0); a < 360; a += 20 {
				th := float64(a+spin/4) * math.Pi / 180
				x1, y1 := cx+float32(math.Cos(th))*d*0.48, cy+float32(math.Sin(th))*d*0.48
				p.Line(cx, cy, x1, y1, max(1, d/40), white.Alpha(0.7))
			}
		case "m1", "m2", "m3", "m4":
			step := map[string]float32{"m1": 4, "m2": 6, "m3": 5, "m4": 4.5}[id] * size / 42
			rot := float64(int(id[1]-'1')*17)*math.Pi/180 + float64(spin)*math.Pi/720
			dot := max(0.8, 1.1*size/42)
			for gy := -d / 2; gy <= d/2; gy += step {
				for gx := -d / 2; gx <= d/2; gx += step {
					x := cx + gx*float32(math.Cos(rot)) - gy*float32(math.Sin(rot))
					y := cy + gx*float32(math.Sin(rot)) + gy*float32(math.Cos(rot))
					dist := float32(math.Hypot(float64(x-cx), float64(y-cy))) / (d / 2)
					a := float32(1)
					if dist > 0.48*2 {
						continue
					}
					if dist > 0.48 {
						a = 1 - (dist-0.48)/0.48
					}
					p.Fill(ui.Rect{X: x - dot, Y: y - dot, W: 2 * dot, H: 2 * dot}, accent.Alpha(a), dot)
				}
			}
		case "m5":
			// A dotted ellipse, turning (design: border 2px dotted, rotate(-24deg)).
			ew, eh := d*0.86, d*0.52
			rot := (-24 + float64(spin)) * math.Pi / 180
			p.Fill(ui.Rect{X: cx - ew/2 - 3, Y: cy - eh/2 - 3, W: ew + 6, H: eh + 6}, clear(bg), eh)
			for k := 0; k < 28; k++ {
				th := float64(k) / 28 * 2 * math.Pi
				ex, ey := float64(ew/2)*math.Cos(th), float64(eh/2)*math.Sin(th)
				x := cx + float32(ex*math.Cos(rot)-ey*math.Sin(rot))
				y := cy + float32(ex*math.Sin(rot)+ey*math.Cos(rot))
				dot := max(0.8, d/38)
				p.Fill(ui.Rect{X: x - dot, Y: y - dot, W: 2 * dot, H: 2 * dot}, accent, dot)
			}
		}
	})
	return e
}

// Loader draws the user's chosen loading indicator. Grids come at the
// design's cell size (2.5 small, 3.5 large); spheres fill a box of the
// grid's size, so swapping the setting never moves the layout.
func Loader(c *ui.Context, id string, pal *theme.Palette, large bool) ui.Element {
	cell, gap := float32(2.5), float32(1.5)
	if large {
		cell, gap = 3.5, 2
	}
	if id == "" || id[0] == 'g' {
		return GridLoader(c, id, pal.Accent, cell, gap, true)
	}
	return SphereLoader(c, id, pal, 4*cell+3*gap+4, true)
}

// ---- controls ----

// CircleButton is the design's 40px round icon button.
func CircleButton(c *ui.Context, pal *theme.Palette, name, label string, size float32) ui.Element {
	b := ui.Row(c).Size(size, size).Radius(size/2).Background(pal.Surf).Border(1, pal.Line).
		AlignItems(ui.Center).Justify(ui.Center).Cursor(ui.CursorPointer).Focusable().Label(label).Tooltip(label)
	b.Children(func() {
		Icon(c, name, 18, pal.Text)
	})
	if b.Hovered() {
		b.Background(pal.Surf2)
	}
	return b
}

// PillButton is the design's rounded text button ("Share").
func PillButton(c *ui.Context, pal *theme.Palette, label string) ui.Element {
	b := ui.Row(c).Height(40).Padding(0, 16).Radius(999).Background(pal.Surf).Border(1, pal.Line).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().Label(label)
	b.Children(func() {
		ui.Text(c, label).FontSize(14).FontWeight(600).TextColor(pal.Text)
	})
	if b.Hovered() {
		b.Background(pal.Surf2)
	}
	return b
}

// Toggle is the design's switch: a 56×32 track and a white knob.
func Toggle(c *ui.Context, pal *theme.Palette, on *bool, label string) ui.Element {
	track := ui.Row(c).Size(56, 32).Radius(999).Padding(3).Shrink(0).Cursor(ui.CursorPointer).
		Focusable().Label(label).Role(ui.RoleSwitch).Checked(*on)
	x := track.Animate("knob", map[bool]float32{true: 24, false: 0}[*on], 160*time.Millisecond)
	if *on {
		track.Background(pal.Accent)
	} else {
		track.Background(pal.Surf2)
	}
	track.Children(func() {
		ui.Box(c).Size(26, 26).Radius(13).Background(ui.RGB(255, 255, 255)).
			Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.3)).Translate(x, 0)
	})
	if track.Clicked() || track.Shortcut(0, ui.KeySpace) {
		*on = !*on
	}
	return track
}

// relTime is the sidebar's short relative time: 3m, 25m, 7h, 4d, 3w.
func relTime(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + "h"
	case d < 7*24*time.Hour:
		return itoa(int(d/(24*time.Hour))) + "d"
	case d < 365*24*time.Hour:
		return itoa(int(d/(7*24*time.Hour))) + "w"
	}
	return itoa(int(d/(365*24*time.Hour))) + "y"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// clock formats a duration as m:ss ("Thinking… 0:42").
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	m := s / 60
	s %= 60
	if s < 10 {
		return itoa(m) + ":0" + itoa(s)
	}
	return itoa(m) + ":" + itoa(s)
}

// stamp is the bubble's time: "22 Nov at 08:02".
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2 Jan") + " at " + t.Format("15:04")
}

func gs(c ui.Color, at float32) ui.GradientStop { return ui.GradientStop{Color: c, At: at} }
