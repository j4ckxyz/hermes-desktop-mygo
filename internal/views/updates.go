package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/settings"
)

// Updater is the app's own updater: mygo.Updater in the app, a fake in
// tests. New versions come from the GitHub releases, signed, and replace
// the app where it is installed; the running version goes on until a
// restart.
type Updater interface {
	// Unavailable says why this build cannot update itself (a development
	// build, or one a package manager installed), or "" when it can.
	Unavailable() string
	// Check returns a newer release, or nil when the app is up to date.
	Check(ctx context.Context) (*Release, error)
	// Install downloads r, checks its signature and puts it in place of
	// the app.
	Install(ctx context.Context, r *Release, progress func(done, total int64)) error
}

// Release is a version Check found.
type Release struct {
	Version string
	Notes   string // Markdown: the version's section of CHANGELOG.md
	Handle  any    // the updater's own
}

// How often, and how soon after launch, the app checks.
var (
	updateFirstCheck = time.Second
	updateEvery      = 6 * time.Hour
)

// Why a check is made.
const (
	checkAtLaunch     = iota // a version found is offered on a page of its own
	checkInBackground        // the app runs: a version found downloads quietly
	checkAsked               // Check now, in Settings
)

// updateState is the state of the app's own updates.
type updateState struct {
	started    bool
	checking   bool
	checkedAt  time.Time
	upToDate   bool
	available  *Release // found, not installed yet
	installing bool
	progress   float64 // of the download, 0 to 1
	ready      string  // the version installed, which a restart runs
	err        string
	// offer shows the page for the version found at launch; reopen
	// restarts as soon as it is installed; declined is the version the
	// user said "Not now" to in this run.
	offer    bool
	reopen   bool
	declined string
}

// StartUpdates checks for a new version about a second after launch, then
// every six hours, while the setting is on. Call it on the UI thread.
func (u *UI) StartUpdates() {
	if u.upd.started || u.Updater == nil || u.Updater.Unavailable() != "" {
		return
	}
	u.upd.started = true
	go func() {
		time.Sleep(updateFirstCheck)
		for why := checkAtLaunch; ; why = checkInBackground {
			u.App.Post(func() {
				if u.Settings.AutoUpdate {
					u.checkForUpdates(why)
				}
			})
			time.Sleep(updateEvery)
		}
	}()
}

// checkForUpdates asks whether a newer version is released. One found at
// launch gets its own page; one found later downloads at once and offers
// a restart.
func (u *UI) checkForUpdates(why int) {
	st := &u.upd
	manual := why == checkAsked
	if u.Updater == nil || st.checking || st.installing || st.ready != "" {
		return
	}
	if why := u.Updater.Unavailable(); why != "" {
		st.err = why
		return
	}
	st.checking, st.err, st.upToDate = true, "", false
	up := u.Updater
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		r, err := up.Check(ctx)
		u.App.Post(func() {
			st.checking, st.checkedAt = false, time.Now()
			switch {
			case err != nil:
				// A background check fails quietly; the next one may not.
				if manual {
					st.err = "Could not check for updates: " + err.Error()
				}
			case r == nil:
				st.upToDate = true
			case why == checkAtLaunch:
				st.available, st.offer = r, true
			case !manual && r.Version == st.declined:
				// "Not now", they said.
			default:
				st.available = r
				u.installUpdate()
			}
		})
	}()
}

