package views

import (
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"hermes-desktop/internal/core"
	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/transcript"
)

// contentWidth is the transcript's column (max-width: 760px).
const contentWidth = 760

type chatKind int

const (
	cUser      chatKind = iota
	cWork               // "Worked for 3 min · 18 tools ⌄", or the running turn's status
	cWorkPanel          // what the work line opens: reasoning, commentary, the tool calls
	cText
	cLink
	cPrompt
	cError
)

type chatRow struct {
	kind  chatKind
	turn  *transcript.Turn
	text  string // a reply chunk, or a link's URL
	chunk int
	last  bool    // the reply's last chunk (streams while running)
	top   float32 // room above the row (the design's gaps)
}

func (r chatRow) key() any {
	return [3]any{r.turn.ID, int(r.kind), r.chunk}
}

// chatRows lays a chat's turns out as list rows: a long reply becomes a
// row per group of paragraphs, so the list never builds a whole long
// message for a sliver of it (MyGo issue #1471).
//
// Spacing follows the design: 18 between the user's message and the
// agent's reply and between turns (24 before a later user message), 10
// between the parts of a reply, 18 before "Thinking…" and cards.
func (u *UI) chatRows(ch *core.Chat) []chatRow {
	var rows []chatRow
	afterUser := true
	add := func(r chatRow) {
		switch {
		case len(rows) == 0:
			r.top = 0
		case r.kind == cUser:
			r.top = 24
		case r.kind == cWorkPanel:
			r.top = 8
		case r.kind == cPrompt || r.kind == cError || afterUser:
			r.top = 18
		default:
			r.top = 10
		}
		afterUser = r.kind == cUser
		rows = append(rows, r)
	}
	for _, t := range ch.Turns {
		afterUser = true // a reply's first part sits 18 below what came before
		if t.User != "" {
			add(chatRow{kind: cUser, turn: t})
		}
		// The agent's work (tool calls, commands, reasoning) stays behind
		// one line until it is opened, as the Hermes iOS app shows it.
		if hasWork(t) || t.Running() {
			add(chatRow{kind: cWork, turn: t})
			if hasWork(t) && u.openWork[t.ID] {
				add(chatRow{kind: cWorkPanel, turn: t})
			}
		}
		chunks := splitReply(t.Text)
		for i, ck := range chunks {
			kind := cText
			if isBareURL(ck) {
				kind = cLink
			}
			add(chatRow{kind: kind, turn: t, text: ck, chunk: i, last: i == len(chunks)-1})
		}
		if t.Prompt != nil {
			add(chatRow{kind: cPrompt, turn: t})
		}
		if t.Error != "" || t.Status == transcript.Interrupted {
			add(chatRow{kind: cError, turn: t})
		}
	}
	return rows
}

// splitReply splits Markdown at blank lines outside code fences into
// chunks of about 2,500 bytes, and gives each bare URL a chunk of its own.
func splitReply(md string) []string {
	md = strings.TrimSpace(md)
	if md == "" {
		return nil
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}
	fence := false
	paras := []string{}
	var para strings.Builder
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
		}
		if !fence && t == "" {
			paras = append(paras, para.String())
			para.Reset()
			continue
		}
		para.WriteString(line)
		para.WriteString("\n")
	}
	paras = append(paras, para.String())
	for _, p := range paras {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if isBareURL(p) {
			flush()
			chunks = append(chunks, strings.TrimSpace(p))
			continue
		}
		if cur.Len() > 0 && cur.Len()+len(p) > 2500 {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(p)
	}
	flush()
	return chunks
}

func isBareURL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, " \n\t") || !(strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Host != ""
}

func (u *UI) mainView(c *ui.Context) {
	conn := u.App.Conn
	pal := &u.pal
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		u.sidebarView(c, conn)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).FillHeight().Background(pal.Bg).Children(func() { // opaque beside a translucent sidebar
			if u.Selected == "" {
				u.newChatView(c, conn)
				return
			}
			ch := conn.Chats[u.Selected]
			if ch == nil {
				ch = conn.OpenChat(u.Selected)
			}
			u.chatView(c, conn, ch)
		})
	})
}

