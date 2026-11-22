// Package appicon draws Hermes Desktop's icon: a glass marble with
// swirling veins on a Midnight tile, in any of the accent colours (the
// icon colour is a setting). It is drawn from maths rather than shipped
// as images, so every colour and size is the same picture.
package appicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Colours are the marble colours the settings offer, by accent name; Pink
// is the design's rose orb.
var Colours = map[string]string{
	"Pink":    "#B9566F",
	"Blue":    "#4A66D8",
	"Sky":     "#2F6FD6",
	"Indigo":  "#6164CF",
	"Teal":    "#3E9E9C",
	"Green":   "#4FA45E",
	"Yellow":  "#C9A93A",
	"Orange":  "#D9823A",
	"Coral":   "#D85550",
	"Rainbow": "rainbow",
}

// Names lists the colours in the settings' order.
var Names = []string{"Blue", "Sky", "Indigo", "Teal", "Green", "Yellow", "Orange", "Coral", "Pink", "Rainbow"}

type vec struct{ x, y, z float64 }

func (a vec) add(b vec) vec       { return vec{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec) mul(k float64) vec   { return vec{a.x * k, a.y * k, a.z * k} }
func (a vec) dot(b vec) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec) norm() vec           { return a.mul(1 / math.Sqrt(a.dot(a))) }
func mix(a, b vec, t float64) vec { return a.mul(1 - t).add(b.mul(t)) }

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func smooth(e0, e1, x float64) float64 {
	t := clamp01((x - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}

func hex(s string) vec {
	var r, g, b int
	for i, p := range []*int{&r, &g, &b} {
		v := 0
		for _, c := range s[1+2*i : 3+2*i] {
			v *= 16
			switch {
			case c >= '0' && c <= '9':
				v += int(c - '0')
			case c >= 'a' && c <= 'f':
				v += int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				v += int(c-'A') + 10
			}
		}
		*p = v
	}
	return vec{float64(r) / 255, float64(g) / 255, float64(b) / 255}
}

// hsv turns a hue (0..1) at full saturation into a colour.
func hue(h float64) vec {
	h = h - math.Floor(h)
	r := clamp01(math.Abs(h*6-3) - 1)
	g := clamp01(2 - math.Abs(h*6-2))
	b := clamp01(2 - math.Abs(h*6-4))
	return vec{r, g, b}
}

// ---- noise ----

func hash3(x, y, z int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263 + uint32(z)*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h)/float64(math.MaxUint32)*2 - 1
}

func noise(p vec) float64 {
	x0, y0, z0 := math.Floor(p.x), math.Floor(p.y), math.Floor(p.z)
	fx, fy, fz := p.x-x0, p.y-y0, p.z-z0
	ix, iy, iz := int(x0), int(y0), int(z0)
	u := func(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }
	ux, uy, uz := u(fx), u(fy), u(fz)
	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }
	c := func(dx, dy, dz int) float64 { return hash3(ix+dx, iy+dy, iz+dz) }
	return lerp(
		lerp(lerp(c(0, 0, 0), c(1, 0, 0), ux), lerp(c(0, 1, 0), c(1, 1, 0), ux), uy),
		lerp(lerp(c(0, 0, 1), c(1, 0, 1), ux), lerp(c(0, 1, 1), c(1, 1, 1), ux), uy), uz)
}

func fbm(p vec) float64 {
	s, a := 0.0, 0.5
	for range 5 {
		s += a * noise(p)
		p = p.mul(2.03).add(vec{1.7, 9.2, 3.1})
		a *= 0.5
	}
	return s
}

// ---- drawing ----

// Options choose what is drawn.
type Options struct {
	Colour string // a name of Colours ("" is Pink)
	// Bare leaves out the tile: the marble alone on transparency (small
	// sizes in the settings, a favicon).
	Bare bool
}

