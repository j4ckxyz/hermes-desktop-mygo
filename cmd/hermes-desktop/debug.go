package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"hermes-desktop/internal/views"
)

// debugHook lets a script drive the app while HERMES_DESKTOP_DEBUG names a
// directory (tools/update-test.sh): each line of its file "do" runs, and
// "done" is written after. The commands:
//
//	state          writes "state": version, the update offered, the instance, chat and draft
//	open <id>      opens a chat
//	type <text>    sets the open chat's unsent text
//	update accept  "Update and reopen" on the update page
//	quit           quits
func debugHook(win *mygo.Window, u *views.UI) {
	dir := os.Getenv("HERMES_DESKTOP_DEBUG")
	if dir == "" {
		return
	}
	do := func(fn func()) {
		done := make(chan struct{})
		win.Update(func() { fn(); close(done) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	go func() {
		for range time.Tick(100 * time.Millisecond) {
			req := filepath.Join(dir, "do")
			b, err := os.ReadFile(req)
			if err != nil {
				continue
			}
			os.Remove(req)
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				verb, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
				switch verb {
				case "state":
					var s string
					do(func() {
						d := u.DebugState()
						s = fmt.Sprintf("version=%s offered=%q instance=%s chat=%s draft=%q\n", appVersion(), d.Offered, d.Instance, d.Chat, d.Draft)
					})
					os.WriteFile(filepath.Join(dir, "state"), []byte(s), 0o644)
				case "open":
					do(func() { u.Select(arg) })
				case "type":
					do(func() { u.SetDraft(arg) })
				case "update":
					if arg == "accept" {
						do(u.AcceptUpdate)
					}
				case "quit":
					mygo.App.Quit()
				}
			}
			os.WriteFile(filepath.Join(dir, "done"), []byte(time.Now().String()), 0o644)
		}
	}()
}