func (u *UI) chatView(c *ui.Context, conn *core.Connection, ch *core.Chat) {
	pal := &u.pal
	s, _ := conn.Session(ch.ID)
	prof, _ := conn.Profile(ch.Profile)
	u.chatHeader(c, conn, ch, s, prof)
	u.signedOutBanner(c, conn)

	rows := u.chatRows(ch)
	ls := u.listFor(ch.ID)
	ls.Key = func(i int) any { return rows[i].key() }
	ls.FollowEnd = true
	ls.FollowAnimation = &ui.Smooth
	list := ui.List(c, ls, len(rows), func(i int) {
		r := rows[i]
		ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(r.top, 0, 0, 0).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(contentWidth).Children(func() { u.chatRow(c, conn, ch, r) })
		})
	}).Grow(1).Padding(18, 24, 24, 24).Label("Conversation")
	switch {
	case !ch.Loaded && ch.Err == nil:
		list.Children(func() {
			ui.Row(c).Margin(ui.Auto).Gap(10).AlignItems(ui.Center).Children(func() {
				Loader(c, u.Settings.Loader, pal, true)
				ui.Text(c, "Loading the chat…").TextColor(pal.Muted)
			})
		})
	case ch.Err != nil && len(rows) == 0:
		list.Children(func() {
			ui.Column(c).Margin(ui.Auto).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Could not load this chat").FontSize(16).FontWeight(700)
				ui.Text(c, ch.Err.Error()).TextColor(pal.Muted).MaxWidth(460).TextAlign(ui.Center)
				if PillButton(c, pal, "Try again").Clicked() {
					ch.Err = nil
					conn.OpenChat(ch.ID)
				}
			})
		})
	case len(rows) == 0:
		list.Children(func() {
			ui.Text(c, "No messages yet.").TextColor(pal.Muted).Margin(ui.Auto)
		})
	}
	u.composer(c, conn, ch.ID, ch.Profile, &ch.Draft, ch.Running(), prof)
}

func (u *UI) chatHeader(c *ui.Context, conn *core.Connection, ch *core.Chat, s hermes.Session, prof hermes.Profile) {
	pal := &u.pal
	// The header is the window's title bar: it drags the window and keeps
	// clear of the system's window buttons (top right on Linux and Windows).
	bar := c.TitleBar()
	ui.Grid(c).ColumnTracks(ui.Fr(1), ui.FitContent(), ui.Fr(1)).AlignItems(ui.Center).Padding(14, max(20, bar.Right+12), 6, 20).DragWindow().Children(func() {
		ui.Box(c)
		pill := ui.Row(c).Gap(10).Padding(7, 18, 7, 8).Radius(999).Background(pal.Surf).Border(1, pal.Line).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.25)).AlignItems(ui.Center).MaxWidth(520).MinWidth(0)
		pill.Children(func() {
			st := avatarStyle(prof, ch.Profile)
			Avatar(c, st, 34, 34, u.Settings.AnimateCharacters, ch.Running() && u.Settings.AnimateCharacters)
			ui.Column(c).AlignItems(ui.Center).MinWidth(0).Shrink(1).Children(func() {
				title := s.DisplayTitle()
				if s.ID == "" {
					title = "New chat"
				}
				ui.Text(c, title).FontSize(15).FontWeight(700).SingleLine().Ellipsis("…")
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if cur := ch.Live.Current(); cur != nil {
						Loader(c, u.Settings.Loader, pal, false)
						ui.Text(c, activity(cur)+" · "+clock(c.Now().Sub(cur.Started))).FontSize(12).TextColor(pal.Muted).SingleLine()
						c.After(time.Second - time.Duration(c.Now().Nanosecond())) // tick the clock
					} else {
						name := prof.Title()
						if name == "" {
							name = ch.Profile
						}
						sub := name
						if !s.Activity().IsZero() {
							sub += " · " + relTime(c.Now(), s.Activity())
						}
						ui.Text(c, sub).FontSize(12).TextColor(pal.Muted).SingleLine()
					}
				})
			})
		})
		pill.ContextMenu(func(m *ui.Menu) { u.sessionMenu(c, m, conn, s) })
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if PillButton(c, pal, "Share").Tooltip("Export this chat as Markdown").Clicked() {
				u.export(ch.ID)
			}
			more := CircleButton(c, pal, "more", "More", 40)
			more.Menu(func(m *ui.Menu) {
				if ch.Running() {
					if m.Item("Stop Hermes").Shortcut(0, ui.KeyEscape).Chosen() {
						conn.Stop(ch.ID)
					}
					m.Separator()
				}
				u.sessionMenu(c, m, conn, s)
			})
		})
	})
}

// listFor returns a chat's list state, keeping the last 32 chats'.
func (u *UI) listFor(id string) *ui.ListState {
	if ls := u.lists[id]; ls != nil {
		return ls
	}
	if len(u.lists) >= 32 {
		for k := range u.lists {
			if k != u.Selected {
				delete(u.lists, k)
				break
			}
		}
	}
	ls := &ui.ListState{}
	u.lists[id] = ls
	return ls
}

