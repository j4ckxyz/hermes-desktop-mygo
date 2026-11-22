// Package core is the app's state and the work that keeps it current: the
// saved instances, the active connection (REST client, socket, sync),
// the agents and sessions of the sidebar, and each open chat's turns.
//
// Threading: every field is read and written on the UI thread only. All
// network, disk and keychain work runs on goroutines that hand their
// results back through Post (MyGo's Window.Update), so the UI thread
// never waits on I/O. Tests pass a Post that queues, and drain it.
package core

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hermes-desktop/internal/cache"
	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/transcript"
)

// Deps are the app's services.
type Deps struct {
	// Post runs fn on the UI thread and redraws.
	Post func(fn func())
	// Cache may be nil (it failed to open): the app still works, slower.
	Cache   *cache.Cache
	Secrets *secrets.Store
	// SaveInstances persists the instance list (off the UI thread).
	SaveInstances func(settings.Instances)
	// Now is the clock (tests fix it).
	Now func() time.Time
	// Notify shows an OS notification (nil: none).
	Notify func(Notice)
	// Dial options for the socket (tests shorten the timings).
	Dial hermes.DialOptions
	// FetchTitle reads a link's page title (nil: FetchTitle).
	FetchTitle func(ctx context.Context, url string) (string, error)
}

// NoticeKind is what a notification is about.
type NoticeKind int

const (
	NoticeFinished NoticeKind = iota // an agent finished a reply
	NoticeNeedsYou                   // an approval, a question, a password or a secret
)

// Notice is a notification about a chat; clicking it opens the chat.
type Notice struct {
	Kind        NoticeKind
	Title, Body string
	Instance    string // instance id
	Chat        string // stored session id
}

// ID is the notification's id: one per chat, so a newer one replaces an
// older, and the click knows what to open.
func (n Notice) ID() string { return "hermes-chat:" + n.Instance + ":" + n.Chat }

// ParseNoticeID reads a notification id back into its instance and chat.
func ParseNoticeID(id string) (instance, chat string, ok bool) {
	rest, ok := strings.CutPrefix(id, "hermes-chat:")
	if !ok {
		return "", "", false
	}
	instance, chat, ok = strings.Cut(rest, ":")
	return instance, chat, ok && instance != "" && chat != ""
}

// App is the whole app state.
type App struct {
	Deps
	Instances settings.Instances
	// Conn is the active instance's connection, nil on the connect screen.
	Conn *Connection
	// Errors shown as toasts; the view pops them.
	Toasts []string
	// LinkPreviews lets link cards fetch their page's title.
	LinkPreviews bool
	links        linkTitles
}

// New returns the app with its saved instances.
func New(d Deps, instances settings.Instances) *App {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &App{Deps: d, Instances: instances}
}

// Toast queues a message for the view.
func (a *App) Toast(msg string) { a.Toasts = append(a.Toasts, msg) }

// ConnStatus is where a connection is.
type ConnStatus int

const (
	Connecting ConnStatus = iota
	Connected
	Offline   // the instance is unreachable; retrying
	SignedOut // the credentials were refused
)

func (s ConnStatus) String() string {
	return [...]string{"connecting", "connected", "offline", "signed out"}[s]
}

// Connection is the open instance.
type Connection struct {
	app       *App
	Instance  settings.Instance
	Client    *hermes.Client
	Status    ConnStatus
	StatusErr error
	Me        hermes.Me

	Profiles []hermes.Profile
	Sessions []hermes.Session // the sessions known so far, most recent first
	// Totals is each agent's session count on the instance, which can be
	// more than Sessions holds while paging is under way.
	Totals map[string]int
	// Paging is true while older sessions are still being fetched.
	Paging bool
	// Running marks sessions the agent is working on (from events and
	// is_active), by stored id.
	Running map[string]bool
	// Rev changes whenever Profiles, Sessions or Running do, so views can
	// cache what they derive from them.
	Rev int
	// Loaded is true once the sidebar has data (cache or network).
	Loaded bool
	Synced bool // a network sync finished

	Chats map[string]*Chat // open chats by stored session id

	ctx     context.Context
	cancel  context.CancelFunc
	client  atomic.Pointer[hermes.Client] // for goroutines; Client is the UI's copy
	syncing atomic.Bool
	ws      *hermes.Conn
	wsGen   int

	evmu     sync.Mutex
	evq      []any // hermes.Event or hermes.ServerRequest
	evPosted bool
}

