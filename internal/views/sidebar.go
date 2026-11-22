package views

import (
	"math"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/core"
	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/theme"
)

// sidebarWidth is the design's sidebar (flex: 1 0 300px; max-width: 320px).
const sidebarWidth = 320

// initialSessions is how many sessions an agent shows before "N more".
const initialSessions = 5

type rowKind int

const (
	rowAgent rowKind = iota
	rowSession
	rowMore
	rowFewer
)

type sideRow struct {
	kind    rowKind
	profile string
	sess    int // index into Connection.Sessions
	more    int
	first   bool
}

func (r sideRow) key(conn *core.Connection) any {
	switch r.kind {
	case rowAgent:
		return "a:" + r.profile
	case rowSession:
		return "s:" + conn.Sessions[r.sess].ID
	case rowMore:
		return "m:" + r.profile
	}
	return "f:" + r.profile
}

// sidebar is the sidebar's state: its rows, cached until the sessions
// or the grouping change, and the jump targets of the last frame.
type sidebar struct {
	list    ui.ListState
	rows    []sideRow
	rev     int
	conn    *core.Connection
	shape   string
	targets []string // session ids of the visible rows, in order (Cmd+1–9)
}

// rowsFor groups the sessions under their agents: pinned agents first,
// then the default agent and the rest in the server's order; within an
// agent, pinned sessions first, then the most recent.
func (u *UI) rowsFor(conn *core.Connection, now time.Time) []sideRow {
	shape := u.Settings.PinnedAgents + "|" + now.Truncate(time.Minute).String()
	for k, v := range u.expanded {
		if v {
			shape += "+" + k
		}
	}
	for k, v := range u.collapsed {
		if v {
			shape += "-" + k
		}
	}
	if u.side.conn == conn && u.side.rev == conn.Rev && u.side.shape == shape && u.side.rows != nil {
		return u.side.rows
	}
	byProfile := map[string][]int{}
	var order []string
	for _, p := range conn.Profiles {
		order = append(order, p.Name)
		byProfile[p.Name] = nil
	}
	for i, s := range conn.Sessions {
		if _, ok := byProfile[s.Profile]; !ok {
			order = append(order, s.Profile) // a profile the list does not have (yet)
		}
		byProfile[s.Profile] = append(byProfile[s.Profile], i)
	}
	inst := conn.Instance.ID
	var pinned, rest []string
	for _, p := range order {
		if u.Settings.AgentPinned(inst, p) {
			pinned = append(pinned, p)
		} else {
			rest = append(rest, p)
		}
	}
	var rows []sideRow
	for gi, p := range append(pinned, rest...) {
		rows = append(rows, sideRow{kind: rowAgent, profile: p, first: gi == 0})
		if u.collapsed[p] {
			continue
		}
		idx := byProfile[p]
		// Pinned sessions first, keeping recency within each part.
		var pin, other []int
		for _, i := range idx {
			if conn.Sessions[i].Pinned {
				pin = append(pin, i)
			} else {
				other = append(other, i)
			}
		}
		idx = append(pin, other...)
		n := len(idx)
		show := n
		if !u.expanded[p] {
			show = visibleCount(conn, idx, now)
		}
		for _, i := range idx[:show] {
			rows = append(rows, sideRow{kind: rowSession, profile: p, sess: i})
		}
		more := n - show
		if t := conn.Totals[p]; t > n && !u.expanded[p] {
			more = t - show // not every page is in yet
		}
		switch {
		case more > 0:
			rows = append(rows, sideRow{kind: rowMore, profile: p, more: more})
		case u.expanded[p] && n > initialSessions:
			rows = append(rows, sideRow{kind: rowFewer, profile: p})
		}
	}
	u.side.rows, u.side.rev, u.side.conn, u.side.shape = rows, conn.Rev, conn, shape
	return rows
}