// installUpdate downloads the version found and puts it in place, then
// offers a restart (or restarts, when asked from the launch page).
func (u *UI) installUpdate() {
	st := &u.upd
	r := st.available
	if r == nil || st.installing || u.Updater == nil {
		return
	}
	st.installing, st.progress, st.err = true, 0, ""
	up := u.Updater
	go func() {
		var last time.Time
		err := up.Install(context.Background(), r, func(done, total int64) {
			if now := time.Now(); total > 0 && now.Sub(last) > 100*time.Millisecond {
				last = now
				u.App.Post(func() { st.progress = float64(done) / float64(total) })
			}
		})
		u.App.Post(func() {
			st.installing = false
			if err != nil {
				st.err = "Could not install version " + r.Version + ": " + err.Error()
				st.reopen = false
				return
			}
			st.available, st.ready, st.progress = nil, r.Version, 1
			if st.reopen {
				u.restart()
				return
			}
			u.pendingToast = &ui.Toast{
				ID:          "update-ready",
				Title:       "Hermes " + r.Version + " is ready",
				Description: "Restart to use it. Your chat and anything you were typing come back.",
				Action:      "Restart to update",
				OnAction:    u.restart,
				Timeout:     -1, // until answered
			}
		})
	}()
}

// ResumeState is what the next launch reopens: the instance, the chat and
// the unsent text of each composer.
func (u *UI) ResumeState() settings.Resume {
	r := settings.Resume{Chat: u.Selected, NewProfile: u.NewProfile, NewDraft: u.draft}
	if conn := u.App.Conn; conn != nil {
		r.Instance = conn.Instance.ID
		for id, ch := range conn.Chats {
			if strings.TrimSpace(ch.Draft) != "" {
				if r.Drafts == nil {
					r.Drafts = map[string]string{}
				}
				r.Drafts[id] = ch.Draft
			}
		}
	}
	return r
}

// Resume reopens what ResumeState saved. Call it once the instance is
// open (or opening).
func (u *UI) Resume(r settings.Resume) {
	conn := u.App.Conn
	if conn == nil || (r.Instance != "" && conn.Instance.ID != r.Instance) {
		return
	}
	u.NewProfile, u.draft = r.NewProfile, r.NewDraft
	for id, text := range r.Drafts {
		conn.OpenChat(id).Draft = text
	}
	if r.Chat != "" {
		u.Select(r.Chat)
	}
}

// restart quits and starts again, as the new version once one is
// installed, reopening the same chat.
func (u *UI) restart() {
	if u.Relaunch != nil {
		u.Relaunch(u.ResumeState())
	}
}

func (u *UI) acceptUpdate() {
	st := &u.upd
	if st.available == nil || st.installing {
		return
	}
	st.reopen = true
	u.installUpdate()
}

func (u *UI) declineUpdate() {
	st := &u.upd
	if st.installing {
		return
	}
	if st.available != nil {
		st.declined = st.available.Version
	}
	st.offer, st.err = false, ""
}