// Chat is one session's transcript and live state.
type Chat struct {
	ID      string
	Profile string
	stored  []*transcript.Turn
	Live    transcript.Live
	// Turns is stored + live, rebuilt by Refresh.
	Turns      []*transcript.Turn
	Loaded     bool // turns are showing (from memory, cache or network)
	Fetched    bool // a network read finished
	Loading    bool
	Err        error
	Snapshot   hermes.Snapshot
	subscribed bool
	// Draft is the composer's text for this chat.
	Draft string
	// Version bumps whenever Turns changes.
	Version int
}

func (ch *Chat) rebuild() {
	ch.Turns = transcript.Merge(ch.stored, &ch.Live)
	ch.Version++
}

// Running reports whether the agent is working in this chat.
func (ch *Chat) Running() bool { return ch.Live.Running() }

// Preload is an instance's cached sidebar, read before the window opened.
type Preload struct {
	Profiles []hermes.Profile
	Sessions []hermes.Session
}

// ReadPreload reads an instance's cached agents and sessions (disk I/O:
// call it off the UI thread, typically while the window system starts).
func ReadPreload(c *cache.Cache, instance string) *Preload {
	if c == nil {
		return nil
	}
	ctx := context.Background()
	ps, _ := c.Profiles(ctx, instance)
	ss, _ := c.Sessions(ctx, instance)
	if len(ps) == 0 && len(ss) == 0 {
		return nil
	}
	return &Preload{Profiles: ps, Sessions: ss}
}

// Open connects to inst: it shows the cached agents and sessions at once,
// then signs in, syncs and opens the socket in the background.
func (a *App) Open(inst settings.Instance) { a.OpenWith(inst, nil) }