// activity says what a running turn is doing, for the header.
func activity(t *transcript.Turn) string {
	if t.Prompt != nil {
		return "Waiting for you"
	}
	for i := len(t.Tools) - 1; i >= 0; i-- {
		tl := t.Tools[i]
		if tl.Status == transcript.ToolRunning || tl.Status == transcript.ToolGenerating {
			return transcript.LabelOf(tl).Title
		}
	}
	if t.Text != "" {
		return "Writing"
	}
	return "Thinking"
}

func (u *UI) chatRow(c *ui.Context, conn *core.Connection, ch *core.Chat, r chatRow) {
	pal := &u.pal
	t := r.turn
	switch r.kind {
	case cUser:
		ui.Column(c).FillWidth().AlignItems(ui.End).Gap(6).Children(func() {
			b := ui.Text(c, t.User).FontSize(pal.Scale(16)).LineHeight(1.45).Padding(13, 18).Radius(22).
				Background(pal.Surf2).MaxWidthPercent(78).Selectable()
			b.ContextMenu(func(m *ui.Menu) {
				if m.Item("Copy").Chosen() {
					c.WriteClipboard(t.User)
				}
				if m.Item("Copy as quote").Chosen() {
					c.WriteClipboard("> " + strings.ReplaceAll(t.User, "\n", "\n> "))
				}
				if !t.Running() && conn.Chats[ch.ID] != nil {
					if m.Item("Edit and resend").Chosen() {
						ch.Draft = t.User
					}
				}
			})
			if !t.UserTime.IsZero() {
				ui.Text(c, stamp(t.UserTime)).FontSize(12).TextColor(pal.Muted).PaddingEnd(6)
			}
		})

	case cWork:
		u.workLine(c, ch, t)

	case cWorkPanel:
		u.workPanel(c, ch, t)

	case cText:
		opts := u.mdOptions(c)
		box := ui.Column(c).FillWidth().Padding(4, 2)
		box.Children(func() {
			if t.Running() && r.last {
				markdown.Stream(c, r.text, opts)
			} else {
				markdown.View(c, r.text, opts)
			}
		})
		box.ContextMenu(func(m *ui.Menu) {
			if m.Item("Copy reply").Chosen() {
				c.WriteClipboard(t.Text)
			}
			if m.Item("Copy this part").Chosen() {
				c.WriteClipboard(r.text)
			}
			m.Separator()
			if m.Item("Export chat as Markdown…").Chosen() {
				u.export(ch.ID)
			}
		})

	case cLink:
		u.linkCard(c, r.text)

	case cPrompt:
		u.promptCard(c, conn, ch, t)

	case cError:
		msg := t.Error
		if msg == "" {
			msg = "Stopped."
		}
		ui.Row(c).Gap(8).PaddingStart(4).AlignItems(ui.Center).Children(func() {
			Icon(c, "warn", 16, ui.Hex("#EE736E"))
			ui.Text(c, msg).FontSize(14).TextColor(pal.Muted).Selectable()
		})
	}
}

func (u *UI) mdOptions(c *ui.Context) *markdown.Options {
	pal := &u.pal
	return &markdown.Options{
		Theme: &markdown.Theme{
			FontSize: pal.Scale(16), Font: pal.Font, CodeFont: pal.Mono, LineHeight: 1.6,
			Text: pal.Text, Muted: pal.Muted, Link: pal.AccentText, Strong: pal.Text,
			Code: pal.Text, CodeBackground: pal.Surf, CodeSize: 0.875,
			BlockSpacing: 0.625, ItemSpacing: 0.375, // 10 px and 6 px at 16 px
		},
		OnLink: func(dest string) { c.OpenURL(dest) },
		LinkMenu: func(m *ui.Menu, dest string) {
			if m.Item("Copy link").Chosen() {
				c.WriteClipboard(dest)
			}
		},
		WrapCode: false,
		Reveal:   &markdown.Reveal{Unit: markdown.ByWord},
	}
}

