package views

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

// Icons are the design's SVGs (design/*.dc.html), 24×24, drawn in the
// text colour of their element unless they carry their own.
const (
	stroke     = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">`
	strokeThin = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">`
	strokeBold = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">`
	filled     = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor">`
	end        = `</svg>`
)

var iconSrc = map[string]string{
	"filter":       stroke + `<path d="M4 7h16M7 12h10M10 17h4"/>` + end,
	"search":       stroke + `<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4-4"/>` + end,
	"plus":         stroke + `<path d="M12 5v14M5 12h14"/>` + end,
	"server":       stroke + `<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/>` + end,
	"chevronDown":  strokeBold + `<path d="m6 9 6 6 6-6"/>` + end,
	"chevronSmall": stroke + `<path d="m8 10 4 4 4-4"/>` + end,
	"chevronRight": strokeBold + `<path d="m9 6 6 6-6 6"/>` + end,
	"chevronLeft":  strokeBold + `<path d="m15 6-6 6 6 6"/>` + end,
	"pin":          `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#F5C542"><path d="M9 3h6l-1 6 4 4H6l4-4-1-6zM12 13v8" stroke="#F5C542" stroke-width="2" stroke-linejoin="round"/></svg>`,
	"gear":         strokeThin + `<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>` + end,
	"more":         filled + `<circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/>` + end,
	"terminal":     stroke + `<rect x="3" y="4" width="18" height="16" rx="3"/><path d="m7 9 3 3-3 3M12 15h5"/>` + end,
	"wrench":       stroke + `<path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18v3h3l6.3-6.3a4 4 0 0 0 5.4-5.4l-2.5 2.5-2.4-.6-.6-2.4z"/>` + end,
	"globe":        stroke + `<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>` + end,
	"mic":          stroke + `<rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5 11a7 7 0 0 0 14 0M12 18v3"/>` + end,
	"stop":         filled + `<rect x="5" y="5" width="14" height="14" rx="3"/>` + end,
	"send":         strokeBold + `<path d="M12 19V5M5 12l7-7 7 7"/>` + end,
	"user":         strokeThin + `<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>` + end,
	"agents":       strokeThin + `<circle cx="9" cy="9" r="4"/><circle cx="17" cy="15" r="3"/><path d="M2 21a7 7 0 0 1 11-5.7"/>` + end,
	"halfCircle":   `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2"/><path d="M12 3a9 9 0 0 1 0 18z" fill="currentColor"/></svg>`,
	"bell":         strokeThin + `<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9M10 21h4"/>` + end,
	"shield":       strokeThin + `<path d="M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z"/>` + end,
	"download":     strokeThin + `<path d="M12 3v12M7 10l5 5 5-5M5 21h14"/>` + end,
	"voice":        `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 10v4M10 7v10M14 9v6M18 11v2"/></svg>`,
	"sun":          stroke + `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>` + end,
	"moon":         stroke + `<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>` + end,
	"palette":      stroke + `<path d="M12 3a9 9 0 1 0 0 18c1.2 0 1.8-.9 1.4-2l-.4-1a1.7 1.7 0 0 1 1.6-2.3H17a4 4 0 0 0 4-4c0-4.7-4-8.7-9-8.7z"/><circle cx="7.5" cy="11" r="1"/><circle cx="10" cy="7" r="1"/><circle cx="15" cy="7.5" r="1"/>` + end,
	"check":        `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 5 5 9-10"/></svg>`,
	"grid":         filled + `<rect x="3" y="3" width="5" height="5" rx="1"/><rect x="10" y="3" width="5" height="5" rx="1"/><rect x="17" y="3" width="4" height="5" rx="1"/><rect x="3" y="10" width="5" height="5" rx="1"/><rect x="10" y="10" width="5" height="5" rx="1"/><rect x="17" y="10" width="4" height="5" rx="1"/><rect x="3" y="17" width="5" height="4" rx="1"/><rect x="10" y="17" width="5" height="4" rx="1"/><rect x="17" y="17" width="4" height="4" rx="1"/>` + end,
	"smile":        filled + `<path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm-3.5 7a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zm7 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zM7.5 15h9a4.5 4.5 0 0 1-9 0z"/>` + end,
	"pulse":        stroke + `<path d="M3 12h2l2-6 4 14 3-10 2 4h5"/>` + end,
	"image":        stroke + `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m3 17 5-5 4 4 3-3 6 6"/><circle cx="16" cy="9" r="1.5"/>` + end,
	"window":       stroke + `<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M9 4v16"/>` + end,
	"key":          stroke + `<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M16 7l3 3M14 9l2 2"/>` + end,
	"copy":         stroke + `<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>` + end,
	"warn":         stroke + `<path d="M12 3 2 21h20zM12 10v5M12 18h.01"/>` + end,
	"x":            stroke + `<path d="M6 6l12 12M18 6 6 18"/>` + end,
}

var (
	iconMu    sync.Mutex
	iconCache = map[string]*ui.SVG{}
)

// icon returns the parsed SVG named name.
func icon(name string) *ui.SVG {
	iconMu.Lock()
	defer iconMu.Unlock()
	if s, ok := iconCache[name]; ok {
		return s
	}
	src, ok := iconSrc[name]
	if !ok {
		panic("views: no icon " + name)
	}
	s := ui.MustParseSVG([]byte(src))
	iconCache[name] = s
	return s
}

// Icon draws a named icon at size px, in col.
func Icon(c *ui.Context, name string, size float32, col ui.Color) ui.Element {
	return ui.Icon(c, icon(name)).Size(size, size).TextColor(col).Shrink(0)
}