// OpenWith is Open with the sidebar already read from the cache, so the
// first frame shows it.
func (a *App) OpenWith(inst settings.Instance, pre *Preload) {
	if a.Conn != nil {
		a.Conn.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Connection{app: a, Instance: inst, ctx: ctx, cancel: cancel, Running: map[string]bool{}, Chats: map[string]*Chat{}, Totals: map[string]int{}}
	a.Conn = c
	if pre != nil {
		c.setProfiles(pre.Profiles)
		c.setSessions(pre.Sessions)
		c.Loaded = true
	}
	go c.loadCache(pre == nil)
	go c.connect()
}

// Close drops the connection.
func (c *Connection) Close() {
	c.cancel()
	if c.ws != nil {
		c.ws.Close()
	}
	if c.app.Conn == c {
		c.app.Conn = nil
	}
}

func (c *Connection) post(fn func()) {
	c.app.Post(func() {
		if c.ctx.Err() != nil {
			return // a result for a connection that has been closed
		}
		fn()
	})
}

func (c *Connection) loadCache(lists bool) {
	if c.app.Cache == nil {
		return
	}
	ps, _ := c.app.Cache.Profiles(c.ctx, c.Instance.ID)
	ss, _ := c.app.Cache.Sessions(c.ctx, c.Instance.ID)
	if len(ps) == 0 && len(ss) == 0 {
		return
	}
	if lists {
		c.post(func() {
			if c.Synced {
				return // the network won the race
			}
			c.setProfiles(ps)
			c.setSessions(ss)
			c.Loaded = true
		})
	}
	// Warm the transcripts of the sessions the sidebar shows first, so
	// switching to them never waits on the disk.
	for _, id := range firstPerAgent(ss, 5) {
		msgs, _, ok, _ := c.app.Cache.Transcript(c.ctx, c.Instance.ID, id)
		if !ok {
			continue
		}
		turns := transcript.FromMessages(msgs)
		c.post(func() {
			ch := c.chat(id, "")
			if !ch.Fetched {
				ch.stored, ch.Loaded = turns, true
				ch.rebuild()
			}
		})
	}
}

// firstPerAgent returns the first n session ids of each agent.
func firstPerAgent(ss []hermes.Session, n int) []string {
	count := map[string]int{}
	var out []string
	for _, s := range ss {
		if count[s.Profile] < n {
			count[s.Profile]++
			out = append(out, s.ID)
		}
	}
	return out
}

// credentials builds the client's auth from the keychain.
func (c *Connection) credentials() (hermes.Auth, error) {
	cr, err := c.app.Secrets.Get(c.Instance.ID)
	if err != nil {
		return nil, err
	}
	if c.Instance.Auth == settings.AuthPassword {
		pa := &hermes.PasswordAuth{Username: cr.Username, Password: cr.Password}
		if cr.Tokens != nil {
			pa.SetTokens(*cr.Tokens)
		}
		id := c.Instance.ID
		store := c.app.Secrets
		pa.OnTokens = func(t hermes.Tokens) {
			cr.Tokens = &t
			store.Set(id, cr)
		}
		return pa, nil
	}
	return &hermes.TokenAuth{Token: cr.Token}, nil
}

func (c *Connection) connect() {
	auth, err := c.credentials()
	if err != nil {
		c.post(func() { c.Status, c.StatusErr = SignedOut, err })
		return
	}
	cl, err := hermes.New(c.Instance.URL, auth)
	if err != nil {
		c.post(func() { c.Status, c.StatusErr = SignedOut, err })
		return
	}
	c.client.Store(cl)
	c.post(func() { c.Client = cl })
	if pa, ok := auth.(*hermes.PasswordAuth); ok && pa.Tokens().AccessToken == "" {
		if err := pa.SignIn(c.ctx, cl); err != nil {
			c.failed(err)
			return
		}
	}
	go c.dialLoop(cl)
	c.sync()
}

func (c *Connection) failed(err error) {
	if c.ctx.Err() != nil {
		return
	}
	st := Offline
	if errors.Is(err, hermes.ErrUnauthorized) || errors.Is(err, hermes.ErrBadCredentials) {
		st = SignedOut
	}
	c.post(func() { c.Status, c.StatusErr = st, err })
}

// First and later page sizes of a session sync: a small first page shows
// the sidebar quickly (a 500-row page takes seconds on a big instance).
const (
	firstPage = 200
	nextPage  = 500
	// fullSyncEvery is how often a sync reads every session, which also
	// finds those deleted elsewhere; between, it stops at known rows.
	fullSyncEvery = 24 * time.Hour
)

// sync reads the agents and the sessions, newest first, page by page,
// showing each page as it comes and caching them.
func (c *Connection) sync() bool {
	if !c.syncing.CompareAndSwap(false, true) {
		return false // one sync at a time
	}
	defer c.syncing.Store(false)
	cl := c.client.Load()
	if cl == nil {
		return false
	}
	me, _ := cl.Me(c.ctx)
	ps, err := cl.Profiles(c.ctx)
	if err != nil {
		c.failed(err)
		return false
	}
	c.post(func() {
		c.Me = me
		c.setProfiles(ps)
	})
	// Each agent's total, for "N more sessions" before every page is in.
	// It runs beside the paging; the sync is done once both are.
	totalsDone := make(chan struct{})
	go func() {
		defer close(totalsDone)
		totals := map[string]int{}
		for _, p := range ps {
			if page, err := cl.Sessions(c.ctx, hermes.SessionQuery{Profile: p.Name, Limit: 1}); err == nil {
				totals[p.Name] = page.Total
			}
		}
		c.post(func() { c.Totals = totals; c.Rev++ })
	}()

	// What the cache knows decides whether this pass can stop early.
	known := map[string]float64{}
	full := true
	if c.app.Cache != nil {
		if cached, _ := c.app.Cache.Sessions(c.ctx, c.Instance.ID); len(cached) > 0 {
			for _, s := range cached {
				known[s.ID] = s.Activity().Sub(time.Unix(0, 0)).Seconds()
			}
			last, _ := time.Parse(time.RFC3339, c.app.Cache.Meta(c.ctx, "fullsync:"+c.Instance.ID))
			full = c.app.Now().Sub(last) > fullSyncEvery
		}
	}
	var all []hermes.Session
	complete := false
	for offset, limit := 0, firstPage; ; limit = nextPage {
		page, err := cl.AllSessions(c.ctx, limit, offset)
		if err != nil {
			c.failed(err)
			return false
		}
		all = append(all, page.Sessions...)
		offset += len(page.Sessions)
		done := len(page.Sessions) == 0 || offset >= page.Total
		// An incremental pass stops at a page that holds nothing new.
		if !full && !done {
			same := len(page.Sessions) > 0
			for _, s := range page.Sessions {
				if v, ok := known[s.ID]; !ok || v != s.Activity().Sub(time.Unix(0, 0)).Seconds() {
					same = false
					break
				}
			}
			done = same
		}
		if c.app.Cache != nil {
			c.app.Cache.PutSessions(c.ctx, c.Instance.ID, page.Sessions, false)
		}
		got := page.Sessions
		last := done
		c.post(func() {
			c.mergeSessions(got)
			c.Loaded, c.Paging = true, !last
			if c.ws != nil {
				c.Status = Connected
			}
		})
		if done {
			complete = full && offset >= page.Total
			break
		}
	}
	if c.app.Cache != nil {
		c.app.Cache.PutProfiles(c.ctx, c.Instance.ID, ps)
		if complete {
			// A full pass: drop what the instance no longer has.
			c.app.Cache.PutSessions(c.ctx, c.Instance.ID, all, true)
			c.app.Cache.SetMeta(c.ctx, "fullsync:"+c.Instance.ID, c.app.Now().Format(time.RFC3339))
		}
	}
	select {
	case <-totalsDone:
	case <-c.ctx.Done():
		return false
	}
	c.post(func() {
		if complete {
			c.setSessions(all)
		}
		c.Synced, c.Paging = true, false
	})
	return true
}

// mergeSessions adds or updates sessions, keeping the list sorted.
func (c *Connection) mergeSessions(ss []hermes.Session) {
	if len(ss) == 0 {
		return
	}
	idx := make(map[string]int, len(c.Sessions))
	for i, s := range c.Sessions {
		idx[s.ID] = i
	}
	merged := append([]hermes.Session(nil), c.Sessions...)
	for _, s := range ss {
		if i, ok := idx[s.ID]; ok {
			merged[i] = s
		} else {
			idx[s.ID] = len(merged)
			merged = append(merged, s)
		}
	}
	c.setSessions(merged)
}

// Refresh re-syncs the sidebar in the background.
func (c *Connection) Refresh() { go c.sync() }

func (c *Connection) setProfiles(ps []hermes.Profile) {
	// The default agent first, then the server's order.
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].IsDefault && !ps[j].IsDefault })
	c.Profiles = ps
	c.Rev++
}