// toolPill draws a tool call (design: the green pills; the running one in
// the accent with a marching grid). Clicking shows its result.
func (u *UI) toolPill(c *ui.Context, tl *transcript.Tool) {
	pal := &u.pal
	l := transcript.LabelOf(tl)
	running := tl.Status == transcript.ToolRunning || tl.Status == transcript.ToolGenerating
	key := tl.ID + "|" + tl.Name
	open := u.openGroups["tool:"+key]
	ui.Column(c).Gap(6).Children(func() {
		p := ui.Row(c).Gap(10).Padding(11, 16).Radius(16).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Focusable().Label(l.Title + " " + l.Detail)
		iconCol := pal.OK
		switch {
		case running:
			p.Background(pal.AccentSoft).Border(1, pal.AccentLine)
			iconCol = pal.AccentText
		case tl.Status == transcript.ToolFailed:
			p.Background(pal.Pill).Border(1, ui.RGBA(238, 115, 110, 0.35))
			iconCol = ui.Hex("#EE736E")
		default:
			p.Background(pal.Pill).Border(1, pal.PillLine)
		}
		p.Children(func() {
			name := "wrench"
			if transcript.IsCommand(tl.Name) {
				name = "terminal"
			}
			Icon(c, name, 18, iconCol)
			ui.Text(c, l.Title).FontSize(14).FontWeight(700).Shrink(0).SingleLine()
			if l.Detail != "" {
				d := ui.Text(c, l.Detail).TextColor(pal.Muted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				if l.Mono {
					d.Font(pal.Mono).FontSize(13)
				} else {
					d.FontSize(14)
				}
				if running {
					d.Grow(1)
				}
			}
			if tl.Progress != "" {
				ui.Text(c, tl.Progress).FontSize(12).TextColor(pal.Muted).Shrink(0)
			}
			if running {
				ui.Spacer(c)
				Loader(c, u.Settings.Loader, pal, true)
			}
		})
		if p.Clicked() && !running {
			u.openGroups["tool:"+key] = !open
		}
		p.ContextMenu(func(m *ui.Menu) {
			what := "Copy arguments"
			if transcript.IsCommand(tl.Name) {
				what = "Copy command"
			}
			if m.Item(what).Disabled(l.Detail == "").Chosen() {
				if transcript.IsCommand(tl.Name) || len(tl.Args) == 0 {
					c.WriteClipboard(l.Detail)
				} else {
					c.WriteClipboard(string(tl.Args))
				}
			}
			if m.Item("Copy result").Disabled(tl.Result == "").Chosen() {
				c.WriteClipboard(tl.Result)
			}
			if m.Item("Copy tool name").Chosen() {
				c.WriteClipboard(tl.Name)
			}
		})
		if open && tl.Result != "" {
			res := tl.Result
			if len(res) > 20000 {
				res = res[:20000] + "\n…"
			}
			ui.ScrollHorizontal(c).FillWidth().MaxHeight(320).Radius(12).Background(pal.Surf).Border(1, pal.Line).Children(func() {
				ui.Text(c, res).Font(pal.Mono).FontSize(12.5).TextColor(pal.Text).Padding(10, 14).Selectable().NoWrap()
			})
		}
	})
}

func (u *UI) linkCard(c *ui.Context, raw string) {
	pal := &u.pal
	pu, _ := url.Parse(raw)
	title := u.App.LinkTitle(raw)
	if title == "" {
		title = linkTitle(pu)
	}
	card := ui.Column(c).FillWidth().Gap(4).Padding(14, 18).Radius(18).Background(pal.Surf).Border(1, pal.Line).
		Cursor(ui.CursorPointer).Focusable().Label("Open " + raw).Role(ui.RoleLink)
	if card.Hovered() {
		card.Background(pal.Surf2)
	}
	card.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			Icon(c, "globe", 13, pal.Muted)
			ui.Text(c, pu.Host).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
		ui.Text(c, title).FontSize(15).FontWeight(700).SingleLine().Ellipsis("…")
	})
	if card.Clicked() {
		c.OpenURL(raw)
	}
	card.ContextMenu(func(m *ui.Menu) {
		if m.Item("Open link").Chosen() {
			c.OpenURL(raw)
		}
		if m.Item("Copy link").Chosen() {
			c.WriteClipboard(raw)
		}
	})
}

// linkTitle names a link from its address ("…/cron" → "Cron").
func linkTitle(u *url.URL) string {
	if u == nil {
		return ""
	}
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	host := u.Host
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	if len(parts) == 0 {
		return transcript.Humanize(host)
	}
	last := parts[len(parts)-1]
	if i := strings.LastIndexByte(last, '.'); i > 0 {
		last = last[:i]
	}
	return transcript.Humanize(host) + " · " + transcript.Humanize(strings.ReplaceAll(last, "-", "_"))
}

// promptForm is what the user has typed or picked on a card so far.
type promptForm struct {
	value  string                     // sudo password, secret, or a free-text answer
	text   map[string]*string         // per question: free text
	picked map[string]map[string]bool // per question: chosen choices
}

func (u *UI) formFor(id string) *promptForm {
	if u.forms == nil {
		u.forms = map[string]*promptForm{}
	}
	f := u.forms[id]
	if f == nil {
		f = &promptForm{text: map[string]*string{}, picked: map[string]map[string]bool{}}
		u.forms[id] = f
	}
	return f
}