// artwork is the faint illustration behind the session list: an orbit and
// a few stars low in the sidebar, in the canvas's line colour.
func artwork(p *ui.Painter, r ui.Rect, pal *theme.Palette) {
	if r.H < 200 {
		return
	}
	col := pal.Line.Alpha(float32(pal.Line.A) / 255 * 0.55)
	cx, cy := r.X+r.W*0.78, r.Y+r.H-40
	for i, rad := range []float32{150, 105, 62} {
		path := &ui.Path{}
		const n = 64
		for k := 0; k <= n; k++ {
			th := float64(k)/n*2*math.Pi - 0.4
			x := cx + rad*float32(math.Cos(th))
			y := cy + rad*0.42*float32(math.Sin(th)) - float32(i)*6
			if k == 0 {
				path.MoveTo(x, y)
			} else {
				path.LineTo(x, y)
			}
		}
		p.StrokePath(path, 1.2, col)
	}
	stars := [][2]float32{{0.12, -210}, {0.3, -150}, {0.52, -250}, {0.2, -90}, {0.6, -120}, {0.42, -40}}
	for _, s := range stars {
		x, y := r.X+r.W*s[0], r.Y+r.H+s[1]
		p.Fill(ui.Rect{X: x - 1, Y: y - 1, W: 2, H: 2}, pal.Line, 1)
	}
}

// visibleCount is how many of an agent's sessions show before "N more":
// those active in the last day, at least one and at most five (the
// design: Hermes 5 + 234 more, sidequest 1 + 3 more, helix 2).
func visibleCount(conn *core.Connection, idx []int, now time.Time) int {
	recent := 0
	for _, i := range idx {
		s := conn.Sessions[i]
		if s.Pinned || conn.Running[s.ID] || now.Sub(s.Activity()) < 24*time.Hour {
			recent++
		}
	}
	return min(len(idx), max(1, min(recent, initialSessions)))
}

// avatarStyle reads an agent's look from its profile.
func avatarStyle(p hermes.Profile, name string) AvatarStyle {
	col := p.Color()
	st := AvatarStyle{Color: ui.Hex("#B9566F")}
	if col != "" && strings.HasPrefix(col, "#") && (len(col) == 7 || len(col) == 4) {
		st.Color = ui.Hex(col)
	} else if name != "" && name != "default" {
		// A stable colour per agent from the accents.
		h := 0
		for _, r := range name {
			h = h*31 + int(r)
		}
		if h < 0 {
			h = -h
		}
		st.Color = theme.Shade(ui.Hex(theme.Accents[h%len(theme.Accents)].Hex), 0.85)
	}
	if strings.Contains(string(p.UIMeta), `"shape":"bot"`) || strings.Contains(string(p.UIMeta), `"shape": "bot"`) {
		st.Bot = true
	}
	return st
}

func (u *UI) sidebarView(c *ui.Context, conn *core.Connection) {
	pal := &u.pal
	side := ui.Column(c).Width(sidebarWidth).FillHeight().Shrink(0).Background(pal.Side).
		BorderWidth(0, 1, 0, 0).BorderColor(pal.Line).Padding(14, 14, 12, 14)
	if u.Settings.TranslucentSidebar && c.Vibrancy() {
		side.Background(pal.Side.Alpha(0.35)) // the canvas, faintly, over the material
	}
	side.Children(func() {
		// Title row: room for the traffic lights on macOS, then the buttons.
		// Room for window buttons on the left (macOS's traffic lights, or a
		// Linux desktop that puts them there); the row drags the window.
		bar := c.TitleBar()
		top := ui.Row(c).Height(max(44, bar.Bottom-14+8)).Padding(0, 4, 0, max(4, bar.Left-14)).AlignItems(ui.Center).DragWindow()
		top.Children(func() {
			ui.Spacer(c)
			ui.Row(c).Gap(8).Children(func() {
				f := CircleButton(c, pal, "filter", "Filter sessions", 40)
				f.Menu(func(m *ui.Menu) {
					if m.Item("Expand all agents").Chosen() {
						clear(u.collapsed)
					}
					if m.Item("Collapse all agents").Chosen() {
						for _, p := range conn.Profiles {
							u.collapsed[p.Name] = true
						}
					}
					m.Separator()
					if m.Item("Show background runs").Checked(u.Settings.ShowBackgroundRuns).
						Tooltip("Cron runs and subagents, beside your chats").Chosen() {
						s := u.Settings
						s.ShowBackgroundRuns = !s.ShowBackgroundRuns
						u.SetSettings(s)
					}
					m.Separator()
					if m.Item("Refresh").Chosen() {
						conn.Refresh()
					}
				})
				if CircleButton(c, pal, "search", "Search", 40).Clicked() {
					u.Do(ActionSearch)
				}
				if CircleButton(c, pal, "plus", "New chat", 40).Clicked() {
					u.Do(ActionNewChat)
				}
			})
		})
		u.instanceCard(c, conn)
		u.sessionTree(c, conn)
		u.sidebarFooter(c, conn)
	})
}

