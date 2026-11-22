package views

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// MenuCommand is one of the app's own menu items. The menu bar is built
// from these (cmd/hermes-desktop/menus.go), and the tests route key
// presses through them as the platform does: a key a menu item's
// accelerator claims reaches the menu, never the view.
type MenuCommand struct {
	Menu        string // "File", "Chat", or "Settings" (the app menu on macOS, Edit elsewhere)
	Label       string
	Accelerator string // as mygo.MenuItem takes it; "" for none
	Action      Action
	Separator   bool // a separator before the item
}

// MenuCommands lists the app's menu items in order.
//
// Esc is deliberately no accelerator: as one it would reach the menu
// before the view, so it could no longer close search, leave settings or
// cancel a rename. The view's ActionEscape handles it, and stops the
// agent when there is nothing else to close.
func MenuCommands() []MenuCommand {
	return []MenuCommand{
		{Menu: "File", Label: "New Chat", Accelerator: "CmdOrCtrl+N", Action: ActionNewChat},
		{Menu: "File", Label: "Search Chats…", Accelerator: "CmdOrCtrl+K", Action: ActionSearch},
		{Menu: "File", Label: "Export Chat as Markdown…", Accelerator: "CmdOrCtrl+Shift+E", Action: ActionExport, Separator: true},
		{Menu: "File", Label: "Add Instance…", Action: ActionAddInstance},
		{Menu: "Chat", Label: "Stop Agent", Action: ActionStop}, // Esc, through the view
		{Menu: "Chat", Label: "Refresh", Accelerator: "CmdOrCtrl+R", Action: ActionRefresh},
		{Menu: "Chat", Label: "Show Chats", Accelerator: "CmdOrCtrl+0", Action: ActionShowChats, Separator: true},
		{Menu: "Settings", Label: "Settings…", Accelerator: "CmdOrCtrl+,", Action: ActionSettings},
	}
}

// ParseAccelerator reads an accelerator ("CmdOrCtrl+Shift+E", "Esc") as
// the view's modifiers and key.
func ParseAccelerator(acc string) (ui.Modifiers, ui.Key, bool) {
	var mods ui.Modifiers
	parts := strings.Split(acc, "+")
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "cmdorctrl", "commandorcontrol":
			mods |= ui.Cmd
		case "shift":
			mods |= ui.Shift
		case "alt", "option":
			mods |= ui.Alt
		case "ctrl", "control":
			mods |= ui.Ctrl
		default:
			return 0, 0, false
		}
	}
	k := parts[len(parts)-1]
	switch {
	case len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z':
		return mods, ui.KeyA + ui.Key(k[0]-'A'), true
	case len(k) == 1 && k[0] >= '0' && k[0] <= '9':
		return mods, ui.Key0 + ui.Key(k[0]-'0'), true
	case k == ",":
		return mods, ui.KeyComma, true
	case strings.EqualFold(k, "Esc") || strings.EqualFold(k, "Escape"):
		return mods, ui.KeyEscape, true
	}
	return 0, 0, false
}
