//go:build demo

// The demo build runs against an in-process fake Hermes instance seeded
// with the design's agents and chats, for screenshots and benchmarks:
//
//	go build -tags demo ./cmd/hermes-desktop
//	HERMES_DEMO_SCREEN=main|connect|settings|new ./hermes-desktop
package main

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"hermes-desktop/internal/core"
	"hermes-desktop/internal/hermes/hermestest"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/views"
)

const demoSession = "20261122_080205_fb120f"

var demoServer *hermestest.Server

func init() {
	demoSetup = func(app *core.App, s *settings.Settings) error {
		// On systems that resolve *.localhost (systemd-resolved), the demo
		// instance has the design's host name.
		url := ""
		if os.Getenv("HERMES_DEMO_HOSTNAME") != "" {
			demoServer = hermestest.New(hermestest.Options{Token: "demo-token", Canonical: true, Delay: 400 * time.Millisecond, Addr: "[::1]:0"})
			_, port, _ := net.SplitHostPort(demoServer.Listener.Addr().String())
			url = "http://hermes-vps.localhost:" + port
		} else {
			demoServer = hermestest.New(hermestest.Options{Token: "demo-token", Canonical: true, Delay: 400 * time.Millisecond})
			url = demoServer.URL
		}
		app.Secrets = secrets.NewWithBackend(&secrets.MemoryBackend{})
		in := settings.Instance{ID: "demo", Name: "Main", URL: url, Auth: settings.AuthToken}
		tok := "demo-token"
		if os.Getenv("HERMES_DEMO_SCREEN") == "signedout" {
			tok = "a-rotated-key" // the instance refuses it: the sign-in banner
		}
		app.Secrets.Set(in.ID, secrets.Credentials{Token: tok})
		app.Instances = settings.Instances{List: []settings.Instance{in}}
		s.ActiveInstance = in.ID
		s.SetAgentPinned(in.ID, "default", true)
		// The design's link card; the demo's address resolves nowhere.
		app.SetLinkTitle("https://hermes-vps.your-tailnet.ts.net/cron", "Hermes dashboard · Cron jobs")
		if v := os.Getenv("HERMES_DEMO_CANVAS"); v != "" {
			s.DarkCanvas, s.LightCanvas = v, v
			if v == "White" || v == "Paper White" {
				s.Appearance = "Light"
			} else {
				s.Appearance = "Dark"
			}
		}
		if v := os.Getenv("HERMES_DEMO_ACCENT"); v != "" {
			s.Accent = v
		}
		if strings.HasPrefix(os.Getenv("HERMES_DEMO_SCREEN"), "connect") {
			app.Instances.List[0].URL = "https://hermes-vps.your-tailnet.ts.net"
			s.ActiveInstance = ""
			app.Instances.List = []settings.Instance{{ID: "demo", Name: "Main", URL: "https://hermes-vps.your-tailnet.ts.net", Auth: settings.AuthToken}}
		}
		return nil
	}
	demoUI = func(app *core.App, u *views.UI) {
		screen := os.Getenv("HERMES_DEMO_SCREEN")
		switch screen {
		case "connect", "connect-dashboard":
			u.Screen = views.ScreenConnect
			addr := "https://hermes-vps.your-tailnet.ts.net"
			if os.Getenv("HERMES_DEMO_HTTP") != "" {
				addr = "http://192.168.1.20:9119"
			}
			u.PrefillConnect(true, screen == "connect-dashboard", addr)
			return
		case "settings":
			u.Screen = views.ScreenSettings
			return
		case "update":
			// The page of a version found at launch.
			u.Updater = demoUpdater{next: &views.Release{Version: "0.2.0", Notes: demoNotes}}
			return
		case "updates":
			u.Updater = demoUpdater{}
			u.Screen, u.SettingsPane = views.ScreenSettings, "Updates"
			return
		case "new":
			return
		}
		u.Select(demoSession)
		if screen == "main-expanded" {
			go func() {
				for range 100 {
					time.Sleep(50 * time.Millisecond)
					done := make(chan bool, 1)
					app.Post(func() {
						ch := app.Conn.Chats[demoSession]
						ok := ch != nil && ch.Fetched
						if ok {
							u.ExpandToolGroups()
							u.ScrollChatToTop()
						}
						done <- ok
					})
					if <-done {
						return
					}
				}
			}()
		}
		// Start the design's running turn once the chat is attached.
		go func() {
			for range 100 {
				time.Sleep(50 * time.Millisecond)
				if demoServer.CallCount("session.resume") > 0 {
					demoServer.Run(demoSession, "design_turn.jsonl", 150*time.Millisecond)
					if screen == "prompt" {
						time.Sleep(2 * time.Second)
						demoServer.Ask(demoSession, "clarify", map[string]any{"questions": []any{
							map[string]any{"qid": "q1", "question": "Which Todoist project should the fieldwork dates go in?", "choices": []string{"Inbox", "School", "Geography"}},
							map[string]any{"qid": "q2", "question": "Anything to add to each task?"},
						}})
					}
					return
				}
			}
		}()
	}
}

const demoNotes = `Faster everywhere, and a few fixes.

- Chats open from the cache before the instance answers.
- Esc closes search, settings and rename again.
- Link previews skip private addresses.
`

// demoUpdater offers a release, or none, and installs slowly.
type demoUpdater struct{ next *views.Release }

func (demoUpdater) Unavailable() string { return "" }
func (d demoUpdater) Check(context.Context) (*views.Release, error) {
	return d.next, nil
}
func (demoUpdater) Install(ctx context.Context, r *views.Release, progress func(done, total int64)) error {
	for i := range 100 {
		progress(int64(i), 100)
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}
