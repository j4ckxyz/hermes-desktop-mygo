package views

import (
	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/core"
	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/settings"
)

// connectForm is the connect screen's state (design/Connect.dc.html).
type connectForm struct {
	local    bool
	password bool // dashboard login, not an API key
	address  string
	token    string
	username string
	pass     string
	busy     bool
	err      string
	// replace signs a saved instance in again.
	replace string
	name    string
	adding  bool
	reading bool
}

func (f *connectForm) reset(app *core.App) {
	*f = connectForm{local: false}
	if len(app.Instances.List) == 0 {
		f.local = false
	}
}

// reauth opens the connect screen to sign a saved instance in again.
func (u *UI) reauth(id string) {
	in, ok := u.App.Instances.Find(id)
	if !ok {
		return
	}
	if u.App.Conn != nil {
		u.App.Conn.Close()
	}
	u.Screen = ScreenConnect
	u.connect = connectForm{local: in.Local, password: in.Auth == settings.AuthPassword, address: in.URL, username: in.Username, replace: in.ID, name: in.Name}
}

// switchInstance opens another saved instance.
func (u *UI) switchInstance(id string) {
	in, ok := u.App.Instances.Find(id)
	if !ok {
		return
	}
	u.Selected, u.NewProfile = "", ""
	u.side = sidebar{}
	u.App.Open(in)
	u.Screen = ScreenMain
	s := u.Settings
	s.ActiveInstance = id
	u.SetSettings(s)
}

func (u *UI) connectView(c *ui.Context) {
	pal := &u.pal
	f := &u.connect
	root := ui.Column(c).Fill().Background(pal.Bg).Clip()
	root.Draw(func(p *ui.Painter, r ui.Rect) {
		// The rose glow behind the orb (760px, centred 40% down).
		d := float32(760)
		cx, cy := r.X+r.W/2, r.Y+r.H*0.4
		p.FillRadialGradient(ui.Rect{X: cx - d/2, Y: cy - d/2, W: d, H: d}, ui.RadialGradient{Stops: []ui.GradientStop{
			gs(ui.RGBA(185, 86, 111, 0.20), 0), gs(ui.RGBA(185, 86, 111, 0.06), 0.4), gs(ui.RGBA(185, 86, 111, 0), 0.68),
		}}, d/2)
	})
	root.Children(func() {
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			// The design's column starts 28 px down (the title-bar strip
			// overlaps it), which also leaves its glow room inside the scroll.
			ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(28, 24, 40, 24).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(480).AlignItems(ui.Center).Gap(22).Children(func() {
					orb := Avatar(c, AvatarStyle{Color: ui.Hex("#B9566F")}, 92, 92, u.Settings.AnimateCharacters, f.busy)
					orb.Shadow(0, 0, 60, 0, pal.Accent.Alpha(0.25)).Radius(46)
					ui.Column(c).Gap(8).AlignItems(ui.Center).Children(func() {
						title := "Connect to Hermes"
						if f.replace != "" {
							title = "Sign in to " + f.name
						}
						ui.Text(c, title).FontSize(30).FontWeight(800).LetterSpacing(-0.3).Role(ui.RoleHeading).Level(1)
						ui.Text(c, "Sign in to a Hermes agent running on this computer or on a server you own.").
							FontSize(15).LineHeight(1.5).TextColor(pal.Muted).TextAlign(ui.Center)
					})
					u.connectCard(c)
					u.savedInstances(c)
				})
			})
		})
		// A strip to drag the window by, over the top of the content.
		ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(56).DragWindow()
	})
}

func fieldLabel(c *ui.Context, u *UI, s string) {
	ui.Text(c, s).FontSize(11).FontWeight(700).LetterSpacing(1.5).TextColor(u.pal.Muted)
}

func (u *UI) field(c *ui.Context, value *string, label, placeholder string, secret bool) ui.Element {
	pal := &u.pal
	in := ui.TextInput(c, value).Height(50).Padding(0, 16).Radius(14).Border(1, pal.Line2).Background(pal.Surf2).
		FontSize(15).Placeholder(placeholder).Label(label).FillWidth()
	if secret {
		in.Password()
	}
	return in
}

