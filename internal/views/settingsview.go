package views

import (
	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/appicon"

	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/theme"
)

// Version is the app's version, set by main.
var Version = "0.1.0"

var settingsPanes = []struct{ name, icon string }{
	{"General", "user"},
	{"Instances", "server"},
	{"Agents & profiles", "agents"},
	{"Look & Feel", "halfCircle"},
	{"Notifications", "bell"},
	{"Permissions", "shield"},
	{"Terminal & SSH", "terminal"},
	{"Updates", "download"},
}

func (u *UI) settingsView(c *ui.Context) {
	pal := &u.pal
	ui.Column(c).Fill().Background(pal.Bg).Children(func() {
		ui.Row(c).Height(56).FillWidth().Padding(0, 22).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(pal.Line).DragWindow().Children(func() {
			ui.Text(c, "Settings").FontSize(14).FontWeight(700).TextColor(pal.Muted).Grow(1).TextAlign(ui.Center)
		})
		ui.Row(c).Grow(1).FillWidth().AlignItems(ui.Stretch).Children(func() {
			u.settingsNav(c)
			ui.Scroll(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(1040).Padding(28, 32, 48, 32).Gap(22).Children(func() {
					ui.Text(c, u.SettingsPane).FontSize(30).FontWeight(800).Role(ui.RoleHeading).Level(1)
					switch u.SettingsPane {
					case "Look & Feel":
						u.lookAndFeel(c)
					case "Instances":
						u.instancesPane(c)
					case "Agents & profiles":
						u.agentsPane(c)
					case "Notifications":
						u.notificationsPane(c)
					case "General":
						u.generalPane(c)
					default:
						u.infoPane(c)
					}
				})
			})
		})
	})
}

func (u *UI) settingsNav(c *ui.Context) {
	pal := &u.pal
	ui.Column(c).Width(230).Shrink(0).Padding(20, 14).Gap(4).Label("Settings sections").Children(func() {
		if u.App.Conn != nil {
			back := ui.Row(c).Gap(10).Padding(10, 12).Margin(0, 0, 10, 0).Radius(12).AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().Label("Back to chats")
			back.Children(func() {
				Icon(c, "chevronLeft", 16, pal.Muted)
				ui.Text(c, "Back to chats").FontSize(14).FontWeight(600).TextColor(pal.Muted)
			})
			if back.Clicked() {
				u.Screen = ScreenMain
			}
		}
		for _, p := range settingsPanes {
			on := u.SettingsPane == p.name
			b := ui.Row(c).Gap(12).Padding(11, 12).Radius(12).AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().Label(p.name).AccessSelected(on)
			if on {
				b.Background(pal.Surf)
			} else if b.Hovered() {
				b.Background(pal.Surf.Alpha(0.5))
			}
			b.Children(func() {
				col := pal.Text
				if on && p.icon == "halfCircle" {
					col = pal.AccentText
				}
				Icon(c, p.icon, 18, col)
				t := ui.Text(c, p.name).FontSize(15)
				if on {
					t.FontWeight(700)
				}
			})
			if b.Clicked() {
				u.SettingsPane = p.name
			}
		}
	})
}

// card is a settings section (padding 24, radius 28).
func (u *UI) card(c *ui.Context, title, iconName string, fn func()) ui.Element {
	pal := &u.pal
	e := ui.Column(c).Padding(24).Radius(28).Background(pal.Surf).Border(1, pal.Line).Gap(18).MinWidth(0)
	e.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Role(ui.RoleHeading).Level(2).Children(func() {
			if iconName == "Aa" {
				ui.Text(c, "Aa").FontSize(15).FontWeight(800)
			} else if iconName != "" {
				Icon(c, iconName, 18, pal.Text)
			}
			ui.Text(c, title).FontSize(17).FontWeight(700)
		})
		fn()
	})
	return e
}

func (u *UI) note(c *ui.Context, s string) {
	ui.Text(c, s).FontSize(13).TextColor(u.pal.Muted)
}

