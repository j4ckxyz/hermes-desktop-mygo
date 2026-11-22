// Command hermes-desktop is the Hermes Desktop app: a native client for
// Hermes Agent instances, drawn with MyGo's GPU UI.
package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/appicon"
	"hermes-desktop/internal/cache"
	"hermes-desktop/internal/core"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/theme"
	"hermes-desktop/internal/views"
)

// version is the version of a build that was not packaged (go build, make
// build sets it from git); a packaged build has mygo.json's, which updates
// compare.
var version = "dev"

// appVersion is the version of the app running.
func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return version
}

// demoSetup is set by main_demo.go (built with -tags demo): it starts an
// in-process fake instance for screenshots and benchmarks.
var demoSetup func(app *core.App, s *settings.Settings) error

// demoUI adjusts the first screen in the demo build.
var demoUI func(app *core.App, u *views.UI)

type loaded struct {
	settings  settings.Settings
	instances settings.Instances
	cache     *cache.Cache
	preload   *core.Preload // the active instance's cached sidebar
	paths     paths
	err       error
}

type paths struct{ settings, instances, cache string }

func resolvePaths() (paths, error) {
	// HERMES_DESKTOP_HOME keeps everything in one directory (tests,
	// screenshots, a portable install).
	if d := os.Getenv("HERMES_DESKTOP_HOME"); d != "" {
		return paths{filepath.Join(d, "settings.json"), filepath.Join(d, "instances.json"), filepath.Join(d, "cache.db")}, nil
	}
	sp, err := settings.DefaultPath()
	if err != nil {
		return paths{}, err
	}
	cp, err := cache.DefaultPath()
	if err != nil {
		return paths{}, err
	}
	return paths{sp, settings.InstancesPath(sp), cp}, nil
}

// load reads the settings and the instance list and opens the cache, all
// off the main thread and at the same time as the window system starts.
func load() <-chan loaded {
	ch := make(chan loaded, 1)
	go func() {
		var l loaded
		l.paths, l.err = resolvePaths()
		if l.err != nil {
			l.settings = settings.Default()
			ch <- l
			return
		}
		done := make(chan struct{})
		go func() {
			c, err := cache.Open(l.paths.cache)
			if err != nil {
				log.Printf("cache: %v (continuing without it)", err)
			}
			l.cache = c
			close(done)
		}()
		var err error
		if l.settings, err = settings.Load(l.paths.settings); err != nil {
			log.Printf("settings: %v (using defaults)", err)
		}
		if l.instances, err = settings.LoadInstances(l.paths.instances); err != nil {
			log.Printf("instances: %v", err)
		}
		trace("settings read")
		<-done
		trace("cache open")
		if in, ok := l.instances.Find(l.settings.ActiveInstance); ok {
			l.preload = core.ReadPreload(l.cache, in.ID)
		} else if len(l.instances.List) > 0 {
			l.preload = core.ReadPreload(l.cache, l.instances.List[0].ID)
		}
		trace("sidebar read")
		ch <- l
	}()
	return ch
}

// trace prints start-up phases when HERMES_DESKTOP_TRACE is set.
var traceOn = os.Getenv("HERMES_DESKTOP_TRACE") != ""
var traceStart = time.Now()

func trace(what string) {
	if traceOn {
		fmt.Fprintf(os.Stderr, "trace %7.1fms %s\n", float64(time.Since(traceStart).Microseconds())/1000, what)
	}
}