func (u *UI) connectCard(c *ui.Context) {
	pal := &u.pal
	f := &u.connect
	ui.Column(c).FillWidth().Padding(22).Radius(28).Background(pal.Surf).Border(1, pal.Line).Gap(18).Children(func() {
		// Where Hermes runs.
		ui.Grid(c).Columns(2).Gap(6).Padding(5).Radius(16).Background(pal.Bg.Mix(pal.Surf, 0.3)).Role(ui.RoleGroup).Label("Where Hermes runs").Children(func() {
			for _, opt := range []struct {
				label string
				local bool
			}{{"This computer", true}, {"Remote server", false}} {
				on := f.local == opt.local
				b := ui.Row(c).Height(42).Radius(12).Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().
					Label(opt.label).AccessSelected(on)
				if on {
					b.Background(pal.Surf2).Border(1.5, pal.Accent)
				}
				b.Children(func() {
					col := pal.Muted
					if on {
						col = pal.Text
					}
					ui.Text(c, opt.label).FontSize(14).FontWeight(700).TextColor(col)
				})
				if b.Clicked() && !on {
					f.local, f.err = opt.local, ""
				}
			}
		})
		ui.Column(c).Gap(8).Children(func() {
			fieldLabel(c, u, "ADDRESS")
			ph := "https://hermes.your-tailnet.ts.net"
			if f.local {
				ph = "http://127.0.0.1:9119 (found automatically)"
			}
			in := u.field(c, &f.address, "Address", ph, false)
			in.Submitted()
		})
		// Sign-in method.
		ui.Row(c).Gap(8).Role(ui.RoleTabList).Label("Sign-in method").Children(func() {
			for _, opt := range []struct {
				label string
				pw    bool
			}{{"API key", false}, {"Dashboard login", true}} {
				on := f.password == opt.pw
				t := ui.Row(c).Height(38).Padding(0, 16).Radius(999).AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().
					Role(ui.RoleTab).AccessSelected(on).Label(opt.label)
				if on {
					t.Background(pal.Accent.Alpha(0.14)).Border(1, pal.Accent.Alpha(0.55))
				} else {
					t.Border(1, pal.Line2)
				}
				t.Children(func() {
					col := pal.Muted
					if on {
						col = pal.AccentText
					}
					ui.Text(c, opt.label).FontSize(14).FontWeight(700).TextColor(col)
				})
				if t.Clicked() {
					f.password, f.err = opt.pw, ""
				}
			}
		})
		submit := false
		if !f.password {
			ui.Column(c).Gap(8).Children(func() {
				fieldLabel(c, u, "API KEY")
				if u.field(c, &f.token, "API key", "Paste your API key", true).Submitted() {
					submit = true
				}
				ui.RichText(c,
					ui.Span{Text: "Copy it from the "},
					ui.Span{Text: ".env", Font: pal.Mono, Size: 12, Background: pal.Surf2, Radius: 6, PaddingX: 6, Color: pal.Text},
					ui.Span{Text: " file where Hermes runs, for example "},
					ui.Span{Text: "~/.hermes/.env", Font: pal.Mono, Size: 12, Background: pal.Surf2, Radius: 6, PaddingX: 6, Color: pal.Text},
					ui.Span{Text: "."},
				).FontSize(13).LineHeight(1.5).TextColor(pal.Muted)
				if f.local {
					label := "Read it from ~/.hermes/.env"
					if f.reading {
						label = "Reading…"
					}
					l := ui.Text(c, label).FontSize(13).FontWeight(700).TextColor(pal.AccentText).Cursor(ui.CursorPointer).Role(ui.RoleButton).Focusable()
					if l.Clicked() && !f.reading {
						f.reading = true
						post := u.App.Post
						go func() {
							tok, err := core.ReadLocalToken()
							post(func() {
								f.reading = false
								if err != nil {
									f.err = err.Error()
									return
								}
								f.token, f.err = tok, ""
							})
						}()
					}
				}
			})
		} else {
			ui.Column(c).Gap(12).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					fieldLabel(c, u, "USERNAME")
					u.field(c, &f.username, "Username", "", false).Autofill(ui.AutofillUsername)
				})
				ui.Column(c).Gap(8).Children(func() {
					fieldLabel(c, u, "PASSWORD")
					if u.field(c, &f.pass, "Password", "", true).Autofill(ui.AutofillPassword).Submitted() {
						submit = true
					}
				})
				ui.Text(c, "The same login you use for the Hermes web dashboard.").FontSize(13).TextColor(pal.Muted)
			})
		}
		if f.err != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				Icon(c, "warn", 16, ui.Hex("#EE736E"))
				ui.Text(c, f.err).FontSize(13).TextColor(pal.Text).Grow(1).Role(ui.RoleAlert)
			})
		}
		btn := ui.Row(c).Height(52).Radius(999).Background(pal.Accent).Justify(ui.Center).AlignItems(ui.Center).Gap(10).
			Cursor(ui.CursorPointer).Focusable().Role(ui.RoleButton).Label("Connect")
		if f.busy {
			btn.Opacity(0.8)
		}
		btn.Children(func() {
			if f.busy {
				GridLoader(c, "g3", pal.OnAccent, 3.5, 2, true)
				ui.Text(c, "Connecting…").FontSize(16).FontWeight(800).TextColor(pal.OnAccent)
			} else {
				ui.Text(c, "Connect").FontSize(16).FontWeight(800).TextColor(pal.OnAccent)
			}
		})
		if (btn.Clicked() || submit) && !f.busy {
			u.submitConnect()
		}
		note := "Credentials are kept in this device's keychain."
		if u.App.Secrets != nil && !u.App.Secrets.Persistent() {
			note = "This system has no keychain, so you will sign in again next time. Nothing is saved to disk."
		}
		ui.Text(c, note).FontSize(12).TextColor(pal.Muted).TextAlign(ui.Center).FillWidth()
	})
}