// updatePage offers the version found at launch, in place of the app:
// what is new, "Update and reopen" and "Not now".
func (u *UI) updatePage(c *ui.Context) {
	pal := &u.pal
	st := &u.upd
	r := st.available
	if r == nil && st.ready == "" {
		st.offer = false
		return
	}
	version := st.ready
	if r != nil {
		version = r.Version
	}
	if st.installing {
		c.After(100 * time.Millisecond)
	}
	ui.Column(c).Fill().Background(pal.Bg).Children(func() {
		bar := c.TitleBar()
		ui.Row(c).Height(max(56, bar.Height)).FillWidth().Shrink(0).DragWindow()
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Padding(0, 24, 48, 24).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(520).MaxHeightPercent(100).Gap(22).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).AlignItems(ui.Center).Gap(10).Shrink(0).Children(func() {
					orb := Avatar(c, AvatarStyle{Color: pal.Accent}, 76, 76, u.Settings.AnimateCharacters, st.installing)
					orb.Shadow(0, 0, 50, 0, pal.Accent.Alpha(0.25)).Radius(38)
					ui.Text(c, "Hermes "+version+" is here").FontSize(28).FontWeight(800).LetterSpacing(-0.3).Margin(8, 0, 0, 0).
						TextAlign(ui.Center).Role(ui.RoleHeading).Level(1)
					ui.Text(c, "You have "+haveVersion()+". Updating takes a moment, and Hermes reopens by itself on the same chat.").
						FontSize(15).LineHeight(1.5).TextColor(pal.Muted).TextAlign(ui.Center)
				})
				if r != nil {
					if notes := releaseNotes(r.Notes); len(notes) > 0 {
						ui.Scroll(c.Key("notes")).MinHeight(60).Shrink(1).Padding(18, 22).Gap(10).Radius(24).Background(pal.Surf).Border(1, pal.Line).Children(func() {
							ui.Text(c, "WHAT'S NEW").FontSize(11).FontWeight(700).LetterSpacing(1.5).TextColor(pal.AccentText)
							for i, n := range notes {
								ui.Row(c.Key(i)).Gap(10).AlignItems(ui.Start).Children(func() {
									ui.Box(c).Size(5, 5).Radius(3).Background(pal.Muted).Margin(8, 0, 0, 0).Shrink(0)
									ui.Text(c, n).FontSize(14).LineHeight(1.45).Grow(1).MinWidth(0)
								})
							}
						})
					}
				}
				ui.Column(c).Gap(12).Shrink(0).AlignItems(ui.Stretch).Children(func() {
					if st.installing || st.ready != "" {
						status := "Reopening…"
						if st.installing {
							status = fmt.Sprintf("Downloading… %d%%", int(st.progress*100))
						}
						u.progressBar(c, st.progress)
						ui.Text(c, status).FontSize(13).TextColor(pal.Muted).TextAlign(ui.Center).FontFeatures("tnum")
						return
					}
					if st.err != "" {
						ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
							Icon(c, "warn", 16, ui.Hex("#EE736E"))
							ui.Text(c, st.err).FontSize(13).TextColor(pal.Text).Grow(1).Role(ui.RoleAlert)
						})
					}
					ui.Row(c).Gap(10).Justify(ui.Center).Children(func() {
						label := "Update and reopen"
						if st.err != "" {
							label = "Try again"
						}
						if u.accentButton(c, label).Clicked() {
							u.acceptUpdate()
						}
						if PillButton(c, pal, "Not now").Height(46).Clicked() {
							u.declineUpdate()
						}
					})
				})
			})
		})
	})
}

// accentButton is the filled pill of the main action.
func (u *UI) accentButton(c *ui.Context, label string) ui.Element {
	pal := &u.pal
	b := ui.Row(c).Height(46).Padding(0, 22).Radius(999).Background(pal.Accent).AlignItems(ui.Center).Justify(ui.Center).
		Cursor(ui.CursorPointer).Focusable().Role(ui.RoleButton).Label(label)
	b.Children(func() {
		ui.Text(c, label).FontSize(15).FontWeight(800).TextColor(pal.OnAccent)
	})
	if b.Hovered() {
		b.Opacity(0.92)
	}
	return b
}

func (u *UI) progressBar(c *ui.Context, v float64) {
	pal := &u.pal
	ui.Box(c).Height(6).FillWidth().Radius(3).Background(pal.Surf2).Role(ui.RoleProgress).Children(func() {
		ui.Box(c).WidthPercent(100*float32(max(v, 0.02))).Height(6).Radius(3).Background(pal.Accent)
	})
}

// releaseNotes are the points of a version's notes (its section of
// CHANGELOG.md): list items, their wrapped lines joined, and paragraphs.
func releaseNotes(md string) []string {
	var notes []string
	para := false // the last note is a paragraph that may go on
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimRight(line, " \t\r")
		text := strings.TrimSpace(line)
		switch {
		case text == "":
			para = false
		case strings.HasPrefix(text, "#"):
			para = false
		case strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "* "):
			notes = append(notes, text[2:])
			para = false
		case len(notes) > 0 && (para || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			notes[len(notes)-1] += " " + text
		default:
			notes = append(notes, text)
			para = true
		}
	}
	for i, n := range notes {
		notes[i] = strings.NewReplacer("`", "", "**", "").Replace(n)
	}
	return notes
}

