package views

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/hermes/hermestest"
	"hermes-desktop/internal/settings"
)

type fakeUpdater struct {
	mu        sync.Mutex
	why       string
	next      *Release
	checkErr  error
	checks    int
	installed []string
	gate      chan struct{} // when set, Install waits for it
}

func (f *fakeUpdater) Unavailable() string { return f.why }
func (f *fakeUpdater) Check(context.Context) (*Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.next, f.checkErr
}
func (f *fakeUpdater) Install(ctx context.Context, r *Release, progress func(done, total int64)) error {
	progress(50, 100)
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	f.installed = append(f.installed, r.Version)
	f.mu.Unlock()
	return nil
}

const notes = "A paragraph that\nwraps.\n\n- Faster sync.\n- Fixed `Esc`\n  in search.\n"

func withUpdater(t *testing.T, h *harness, f *fakeUpdater) *[]settings.Resume {
	t.Helper()
	var relaunched []settings.Resume
	h.u.Updater = f
	h.u.Relaunch = func(r settings.Resume) { relaunched = append(relaunched, r) }
	old := updateFirstCheck
	updateFirstCheck = 0
	t.Cleanup(func() { updateFirstCheck = old })
	return &relaunched
}

func TestReleaseNotes(t *testing.T) {
	got := releaseNotes(notes)
	want := []string{"A paragraph that wraps.", "Faster sync.", "Fixed Esc in search."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("notes %q", got)
	}
}

func TestUpdateFoundAtLaunchGetsItsOwnPage(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	f := &fakeUpdater{next: &Release{Version: "9.9.9", Notes: notes}}
	relaunched := withUpdater(t, h, f)
	h.u.StartUpdates()
	h.settle("the update page", func() bool { return h.tt.HasText("Hermes 9.9.9 is here") })
	for _, s := range []string{"Faster sync.", "Update and reopen", "Not now", "WHAT'S NEW"} {
		if !h.tt.HasText(s) {
			t.Errorf("update page lacks %q", s)
		}
	}
	if h.tt.HasText("New chat") {
		t.Error("the page shows in place of the app")
	}
	// "Not now" goes to the app, and the same version is not offered again.
	if err := h.tt.Click("Not now"); err != nil {
		t.Fatal(err)
	}
	h.settle("the app", func() bool { return !h.tt.HasText("Hermes 9.9.9 is here") })
	if h.u.upd.declined != "9.9.9" || len(f.installed) != 0 || len(*relaunched) != 0 {
		t.Fatalf("declined %q, installed %v", h.u.upd.declined, f.installed)
	}
	h.u.checkForUpdates(checkInBackground)
	h.settle("the second check", func() bool { return !h.u.upd.checking })
	if len(f.installed) != 0 {
		t.Fatal("a declined version is not downloaded in the background")
	}
}

func TestUpdateAndReopenKeepsTheChatAndDraft(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	id := h.u.side.targets[1]
	h.u.Select(id)
	h.u.SetDraft("half a thought")
	f := &fakeUpdater{next: &Release{Version: "9.9.9", Notes: notes}, gate: make(chan struct{})}
	relaunched := withUpdater(t, h, f)
	h.u.StartUpdates()
	h.settle("the update page", func() bool { return h.tt.HasText("Update and reopen") })
	if err := h.tt.Click("Update and reopen"); err != nil {
		t.Fatal(err)
	}
	h.settle("the download", func() bool { return h.tt.HasText("Downloading… 50%") })
	close(f.gate)
	h.settle("the relaunch", func() bool { return len(*relaunched) == 1 })
	r := (*relaunched)[0]
	if r.Instance != "i1" || r.Chat != id || r.Drafts[id] != "half a thought" {
		t.Fatalf("resume %+v", r)
	}

	// The next launch reopens them.
	h2 := newHarness(t, hermestest.Options{}, true)
	h2.synced()
	h2.u.Resume(r)
	h2.settle("the chat", func() bool { return h2.u.Selected == id })
	if d := h2.u.DebugState(); d.Draft != "half a thought" || d.Chat != id {
		t.Fatalf("reopened %+v", d)
	}
}

func TestUpdateWhileRunningDownloadsQuietlyAndOffersARestart(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	f := &fakeUpdater{next: &Release{Version: "9.9.9"}}
	relaunched := withUpdater(t, h, f)
	h.u.checkForUpdates(checkInBackground)
	h.settle("the toast", func() bool { return h.tt.HasText("Hermes 9.9.9 is ready") })
	if h.u.upd.offer {
		t.Fatal("no page for a version found while the app runs")
	}
	if len(f.installed) != 1 {
		t.Fatalf("installed %v", f.installed)
	}
	if err := h.tt.Click("Restart to update"); err != nil {
		t.Fatal(err)
	}
	h.settle("the relaunch", func() bool { return len(*relaunched) == 1 })
}

func TestUpdatesPane(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	f := &fakeUpdater{}
	withUpdater(t, h, f)
	h.u.Screen, h.u.SettingsPane = ScreenSettings, "Updates"
	h.settle("the pane", func() bool { return h.tt.HasText("Check now") })
	if !h.tt.HasText("Hermes Desktop "+Version) || !h.tt.HasText("Update automatically") {
		t.Fatalf("pane: %q", h.tt.Texts())
	}
	if err := h.tt.Click("Check now"); err != nil {
		t.Fatal(err)
	}
	h.settle("up to date", func() bool { return h.tt.HasText("This is the latest version. Checked just now.") })

	f.checkErr = errors.New("offline")
	h.tt.Click("Check now")
	h.settle("the error", func() bool { return h.tt.HasText("Could not check for updates: offline") })

	f.checkErr, f.next = nil, &Release{Version: "9.9.9"}
	h.tt.Click("Check now")
	h.settle("the restart button", func() bool { return h.tt.HasText("Version 9.9.9 is installed. Restart to use it.") })

	// Turning updates off is a setting.
	sw, ok := h.tt.AccessByRole(ui.RoleSwitch, "Update automatically")
	if !ok {
		t.Fatal("the switch is labelled")
	}
	h.tt.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
	h.settle("saved", func() bool { return len(h.saved) > 0 && !h.saved[len(h.saved)-1].AutoUpdate })
}

func TestBuildsThatCannotUpdateSaySo(t *testing.T) {
	h := newHarness(t, hermestest.Options{}, true)
	h.synced()
	f := &fakeUpdater{why: "Installed from the .deb package: your package manager updates it, with the rest of the system."}
	withUpdater(t, h, f)
	h.u.StartUpdates()
	h.u.Screen, h.u.SettingsPane = ScreenSettings, "Updates"
	h.settle("the pane", func() bool { return h.tt.HasText(f.why) })
	if h.tt.HasText("Check now") || h.tt.HasText("Update automatically") {
		t.Fatal("no check or switch in a build that cannot update")
	}
	time.Sleep(20 * time.Millisecond)
	if f.checks != 0 {
		t.Fatal("a build that cannot update never checks")
	}
}
