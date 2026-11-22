//go:build demo

// The demo build runs against an in-process fake Hermes instance seeded
// with the design's agents and chats, for screenshots and benchmarks:
//
//	go build -tags demo ./cmd/hermes-desktop
//	HERMES_DEMO_SCREEN=main|connect|settings|new ./hermes-desktop
package main

import (
	"os"
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
		demoServer = hermestest.New(hermestest.Options{Token: "demo-token", Canonical: true, Delay: 400 * time.Millisecond})
		app.Secrets = secrets.NewWithBackend(&secrets.MemoryBackend{})
		in := settings.Instance{ID: "demo", Name: "Main", URL: demoServer.URL, Auth: settings.AuthToken}
		app.Secrets.Set(in.ID, secrets.Credentials{Token: "demo-token"})
		app.Instances = settings.Instances{List: []settings.Instance{in}}
		s.ActiveInstance = in.ID
		s.SetAgentPinned(in.ID, "default", true)
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
		if os.Getenv("HERMES_DEMO_SCREEN") == "connect" {
			app.Instances.List[0].URL = "https://hermes-vps.your-tailnet.ts.net"
			s.ActiveInstance = ""
			app.Instances.List = []settings.Instance{{ID: "demo", Name: "Main", URL: "https://hermes-vps.your-tailnet.ts.net", Auth: settings.AuthToken}}
		}
		return nil
	}
	demoUI = func(app *core.App, u *views.UI) {
		switch os.Getenv("HERMES_DEMO_SCREEN") {
		case "connect":
			u.Screen = views.ScreenConnect
			return
		case "settings":
			u.Screen = views.ScreenSettings
			return
		case "new":
			return
		}
		u.Select(demoSession)
		// Start the design's running turn once the chat is attached.
		go func() {
			for range 100 {
				time.Sleep(50 * time.Millisecond)
				if demoServer.CallCount("session.resume") > 0 {
					demoServer.Run(demoSession, "design_turn.jsonl", 150*time.Millisecond)
					return
				}
			}
		}()
	}
}
