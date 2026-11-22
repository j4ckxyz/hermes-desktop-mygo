package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"hermes-desktop/internal/views"
)

// mygoUpdater is views.Updater over mygo.Updater: signed releases from
// the GitHub repository in mygo.json.
type mygoUpdater struct {
	once sync.Once
	why  string
}

// Unavailable is decided once: whether the app can write where it is
// installed does not change while it runs.
func (m *mygoUpdater) Unavailable() string {
	m.once.Do(func() { m.why = cannotUpdate() })
	return m.why
}

func (m *mygoUpdater) Check(ctx context.Context) (*views.Release, error) {
	up, err := mygo.Updater.Check(ctx)
	if err != nil || up == nil {
		return nil, err
	}
	return &views.Release{Version: up.Version, Notes: up.Notes, Handle: up}, nil
}

func (m *mygoUpdater) Install(ctx context.Context, r *views.Release, progress func(done, total int64)) error {
	up, ok := r.Handle.(*mygo.Update)
	if !ok {
		return errors.New("not a release of this updater")
	}
	return up.Install(ctx, progress)
}

// cannotUpdate says why this copy does not update itself, or "".
func cannotUpdate() string {
	if mygo.App.Version() == "" || mygo.IsDev() {
		return "This is a development build, which does not update itself. Install a release to get updates."
	}
	if mygo.Updater.Enabled() {
		return ""
	}
	if runtime.GOOS == "linux" {
		if exe, err := os.Executable(); err == nil {
			if exe, err = filepath.EvalSymlinks(exe); err == nil && (strings.HasPrefix(exe, "/opt/") || strings.HasPrefix(exe, "/usr/")) {
				return "Installed from the .deb package: your package manager updates it, with the rest of the system."
			}
		}
	}
	return "Hermes is installed where it cannot write, so it cannot update itself. Reinstall it with the install script to get updates."
}

// argFlags turns --home DIR and --debug DIR into HERMES_DESKTOP_HOME and
// HERMES_DESKTOP_DEBUG. They are arguments too because a relaunch keeps
// the arguments everywhere, but not the environment on macOS (it reopens
// the app with /usr/bin/open): the update test relies on both.
func argFlags(args []string) {
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--home":
			os.Setenv("HERMES_DESKTOP_HOME", args[i+1])
		case "--debug":
			os.Setenv("HERMES_DESKTOP_DEBUG", args[i+1])
		}
	}
}

// commandLine handles the arguments that make the app do one thing and
// exit, without a window. It reports whether it did.
//
//	hermes-desktop --version             prints the version
//	hermes-desktop --write-version FILE  writes it to a file (Windows apps have no console)
//	hermes-desktop --self-update [FILE]  installs the newest release over the app
func commandLine(args []string) bool {
	if len(args) == 0 {
		return false
	}
	out := ""
	if len(args) > 1 {
		out = args[1]
	}
	say := func(s string) {
		fmt.Println(s)
		if out != "" {
			os.WriteFile(out, []byte(s+"\n"), 0o644)
		}
	}
	switch args[0] {
	case "--version", "-version", "-v":
		out = ""
		say("Hermes " + appVersion())
	case "--write-version":
		say(appVersion())
	case "--self-update":
		say(selfUpdate())
	default:
		return false
	}
	return true
}

// selfUpdate installs the newest release and says what happened.
func selfUpdate() string {
	from := appVersion()
	if why := cannotUpdate(); why != "" {
		return "error: Hermes " + from + ": " + why
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	up, err := mygo.Updater.Check(ctx)
	switch {
	case errors.Is(err, mygo.ErrUpdatesDisabled):
		return "error: updates are not enabled in this build"
	case err != nil:
		return "error: checking: " + err.Error()
	case up == nil:
		return "up to date: " + from
	}
	if err := up.Install(ctx, nil); err != nil {
		return "error: installing " + up.Version + ": " + err.Error()
	}
	return "updated: " + from + " -> " + up.Version
}
