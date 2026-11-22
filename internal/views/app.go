// Package views is Hermes Desktop's interface, drawn with MyGo's native
// GPU UI (package ui): the connect screen, the agent → session sidebar,
// the chat, settings and the search palette. Views only read core state
// and call its actions; they never do I/O themselves.
package views

import (
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/core"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/theme"
)

// Screen is what the window shows.
type Screen int

const (
	ScreenConnect Screen = iota
	ScreenMain
	ScreenSettings
)

// Native is what the views need from the OS, wrapped so tests can fake it.
type Native interface {
	// Confirm asks a yes/no question with a native dialog.
	Confirm(message, detail, action string) bool
	// SaveText offers a native save dialog for content.
	SaveText(suggestedName, content string) error
	// Platform is "darwin", "windows" or "linux".
	Platform() string
	// SetSidebarMaterial shows the system material behind the sidebar
	// (macOS vibrancy, Windows 11 Mica) or removes it. It reports a note
	// when the change needs a restart to take full effect, else "".
	SetSidebarMaterial(on bool) string
	// SetAppIcon recolours the app's icon (window, taskbar, Dock).
	SetAppIcon(colour string)
}

// UI is the window's view state.
type UI struct {
	App      *core.App
	Settings settings.Settings
	// SettingsChanged persists the settings (it must not block).
	SettingsChanged func(settings.Settings)
	Native          Native

	Screen       Screen
	SettingsPane string

	pal theme.Palette

	// Main
	Selected   string // the open session, "" for a new chat
	NewProfile string // the agent a new chat goes to
	expanded   map[string]bool
	collapsed  map[string]bool
	side       sidebar
	// lists keeps each chat's list state (measured rows, scroll position)
	// so switching back to a chat neither re-measures nor loses its place.
	lists      map[string]*ui.ListState
	openGroups map[string]bool
	openWork   map[string]bool // turns whose work line is open
	draft      string          // the new chat's composer text
	renaming   string
	renameText string
	// marbles are the icon-colour swatches, drawn in the background once.
	marbles     map[string]*ui.Bitmap
	marblesBusy bool
	// materialNote says when the sidebar material needs a restart.
	materialNote string
	// forms holds what is typed on prompt cards, by prompt id.
	forms map[string]*promptForm
	// focusComposer moves the keyboard to the message box next frame.
	focusComposer bool

	hold   holdTracker
	badges bool
	search searchState

	connect connectForm

	// Updater checks for and installs new versions (nil: none), and
	// Relaunch saves what to reopen and restarts the app.
	Updater      Updater
	Relaunch     func(settings.Resume)
	upd          updateState
	pendingToast *ui.Toast

	// SidebarShown, when set, is called once, in the frame after the first
	// one that showed sessions (the launch benchmark's "ready" mark).
	SidebarShown func()
	sidebarDrawn int
}

// New returns the UI on its first screen.
func New(app *core.App, s settings.Settings, native Native) *UI {
	u := &UI{App: app, Settings: s, Native: native, expanded: map[string]bool{}, collapsed: map[string]bool{}, openGroups: map[string]bool{}, openWork: map[string]bool{}, lists: map[string]*ui.ListState{}}
	u.SettingsPane = "Look & Feel"
	if app.Conn != nil {
		u.Screen = ScreenMain
	} else {
		u.Screen = ScreenConnect
		u.connect.reset(app)
	}
	return u
}

// Palette is the palette the last frame drew with.
func (u *UI) Palette() *theme.Palette { return &u.pal }

// SetSettings changes the settings and persists them; the whole window
// follows on the next frame.
func (u *UI) SetSettings(s settings.Settings) {
	s.Normalize()
	if s == u.Settings {
		return
	}
	if s.AppIcon != u.Settings.AppIcon && u.Native != nil {
		u.Native.SetAppIcon(s.AppIcon)
	}
	if s.TranslucentSidebar != u.Settings.TranslucentSidebar && u.Native != nil {
		u.materialNote = u.Native.SetSidebarMaterial(s.TranslucentSidebar)
	}
	u.Settings = s
	if u.SettingsChanged != nil {
		u.SettingsChanged(s)
	}
}