// cards lays sections out two to a row when there is room
// (grid-template-columns: repeat(auto-fit, minmax(380px, 1fr))).
func (u *UI) cards(c *ui.Context, fn func()) {
	w, _ := c.Size()
	cols := 2
	if w < 250+64+380*2+20 {
		cols = 1
	}
	ui.Grid(c).Columns(cols).Gap(20).FillWidth().AlignItems(ui.Stretch).Children(fn)
}

func (u *UI) lookAndFeel(c *ui.Context) {
	pal := &u.pal
	s := u.Settings
	set := func(fn func(*settings.Settings)) {
		n := u.Settings
		fn(&n)
		u.SetSettings(n)
	}
	tile := func(bg ui.Color, fg ui.Color, sel bool, split bool) ui.Element {
		// The design's tiles are 40 px pills around their "Aa".
		t := ui.Row(c).Size(40, 84).Radius(18).Border(1, pal.Line).Justify(ui.Center).AlignItems(ui.Center).Clip()
		if split {
			t.Draw(func(p *ui.Painter, r ui.Rect) {
				p.Fill(r, ui.Hex("#FFFFFF"), 18)
				path := &ui.Path{}
				path.MoveTo(r.X, r.Y)
				path.LineTo(r.X+r.W, r.Y)
				path.LineTo(r.X, r.Y+r.H)
				path.Close()
				p.Clip(r, 18, func() { p.FillPath(path, ui.Hex("#030320")) })
			})
		} else {
			t.Background(bg)
		}
		if sel {
			t.Shadow(0, 0, 0, 2.5, pal.Accent)
		}
		t.Children(func() { ui.Text(c, "Aa").FontSize(26).FontWeight(800).TextColor(fg) })
		return t
	}
	option := func(label string, sel bool, draw func()) bool {
		b := ui.Column(c).Gap(10).Cursor(ui.CursorPointer).Focusable().Label(label).AccessSelected(sel).MinWidth(0)
		b.Children(func() {
			draw()
			col := pal.Muted
			if sel {
				col = pal.Text
			}
			ui.Text(c, label).FontSize(14).TextColor(col)
		})
		return b.Clicked() || b.Shortcut(0, ui.KeySpace)
	}

	u.cards(c, func() {
		u.card(c, "Appearance", "halfCircle", func() {
			ui.Grid(c).Columns(3).Gap(12).Children(func() {
				for _, ap := range []theme.Appearance{theme.Automatic, theme.Light, theme.Dark} {
					bg := map[theme.Appearance]ui.Color{theme.Light: ui.Hex("#FFFFFF"), theme.Dark: ui.Hex("#030320")}[ap]
					if option(string(ap), s.Appearance == ap, func() { tile(bg, pal.Accent, s.Appearance == ap, ap == theme.Automatic) }) {
						set(func(n *settings.Settings) { n.Appearance = ap })
					}
				}
			})
			u.note(c, "Follow the system setting or stay in light or dark mode.")
		})
		u.preview(c)
		u.card(c, "Light Canvas", "sun", func() {
			ui.Grid(c).Columns(2).Gap(12).Children(func() {
				for _, name := range theme.LightCanvases {
					cv := theme.CanvasNamed(name)
					if option(name, s.LightCanvas == name, func() { tile(cv.Bg, theme.Shade(pal.Accent, 0.6), s.LightCanvas == name, false) }) {
						set(func(n *settings.Settings) {
							n.LightCanvas = name
							if n.Appearance == theme.Dark {
								n.Appearance = theme.Light
							}
						})
					}
				}
			})
			u.note(c, "The paper the app is printed on while it is in light mode.")
		})
		u.card(c, "Dark Canvas", "moon", func() {
			ui.Grid(c).Columns(3).Gap(12).Children(func() {
				for _, name := range theme.DarkCanvases {
					cv := theme.CanvasNamed(name)
					if option(name, s.DarkCanvas == name, func() { tile(cv.Bg, pal.Accent, s.DarkCanvas == name, false) }) {
						set(func(n *settings.Settings) {
							n.DarkCanvas = name
							if n.Appearance == theme.Light {
								n.Appearance = theme.Dark
							}
						})
					}
				}
			})
			u.note(c, "The same, for dark mode.")
		})
		u.card(c, "Accent Colour", "palette", func() {
			ui.Grid(c).Columns(5).Gap(14).JustifyItems(ui.Center).Children(func() {
				for _, a := range theme.Accents {
					sel := s.Accent == a.Name
					col := ui.Hex(a.Hex)
					sw := ui.Row(c).Size(52, 52).Radius(26).Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).
						Focusable().Label(a.Name + " accent").AccessSelected(sel)
					if a.Name == "Rainbow" {
						var stops []ui.GradientStop
						for i, h := range theme.RainbowStops {
							stops = append(stops, gs(ui.Hex(h), float32(i)/float32(len(theme.RainbowStops)-1)))
						}
						sw.ConicGradient(ui.ConicGradient{Stops: stops})
					} else {
						sw.Background(col)
					}
					if sel {
						sw.Border(3, ui.Hex("#FFFFFF")).Shadow(0, 0, 0, 3, col).Shadow(0, 0, 18, 0, col)
					}
					sw.Children(func() {
						if sel {
							Icon(c, "check", 22, ui.Hex("#FFFFFF"))
						}
					})
					if sw.Clicked() {
						set(func(n *settings.Settings) { n.Accent = a.Name })
					}
				}
			})
			u.note(c, "Used for links, loaders, the voice button and other emphasised controls.")
		})
		u.card(c, "Font & Text Size", "Aa", func() {
			font := theme.FontLabel(s.Font)
			var labels []string
			for _, f := range theme.Fonts {
				labels = append(labels, theme.FontLabel(f))
			}
			sel := ui.Select(c, &font, labels).Height(48).FillWidth().Label("Font")
			if sel.Changed() {
				for _, f := range theme.Fonts {
					if theme.FontLabel(f) == font {
						set(func(n *settings.Settings) { n.Font = f })
					}
				}
			}
			ui.Text(c, "A clear view of your conversations.").FontSize(pal.Scale(16)).Font(pal.Font)
			ui.Column(c).Gap(8).Padding(4, 0, 0, 0).Children(func() {
				label := "System"
				if s.TextSize > 0 {
					label = "Size " + itoa(s.TextSize+1)
				}
				ui.Text(c, label).FontSize(15).FontWeight(700)
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "A").FontSize(13)
					v := float64(s.TextSize)
					// The design's slider: a thin grey track and an accent thumb.
					sl := ui.SliderBase(c, &v, 0, theme.MaxTextSize).Step(1).Grow(1).Height(20).PaddingX(8).Label("Text size")
					at := float32(v / theme.MaxTextSize)
					if c.Direction() == ui.RightToLeft {
						at = 1 - at
					}
					line, acc := pal.Line2, pal.Accent
					sl.Draw(func(p *ui.Painter, r ui.Rect) {
						p.Fill(ui.Rect{X: r.X + 8, Y: r.Y + 8, W: r.W - 16, H: 4}, line, 2)
						x := r.X + 8 + (r.W-16)*at
						p.Fill(ui.Rect{X: x - 7, Y: r.Y + 3, W: 14, H: 14}, acc, 7)
					})
					if sl.Changed() {
						set(func(n *settings.Settings) { n.TextSize = int(v + 0.5) })
					}
					ui.Text(c, "A").FontSize(22)
				})
			})
			u.note(c, "The first step follows your system text size.")
		})
	})

	// Loading indicator: a full-width card of fifteen tiles.
	u.card(c, "Loading Indicator", "grid", func() {
		ui.Grid(c).Columns(5).Gap(14).Children(func() {
			for _, id := range settings.Loaders {
				sel := s.Loader == id
				t := ui.Row(c).Height(72).Radius(18).Border(1, pal.Line).Background(pal.Surf2).Justify(ui.Center).AlignItems(ui.Center).
					Cursor(ui.CursorPointer).Focusable().Label(loaderName(id)).AccessSelected(sel)
				if sel {
					t.Shadow(0, 0, 0, 2.5, pal.Accent).Shadow(0, 0, 16, 0, pal.Accent)
				}
				t.Children(func() {
					if id[0] == 'g' {
						GridLoader(c, id, pal.Accent, 6, 3, sel)
					} else {
						SphereLoader(c, id, pal, 42, sel)
					}
				})
				if t.Clicked() {
					set(func(n *settings.Settings) { n.Loader = id })
				}
			}
		})
		u.note(c, "Shown while agents and tools are working: a marching grid, a sphere of light, or a sphere of mist.")
	}).FillWidth()

	u.cards(c, func() {
		u.card(c, "Characters", "smile", func() {
			u.toggleRow(c, "Animate agent characters", &s.AnimateCharacters, func(v bool) { set(func(n *settings.Settings) { n.AnimateCharacters = v }) })
			u.note(c, "Agent orbs breathe while idle and move about while an agent is working. Turn this off to keep them still.")
		})
		u.card(c, "Interaction", "pulse", func() {
			u.toggleRow(c, "Sounds", &s.Sounds, func(v bool) { set(func(n *settings.Settings) { n.Sounds = v }) })
			u.note(c, "Plays a soft chime when an agent finishes or needs your approval.")
		})
		u.card(c, "Background Artwork", "image", func() {
			u.toggleRow(c, "Show background artwork", &s.BackgroundArtwork, func(v bool) { set(func(n *settings.Settings) { n.BackgroundArtwork = v }) })
			u.note(c, "A faint illustration behind the session list.")
		})
		u.card(c, "App Icon", "image", func() {
			u.marblePicker(c, set)
			u.note(c, "The marble in the Dock, taskbar and title bar.")
		})
		u.card(c, "Window", "window", func() {
			u.toggleRow(c, "Translucent sidebar", &s.TranslucentSidebar, func(v bool) { set(func(n *settings.Settings) { n.TranslucentSidebar = v }) })
			n := "Lets the desktop show through the sidebar on macOS and Windows 11."
			switch {
			case u.Native != nil && u.Native.Platform() == "linux":
				n += " Linux has no window materials, so the sidebar stays opaque here."
			case u.Native != nil && u.Native.Platform() == "windows":
				n += " On Windows the menus then open from Alt or F10, as Windows draws no menu bar over Mica."
			}
			u.note(c, n)
			if u.materialNote != "" {
				ui.Text(c, u.materialNote).FontSize(13).FontWeight(600).TextColor(pal.AccentText)
			}
		})
	})
}

