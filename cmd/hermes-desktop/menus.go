package main

import (
	"runtime"

	"github.com/egoist/mygo"

	"hermes-desktop/internal/views"
)

// installMenus sets the real menu bar: the app menu on macOS, and a menu
// bar on the window on Windows and Linux. Each command runs on the UI
// thread through Window.Update, and cancels the held-modifier badges,
// since a key a menu takes never reaches the view.
func installMenus(win *mygo.Window, u *views.UI) {
	act := func(a views.Action) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			win.Update(func() { u.Do(a) })
		}
	}
	file := &mygo.MenuItem{Label: "File", Submenu: []*mygo.MenuItem{
		{Label: "New Chat", Accelerator: "CmdOrCtrl+N", Click: act(views.ActionNewChat)},
		{Label: "Search Chats…", Accelerator: "CmdOrCtrl+K", Click: act(views.ActionSearch)},
		mygo.Separator(),
		{Label: "Export Chat as Markdown…", Accelerator: "CmdOrCtrl+Shift+E", Click: act(views.ActionExport)},
		{Label: "Add Instance…", Click: act(views.ActionAddInstance)},
		mygo.Separator(),
		{Role: mygo.RoleClose},
	}}
	edit := &mygo.MenuItem{Label: "Edit", Submenu: []*mygo.MenuItem{
		{Role: mygo.RoleUndo}, {Role: mygo.RoleRedo}, mygo.Separator(),
		{Role: mygo.RoleCut}, {Role: mygo.RoleCopy}, {Role: mygo.RolePaste}, {Role: mygo.RoleSelectAll},
	}}
	chat := &mygo.MenuItem{Label: "Chat", Submenu: []*mygo.MenuItem{
		{Label: "Stop Agent", Accelerator: "Esc", Click: act(views.ActionStop)},
		{Label: "Refresh", Accelerator: "CmdOrCtrl+R", Click: act(views.ActionRefresh)},
		mygo.Separator(),
		{Label: "Show Chats", Accelerator: "CmdOrCtrl+0", Click: act(views.ActionShowChats)},
	}}
	view := &mygo.MenuItem{Label: "View", Submenu: []*mygo.MenuItem{
		{Role: mygo.RoleResetZoom}, {Role: mygo.RoleZoomIn}, {Role: mygo.RoleZoomOut},
		mygo.Separator(), {Role: mygo.RoleToggleFullScreen},
	}}
	help := &mygo.MenuItem{Label: "Help", Submenu: []*mygo.MenuItem{
		{Label: "Hermes Agent Documentation", Click: func(*mygo.MenuItem, *mygo.Window) {
			mygo.Shell.OpenExternal("https://hermes-agent.nousresearch.com/docs/")
		}},
	}}
	settingsItem := &mygo.MenuItem{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: act(views.ActionSettings)}

	if runtime.GOOS == "darwin" {
		app := &mygo.MenuItem{Label: "Hermes", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout}, mygo.Separator(), settingsItem, mygo.Separator(),
			{Role: mygo.RoleServices}, mygo.Separator(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(),
			{Role: mygo.RoleQuit},
		}}
		window := &mygo.MenuItem{Label: "Window", Role: mygo.RoleWindow, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize}, {Role: mygo.RoleZoom}, mygo.Separator(), {Role: mygo.RoleFront},
		}}
		help.Role = mygo.RoleHelp
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{app, file, edit, chat, view, window, help}))
		return
	}
	// Windows and Linux: Settings sits in Edit, Quit in File.
	edit.Submenu = append(edit.Submenu, mygo.Separator(), settingsItem)
	file.Submenu = append(file.Submenu, &mygo.MenuItem{Role: mygo.RoleQuit, Accelerator: "CmdOrCtrl+Q"})
	win.SetMenu(mygo.NewMenu([]*mygo.MenuItem{file, edit, chat, view, help}))
}