func (u *UI) instanceCard(c *ui.Context, conn *core.Connection) {
	pal := &u.pal
	card := ui.Row(c).Margin(14, 0, 10, 0).Gap(10).Padding(10, 12).Radius(16).Border(1, pal.Line).Background(pal.Surf).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().Label("Instance " + conn.Instance.Name)
	if card.Hovered() {
		card.Background(pal.Surf2)
	}
	card.Children(func() {
		ui.Row(c).Size(30, 30).Radius(9).Background(pal.Surf2).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
			Icon(c, "server", 16, pal.Text)
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
			ui.Text(c, conn.Instance.Name).FontSize(14).FontWeight(700).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				dot := pal.OK
				switch conn.Status {
				case core.Connecting:
					dot = pal.Muted
				case core.Offline:
					dot = ui.Hex("#E8B04A")
				case core.SignedOut:
					dot = ui.Hex("#EE736E")
				}
				ui.Box(c).Size(7, 7).Radius(3.5).Background(dot).Shrink(0)
				host := conn.Instance.Host()
				if h, _, err := net.SplitHostPort(host); err == nil {
					host = h
				}
				if net.ParseIP(host) == nil {
					if i := strings.IndexByte(host, '.'); i > 0 {
						host = host[:i]
					}
				}
				ui.Text(c, host+" · "+conn.Status.String()).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
			})
		})
		Icon(c, "chevronSmall", 16, pal.Muted)
	})
	card.Menu(func(m *ui.Menu) {
		for _, in := range u.App.Instances.List {
			if m.Item(in.Name).Subtitle(in.Host()).Radio(in.ID == conn.Instance.ID).ID("inst-"+in.ID).Chosen() && in.ID != conn.Instance.ID {
				u.switchInstance(in.ID)
			}
		}
		m.Separator()
		if m.Item("Add instance…").Chosen() {
			u.Do(ActionAddInstance)
		}
		if m.Item("Manage instances…").Chosen() {
			u.Screen, u.SettingsPane = ScreenSettings, "Instances"
		}
		if conn.Status == core.SignedOut {
			if m.Item("Sign in again…").Chosen() {
				u.reauth(conn.Instance.ID)
			}
		}
	})
}