func loaderName(id string) string {
	n := itoa(int(id[1] - '0'))
	switch id[0] {
	case 'g':
		return "Marching grid " + n
	case 'l':
		return "Sphere of light " + n
	}
	return "Sphere of mist " + n
}

func (u *UI) toggleRow(c *ui.Context, label string, v *bool, changed func(bool)) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(16).Grow(1)
		on := *v
		Toggle(c, &u.pal, &on, label)
		if on != *v {
			changed(on)
		}
	})
}

// preview is the live preview card: it repaints with every change.
func (u *UI) preview(c *ui.Context) {
	pal := &u.pal
	ui.Column(c).Padding(24).Radius(28).Background(pal.Bg).Border(1, pal.Line2).Gap(12).MinWidth(0).Label("Preview").Children(func() {
		ui.Text(c, "PREVIEW").FontSize(12).FontWeight(700).LetterSpacing(1.7).TextColor(pal.Muted)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			Avatar(c, AvatarStyle{Color: ui.Hex("#B9566F")}, 30, 30, u.Settings.AnimateCharacters, false)
			ui.Text(c, "Hermes").FontSize(pal.Scale(16)).FontWeight(700)
		})
		ui.Row(c).MarginStart(14).BorderWidth(0, 0, 0, 1.5).BorderColor(pal.Line2).PaddingStart(12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Shorten morning news and surf links").FontSize(pal.Scale(14)).Grow(1).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
			Loader(c, u.Settings.Loader, pal, false)
			ui.Text(c, "Working").FontSize(12).TextColor(pal.AccentText)
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.Text(c, "Keep the surf report to one line.").FontSize(pal.Scale(14)).Padding(10, 14).Radius(18).Background(pal.Surf2).MaxWidthPercent(80)
		})
		ui.Row(c).Gap(8).Padding(9, 12).Radius(14).Background(pal.Pill).Border(1, pal.PillLine).AlignItems(ui.Center).Children(func() {
			Icon(c, "terminal", 16, pal.OK)
			ui.Text(c, "Ran command").FontSize(13).FontWeight(700)
			ui.Text(c, "hermes sessions list").FontSize(12).Font(pal.Mono).TextColor(pal.Muted).SingleLine()
		})
		ui.Row(c).Gap(8).Padding(6).Radius(22).Background(pal.Surf).Border(1, pal.Line).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Message Hermes").FontSize(13).TextColor(pal.Muted).PaddingStart(10).Grow(1)
			ui.Row(c).Size(32, 32).Radius(16).Background(pal.Accent).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				Icon(c, "voice", 16, pal.OnAccent)
			})
		})
	})
}