// dropForm forgets a card's form, wiping a password or secret first.
func (u *UI) dropForm(id string) {
	if f := u.forms[id]; f != nil {
		f.value = ""
		delete(u.forms, id)
	}
}

// promptCard shows what the agent is waiting on: an approval, a question
// (or a batch of them), a sudo password or a secret.
func (u *UI) promptCard(c *ui.Context, conn *core.Connection, ch *core.Chat, t *transcript.Turn) {
	pal := &u.pal
	p := t.Prompt
	f := u.formFor(p.PromptID)
	answer := func(v string) {
		conn.Answer(ch.ID, v)
		u.dropForm(p.PromptID)
	}
	title, iconName := "Hermes wants to run this", "shield"
	switch p.Kind {
	case "clarify", "questions":
		title, iconName = "Hermes has a question", "pulse"
		if len(p.Questions) > 1 {
			title = "Hermes has " + itoa(len(p.Questions)) + " questions"
		}
	case "sudo":
		title, iconName = "Hermes needs your sudo password", "key"
	case "secret":
		title, iconName = "Hermes needs "+p.EnvVar, "key"
	}
	ui.Column(c).FillWidth().Gap(12).Padding(16, 18).Radius(18).Background(pal.Surf).Border(1, pal.AccentLine).Role(ui.RoleGroup).Label(title).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			Icon(c, iconName, 18, pal.AccentText)
			ui.Text(c, title).FontSize(15).FontWeight(700)
		})
		if p.Question != "" && p.Kind != "questions" {
			ui.Text(c, p.Question).FontSize(15).Selectable()
		}
		if p.Description != "" {
			ui.Text(c, p.Description).FontSize(14).TextColor(pal.Muted).Selectable()
		}
		if p.Command != "" {
			ui.Text(c, p.Command).Font(pal.Mono).FontSize(13).Padding(10, 14).Radius(12).Background(pal.Surf2).FillWidth().Selectable()
		}
		switch p.Kind {
		case "sudo", "secret":
			ph, send := "Password", "Run with sudo"
			if p.Kind == "secret" {
				ph, send = p.EnvVar, "Send"
			}
			in := u.field(c, &f.value, ph, ph, true).AutoFocus()
			submitted := in.Submitted()
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, send).Disabled(f.value == "").Clicked() || (submitted && f.value != "") {
					answer(f.value)
				}
				if ui.Button(c, "Skip").Clicked() {
					answer("")
				}
			})
			note := "Sent to " + conn.Instance.Name + " for this command only. Hermes Desktop does not keep it."
			if p.Kind == "secret" {
				note = "Sent to " + conn.Instance.Name + ", which keeps it as " + p.EnvVar + ". Hermes Desktop does not keep it."
			}
			u.note(c, note)

		case "questions":
			for _, q := range p.Questions {
				u.questionField(c, f, q)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, "Send answers").Clicked() {
					answers := map[string]*string{}
					for _, q := range p.Questions {
						answers[q.QID] = f.answerFor(q)
					}
					conn.AnswerQuestions(ch.ID, answers)
					u.dropForm(p.PromptID)
				}
				if ui.Button(c, "Skip").Clicked() {
					conn.AnswerQuestions(ch.ID, nil)
					u.dropForm(p.PromptID)
				}
			})

		case "clarify":
			if len(p.Choices) == 0 {
				in := u.field(c, &f.value, "Answer", "Your answer", false).AutoFocus()
				submitted := in.Submitted()
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "Send").Disabled(f.value == "").Clicked() || (submitted && f.value != "") {
						answer(f.value)
					}
					if ui.Button(c, "Skip").Clicked() {
						answer("")
					}
				})
				return
			}
			fallthrough
		default:
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for i, choice := range p.Choices {
					var b ui.Element
					if choice == "once" || (p.Kind == "clarify" && i == 0) {
						b = ui.PrimaryButton(c, choiceLabel(choice))
					} else {
						b = ui.Button(c, choiceLabel(choice))
					}
					if b.Clicked() {
						answer(choice)
					}
				}
			})
		}
	})
}