func (c *Connection) setSessions(ss []hermes.Session) {
	c.Rev++
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].Activity().After(ss[j].Activity()) })
	c.Sessions = ss
	for _, s := range ss {
		if s.IsActive {
			c.Running[s.ID] = true
		} else if ch := c.Chats[s.ID]; ch == nil || !ch.Running() {
			delete(c.Running, s.ID)
		}
	}
}

// Session returns a session's row.
func (c *Connection) Session(id string) (hermes.Session, bool) {
	for _, s := range c.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return hermes.Session{}, false
}

// Profile returns an agent by name.
func (c *Connection) Profile(name string) (hermes.Profile, bool) {
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return hermes.Profile{}, false
}

func (c *Connection) chat(id, profile string) *Chat {
	ch := c.Chats[id]
	if ch == nil {
		ch = &Chat{ID: id, Profile: profile}
		c.Chats[id] = ch
	}
	if ch.Profile == "" {
		if s, ok := c.Session(id); ok {
			ch.Profile = s.Profile
		}
	}
	return ch
}

// OpenChat shows a session: at once from memory, else from the cache
// (off the UI thread), and fetches the latest transcript and attaches the
// socket in the background.
func (c *Connection) OpenChat(id string) *Chat {
	ch := c.chat(id, "")
	if !ch.Loading {
		ch.Loading = true
		go c.loadChat(ch.ID, ch.Profile, ch.Loaded)
	}
	return ch
}

