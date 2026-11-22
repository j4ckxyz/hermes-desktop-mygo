package views

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"hermes-desktop/internal/hermes"
)

// searchState is the Cmd/Ctrl+K palette: titles filter locally as you
// type; the instance's full-text search follows after a pause.
type searchState struct {
	open    bool
	query   string
	sel     int
	remote  []hermes.SearchResult
	remoteQ string // the query remote answers
	asked   string // the query last sent
	typed   time.Time
}

func (s *searchState) toggle() {
	s.open = !s.open
	if s.open {
		s.query, s.sel, s.remote, s.remoteQ, s.asked = "", 0, nil, "", ""
	}
}

type hit struct {
	id, title, sub, profile, source string
}

// results are the local matches, then the instance's, deduplicated.
func (u *UI) searchResults() []hit {
	conn := u.App.Conn
	if conn == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(u.search.query))
	var out []hit
	seen := map[string]bool{}
	for _, s := range conn.Sessions {
		if len(out) >= 12 {
			break
		}
		t := conn.Title(s)
		if q == "" || strings.Contains(strings.ToLower(t), q) || strings.HasPrefix(s.ID, q) {
			prof, _ := conn.Profile(s.Profile)
			out = append(out, hit{id: s.ID, title: t, sub: prof.Title(), profile: s.Profile, source: backgroundSource(s.Source)})
			seen[s.ID] = true
		}
	}
	if q != "" && u.search.remoteQ == u.search.query {
		for _, r := range u.search.remote {
			if seen[r.SessionID] || len(out) >= 20 {
				continue
			}
			seen[r.SessionID] = true
			title := r.Title
			if s, ok := conn.Session(r.SessionID); ok {
				title = conn.Title(s)
			}
			if title == "" {
				title = r.SessionID
			}
			out = append(out, hit{id: r.SessionID, title: title, sub: strings.TrimSpace(r.Snippet), profile: r.Profile, source: backgroundSource(r.Source)})
		}
	}
	return out
}

// backgroundSource is a background run's source, for its badge, or "".
func backgroundSource(source string) string {
	if hermes.IsBackgroundSource(source) {
		return source
	}
	return ""
}

func (u *UI) searchView(c *ui.Context) {
	pal := &u.pal
	conn := u.App.Conn
	hits := u.searchResults()
	u.search.sel = max(0, min(u.search.sel, len(hits)-1))
	// Ask the instance after a 250 ms pause in typing.
	if q := strings.TrimSpace(u.search.query); q != "" && conn != nil && u.search.asked != u.search.query {
		if wait := 250*time.Millisecond - c.Now().Sub(u.search.typed); wait > 0 {
			c.After(wait)
		} else {
			asked := u.search.query
			u.search.asked = asked
			conn.Search(q, func(res []hermes.SearchResult, err error) {
				if err == nil && u.search.query == asked {
					u.search.remote, u.search.remoteQ = res, asked
				}
			})
		}
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(620).MaxHeight(520).Radius(20).Background(pal.Surf).Border(1, pal.Line2).
			Shadow(0, 24, 64, 0, ui.RGBA(0, 0, 0, 0.45)).Clip().Children(func() {
			ui.Row(c).Gap(10).Padding(14, 18).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(pal.Line).Children(func() {
				Icon(c, "search", 18, pal.Muted)
				in := ui.TextInput(c, &u.search.query).Placeholder("Search chats").AutoFocus().Grow(1).FontSize(17).
					Background(ui.Transparent).Border(0, ui.Transparent).Label("Search chats")
				if in.Changed() {
					u.search.sel, u.search.typed = 0, c.Now()
				}
				in.OnTextKey(func(k ui.TextKey) bool {
					switch k.Key {
					case ui.KeyDown:
						u.search.sel = min(u.search.sel+1, len(hits)-1)
						return true
					case ui.KeyUp:
						u.search.sel = max(u.search.sel-1, 0)
						return true
					case ui.KeyEnter:
						if u.search.sel < len(hits) {
							u.search.open = false
							u.Select(hits[u.search.sel].id)
						}
						return true
					}
					return false
				})
			})
			ui.Scroll(c).Grow(1).Padding(6).Children(func() {
				if len(hits) == 0 {
					ui.Text(c, "No chats match.").TextColor(pal.Muted).Padding(16)
				}
				for i, h := range hits {
					row := ui.Row(c).Gap(10).Padding(10, 12).Radius(12).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(h.title)
					if i == u.search.sel {
						row.Background(pal.AccentSoft)
					} else if row.Hovered() {
						row.Background(pal.Surf2)
					}
					row.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Children(func() {
							ui.Text(c, h.title).FontSize(15).FontWeight(600).TextColor(pal.Text).SingleLine().Ellipsis("…")
							if h.sub != "" {
								ui.Text(c, h.sub).FontSize(12).TextColor(pal.Muted).SingleLine().Ellipsis("…")
							}
						})
						if h.source != "" {
							sourceBadge(c, pal, h.source)
						}
						if conn != nil && conn.Running[h.id] {
							Loader(c, u.Settings.Loader, pal, false)
						}
					})
					if row.Clicked() {
						u.search.open = false
						u.Select(h.id)
					}
				}
			})
		})
	}).Label("Search")
	if !open {
		u.search.open = false
	}
}