// questionField is one question of a batch: choice chips or a text field.
func (u *UI) questionField(c *ui.Context, f *promptForm, q hermes.Question) {
	pal := &u.pal
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, q.Question).FontSize(15).FontWeight(600)
		if len(q.Choices) == 0 {
			if f.text[q.QID] == nil {
				f.text[q.QID] = new(string)
			}
			u.field(c, f.text[q.QID], q.Question, "Your answer", false)
			return
		}
		if f.picked[q.QID] == nil {
			f.picked[q.QID] = map[string]bool{}
		}
		picked := f.picked[q.QID]
		ui.Row(c).Gap(8).Wrap().Role(ui.RoleGroup).Label(q.Question).Children(func() {
			for _, choice := range q.Choices {
				on := picked[choice]
				chip := ui.Row(c).Height(36).Padding(0, 14).Radius(999).AlignItems(ui.Center).Cursor(ui.CursorPointer).
					Focusable().Label(choice).AccessSelected(on)
				if q.MultiSelect {
					chip.Role(ui.RoleCheckBox).Checked(on)
				} else {
					chip.Role(ui.RoleRadio).Checked(on)
				}
				if on {
					chip.Background(pal.Accent.Alpha(0.14)).Border(1, pal.Accent.Alpha(0.55))
				} else {
					chip.Border(1, pal.Line2)
				}
				chip.Children(func() {
					col := pal.Text
					if on {
						col = pal.AccentText
					}
					ui.Text(c, choice).FontSize(14).FontWeight(600).TextColor(col)
				})
				if chip.Clicked() || chip.Shortcut(0, ui.KeySpace) {
					if !q.MultiSelect {
						clear(picked)
					}
					picked[choice] = !on
				}
			}
		})
	})
}

// answerFor is a question's answer: the chosen choices (joined), or the
// text typed, or nil when it was left blank (skipped).
func (f *promptForm) answerFor(q hermes.Question) *string {
	var parts []string
	for _, choice := range q.Choices {
		if f.picked[q.QID][choice] {
			parts = append(parts, choice)
		}
	}
	if t := f.text[q.QID]; t != nil && strings.TrimSpace(*t) != "" {
		parts = append(parts, strings.TrimSpace(*t))
	}
	if len(parts) == 0 {
		return nil
	}
	a := strings.Join(parts, ", ")
	return &a
}

func choiceLabel(choice string) string {
	switch choice {
	case "once":
		return "Allow once"
	case "session":
		return "Allow for this chat"
	case "always":
		return "Always allow"
	case "deny":
		return "Deny"
	}
	return choice
}

// composer is the message box (design: the rounded form under the chat).
func (u *UI) composer(c *ui.Context, conn *core.Connection, id, profile string, draft *string, running bool, prof hermes.Profile) {
	pal := &u.pal
	name := prof.Title()
	if name == "" {
		name = "Hermes"
	}
	send := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		conn.Send(id, profile, text, func(newID string) {
			if u.Selected == "" {
				u.Selected = newID
			}
		})
		*draft = ""
	}
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, 24, 20, 24).Children(func() {
		form := ui.Row(c).FillWidth().MaxWidth(contentWidth).Gap(8).Padding(8).Radius(30).Background(pal.Surf).Border(1, pal.Line).AlignItems(ui.End)
		form.Children(func() {
			att := ui.Row(c).Size(44, 44).Radius(22).Border(1, pal.Line).Background(pal.Surf2).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).
				Label("Attach").Tooltip("Attachments are not supported yet").Opacity(0.6)
			att.Children(func() { Icon(c, "plus", 18, pal.Text) })
			placeholder := "Message " + name + "…"
			if running {
				placeholder = "Queue a message for " + name + "…"
			}
			in := ui.TextArea(c, draft).Lines(1, 8).MinHeight(0).Placeholder(placeholder).Grow(1).MinWidth(0).
				FontSize(pal.Scale(16)).Background(ui.Transparent).Border(0, ui.Transparent).Padding(11, 6).Label("Message " + name).AutoFocus()
			if u.focusComposer && !u.search.open {
				in.Focus()
				u.focusComposer = false
			}
			in.OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEscape && k.Mods == 0 && running {
					conn.Stop(id) // Esc stops the agent, even while typing
					return true
				}
				if k.Key == ui.KeyEnter && k.Mods == 0 {
					send(k.Text())
					k.SetText("")
					return true
				}
				return false
			})
			mic := ui.Row(c).Size(44, 44).Radius(22).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).
				Label("Dictate").Tooltip("Dictation is not available yet").Opacity(0.6)
			mic.Children(func() { Icon(c, "mic", 20, pal.Muted) })
			stop := running && strings.TrimSpace(*draft) == ""
			label := "Send"
			if stop {
				label = "Stop " + name
			}
			b := ui.Row(c).Size(44, 44).Radius(22).Background(pal.Accent).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).
				Cursor(ui.CursorPointer).Focusable().Label(label).Tooltip(label)
			if !stop && strings.TrimSpace(*draft) == "" {
				b.Opacity(0.55)
			}
			b.Children(func() {
				if stop {
					Icon(c, "stop", 16, pal.OnAccent)
				} else {
					Icon(c, "send", 18, pal.OnAccent)
				}
			})
			if b.Clicked() {
				if stop {
					conn.Stop(id)
				} else {
					send(*draft)
				}
			}
		})
	})
}