func (c *Connection) loadChat(id, profile string, inMemory bool) {
	if !inMemory && c.app.Cache != nil {
		if msgs, _, ok, _ := c.app.Cache.Transcript(c.ctx, c.Instance.ID, id); ok {
			turns := transcript.FromMessages(msgs)
			c.post(func() {
				ch := c.chat(id, profile)
				if !ch.Fetched {
					ch.stored, ch.Loaded = turns, true
					ch.rebuild()
				}
			})
		}
	}
	cl := c.client.Load()
	if cl == nil {
		c.post(func() { c.chat(id, profile).Loading = false })
		return
	}
	readStarted := c.app.Now()
	msgs, err := cl.Messages(c.ctx, profile, id, 500)
	if err == nil && c.app.Cache != nil {
		c.app.Cache.PutTranscript(c.ctx, c.Instance.ID, id, msgs, c.app.Now())
	}
	turns := transcript.FromMessages(msgs)
	c.post(func() {
		ch := c.chat(id, profile)
		ch.Loading = false
		if err != nil {
			ch.Err = err
			return
		}
		ch.stored, ch.Loaded, ch.Fetched, ch.Err = turns, true, true, nil
		ch.Live.Prune(readStarted)
		ch.rebuild()
		c.subscribe(ch)
	})
}

// subscribe attaches the socket to a chat so its live events arrive.
func (c *Connection) subscribe(ch *Chat) {
	ws := c.ws
	if ws == nil || ch.subscribed {
		return
	}
	ch.subscribed = true
	id, profile := ch.ID, ch.Profile
	go func() {
		snap, err := ws.Resume(c.ctx, profile, id)
		if err != nil {
			c.post(func() { c.chat(id, profile).subscribed = false })
			return
		}
		var replay hermes.Replay
		if snap.Running {
			// A turn is already running: rebuild what it has streamed so far.
			replay, _ = ws.EventsSince(c.ctx, id, 0, snap.ReplayEpoch)
		}
		c.post(func() {
			ch := c.chat(id, profile)
			ch.Snapshot = snap
			if snap.ExecutionGeneration > 0 {
				ch.Live.Generation = snap.ExecutionGeneration
			}
			if snap.Running {
				c.Running[id] = true
				now := c.app.Now()
				if ch.Live.Current() == nil {
					ch.Live.Apply(hermes.Event{Type: "message.start", SessionID: snap.SessionID}, now)
				}
				if !replay.Truncated && !replay.SnapshotRequired {
					for _, ev := range replay.Events {
						ch.Live.Apply(ev, now)
					}
				}
				if snap.Inflight != nil && snap.Inflight.Assistant != "" && ch.Live.Current().Text == "" {
					ch.Live.Current().Text = snap.Inflight.Assistant
				}
			}
			for _, p := range snap.Prompts {
				ch.Live.SetPrompt(p, c.app.Now())
			}
			ch.rebuild()
		})
	}()
}

// ---- the socket ----

func (c *Connection) dialLoop(cl *hermes.Client) {
	backoff := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	for attempt := 0; c.ctx.Err() == nil; attempt++ {
		ws, err := cl.Dial(c.ctx, c, c.app.Dial)
		if err == nil {
			first := attempt == 0
			c.post(func() {
				c.ws = ws
				c.wsGen++
				c.Status, c.StatusErr = Connected, nil
				// Reattach the open chats; after a drop, refetch what was missed.
				for _, ch := range c.Chats {
					ch.subscribed = false
					if ch.Fetched {
						c.subscribe(ch)
					}
				}
				if !first {
					go c.sync()
				}
			})
			select {
			case <-ws.Done():
			case <-c.ctx.Done():
				return
			}
			if errors.Is(ws.Err(), hermes.ErrUnauthorized) {
				c.failed(ws.Err())
				return
			}
			attempt = 0
			c.post(func() { c.ws, c.Status, c.StatusErr = nil, Offline, ws.Err() })
		} else {
			if errors.Is(err, hermes.ErrUnauthorized) || errors.Is(err, hermes.ErrForbidden) {
				c.failed(err)
				return
			}
			c.failed(err)
		}
		d := backoff[min(attempt, len(backoff)-1)]
		select {
		case <-time.After(d):
		case <-c.ctx.Done():
			return
		}
	}
}

// OnEvent queues a socket event; the queue drains in one UI update, so a
// burst of tokens costs one frame.
func (c *Connection) OnEvent(ev hermes.Event) { c.enqueue(ev) }

// OnRequest queues a server request.
func (c *Connection) OnRequest(r hermes.ServerRequest) { c.enqueue(r) }

// OnClose is handled by dialLoop.
func (c *Connection) OnClose(error) {}

func (c *Connection) enqueue(v any) {
	c.evmu.Lock()
	c.evq = append(c.evq, v)
	schedule := !c.evPosted
	c.evPosted = true
	c.evmu.Unlock()
	if schedule {
		c.post(c.drain)
	}
}