// Render draws the icon at size×size pixels.
func Render(size int, o Options) *image.RGBA {
	col, ok := Colours[o.Colour]
	if !ok {
		col = Colours["Pink"]
	}
	rainbow := col == "rainbow"
	base := vec{0.72, 0.42, 0.9}
	if !rainbow {
		base = hex(col)
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	S := float64(size)
	ss := 3 // supersamples per axis: small sizes need more
	if size >= 160 {
		ss = 2
	}
	// Geometry, in units of the canvas.
	tileInset, tileR := 0.098, 0.225 // macOS-style 824/1024 tile, rounded
	cx, cy, R := 0.5, 0.46, 0.27
	if o.Bare {
		cx, cy, R = 0.5, 0.48, 0.42
	}
	light := vec{-0.55, -0.65, 0.75}.norm()
	half := light.add(vec{0, 0, 1}).norm()

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var acc [4]float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(px) + (float64(sx)+0.5)/float64(ss)) / S
					v := (float64(py) + (float64(sy)+0.5)/float64(ss)) / S
					c, a := shade(u, v, o.Bare, rainbow, base, cx, cy, R, tileInset, tileR, light, half)
					acc[0] += c.x * a
					acc[1] += c.y * a
					acc[2] += c.z * a
					acc[3] += a
				}
			}
			n := float64(ss * ss)
			a := acc[3] / n
			if a <= 0 {
				continue
			}
			// premultiplied average, stored straight in RGBA (which is premultiplied)
			img.SetRGBA(px, py, color.RGBA{
				R: uint8(clamp01(acc[0]/n) * 255), G: uint8(clamp01(acc[1]/n) * 255),
				B: uint8(clamp01(acc[2]/n) * 255), A: uint8(clamp01(a) * 255),
			})
		}
	}
	return img
}

// roundedRect is the signed distance to a rounded square (negative inside).
func roundedRect(u, v, inset, r float64) float64 {
	h := 0.5 - inset
	qx := math.Abs(u-0.5) - (h - r)
	qy := math.Abs(v-0.5) - (h - r)
	out := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
	return out + math.Min(math.Max(qx, qy), 0) - r
}