func (u *UI) submitConnect() {
	f := &u.connect
	f.busy, f.err = true, ""
	mode := settings.AuthToken
	if f.password {
		mode = settings.AuthPassword
	}
	req := core.ConnectRequest{Name: f.name, Address: f.address, Local: f.local, Mode: mode, Token: f.token, Username: f.username, Password: f.pass, ReplaceID: f.replace}
	if !f.local && f.address != "" {
		if _, err := hermes.NormalizeURL(f.address); err != nil {
			f.busy, f.err = false, err.Error()
			return
		}
	}
	u.App.Connect(req, func(inst settings.Instance, err error) {
		f.busy = false
		if err != nil {
			f.err = err.Error()
			return
		}
		f.token, f.pass = "", "" // the keychain has them now
		u.switchInstance(inst.ID)
	})
}

func (u *UI) savedInstances(c *ui.Context) {
	pal := &u.pal
	list := u.App.Instances.List
	if len(list) == 0 {
		return
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, "Saved instances").FontSize(13).TextColor(pal.Muted).PaddingStart(6)
		for _, in := range list {
			row := ui.Row(c).Gap(12).Padding(12, 14).Radius(18).Background(pal.Surf).Border(1, pal.Line).AlignItems(ui.Center).
				Cursor(ui.CursorPointer).Focusable().Label("Open " + in.Name)
			if row.Hovered() {
				row.Background(pal.Surf2)
			}
			row.Children(func() {
				ui.Row(c).Size(34, 34).Radius(10).Background(pal.Surf2).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
					Icon(c, "server", 16, pal.Text)
				})
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, in.Name).FontSize(15).FontWeight(700)
					ui.Text(c, in.Host()+" · "+in.AuthLabel()).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
				})
				Icon(c, "chevronRight", 16, pal.Muted)
			})
			if row.Clicked() {
				u.switchInstance(in.ID)
			}
			row.ContextMenu(func(m *ui.Menu) {
				if m.Item("Open").Chosen() {
					u.switchInstance(in.ID)
				}
				if m.Item("Sign in again…").Chosen() {
					u.reauth(in.ID)
				}
				if m.Item("Copy address").Chosen() {
					c.WriteClipboard(in.URL)
				}
				m.Separator()
				if m.Item("Remove…").Danger().Chosen() {
					if u.Native == nil || u.Native.Confirm("Remove “"+in.Name+"”?", "Its saved credentials are deleted from the keychain and its cached chats from this device. Nothing changes on the instance.", "Remove") {
						u.App.Forget(in.ID)
					}
				}
			})
		}
	})
}