func (c *Connection) drain() {
	c.evmu.Lock()
	q := c.evq
	c.evq, c.evPosted = nil, false
	c.evmu.Unlock()
	now := c.app.Now()
	touched := map[*Chat]bool{}
	for _, v := range q {
		switch x := v.(type) {
		case hermes.Event:
			c.handleEvent(x, now, touched)
		case hermes.ServerRequest:
			c.handleRequest(x, now, touched)
		}
	}
	for ch := range touched {
		ch.rebuild()
	}
}

func (c *Connection) handleEvent(ev hermes.Event, now time.Time, touched map[*Chat]bool) {
	switch ev.Type {
	case "sessions.changed":
		go c.sync()
		return
	case "session.title":
		var p struct {
			Title string `json:"title"`
		}
		ev.Decode(&p)
		c.patchSession(ev.StoredID, func(s *hermes.Session) { s.Title = p.Title })
		return
	}
	if ev.StoredID == "" {
		return
	}
	ch := c.chat(ev.StoredID, "")
	wasRunning := ch.Running()
	if ch.Live.Apply(ev, now) {
		touched[ch] = true
	}
	switch {
	case ch.Running():
		if !c.Running[ch.ID] {
			c.Rev++
		}
		c.Running[ch.ID] = true
		c.patchSession(ch.ID, func(s *hermes.Session) { s.LastActive = hermes.Time{Time: now}; s.IsActive = true })
	case wasRunning:
		delete(c.Running, ch.ID)
		c.Rev++
		c.patchSession(ch.ID, func(s *hermes.Session) { s.IsActive = false })
		if t := lastTurn(ch); t != nil && c.app.Notify != nil {
			title := "Hermes"
			if s, ok := c.Session(ch.ID); ok {
				title = s.DisplayTitle()
			}
			body := "The agent finished."
			if t.Status == transcript.Errored {
				body = "The agent stopped with an error."
			}
			c.app.Notify(Notice{Kind: NoticeFinished, Title: title, Body: body, Instance: c.Instance.ID, Chat: ch.ID})
		}
		// Persist the finished turn with the stored transcript.
		go c.loadChat(ch.ID, ch.Profile, true)
	}
	if cur := ch.Live.Current(); cur != nil && cur.Prompt != nil && (ev.Type == "approval.request" || ev.Type == "clarify.request") && c.app.Notify != nil {
		c.app.Notify(Notice{Kind: NoticeNeedsYou, Title: promptTitle(*cur.Prompt), Body: promptText(*cur.Prompt), Instance: c.Instance.ID, Chat: ch.ID})
	}
}

func lastTurn(ch *Chat) *transcript.Turn {
	if n := len(ch.Live.Turns); n > 0 {
		return ch.Live.Turns[n-1]
	}
	return nil
}

func promptTitle(p hermes.Prompt) string {
	switch p.Kind {
	case "sudo":
		return "Hermes needs your sudo password"
	case "secret":
		return "Hermes needs " + p.EnvVar
	case "questions", "clarify":
		return "Hermes has a question"
	}
	return "Hermes needs your approval"
}

func promptText(p hermes.Prompt) string {
	if len(p.Questions) > 0 {
		return p.Questions[0].Question
	}
	for _, s := range []string{p.Command, p.Question, p.Description} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return "A tool call is waiting for you."
}

func (c *Connection) handleRequest(r hermes.ServerRequest, now time.Time, touched map[*Chat]bool) {
	// Approvals (legacy wire), sudo passwords, secrets and clarify batches
	// become a card in the chat, as canonical approvals do.
	if p, ok := hermes.PromptFromRequest(r); ok && r.StoredID != "" {
		ch := c.chat(r.StoredID, "")
		ch.Live.SetPrompt(p, now)
		touched[ch] = true
		if c.app.Notify != nil {
			c.app.Notify(Notice{Kind: NoticeNeedsYou, Title: promptTitle(p), Body: promptText(p), Instance: c.Instance.ID, Chat: ch.ID})
		}
		return
	}
	// A desktop-only request (preview.*, vault.*, …) this app cannot show:
	// decline it so the agent is not left waiting.
	if ws := c.ws; ws != nil {
		go ws.Decline(r.ID)
	}
}