// newChatView is the empty chat: the agent, a greeting and the composer.
func (u *UI) newChatView(c *ui.Context, conn *core.Connection) {
	pal := &u.pal
	if u.NewProfile == "" && len(conn.Profiles) > 0 {
		u.NewProfile = conn.Profiles[0].Name
	}
	prof, _ := conn.Profile(u.NewProfile)
	name := prof.Title()
	if name == "" {
		name = "Hermes"
	}
	ui.Row(c).Height(64).DragWindow()
	u.signedOutBanner(c, conn)
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(14).Children(func() {
		st := avatarStyle(prof, u.NewProfile)
		h := float32(88)
		if st.Bot {
			h = 70
		}
		Avatar(c, st, 88, h, u.Settings.AnimateCharacters, false)
		ui.Text(c, "New chat with "+name).FontSize(26).FontWeight(800)
		if len(conn.Profiles) > 1 {
			b := PillButton(c, pal, "Change agent")
			b.Menu(func(m *ui.Menu) {
				for _, p := range conn.Profiles {
					if m.Item(p.Title()).Radio(p.Name == u.NewProfile).Subtitle(p.Model).ID("np-" + p.Name).Chosen() {
						u.NewProfile = p.Name
					}
				}
			})
		}
		if prof.Model != "" {
			ui.Text(c, prof.Model).FontSize(13).TextColor(pal.Muted)
		}
		if conn.Status != core.Connected {
			ui.Text(c, "Waiting for "+conn.Instance.Name+"…").FontSize(13).TextColor(pal.Muted)
		}
	})
	u.composer(c, conn, "", u.NewProfile, &u.draft, false, prof)
}

// hasWork reports whether a turn did anything besides replying.
func hasWork(t *transcript.Turn) bool {
	return len(t.Tools) > 0 || strings.TrimSpace(t.Reasoning) != "" || len(t.Interim) > 0
}

// workedFor reads a duration the way the iOS app does: "42 secs",
// "3 min, 36 secs", "1 hr, 5 min".
func workedFor(d time.Duration) string {
	unit := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return itoa(n) + " " + many
	}
	sec := int(d.Round(time.Second) / time.Second)
	switch {
	case sec < 60:
		return unit(max(sec, 1), "sec", "secs")
	case sec < 3600:
		if sec%60 == 0 {
			return unit(sec/60, "min", "min")
		}
		return unit(sec/60, "min", "min") + ", " + unit(sec%60, "sec", "secs")
	}
	return unit(sec/3600, "hr", "hr") + ", " + unit(sec%3600/60, "min", "min")
}

// workLabel is the work line's text: what the agent is doing while it
// runs, how long it worked once it is done.
func workLabel(t *transcript.Turn, now time.Time) string {
	tools := ""
	if n := len(t.Tools); n > 0 {
		tools = " · " + itoa(n) + map[bool]string{true: " tool", false: " tools"}[n == 1]
	}
	if t.Running() {
		label := activity(t)
		if label == "Thinking" && t.Thinking != "" && !strings.EqualFold(strings.Trim(t.Thinking, "…. "), "thinking") {
			label = strings.TrimRight(t.Thinking, "…. ")
		}
		return label + "… " + clock(now.Sub(t.Started)) + tools
	}
	d := t.Ended.Sub(t.Started)
	verb := "Worked"
	switch {
	case t.Status == transcript.Interrupted:
		verb = "Stopped"
	case len(t.Tools) == 0:
		verb = "Thought"
	}
	if t.Started.IsZero() || t.Ended.IsZero() || d <= 0 {
		if tools == "" {
			return verb
		}
		return verb + tools
	}
	if verb == "Stopped" {
		return "Stopped after " + workedFor(d) + tools
	}
	return verb + " for " + workedFor(d) + tools
}