// updatesPane is Settings → Updates: the version, a check, the download,
// and the auto-update switch.
func (u *UI) updatesPane(c *ui.Context) {
	st := &u.upd
	if st.checking || st.installing {
		c.After(200 * time.Millisecond)
	}
	unavailable := ""
	if u.Updater == nil {
		unavailable = "This build does not update itself."
	} else {
		unavailable = u.Updater.Unavailable()
	}
	u.card(c, "Hermes Desktop "+Version, "download", func() {
		status := ""
		switch {
		case unavailable != "":
			status = unavailable
		case st.ready != "":
			status = "Version " + st.ready + " is installed. Restart to use it."
		case st.installing:
			status = fmt.Sprintf("Downloading version %s… %d%%", st.available.Version, int(st.progress*100))
		case st.available != nil:
			status = "Version " + st.available.Version + " is available."
		case st.checking:
			status = "Checking for a new version…"
		case st.err != "":
			status = st.err
		case st.upToDate:
			status = "This is the latest version. Checked " + relAgo(time.Now(), st.checkedAt) + "."
		default:
			status = "New versions come from the releases on GitHub, signed."
		}
		ui.Text(c, status).FontSize(15).LineHeight(1.45).Role(ui.RoleStatus)
		if st.installing {
			u.progressBar(c, st.progress)
		}
		if unavailable == "" {
			ui.Row(c).Gap(10).Children(func() {
				switch {
				case st.ready != "":
					if u.accentButton(c, "Restart to update").Clicked() {
						u.restart()
					}
				case st.available != nil && !st.installing:
					if u.accentButton(c, "Download and install").Clicked() {
						u.installUpdate()
					}
				default:
					b := PillButton(c, &u.pal, "Check now")
					if st.checking || st.installing {
						b.Opacity(0.5)
					} else if b.Clicked() {
						u.checkForUpdates(checkAsked)
					}
				}
			})
			ui.Divider(c)
			s := u.Settings
			u.toggleRow(c, "Update automatically", &s.AutoUpdate, func(v bool) {
				n := u.Settings
				n.AutoUpdate = v
				u.SetSettings(n)
			})
			u.note(c, "Checks a second after launch and every six hours. A version found as Hermes opens is offered on its own page; one released while it runs downloads quietly, then asks to restart. Turned off, Hermes checks only when you ask.")
		}
	}).FillWidth()
}

// relAgo is "just now", "5 minutes ago", "3 hours ago".
func relAgo(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < 2*time.Minute:
		return "a minute ago"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + " minutes ago"
	case d < 2*time.Hour:
		return "an hour ago"
	default:
		return itoa(int(d/time.Hour)) + " hours ago"
	}
}

// AcceptUpdate is "Update and reopen".
func (u *UI) AcceptUpdate() { u.acceptUpdate() }

// SetDraft sets the open composer's unsent text.
func (u *UI) SetDraft(text string) {
	if conn := u.App.Conn; conn != nil && u.Selected != "" {
		conn.OpenChat(u.Selected).Draft = text
		return
	}
	u.draft = text
}

// DebugInfo is what the debug hook reports.
type DebugInfo struct{ Offered, Instance, Chat, Draft string }

// DebugState reports the update offered and what is open.
func (u *UI) DebugState() DebugInfo {
	d := DebugInfo{Chat: u.Selected, Draft: u.draft}
	if u.upd.offer && u.upd.available != nil {
		d.Offered = u.upd.available.Version
	}
	if conn := u.App.Conn; conn != nil {
		d.Instance = conn.Instance.ID
		if ch := conn.Chats[u.Selected]; ch != nil && u.Selected != "" {
			d.Draft = ch.Draft
		}
	}
	return d
}

// haveVersion is the running version as "You have …" says it.
func haveVersion() string {
	if Version == "" || Version == "dev" {
		return "a development build"
	}
	return Version
}