func (c *Connection) patchSession(id string, fn func(*hermes.Session)) {
	c.Rev++
	for i := range c.Sessions {
		if c.Sessions[i].ID == id {
			fn(&c.Sessions[i])
			return
		}
	}
}

// ---- actions ----

// Send sends text to a chat; with id "" it starts a new chat with the
// agent profile, and calls created with the new session's id.
func (c *Connection) Send(id, profile, text string, created func(id string)) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	ws := c.ws
	if ws == nil {
		c.app.Toast("Not connected to " + c.Instance.Name + " yet. Try again in a moment.")
		return
	}
	now := c.app.Now()
	var ch *Chat
	if id != "" {
		ch = c.chat(id, profile)
		ch.Live.AddUser("local-"+now.Format("150405.000"), text, now)
		ch.rebuild()
		c.Running[id] = true
	}
	// A message sent while the agent still works on the last one queues.
	queued := ch != nil && len(ch.Live.Turns) > 1 && ch.Live.Turns[len(ch.Live.Turns)-2].Running()
	go func() {
		stored := id
		if stored == "" {
			snap, err := ws.CreateSession(c.ctx, profile, "")
			if err != nil {
				c.post(func() { c.app.Toast("Could not start a chat: " + err.Error()) })
				return
			}
			stored = snap.Stored()
			c.post(func() {
				ch := c.chat(stored, profile)
				ch.Snapshot, ch.Loaded, ch.Fetched, ch.subscribed = snap, true, true, true
				ch.Live.AddUser("local-new", text, now)
				ch.rebuild()
				c.Running[stored] = true
				c.Rev++
				c.Sessions = append([]hermes.Session{{ID: stored, Profile: profile, StartedAt: hermes.Time{Time: now}, LastActive: hermes.Time{Time: now}, IsActive: true}}, c.Sessions...)
				if created != nil {
					created(stored)
				}
			})
		}
		// The legacy wire addresses a chat by the runtime id its resume
		// returned: attach first if the chat's own resume has not finished.
		if ws.RuntimeID(stored) == "" {
			if _, err := ws.Resume(c.ctx, profile, stored); err != nil {
				c.post(func() { c.app.Toast("Could not open the chat: " + err.Error()) })
			}
		}
		if _, err := ws.Submit(c.ctx, profile, stored, text, queued); err != nil {
			c.post(func() {
				ch := c.chat(stored, profile)
				if t := ch.Live.Current(); t != nil {
					t.Status, t.Error = transcript.Errored, "Not sent: "+err.Error()
					t.Version++
				}
				delete(c.Running, stored)
				ch.rebuild()
			})
		}
	}()
}

// Stop interrupts the agent's running turn in a chat.
func (c *Connection) Stop(id string) bool {
	ch := c.Chats[id]
	if ch == nil || !ch.Running() || c.ws == nil {
		return false
	}
	ws, gen := c.ws, ch.Live.Generation
	go func() {
		if err := ws.Interrupt(c.ctx, id, gen); err != nil {
			c.post(func() { c.app.Toast("Could not stop the agent: " + err.Error()) })
		}
	}()
	return true
}

// Answer answers the pending approval or clarify card of a chat.
func (c *Connection) Answer(id, answer string) {
	ch := c.Chats[id]
	if ch == nil || c.ws == nil {
		return
	}
	cur := ch.Live.Current()
	if cur == nil || cur.Prompt == nil {
		return
	}
	p := *cur.Prompt
	cur.Prompt = nil
	cur.Version++
	ch.rebuild()
	ws := c.ws
	go func() {
		if err := ws.AnswerPrompt(c.ctx, id, p, answer); err != nil {
			c.post(func() { c.app.Toast("Could not answer: " + err.Error()) })
		}
	}()
}

// AnswerQuestions answers a clarify batch (nil answers cancel it).
func (c *Connection) AnswerQuestions(id string, answers map[string]*string) {
	p, ws := c.takePrompt(id)
	if p == nil {
		return
	}
	go func() {
		if err := ws.AnswerQuestions(*p, answers); err != nil {
			c.post(func() { c.app.Toast("Could not answer: " + err.Error()) })
		}
	}()
}