func (u *UI) instancesPane(c *ui.Context) {
	pal := &u.pal
	u.card(c, "Saved instances", "server", func() {
		if len(u.App.Instances.List) == 0 {
			u.note(c, "No instances yet.")
		}
		for _, in := range u.App.Instances.List {
			active := u.App.Conn != nil && u.App.Conn.Instance.ID == in.ID
			ui.Row(c).Gap(12).Padding(12, 14).Radius(18).Background(pal.Surf2).AlignItems(ui.Center).Children(func() {
				Icon(c, "server", 18, pal.Text)
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					name := in.Name
					if active {
						name += " · open"
					}
					ui.Text(c, name).FontSize(15).FontWeight(700)
					ui.Text(c, in.Host()+" · "+in.AuthLabel()).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
				})
				if !active && ui.Button(c, "Open").Clicked() {
					u.switchInstance(in.ID)
				}
				if ui.Button(c, "Sign in again").Clicked() {
					u.reauth(in.ID)
				}
				if ui.Button(c, "Remove").Clicked() {
					if u.Native == nil || u.Native.Confirm("Remove “"+in.Name+"”?", "Its credentials are deleted from the keychain and its cached chats from this device.", "Remove") {
						u.App.Forget(in.ID)
					}
				}
			})
		}
		if ui.PrimaryButton(c, "Add instance…").Clicked() {
			u.Do(ActionAddInstance)
		}
	}).FillWidth()
	if u.App.Secrets != nil && !u.App.Secrets.Persistent() {
		u.note(c, "This system has no keychain (Secret Service), so credentials are kept in memory only.")
	}
}

