// Package bench measures the app's hot paths headlessly: switching chats
// and streaming a reply, through the real views and core against the
// fake instance. Frames are drawn by MyGo's CPU rasterizer at 1440×900,
// so the times are an upper bound on a GPU window's.
package bench

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/cache"
	"hermes-desktop/internal/core"
	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/hermes/hermestest"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/theme"
	"hermes-desktop/internal/views"
)

// Stats summarises durations.
type Stats struct {
	N             int
	P50, P95, Max time.Duration
}

func (s Stats) String() string {
	return fmt.Sprintf("p50 %s · p95 %s · max %s (n=%d)", ms(s.P50), ms(s.P95), ms(s.Max), s.N)
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d)/1e6) }

// Summarise computes the stats of ds.
func Summarise(ds []time.Duration) Stats {
	if len(ds) == 0 {
		return Stats{}
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(q float64) time.Duration { return s[min(len(s)-1, int(q*float64(len(s))))] }
	return Stats{N: len(s), P50: at(0.5), P95: at(0.95), Max: s[len(s)-1]}
}

type rig struct {
	srv   *hermestest.Server
	app   *core.App
	u     *views.UI
	tt    *ui.Tester
	queue chan func()
	dir   string
}

func newRig() (*rig, error) {
	if err := theme.RegisterFonts(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "hermes-bench")
	if err != nil {
		return nil, err
	}
	r := &rig{srv: hermestest.New(hermestest.Options{Token: "t", Canonical: true}), queue: make(chan func(), 1<<16), dir: dir}
	c, err := cache.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		return nil, err
	}
	sec := secrets.NewWithBackend(&secrets.MemoryBackend{})
	sec.Set("i", secrets.Credentials{Token: "t"})
	inst := settings.Instance{ID: "i", Name: "Main", URL: r.srv.URL, Auth: settings.AuthToken}
	r.app = core.New(core.Deps{Post: func(fn func()) { r.queue <- fn }, Cache: c, Secrets: sec}, settings.Instances{List: []settings.Instance{inst}})
	r.app.Open(inst)
	s := settings.Default()
	s.Appearance, s.DarkCanvas = theme.Dark, theme.Midnight
	r.u = views.New(r.app, s, nil)
	r.u.Screen = views.ScreenMain
	r.tt = ui.NewTester(r.u.View, 1440, 900)
	// With motion on, the Tester's Frame runs every animation frame until
	// the view settles (orbs breathe, loaders march, the list glides), so
	// one switch would time several frames; a window shows the chat in the
	// first. Reduced motion makes Frame one frame.
	r.tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	return r, nil
}

func (r *rig) close() {
	if r.app.Conn != nil {
		r.app.Conn.Close()
	}
	r.srv.Close()
	if r.app.Cache != nil {
		r.app.Cache.Close()
	}
	os.RemoveAll(r.dir)
}

func (r *rig) drain() {
	for {
		select {
		case fn := <-r.queue:
			fn()
		default:
			return
		}
	}
}

func (r *rig) settle(cond func() bool) error {
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			return errors.New("bench: timed out")
		}
		r.drain()
		r.tt.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// ChatSwitch opens the sidebar's first chats once (as the app warms
// them), then measures switching between them: Select plus the frame that
// shows the new chat, n times.
func ChatSwitch(n int) (Stats, error) {
	r, err := newRig()
	if err != nil {
		return Stats{}, err
	}
	defer r.close()
	if err := r.settle(func() bool { c := r.app.Conn; return c.Synced && c.Status == core.Connected }); err != nil {
		return Stats{}, err
	}
	ids := []string{"20261122_080205_fb120f", "20261122_060000_0a0b0c", "20261122_071000_a1b2c3", "20261121_130000_6d6e6f"}
	for _, id := range ids {
		r.u.Select(id)
		if err := r.settle(func() bool { ch := r.app.Conn.Chats[id]; return ch != nil && ch.Fetched }); err != nil {
			return Stats{}, err
		}
	}
	var ds []time.Duration
	for i := range n + 10 {
		id := ids[i%len(ids)]
		t0 := time.Now()
		r.u.Select(id)
		r.tt.Frame()
		d := time.Since(t0)
		if os.Getenv("HERMES_BENCH_VERBOSE") != "" {
			fmt.Printf("switch %3d %-26s %6.2f ms\n", i, id, float64(d)/1e6)
		}
		if i >= 10 { // the first rounds warm the text caches
			ds = append(ds, d)
		}
		r.drain()
	}
	return Summarise(ds), nil
}

// IdleFrame measures a frame of the open chat with nothing changed: the
// floor every frame pays (in the Tester, mostly CPU rasterization).
func IdleFrame(n int) (Stats, error) {
	r, err := newRig()
	if err != nil {
		return Stats{}, err
	}
	defer r.close()
	if err := r.settle(func() bool { c := r.app.Conn; return c.Synced && c.Status == core.Connected }); err != nil {
		return Stats{}, err
	}
	id := "20261122_080205_fb120f"
	r.u.Select(id)
	if err := r.settle(func() bool { ch := r.app.Conn.Chats[id]; return ch != nil && ch.Fetched }); err != nil {
		return Stats{}, err
	}
	var ds []time.Duration
	for range n {
		t0 := time.Now()
		r.tt.Frame()
		ds = append(ds, time.Since(t0))
	}
	return Summarise(ds), nil
}

// Streaming feeds a reply into a running chat one token per frame, as the
// socket delivers it, and measures each token's update plus frame.
func Streaming(tokens int) (Stats, error) {
	r, err := newRig()
	if err != nil {
		return Stats{}, err
	}
	defer r.close()
	if err := r.settle(func() bool { c := r.app.Conn; return c.Synced && c.Status == core.Connected }); err != nil {
		return Stats{}, err
	}
	id := "20261122_060000_0a0b0c"
	r.u.Select(id)
	if err := r.settle(func() bool { ch := r.app.Conn.Chats[id]; return ch != nil && ch.Fetched }); err != nil {
		return Stats{}, err
	}
	conn := r.app.Conn
	conn.OnEvent(hermes.Event{Type: "message.start", StoredID: id, SessionID: id, Seq: 1_000_000})
	r.drain()
	words := []string{"The ", "morning ", "brief ", "now ", "lists ", "**one** ", "line ", "per ", "link, ", "`site` ", "then ", "headline. "}
	var ds []time.Duration
	for i := range tokens {
		text := words[i%len(words)]
		if i%40 == 39 {
			text = "\n\n"
		}
		payload, _ := json.Marshal(map[string]string{"text": text})
		t0 := time.Now()
		conn.OnEvent(hermes.Event{Type: "message.delta", StoredID: id, SessionID: id, Seq: int64(1_000_001 + i), Payload: payload})
		r.drain()
		r.tt.Frame() // Window.Update asks for this frame in the app
		ds = append(ds, time.Since(t0))
		r.tt.Advance(33 * time.Millisecond) // the server's 33 ms coalescing
	}
	return Summarise(ds), nil
}

// Shot renders a screen of the app (with the design's data) headlessly at
// w×h and writes it as a PNG: for screens taller than a display.
func Shot(screen views.Screen, w, h int, path string) error {
	r, err := newRig()
	if err != nil {
		return err
	}
	defer r.close()
	if err := r.settle(func() bool { c := r.app.Conn; return c.Synced && c.Status == core.Connected }); err != nil {
		return err
	}
	r.u.Screen = screen
	r.tt.SetSize(w, h)
	for range 5 {
		r.drain()
		r.tt.Frame()
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, r.tt.Image())
}