// View builds the window.
func (u *UI) View(c *ui.Context) {
	// The default theme follows the system; read it before replacing it.
	systemDark := c.Theme().Dark
	u.pal = theme.Resolve(u.Settings.Theme(), systemDark, c.Preferences().TextScale)
	c.SetTheme(u.pal.UITheme())
	pal := &u.pal

	u.App.LinkPreviews = u.Settings.LinkPreviews
	u.App.SetShowBackground(u.Settings.ShowBackgroundRuns)
	u.keys(c)
	if u.SidebarShown != nil && u.sidebarDrawn == 1 {
		u.sidebarDrawn = 2
		u.SidebarShown()
	}
	// With the material showing, the root lets it through and each part
	// paints its own background: the chat and settings opaque, the sidebar
	// a light tint over the material.
	root := ui.Column(c).Fill()
	if u.Settings.TranslucentSidebar && c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	} else {
		root.Background(pal.Bg)
	}
	root.Children(func() {
		if u.upd.offer {
			u.updatePage(c)
			if u.upd.offer {
				return
			}
		}
		switch u.Screen {
		case ScreenConnect:
			u.connectView(c)
		case ScreenSettings:
			u.settingsView(c)
		default:
			if u.App.Conn == nil {
				if len(u.App.Instances.List) == 0 {
					u.Screen = ScreenConnect
					u.connect.reset(u.App)
				}
				return // the instance opens on the next frame
			}
			u.mainView(c)
		}
	})
	if u.search.open {
		u.searchView(c)
	}
	for _, msg := range u.App.Toasts {
		c.Toast(msg)
	}
	u.App.Toasts = nil
	if t := u.pendingToast; t != nil {
		u.pendingToast = nil
		c.AddToast(*t)
	}
}

// ---- keyboard ----

// holdDelay is how long Cmd/Ctrl is held alone before badges show.
const holdDelay = 300 * time.Millisecond

// holdTracker decides when the number badges show: the modifier held on
// its own for holdDelay. Pure state, so it is unit-tested without a window.
type holdTracker struct {
	since   time.Time
	spoiled bool
}

// update returns whether badges show now and, if not yet, how long until
// they would.
func (h *holdTracker) update(now time.Time, mods ui.Modifiers, keysHeld int) (show bool, wait time.Duration) {
	if mods&ui.Cmd == 0 {
		*h = holdTracker{}
		return false, 0
	}
	if mods != ui.Cmd || keysHeld > 0 {
		h.spoiled = true // a chord (Cmd+K) or another modifier
	}
	if h.spoiled {
		return false, 0
	}
	if h.since.IsZero() {
		h.since = now
	}
	if d := now.Sub(h.since); d < holdDelay {
		return false, holdDelay - d
	}
	return true, 0
}

// cancel spoils the hold: menu accelerators call it, since a key a menu
// item takes never reaches the window's KeysDown.
func (h *holdTracker) cancel() { h.spoiled = true }

// CancelHold is for the app menu's handlers.
func (u *UI) CancelHold() { u.hold.cancel() }

func (u *UI) keys(c *ui.Context) {
	show, wait := u.hold.update(c.Now(), c.Modifiers(), len(c.KeysDown()))
	if !c.WindowActive() {
		u.hold = holdTracker{}
		show = false
	} else if wait > 0 {
		c.After(wait)
	}
	u.badges = show && u.Screen == ScreenMain && !u.search.open

	// The app menu owns these on platforms with one; there the keys go to
	// the menu and these never fire, so they never fire twice.
	if c.Shortcut(ui.Cmd, ui.KeyN) {
		u.Do(ActionNewChat)
	}
	if c.Shortcut(ui.Cmd, ui.KeyK) {
		u.Do(ActionSearch)
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		u.Do(ActionSettings)
	}
	if c.Shortcut(0, ui.KeyEscape) {
		u.Do(ActionEscape)
	}
	if u.Screen == ScreenMain && !u.search.open && u.Selected != "" && u.App.Conn != nil && c.Shortcut(0, ui.KeyF2) {
		if s, ok := u.App.Conn.Session(u.Selected); ok {
			u.renaming, u.renameText = s.ID, s.Title // F2 renames the open chat
		}
	}
	if u.Screen == ScreenMain && !u.search.open {
		for i := 1; i <= 9; i++ {
			if c.Shortcut(ui.Cmd, ui.Key0+ui.Key(i)) {
				u.JumpTo(i)
			}
		}
	}
}

// Action is a command the menus and the keyboard share.
type Action int

const (
	ActionNewChat Action = iota
	ActionSearch
	ActionSettings
	ActionEscape
	ActionStop
	ActionRefresh
	ActionExport
	ActionCopySessionID
	ActionToggleSidebarAgent
	ActionShowChats
	ActionAddInstance
)