func (u *UI) agentsPane(c *ui.Context) {
	pal := &u.pal
	conn := u.App.Conn
	if conn == nil {
		u.note(c, "Connect to an instance to see its agents.")
		return
	}
	u.card(c, "Agents on "+conn.Instance.Name, "agents", func() {
		for _, p := range conn.Profiles {
			ui.Row(c).Gap(12).Padding(12, 14).Radius(18).Background(pal.Surf2).AlignItems(ui.Center).Children(func() {
				st := avatarStyle(p, p.Name)
				h := float32(36)
				if st.Bot {
					h = 28
				}
				Avatar(c, st, 36, h, false, false)
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, p.Title()).FontSize(15).FontWeight(700)
					sub := p.Model
					if p.Description != "" {
						sub += " · " + p.Description
					}
					ui.Text(c, sub).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
				})
				pinned := u.Settings.AgentPinned(conn.Instance.ID, p.Name)
				on := pinned
				Toggle(c, pal, &on, "Pin "+p.Title())
				if on != pinned {
					s := u.Settings
					s.SetAgentPinned(conn.Instance.ID, p.Name, on)
					u.SetSettings(s)
				}
			})
		}
		u.note(c, "Pinned agents stay at the top of the sidebar. Agents are Hermes profiles; create them with `hermes profile`.")
	}).FillWidth()
}