func main() {
	start := time.Now()
	trace("main")
	bench := os.Getenv("HERMES_DESKTOP_BENCH")
	mygo.App.SetName("Hermes")
	if mygo.App.Version() == "" {
		mygo.App.SetVersion(version)
	}
	views.Version = appVersion()
	loading := load()
	fontsDone := make(chan error, 1)
	go func() { err := theme.RegisterFonts(); trace("fonts registered"); fontsDone <- err }()

	mygo.App.WhenReady(func() {
		trace("ready")
		l := <-loading
		trace("loaded")
		if err := <-fontsDone; err != nil {
			log.Printf("fonts: %v", err)
		}
		trace("fonts")
		saver := &settings.Saver{Path: l.paths.settings, OnError: func(err error) { log.Printf("settings: %v", err) }}
		var win *mygo.Window
		var u *views.UI
		// Work that finishes before the window exists waits for it.
		var postMu sync.Mutex
		var early []func()
		post := func(fn func()) {
			postMu.Lock()
			w := win
			if w == nil {
				early = append(early, fn)
			}
			postMu.Unlock()
			if w != nil {
				w.Update(fn)
			}
		}
		app := core.New(core.Deps{
			Post:    post,
			Cache:   l.cache,
			Secrets: secrets.New(),
			SaveInstances: func(is settings.Instances) {
				if err := settings.SaveInstances(l.paths.instances, is); err != nil {
					log.Printf("instances: %v", err)
				}
			},
		}, l.instances)
		s := l.settings
		if demoSetup != nil {
			if err := demoSetup(app, &s); err != nil {
				log.Fatal(err)
			}
		}
		native := &nativeOS{iconDir: filepath.Dir(l.paths.cache)}
		app.Notify = func(n core.Notice) {
			if u == nil || (win != nil && win.IsFocused()) {
				return
			}
			want := u.Settings.NotifyOnFinish
			if n.Kind == core.NoticeNeedsYou {
				want = u.Settings.NotifyOnApproval
			}
			if !want {
				return
			}
			native.chime(u.Settings.Sounds)
			mygo.NewNotification(mygo.NotificationOptions{ID: n.ID(), Title: n.Title, Body: n.Body, Group: n.Chat}).Show()
		}
		// Clicking a notification (also one from before a relaunch) brings
		// the window forward and opens its chat.
		mygo.App.OnNotificationClick(func(id string) {
			inst, chat, ok := core.ParseNoticeID(id)
			if !ok || win == nil {
				return
			}
			if win.IsMinimized() {
				win.Restore()
			}
			win.Show()
			win.Focus()
			win.Update(func() { u.OpenFromNotice(inst, chat) })
		})
		// Which instance opens: the last one, else the first saved.
		open, hasOpen := app.Instances.Find(s.ActiveInstance)
		if !hasOpen && len(app.Instances.List) > 0 {
			open, hasOpen = app.Instances.List[0], true
		}
		u = views.New(app, s, native)
		if hasOpen {
			u.Screen = views.ScreenMain
			app.OpenWith(open, l.preload)
		}
		u.SettingsChanged = saver.Request

		opts := mygo.WindowOptions{
			Title:     "Hermes",
			Width:     1280,
			Height:    820,
			MinWidth:  820,
			MinHeight: 560,
			StateKey:  "main",
			Content:   ui.View(u.View),
		}
		if runtime.GOOS == "darwin" {
			opts.TitleBarStyle = mygo.TitleBarHiddenInset
			if s.TranslucentSidebar {
				opts.Vibrancy = mygo.VibrancySidebar
			}
		} else {
			// The app draws its own title bar, as the design does, with the
			// system's window buttons in it. The menu bar stays a real one
			// but hides: Alt or F10 opens it (Linux), or its menus in a popup
			// (Windows), and its shortcuts work all along.
			opts.TitleBarStyle = mygo.TitleBarHidden
		}
		// Windows shows a material only in a window created with one, and
		// such a window has no menu bar (Alt or F10 opens the menus): so
		// Mica is chosen at launch, from the setting.
		if runtime.GOOS == "windows" && s.TranslucentSidebar {
			opts.Vibrancy = mygo.VibrancyMica
			native.createdWithMaterial = true
		}
		if bench != "" {
			opts.StateKey = ""
			opts.Width, opts.Height = 1440, 900
		}
		w := mygo.NewWindow(opts)
		trace("window")
		postMu.Lock()
		win = w
		pending := early
		early = nil
		postMu.Unlock()
		for _, fn := range pending {
			win.Update(fn)
		}
		native.win = win
		native.SetAppIcon(s.AppIcon)
		if demoUI != nil {
			demoUI(app, u)
		}
		installMenus(win, u)
		win.OnReadyToShow(func() {
			trace("first frame")
			if bench == "launch" {
				fmt.Printf("first-frame %d\n", time.Since(start).Microseconds())
				os.Stdout.Sync()
				if !hasOpen {
					mygo.App.Quit()
				}
			}
		})
		if bench == "launch" && hasOpen {
			u.SidebarShown = func() {
				trace("sidebar")
				fmt.Printf("sidebar-ready %d\n", time.Since(start).Microseconds())
				os.Stdout.Sync()
				mygo.App.Quit()
			}
		}
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { saver.Flush() })
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// nativeOS implements views.Native with MyGo's dialogs.
type nativeOS struct {
	win   *mygo.Window
	sound *audio.Sound
	// createdWithMaterial: the Windows window was made with Mica.
	createdWithMaterial bool
	iconSeq             atomic.Int64 // the latest SetAppIcon (only the last one applies)
	iconDir             string       // where drawn icons are kept
}

// SetAppIcon draws the icon in a colour off the main thread and sets it
// on the window (title bar, taskbar) and, on macOS, the Dock.
func (n *nativeOS) SetAppIcon(colour string) {
	seq := n.iconSeq.Add(1)
	win := n.win
	dir := n.iconDir
	go func() {
		// Drawn once per colour, then read from the cache directory.
		path := ""
		if dir != "" {
			path = filepath.Join(dir, "icon-"+strings.ToLower(colour)+"-v1.png")
		}
		png, err := os.ReadFile(path)
		if err != nil || len(png) == 0 {
			png = appicon.PNG(256, appicon.Options{Colour: colour})
			if path != "" {
				os.WriteFile(path, png, 0o644)
			}
		}
		if n.iconSeq.Load() != seq || win == nil {
			return // a newer colour was picked meanwhile
		}
		if runtime.GOOS == "darwin" {
			mygo.App.Dock.SetIcon(png)
			return
		}
		win.SetIcon(png)
	}()
}

// SetSidebarMaterial turns the sidebar's material on or off. macOS
// changes it at once. Windows changes it at once in a window created with
// one; otherwise it takes the next launch.
func (n *nativeOS) SetSidebarMaterial(on bool) string {
	switch runtime.GOOS {
	case "darwin":
		v := mygo.VibrancyNone
		if on {
			v = mygo.VibrancySidebar
		}
		n.win.SetVibrancy(v)
	case "windows":
		if !n.createdWithMaterial {
			if on {
				return "Mica shows the next time Hermes starts."
			}
			return ""
		}
		v := mygo.VibrancyNone
		if on {
			v = mygo.VibrancyMica
		}
		n.win.SetVibrancy(v)
		if !on {
			return "The menu bar comes back the next time Hermes starts."
		}
	}
	return ""
}

func (n *nativeOS) Platform() string { return runtime.GOOS }

func (n *nativeOS) Confirm(message, detail, action string) bool {
	r, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent: n.win, Type: mygo.MessageWarning, Message: message, Detail: detail,
		Buttons: []string{action, "Cancel"}, DefaultButton: 1, CancelButton: 1, DestructiveButtons: []int{0},
	})
	return err == nil && r.Button == 0
}

func (n *nativeOS) SaveText(name, content string) error {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent: n.win, Title: "Export chat", DefaultPath: name,
		Filters: []mygo.FileFilter{{Name: "Markdown", Extensions: []string{"md"}}},
	})
	if err != nil || path == "" {
		return err
	}
	go func() {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			log.Printf("export: %v", err)
		}
	}()
	return nil
}

// chime plays a soft two-note chime (synthesized, so no asset ships).
func (n *nativeOS) chime(on bool) {
	if !on {
		return
	}
	if n.sound == nil {
		const rate = 44100
		samples := make([]float32, rate*6/10)
		for i := range samples {
			t := float64(i) / rate
			f := 880.0
			if t > 0.12 {
				f = 1318.5
			}
			env := math.Exp(-t*7) * math.Min(1, t*200)
			samples[i] = float32(0.18 * env * math.Sin(2*math.Pi*f*t))
		}
		n.sound = audio.NewSound(rate, 1, samples)
	}
	go func() { n.sound.Play() }()
}