// shade returns the colour and coverage of one sample (straight colour).
func shade(u, v float64, bare, rainbow bool, base vec, cx, cy, R, inset, tileR float64, light, half vec) (vec, float64) {
	var out vec
	alpha := 0.0
	over := func(c vec, a float64) { // c over out, straight alpha
		if a <= 0 {
			return
		}
		na := a + alpha*(1-a)
		out = c.mul(a).add(out.mul(alpha * (1 - a))).mul(1 / na)
		alpha = na
	}
	if !bare {
		// Drop shadow of the tile, then the tile: Midnight, lit from the top.
		d := roundedRect(u, v-0.012, inset, tileR)
		over(vec{0, 0, 0.02}, 0.35*(1-smooth(-0.01, 0.03, d)))
		d = roundedRect(u, v, inset, tileR)
		if d < 0.002 {
			t := clamp01((v - inset) / (1 - 2*inset))
			tile := mix(hex("#1A1745"), hex("#04041F"), t)
			// a soft rose glow behind the marble
			g := math.Exp(-math.Pow(math.Hypot(u-cx, v-cy)/0.36, 2))
			tile = tile.add(mix(base, vec{1, 1, 1}, 0.2).mul(0.32 * g))
			// a hairline of light along the top edge
			tile = tile.add(vec{1, 1, 1}.mul(0.08 * smooth(-0.012, 0, d) * (1 - t)))
			over(tile, 1-smooth(-0.002, 0.002, d))
		}
		// Contact shadow under the marble, and the caustic: light the
		// marble gathers onto the tile beside its shadow.
		sx, sy := (u-cx)/(R*0.9), (v-(cy+R*0.98))/(R*0.22)
		over(vec{0, 0, 0.03}, 0.55*math.Exp(-(sx*sx+sy*sy)*1.6))
		kx, ky := (u-(cx+R*0.32))/(R*0.34), (v-(cy+R*1.02))/(R*0.09)
		over(mix(base, vec{1, 1, 1}, 0.45), 0.45*math.Exp(-(kx*kx+ky*ky)*2))
	}

	x, y := (u-cx)/R, (v-cy)/R
	r2 := x*x + y*y
	edge := 1 - smooth(1-0.006/R, 1+0.006/R, math.Sqrt(r2))
	if edge <= 0 {
		return out, alpha
	}
	z := math.Sqrt(math.Max(0, 1-r2))
	n := vec{x, y, z}
	// Swirls inside the glass: a field on a slightly shrunk sphere (the
	// marble's depth), domain-warped twice for broad, smooth ribbons.
	p := vec{x, y, z*0.8 + 0.2}.mul(1.15)
	w := vec{fbm(p.add(vec{3.1, 0, 0})), fbm(p.add(vec{0, 5.7, 0})), fbm(p.add(vec{0, 0, 8.3}))}
	q := p.add(w.mul(1.25))
	w2 := vec{fbm(q.add(vec{1.3, 2.1, 0})), fbm(q.add(vec{4.4, 0, 1.9})), 0}
	f := fbm(q.add(w2.mul(1.1)))     // -~1..~1
	ribbon := smooth(-0.05, 0.35, f) // broad lighter ribbons
	band := math.Sin(f*9 + q.x*1.5)  // fine lines within them
	vein := math.Pow(1-math.Abs(band), 10) * smooth(0, 0.5, ribbon)
	cloud := 0.5 + 0.5*fbm(q.mul(0.7).add(vec{7, 7, 7}))

	deep := base.mul(0.18)
	pale := mix(base, vec{1, 1, 1}, 0.6)
	diffuse := clamp01(n.dot(light))
	body := mix(deep, base, 0.2+0.8*diffuse)
	body = mix(body, mix(base, pale, 0.55), 0.7*ribbon*(0.35+0.65*diffuse))
	body = mix(body, deep, 0.55*smooth(-0.15, -0.45, f)) // dark ribbons
	body = body.mul(0.85 + 0.25*cloud)
	body = body.mul(0.55 + 0.45*math.Sqrt(z)) // denser glass towards the edge
	if rainbow {
		body = mix(body, mix(hue(0.72+0.45*f+0.15*x), vec{1, 1, 1}, 0.2).mul(0.35+0.55*diffuse), 0.45)
	}
	veinCol := pale
	if rainbow {
		veinCol = mix(hue(0.55+0.6*f), vec{1, 1, 1}, 0.5)
	}
	body = mix(body, veinCol, 0.45*vein*(0.3+0.7*diffuse))
	// Light through the glass, glowing on the side away from the light.
	back := clamp01(-(x*light.x + y*light.y) / math.Max(1e-6, math.Hypot(light.x, light.y)))
	sss := math.Pow(1-z, 1.3) * back
	body = body.add(mix(base, vec{1, 1, 1}, 0.3).mul(0.7 * sss))
	// Fresnel rim, then polished-glass highlights: a small sharp one and
	// a soft window reflection above it.
	rim := math.Pow(1-z, 3.5)
	body = mix(body, pale, 0.3*rim)
	nh := clamp01(n.dot(half))
	body = body.add(vec{1, 1, 1}.mul(math.Pow(nh, 400)*0.85 + math.Pow(nh, 30)*0.07))
	wx, wy := (x+0.36)/0.34, (y+0.46)/0.17 // a soft window, wider than tall
	win := math.Exp(-math.Pow(wx*wx+wy*wy, 2)) * 0.45
	body = body.add(vec{1, 1, 1}.mul(win * smooth(0.0, 0.4, z)))
	// Ambient occlusion towards the bottom.
	body = body.mul(0.82 + 0.18*(1-clamp01((y+1)/2)))
	over(vec{clamp01(body.x), clamp01(body.y), clamp01(body.z)}, edge)
	return out, alpha
}

// PNG encodes the icon as a PNG.
func PNG(size int, o Options) []byte {
	var b bytes.Buffer
	png.Encode(&b, Render(size, o))
	return b.Bytes()
}