func (u *UI) sessionTree(c *ui.Context, conn *core.Connection) {
	pal := &u.pal
	rows := u.rowsFor(conn, c.Now())
	u.side.list.Key = func(i int) any { return rows[i].key(conn) }
	now := c.Now()

	// Jump targets: the session rows on screen, in order.
	first, last := u.side.list.Visible()
	u.side.targets = u.side.targets[:0]
	for i := max(first, 0); i <= last && i < len(rows); i++ {
		if rows[i].kind == rowSession && len(u.side.targets) < 9 {
			u.side.targets = append(u.side.targets, conn.Sessions[rows[i].sess].ID)
		}
	}
	if len(u.side.targets) == 0 { // before the first layout: the first rows
		for _, r := range rows {
			if r.kind == rowSession && len(u.side.targets) < 9 {
				u.side.targets = append(u.side.targets, conn.Sessions[r.sess].ID)
			}
		}
	}
	badge := map[string]int{}
	if u.badges {
		for i, id := range u.side.targets {
			badge[id] = i + 1
		}
	}

	if conn.Loaded && len(rows) > 0 && u.sidebarDrawn == 0 {
		u.sidebarDrawn = 1
		c.After(0)
	}
	list := ui.List(c, &u.side.list, len(rows), func(i int) {
		r := rows[i]
		switch r.kind {
		case rowAgent:
			u.agentRow(c, conn, r, now)
		case rowSession:
			u.sessionRow(c, conn, r, now, badge[conn.Sessions[r.sess].ID])
		case rowMore, rowFewer:
			u.moreRow(c, r)
		}
	}).Grow(1).PaddingY(6).Label("Agents and sessions")
	if !conn.Loaded {
		list.Children(func() {
			ui.Row(c).Margin(ui.Auto).Gap(10).AlignItems(ui.Center).Children(func() {
				if conn.Status == core.SignedOut {
					ui.Text(c, "Sign in again to see your chats.").TextColor(pal.Muted)
					return
				}
				Loader(c, u.Settings.Loader, pal, true)
				ui.Text(c, "Loading sessions…").TextColor(pal.Muted)
			})
		})
	}
	if u.Settings.BackgroundArtwork {
		list.Draw(func(p *ui.Painter, r ui.Rect) { artwork(p, r, &u.pal) })
	}
}

func (u *UI) agentRow(c *ui.Context, conn *core.Connection, r sideRow, now interface{ IsZero() bool }) {
	pal := &u.pal
	prof, _ := conn.Profile(r.profile)
	name := prof.Title()
	if name == "" {
		name = r.profile
	}
	// The agent's latest activity, and whether any of its chats is running.
	var lastAct hermes.Time
	working := false
	for _, s := range conn.Sessions {
		if s.Profile == r.profile {
			if lastAct.IsZero() {
				lastAct = hermes.Time{Time: s.Activity()}
			}
			if conn.Running[s.ID] {
				working = true
			}
		}
	}
	pad := []float32{14, 6, 8, 6}
	if r.first {
		pad = []float32{8, 6, 8, 6}
	}
	row := ui.Row(c).Gap(12).Padding(pad...).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Focusable().Label("Agent " + name).Expanded(!u.collapsed[r.profile])
	row.Children(func() {
		Avatar(c, avatarStyle(prof, r.profile), 38, map[bool]float32{true: 30, false: 38}[avatarStyle(prof, r.profile).Bot], u.Settings.AnimateCharacters, working && u.Settings.AnimateCharacters)
		ui.Text(c, name).FontSize(17).FontWeight(700).Grow(1).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
		if u.Settings.AgentPinned(conn.Instance.ID, r.profile) {
			ui.Icon(c, icon("pin")).Size(15, 15).TextColor(ui.Hex("#F5C542")).Shrink(0).Label("Pinned")
		}
		if !lastAct.IsZero() {
			ui.Text(c, relTime(c.Now(), lastAct.Time)).FontSize(13).TextColor(pal.Muted).Shrink(0)
		}
	})
	if row.Clicked() {
		u.collapsed[r.profile] = !u.collapsed[r.profile]
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("New chat with " + name).Chosen() {
			u.Screen, u.Selected, u.NewProfile = ScreenMain, "", r.profile
		}
		pinned := u.Settings.AgentPinned(conn.Instance.ID, r.profile)
		if m.Item(map[bool]string{true: "Unpin", false: "Pin"}[pinned]).Chosen() {
			s := u.Settings
			s.SetAgentPinned(conn.Instance.ID, r.profile, !pinned)
			u.SetSettings(s)
		}
		if m.Item(map[bool]string{true: "Show sessions", false: "Hide sessions"}[u.collapsed[r.profile]]).Chosen() {
			u.collapsed[r.profile] = !u.collapsed[r.profile]
		}
		m.Separator()
		if m.Item("Copy agent name").Chosen() {
			c.WriteClipboard(r.profile)
		}
		if prof.Model != "" {
			m.Item("Model: " + prof.Model).Disabled(true)
		}
	})
}