// workLine is one quiet line per reply; clicking it shows the work.
func (u *UI) workLine(c *ui.Context, ch *core.Chat, t *transcript.Turn) {
	pal := &u.pal
	open := u.openWork[t.ID]
	label := workLabel(t, c.Now())
	row := ui.Row(c).FillWidth().Gap(8).Padding(4, 2, 12, 2).BorderWidth(0, 0, 1, 0).BorderColor(pal.Line).AlignItems(ui.Center).Label(label)
	if hasWork(t) {
		row.Cursor(ui.CursorPointer).Focusable().Expanded(open).Role(ui.RoleDisclosure)
	}
	row.Children(func() {
		if t.Running() {
			SphereLoader(c, "think", pal, 18, true)
			c.After(time.Second - time.Duration(c.Now().Nanosecond())) // tick the clock
		}
		ui.Text(c, label).FontSize(15).TextColor(pal.Muted).SingleLine().Ellipsis("…").Shrink(1)
		if hasWork(t) {
			ui.Icon(c, icon("chevronDown")).Size(16, 16).TextColor(pal.Muted).Shrink(0).
				Rotate(row.Animate("rot", map[bool]float32{true: 180, false: 0}[open], 150*time.Millisecond))
		}
	})
	if hasWork(t) && (row.Clicked() || row.Shortcut(0, ui.KeySpace)) {
		u.openWork[t.ID] = !open
		u.listFor(ch.ID).HoldPlace() // keep the line under the pointer
	}
}

// workPanel is what a work line opens: the reasoning, the commentary, and
// the tool calls (live while running, folded into one pill once done).
func (u *UI) workPanel(c *ui.Context, ch *core.Chat, t *transcript.Turn) {
	pal := &u.pal
	ui.Column(c).FillWidth().BorderWidth(0, 0, 0, 3).BorderColor(pal.Line2).Padding(2, 0, 2, 24).Gap(10).Children(func() {
		if r := strings.TrimSpace(t.Reasoning); r != "" {
			ui.Text(c, r).FontSize(14).LineHeight(1.5).TextColor(pal.Muted).Italic().Selectable()
		}
		for _, it := range t.Interim {
			ui.Text(c, it).FontSize(14).LineHeight(1.5).TextColor(pal.Muted).Selectable()
		}
		if len(t.Tools) == 0 {
			return
		}
		if t.Running() {
			for _, tl := range t.Tools {
				u.toolPill(c, tl)
			}
			return
		}
		key := "tools:" + t.ID
		open := u.openGroups[key]
		failed := false
		for _, tl := range t.Tools {
			failed = failed || tl.Status == transcript.ToolFailed
		}
		summary := t.Summary()
		g := ui.Row(c).FillWidth().Gap(12).Padding(12, 18).Radius(999).Background(pal.Pill).Border(1, pal.PillLine).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).Focusable().Expanded(open).Label(summary)
		g.Children(func() {
			if failed {
				Icon(c, "warn", 20, ui.Hex("#EE736E"))
			} else {
				Icon(c, "checkCircle", 20, pal.OK)
			}
			ui.Text(c, summary).FontSize(15).TextColor(pal.Muted).Grow(1).SingleLine().Ellipsis("…")
			ui.Icon(c, icon("chevronDown")).Size(16, 16).TextColor(pal.Muted).
				Rotate(g.Animate("rot", map[bool]float32{true: 180, false: 0}[open], 150*time.Millisecond))
		})
		if g.Clicked() || g.Shortcut(0, ui.KeySpace) {
			u.openGroups[key] = !open
			u.listFor(ch.ID).HoldPlace()
		}
		if open {
			ui.Column(c).MarginStart(18).BorderWidth(0, 0, 0, 1.5).BorderColor(pal.Line2).Padding(2, 0, 2, 14).Gap(8).Children(func() {
				for _, tl := range t.Tools {
					u.toolPill(c, tl)
				}
			})
		}
	})
}

// signedOutBanner asks to sign in again when the instance refused the
// saved credentials (an expired dashboard sign-in, a rotated API key).
func (u *UI) signedOutBanner(c *ui.Context, conn *core.Connection) {
	if conn.Status != core.SignedOut {
		return
	}
	pal := &u.pal
	msg := "Your sign-in to " + conn.Instance.Name + " has expired."
	if conn.Instance.Auth == settings.AuthToken {
		msg = conn.Instance.Name + " no longer accepts the saved API key."
	}
	// Under the header (the title bar), as a card in the transcript's column.
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(4, 24, 8, 24).Children(func() {
		ui.Row(c).FillWidth().MaxWidth(contentWidth).Gap(12).Padding(10, 16).Radius(16).Background(pal.AccentSoft).Border(1, pal.AccentLine).
			AlignItems(ui.Center).Role(ui.RoleAlert).Children(func() {
			Icon(c, "key", 18, pal.AccentText)
			ui.Text(c, msg).FontSize(14).Grow(1)
			if ui.PrimaryButton(c, "Sign in again").Clicked() {
				u.reauth(conn.Instance.ID)
			}
		})
	})
}