// Do runs an action (from a shortcut, a menu or a button).
func (u *UI) Do(a Action) {
	u.hold.cancel()
	conn := u.App.Conn
	switch a {
	case ActionNewChat:
		if conn == nil {
			return
		}
		u.Screen = ScreenMain
		u.search.open = false
		prof := u.NewProfile
		if s, ok := conn.Session(u.Selected); ok {
			prof = s.Profile
		}
		if prof == "" && len(conn.Profiles) > 0 {
			prof = conn.Profiles[0].Name
		}
		u.Selected, u.NewProfile = "", prof
		u.focusComposer = true
	case ActionSearch:
		if conn == nil {
			return
		}
		u.Screen = ScreenMain
		u.search.toggle()
	case ActionSettings:
		if u.Screen == ScreenSettings {
			u.Screen = ScreenMain
		} else {
			u.Screen = ScreenSettings
		}
	case ActionShowChats:
		u.Screen = ScreenMain
	case ActionEscape:
		switch {
		case u.search.open:
			u.search.open = false
		case u.renaming != "":
			u.renaming = ""
		case u.Screen == ScreenSettings && conn != nil:
			u.Screen = ScreenMain
		default:
			u.Do(ActionStop)
		}
	case ActionStop:
		if conn != nil && u.Selected != "" {
			conn.Stop(u.Selected)
		}
	case ActionRefresh:
		if conn != nil {
			conn.Refresh()
		}
	case ActionExport:
		if conn != nil && u.Selected != "" {
			u.export(u.Selected)
		}
	case ActionAddInstance:
		if conn != nil {
			conn.Close()
		}
		u.Screen = ScreenConnect
		u.connect.reset(u.App)
		u.connect.adding = true
	}
}

// Select opens a session.
func (u *UI) Select(id string) {
	conn := u.App.Conn
	if conn == nil {
		return
	}
	u.Screen = ScreenMain
	if u.Selected == id && conn.Chats[id] != nil {
		return
	}
	u.Selected = id
	u.focusComposer = true
	conn.OpenChat(id)
}

// OpenFromNotice opens the chat a clicked notification is about, on its
// instance.
func (u *UI) OpenFromNotice(instance, chat string) {
	if u.App.Conn == nil || u.App.Conn.Instance.ID != instance {
		if _, ok := u.App.Instances.Find(instance); !ok {
			return
		}
		u.switchInstance(instance)
	}
	u.search.open = false
	u.Select(chat)
}

// JumpTo selects the n-th session row visible in the sidebar (Cmd+n).
func (u *UI) JumpTo(n int) {
	targets := u.side.targets
	if n >= 1 && n <= len(targets) {
		u.Select(targets[n-1])
	}
}

// export saves a chat as Markdown through the native save dialog.
func (u *UI) export(id string) {
	conn := u.App.Conn
	s, _ := conn.Session(id)
	conn.Export(id, func(md string, err error) {
		if err != nil {
			u.App.Toast("Could not export: " + err.Error())
			return
		}
		if u.Native != nil {
			if err := u.Native.SaveText(fileName(s.DisplayTitle())+".md", md); err != nil {
				u.App.Toast("Could not save: " + err.Error())
			}
		}
	})
}

func fileName(title string) string {
	out := make([]rune, 0, len(title))
	for _, r := range title {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			out = append(out, '-')
		default:
			out = append(out, r)
		}
	}
	if len(out) > 80 {
		out = out[:80]
	}
	if len(out) == 0 {
		return "Chat"
	}
	return string(out)
}

// ExpandToolGroups opens every finished turn's tool group in the open
// chat (the design's "expanded" state; also used by the demo build).
func (u *UI) ExpandToolGroups() {
	if c := u.App.Conn; c != nil {
		if ch := c.Chats[u.Selected]; ch != nil {
			for _, t := range ch.Turns {
				u.openWork[t.ID] = true
				u.openGroups["tools:"+t.ID] = true
			}
		}
	}
}

// ScrollChatToTop shows the start of the open chat.
func (u *UI) ScrollChatToTop() {
	if u.Selected != "" {
		u.listFor(u.Selected).ScrollTo(0, ui.Start)
	}
}

// PrefillConnect fills the connect form (the demo build shows the
// design's states with it).
func (u *UI) PrefillConnect(remote, password bool, address string) {
	u.connect.local, u.connect.password, u.connect.address = !remote, password, address
}