func (u *UI) sessionRow(c *ui.Context, conn *core.Connection, r sideRow, now interface{ IsZero() bool }, badge int) {
	pal := &u.pal
	s := conn.Sessions[r.sess]
	selected := s.ID == u.Selected && u.Screen == ScreenMain
	running := conn.Running[s.ID]
	title := conn.Title(s)
	label := title
	if s.Background() {
		src := strings.ToLower(s.Source)
		label = strings.ToUpper(src[:1]) + src[1:] + " run: " + title
	}
	row := ui.Row(c).MarginStart(24).BorderWidth(0, 0, 0, 1.5).BorderColor(pal.Line2).
		Gap(8).Padding(9, 10, 9, 0).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Focusable().Label(label).AccessSelected(selected)
	switch {
	case selected:
		row.Background(pal.Surf).Radius(0, 12, 12, 0)
	case row.Hovered():
		row.Background(pal.Surf.Alpha(0.6)).Radius(0, 12, 12, 0)
	}
	row.Children(func() {
		ui.Box(c).Size(14, 1.5).Background(pal.Line2).Shrink(0)
		if u.renaming == s.ID {
			in := ui.TextInput(c, &u.renameText).AutoFocus().Grow(1).FontSize(14).Label("Rename chat")
			if in.Submitted() {
				if t := strings.TrimSpace(u.renameText); t != "" && t != s.Title {
					conn.Rename(s.ID, t)
				}
				u.renaming = ""
			}
			if in.FocusLeft() {
				u.renaming = ""
			}
			return
		}
		t := ui.Text(c, title).FontSize(14).TextColor(pal.Text).Grow(1).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
		if selected {
			t.FontWeight(600)
		}
		if s.Background() {
			sourceBadge(c, pal, s.Source)
		}
		if s.Unread && !selected {
			ui.Box(c).Size(8, 8).Radius(4).Background(pal.Accent).Shrink(0).Label("Unread").Role(ui.RoleImage)
		}
		if running {
			Loader(c, u.Settings.Loader, pal, false)
		}
		switch {
		case badge > 0:
			ui.Text(c, itoa(badge)).FontSize(11).FontWeight(700).TextColor(pal.OnAccent).
				Padding(1, 6).Radius(6).Background(pal.Accent).Shrink(0).Label("Shortcut " + itoa(badge))
		case running && selected:
			ui.Text(c, "Working").FontSize(12).TextColor(pal.AccentText).Shrink(0)
		default:
			col := pal.Muted
			if running {
				col = pal.AccentText
			}
			ui.Text(c, relTime(c.Now(), s.Activity())).FontSize(12).TextColor(col).Shrink(0)
		}
	})
	if row.Clicked() {
		u.Select(s.ID)
	}
	if row.DoubleClicked() {
		u.renaming, u.renameText = s.ID, s.Title
	}
	row.ContextMenu(func(m *ui.Menu) { u.sessionMenu(c, m, conn, s) })
}

// sourceBadge marks a background run with its source ("cron", "subagent").
func sourceBadge(c *ui.Context, pal *theme.Palette, source string) {
	ui.Text(c, strings.ToLower(source)).FontSize(11).FontWeight(600).TextColor(pal.Muted).
		Padding(1, 6).Radius(6).Border(1, pal.Line2).Shrink(0)
}