// takePrompt removes a chat's card and returns it with the socket.
func (c *Connection) takePrompt(id string) (*hermes.Prompt, *hermes.Conn) {
	ch := c.Chats[id]
	if ch == nil || c.ws == nil {
		return nil, nil
	}
	cur := ch.Live.Current()
	if cur == nil || cur.Prompt == nil {
		return nil, nil
	}
	p := *cur.Prompt
	cur.Prompt = nil
	cur.Version++
	ch.rebuild()
	return &p, c.ws
}

// Rename renames a session.
func (c *Connection) Rename(id, title string) {
	s, _ := c.Session(id)
	old := s.Title
	c.patchSession(id, func(s *hermes.Session) { s.Title = title })
	c.mutate(id, s.Profile, hermes.SessionPatch{Title: &title}, func() {
		c.patchSession(id, func(s *hermes.Session) { s.Title = old })
	})
}

// SetPinned pins or unpins a session.
func (c *Connection) SetPinned(id string, pinned bool) {
	s, _ := c.Session(id)
	c.patchSession(id, func(s *hermes.Session) { s.Pinned = pinned })
	c.mutate(id, s.Profile, hermes.SessionPatch{Pinned: &pinned}, func() {
		c.patchSession(id, func(s *hermes.Session) { s.Pinned = !pinned })
	})
}

// mutate applies an edit optimistically and undoes it if the server refuses.
func (c *Connection) mutate(id, profile string, p hermes.SessionPatch, undo func()) {
	cl := c.Client
	if cl == nil {
		undo()
		return
	}
	go func() {
		err := cl.UpdateSession(c.ctx, profile, id, p)
		if err != nil {
			c.post(func() {
				undo()
				c.app.Toast("Could not save the change: " + err.Error())
			})
			return
		}
		if s, ok := c.sessionCopy(id); ok && c.app.Cache != nil {
			c.app.Cache.PutSessions(c.ctx, c.Instance.ID, []hermes.Session{s}, false)
		}
	}()
}

// sessionCopy reads a row from a background goroutine via the UI thread.
func (c *Connection) sessionCopy(id string) (hermes.Session, bool) {
	done := make(chan struct{})
	var s hermes.Session
	var ok bool
	c.post(func() { s, ok = c.Session(id); close(done) })
	select {
	case <-done:
	case <-c.ctx.Done():
	case <-time.After(5 * time.Second):
	}
	return s, ok
}

// Delete deletes a session.
func (c *Connection) Delete(id string) {
	s, ok := c.Session(id)
	if !ok {
		return
	}
	idx := -1
	for i := range c.Sessions {
		if c.Sessions[i].ID == id {
			idx = i
		}
	}
	c.Sessions = append(c.Sessions[:idx:idx], c.Sessions[idx+1:]...)
	c.Rev++
	delete(c.Chats, id)
	cl := c.Client
	go func() {
		var err error
		if cl != nil {
			err = cl.DeleteSession(c.ctx, s.Profile, id)
		}
		if err != nil {
			c.post(func() {
				c.Sessions = append(c.Sessions, s)
				c.setSessions(c.Sessions)
				c.app.Toast("Could not delete the chat: " + err.Error())
			})
			return
		}
		if c.app.Cache != nil {
			c.app.Cache.DeleteSession(c.ctx, c.Instance.ID, id)
		}
	}()
}

// Export returns a session as Markdown (off the UI thread: it may fetch).
func (c *Connection) Export(id string, done func(md string, err error)) {
	ch := c.Chats[id]
	s, _ := c.Session(id)
	var turns []*transcript.Turn
	if ch != nil && ch.Fetched {
		turns = ch.Turns
	}
	cl := c.Client
	go func() {
		if turns == nil {
			if cl == nil {
				c.post(func() { done("", errors.New("not connected")) })
				return
			}
			msgs, err := cl.Messages(c.ctx, s.Profile, id, 0)
			if err != nil {
				c.post(func() { done("", err) })
				return
			}
			turns = transcript.FromMessages(msgs)
		}
		md := transcript.Markdown(s.DisplayTitle(), turns)
		c.post(func() { done(md, nil) })
	}()
}

// Search asks the instance for sessions matching q (title, ID or text).
func (c *Connection) Search(q string, done func([]hermes.SearchResult, error)) {
	cl := c.client.Load()
	if cl == nil {
		done(nil, errors.New("not connected"))
		return
	}
	go func() {
		res, err := cl.Search(c.ctx, "", q, 30)
		c.post(func() { done(res, err) })
	}()
}
