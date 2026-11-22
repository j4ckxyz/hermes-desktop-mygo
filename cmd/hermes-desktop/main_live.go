//go:build live

// The live build signs in to the instance in the gitignored .env.local
// (HERMES_URL with HERMES_USERNAME/HERMES_PASSWORD or HERMES_API_KEY),
// keeping the credentials in memory: for end-to-end runs and screenshots
// on machines without a keychain. Never shipped.
package main

import (
	"bufio"
	"os"
	"strings"
	"time"

	"hermes-desktop/internal/views"

	"hermes-desktop/internal/core"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
)

func init() {
	demoSetup = func(app *core.App, s *settings.Settings) error {
		path := os.Getenv("HERMES_LIVE_ENV")
		if path == "" {
			path = ".env.local"
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		v := map[string]string{}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if k, val, ok := strings.Cut(sc.Text(), "="); ok {
				v[strings.TrimSpace(k)] = strings.TrimRight(val, "\r\n")
			}
		}
		in := settings.Instance{ID: "live", Name: "Main", URL: strings.TrimRight(v["HERMES_URL"], "/"), Auth: settings.AuthToken}
		cred := secrets.Credentials{Token: v["HERMES_API_KEY"]}
		if v["HERMES_USERNAME"] != "" {
			in.Auth, in.Username = settings.AuthPassword, v["HERMES_USERNAME"]
			cred = secrets.Credentials{Username: v["HERMES_USERNAME"], Password: v["HERMES_PASSWORD"]}
		}
		app.Secrets = secrets.NewWithBackend(&secrets.MemoryBackend{})
		app.Secrets.Set(in.ID, cred)
		app.Instances = settings.Instances{List: []settings.Instance{in}}
		s.ActiveInstance = in.ID
		s.Appearance, s.DarkCanvas = "Dark", "Midnight"
		return nil
	}
}

func init() {
	// HERMES_LIVE_OPEN opens the chat with that title; HERMES_LIVE_SEND
	// then sends it a message, to watch a real reply stream.
	demoUI = func(app *core.App, u *views.UI) {
		title, msg := os.Getenv("HERMES_LIVE_OPEN"), os.Getenv("HERMES_LIVE_SEND")
		if title == "" {
			return
		}
		go func() {
			for range 600 {
				time.Sleep(100 * time.Millisecond)
				opened := make(chan bool, 1)
				app.Post(func() {
					c := app.Conn
					if c == nil || !c.Loaded || c.Status != core.Connected {
						opened <- false
						return
					}
					for _, s := range c.Sessions {
						if s.Title == title {
							u.Select(s.ID)
							if msg != "" {
								id, prof := s.ID, s.Profile
								go func() {
									time.Sleep(2 * time.Second)
									app.Post(func() { app.Conn.Send(id, prof, msg, nil) })
								}()
							}
							opened <- true
							return
						}
					}
					opened <- false
				})
				if <-opened {
					return
				}
			}
		}()
	}
}