// sessionMenu is the menu of a session, in the sidebar and the chat header.
func (u *UI) sessionMenu(c *ui.Context, m *ui.Menu, conn *core.Connection, s hermes.Session) {
	if m.Item("Rename…").Shortcut(0, ui.KeyF2).Chosen() {
		u.renaming, u.renameText = s.ID, s.Title
	}
	if m.Item(map[bool]string{true: "Unpin", false: "Pin"}[s.Pinned]).Chosen() {
		conn.SetPinned(s.ID, !s.Pinned)
	}
	m.Separator()
	if m.Item("Copy").Chosen() {
		c.WriteClipboard(s.DisplayTitle())
	}
	if m.Item("Copy session ID").Chosen() {
		c.WriteClipboard(s.ID)
	}
	if m.Item("Copy resume command").Chosen() {
		c.WriteClipboard("hermes --resume " + s.ID)
	}
	if m.Item("Export as Markdown…").Chosen() {
		u.export(s.ID)
	}
	m.Separator()
	if m.Item("Delete…").Danger().Chosen() {
		title := s.DisplayTitle()
		if u.Native == nil || u.Native.Confirm("Delete “"+title+"”?", "The chat and its history are deleted on "+conn.Instance.Name+". This cannot be undone.", "Delete") {
			if u.Selected == s.ID {
				u.Selected = ""
			}
			conn.Delete(s.ID)
		}
	}
}

func (u *UI) moreRow(c *ui.Context, r sideRow) {
	pal := &u.pal
	label := itoa(r.more) + " more sessions"
	if r.more == 1 {
		label = "1 more session"
	}
	if r.kind == rowFewer {
		label = "Show fewer"
	}
	row := ui.Row(c).MarginStart(24).BorderWidth(0, 0, 0, 1.5).BorderColor(pal.Line2).
		Gap(8).Padding(9, 10, 9, 0).AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().Label(label)
	row.Children(func() {
		ui.Box(c).Size(14, 1.5).Background(pal.Line2).Shrink(0)
		ui.Text(c, label).FontSize(14).TextColor(pal.Muted).Grow(1)
		if r.kind == rowMore {
			Icon(c, "chevronRight", 14, pal.Muted)
		}
	})
	if row.Hovered() {
		row.Background(pal.Surf.Alpha(0.6)).Radius(0, 12, 12, 0)
	}
	if row.Clicked() {
		u.expanded[r.profile] = r.kind == rowMore
	}
}

func (u *UI) sidebarFooter(c *ui.Context, conn *core.Connection) {
	pal := &u.pal
	name := conn.Me.DisplayName
	if name == "" {
		name = conn.Me.UserID
	}
	if name == "" || name == "deployment-token" || name == "local" {
		name = conn.Instance.Name
	}
	ui.Row(c).Gap(10).Padding(12, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(pal.Line).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Size(40, 40).Radius(20).Background(pal.Surf).Border(1, pal.Line).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, initials(name)).FontSize(15).FontWeight(600).TextColor(pal.Muted)
		})
		ui.Text(c, name).FontSize(15).FontWeight(600).Grow(1).SingleLine().Ellipsis("…")
		if CircleButton(c, pal, "gear", "Settings", 40).Clicked() {
			u.Do(ActionSettings)
		}
	})
}

func initials(name string) string {
	f := strings.Fields(name)
	switch {
	case len(f) == 0:
		return "?"
	case len(f) == 1:
		r := []rune(f[0])
		if len(r) > 1 {
			return strings.ToUpper(string(r[:2]))
		}
		return strings.ToUpper(string(r))
	}
	return strings.ToUpper(string([]rune(f[0])[:1]) + string([]rune(f[len(f)-1])[:1]))
}

// macTitleInset reports whether the window draws its own title bar with
// the traffic lights over the sidebar (macOS only).
func macTitleInset() bool { return runtime.GOOS == "darwin" }