func (u *UI) notificationsPane(c *ui.Context) {
	s := u.Settings
	set := func(fn func(*settings.Settings)) {
		n := u.Settings
		fn(&n)
		u.SetSettings(n)
	}
	u.cards(c, func() {
		u.card(c, "When an agent finishes", "bell", func() {
			u.toggleRow(c, "Notify me", &s.NotifyOnFinish, func(v bool) { set(func(n *settings.Settings) { n.NotifyOnFinish = v }) })
			u.note(c, "A system notification when a reply finishes while the window is in the background.")
		})
		u.card(c, "When an agent needs approval", "shield", func() {
			u.toggleRow(c, "Notify me", &s.NotifyOnApproval, func(v bool) { set(func(n *settings.Settings) { n.NotifyOnApproval = v }) })
			u.note(c, "A system notification when a tool call waits for you.")
		})
	})
}

func (u *UI) generalPane(c *ui.Context) {
	conn := u.App.Conn
	u.card(c, "About", "user", func() {
		ui.Text(c, "Hermes Desktop "+Version).FontSize(15).FontWeight(700)
		if conn != nil {
			u.note(c, "Connected to "+conn.Instance.Name+" ("+conn.Instance.Host()+") as "+conn.Me.DisplayName+".")
		}
		ui.Divider(c)
		s := u.Settings
		u.toggleRow(c, "Load link previews", &s.LinkPreviews, func(v bool) {
			n := u.Settings
			n.LinkPreviews = v
			u.SetSettings(n)
		})
		u.note(c, "Link cards in replies show the page's title, fetched once, without cookies.")
		u.note(c, "Keyboard: ⌘/Ctrl+1–9 jumps to the chats in the sidebar (hold the key to see their numbers), ⌘/Ctrl+N starts a chat, ⌘/Ctrl+K searches, ⌘/Ctrl+, opens settings and Esc stops a running agent.")
	}).FillWidth()
}

func (u *UI) infoPane(c *ui.Context) {
	text := map[string]string{
		"Permissions":    "Hermes asks before running risky commands. Approval cards appear in the chat; answer them there or from the notification. Your instance's approval rules (config.yaml → approvals) decide what needs asking.",
		"Terminal & SSH": "Every chat can be continued from a shell on the instance: right-click it and choose Copy resume command (`hermes --resume <id>`).",
		"Updates":        "Hermes Desktop " + Version + ". Updates are published as releases; this build checks for none on its own.",
	}[u.SettingsPane]
	u.card(c, u.SettingsPane, "", func() { u.note(c, text) }).FillWidth()
}

// marblePicker offers the icon colours as small marbles, drawn once off
// the UI thread (each is a few milliseconds of shading).
func (u *UI) marblePicker(c *ui.Context, set func(func(*settings.Settings))) {
	pal := &u.pal
	if u.marbles == nil && !u.marblesBusy && u.App.Post != nil {
		u.marblesBusy = true
		go func() {
			out := map[string]*ui.Bitmap{}
			for _, n := range appicon.Names {
				out[n] = ui.NewBitmap(appicon.Render(96, appicon.Options{Colour: n, Bare: true}))
			}
			u.App.Post(func() { u.marbles, u.marblesBusy = out, false })
		}()
	}
	ui.Grid(c).Columns(5).Gap(14).JustifyItems(ui.Center).Children(func() {
		for _, n := range appicon.Names {
			sel := u.Settings.AppIcon == n
			b := ui.Row(c).Size(52, 52).Radius(26).Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Focusable().Label(n + " icon").AccessSelected(sel)
			if sel {
				b.Shadow(0, 0, 0, 2.5, pal.Accent)
			}
			b.Children(func() {
				if bm := u.marbles[n]; bm != nil {
					ui.Image(c, bm).Size(48, 48)
				} else {
					ui.Box(c).Size(40, 40).Radius(20).Background(pal.Surf2)
				}
			})
			if b.Clicked() || b.Shortcut(0, ui.KeySpace) {
				set(func(s *settings.Settings) { s.AppIcon = n })
			}
		}
	})
}
