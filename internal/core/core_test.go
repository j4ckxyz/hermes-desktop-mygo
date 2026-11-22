package core

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hermes-desktop/internal/cache"
	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/hermes/hermestest"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
	"hermes-desktop/internal/transcript"
)

// uiLoop stands in for the UI thread: posted functions queue up and the
// test runs them, so every state change happens on the test goroutine.
type uiLoop struct{ ch chan func() }

func newLoop() *uiLoop           { return &uiLoop{ch: make(chan func(), 4096)} }
func (l *uiLoop) post(fn func()) { l.ch <- fn }
func (l *uiLoop) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case fn := <-l.ch:
			fn()
		case <-time.After(10 * time.Millisecond): // state that changed off the loop (the fake server)
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

type env struct {
	srv   *hermestest.Server
	app   *App
	loop  *uiLoop
	inst  settings.Instance
	notes []string
	mu    sync.Mutex
}

func setup(t *testing.T, opt hermestest.Options, cachePath string) *env {
	t.Helper()
	if opt.Token == "" && opt.Username == "" {
		opt.Token = "tok"
	}
	e := &env{srv: hermestest.New(opt), loop: newLoop()}
	t.Cleanup(e.srv.Close)
	if cachePath == "" {
		cachePath = filepath.Join(t.TempDir(), "cache.db")
	}
	c, err := cache.Open(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	sec := secrets.NewWithBackend(&secrets.MemoryBackend{})
	e.inst = settings.Instance{ID: "i1", Name: "Main", URL: e.srv.URL, Auth: settings.AuthToken}
	if opt.Username != "" {
		e.inst.Auth = settings.AuthPassword
		sec.Set("i1", secrets.Credentials{Username: opt.Username, Password: opt.Password})
	} else {
		sec.Set("i1", secrets.Credentials{Token: opt.Token})
	}
	e.app = New(Deps{
		Post: e.loop.post, Cache: c, Secrets: sec,
		Notify: func(title, body string) { e.mu.Lock(); e.notes = append(e.notes, title+": "+body); e.mu.Unlock() },
	}, settings.Instances{List: []settings.Instance{e.inst}})
	t.Cleanup(func() {
		if e.app.Conn != nil {
			e.app.Conn.Close()
		}
	})
	return e
}

func TestOpenSyncsSidebar(t *testing.T) {
	e := setup(t, hermestest.Options{Canonical: true}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "sync", func() bool { return c.Synced && c.Status == Connected })
	if len(c.Profiles) != 3 || c.Profiles[0].Name != "default" {
		t.Fatalf("profiles %+v", c.Profiles)
	}
	if len(c.Sessions) != 245 {
		t.Fatalf("sessions %d", len(c.Sessions))
	}
	if c.Me.DisplayName != "Jack G" {
		t.Fatal("me")
	}
	if !c.Running["20261122_080205_fb120f"] {
		t.Fatal("is_active sessions show as running")
	}
}

func TestCachedLaunchShowsBeforeNetwork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	e := setup(t, hermestest.Options{}, path)
	e.app.Open(e.inst)
	e.loop.until(t, "first sync", func() bool { return e.app.Conn.Synced })
	c1 := e.app.Conn
	ch := c1.OpenChat("20261122_060000_0a0b0c")
	e.loop.until(t, "transcript", func() bool { return ch.Fetched })
	c1.Close()

	// Relaunch against a slow instance: the sidebar comes from the cache.
	e2 := setup(t, hermestest.Options{Latency: 2 * time.Second}, path)
	e2.inst.ID = "i1"
	e2.app.Open(e.inst)
	c := e2.app.Conn
	start := time.Now()
	e2.loop.until(t, "cached sidebar", func() bool { return c.Loaded })
	if c.Synced {
		t.Fatal("the network should not have answered yet")
	}
	if len(c.Sessions) != 245 || time.Since(start) > time.Second {
		t.Fatalf("cached sidebar: %d sessions after %v", len(c.Sessions), time.Since(start))
	}
	// Its transcripts were warmed too: opening it is instant.
	e2.loop.until(t, "warm transcript", func() bool {
		ch := c.Chats["20261122_060000_0a0b0c"]
		return ch != nil && ch.Loaded
	})
}

func TestChatStreamsAndFinishes(t *testing.T) {
	for _, canonical := range []bool{true, false} {
		t.Run(map[bool]string{true: "canonical", false: "legacy"}[canonical], func(t *testing.T) {
			e := setup(t, hermestest.Options{Canonical: canonical, Delay: time.Millisecond}, "")
			e.app.Open(e.inst)
			c := e.app.Conn
			e.loop.until(t, "connected", func() bool { return c.Synced && c.ws != nil })
			id := "20261122_060000_0a0b0c"
			ch := c.OpenChat(id)
			e.loop.until(t, "subscribed", func() bool { return ch.Fetched && ch.subscribed })
			if len(ch.Turns) != 1 || ch.Turns[0].Text != "Hey! What can I help with?" {
				t.Fatalf("stored turns %+v", ch.Turns)
			}
			c.Send(id, "default", "what is in my project?", nil)
			if !ch.Running() || len(ch.Turns) != 2 || ch.Turns[1].User != "what is in my project?" {
				t.Fatal("the user's bubble shows at once")
			}
			e.loop.until(t, "reply", func() bool {
				return !ch.Running() && len(ch.Turns) >= 2 && ch.Turns[len(ch.Turns)-1].Status == transcript.Complete
			})
			last := ch.Turns[len(ch.Turns)-1]
			if canonical && last.Text != "Your project has a README and src/." {
				t.Fatalf("reply %q", last.Text)
			}
			if !canonical && (last.Text != "hi" || len(last.Tools) != 2) {
				t.Fatalf("legacy reply %q tools %d", last.Text, len(last.Tools))
			}
			if c.Running[id] {
				t.Fatal("running mark must clear")
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			if len(e.notes) != 1 || !strings.Contains(e.notes[0], "finished") {
				t.Fatalf("notifications %v", e.notes)
			}
		})
	}
}

func TestNewChatAndStop(t *testing.T) {
	e := setup(t, hermestest.Options{Canonical: true, Reply: "design_turn.jsonl", Delay: time.Millisecond}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "connected", func() bool { return c.Synced && c.ws != nil })
	var created string
	c.Send("", "helix", "find my notes", func(id string) { created = id })
	e.loop.until(t, "created", func() bool { return created != "" })
	ch := c.Chats[created]
	if ch.Profile != "helix" || c.Sessions[0].ID != created {
		t.Fatal("the new chat leads the sidebar")
	}
	e.loop.until(t, "tools", func() bool { return len(ch.Turns) == 1 && len(ch.Turns[0].Tools) == 3 })
	if !c.Stop(created) {
		t.Fatal("stop")
	}
	e.loop.until(t, "interrupted", func() bool { return !ch.Running() })
	if ch.Turns[0].Status != transcript.Interrupted {
		t.Fatalf("status %s", ch.Turns[0].Status)
	}
	if e.srv.CallCount("session.interrupt") != 1 {
		t.Fatal("interrupt not sent")
	}
}

func TestApprovalCard(t *testing.T) {
	e := setup(t, hermestest.Options{Canonical: true, Reply: "approval_turn.jsonl"}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "connected", func() bool { return c.Synced && c.ws != nil })
	id := "20261122_060000_0a0b0c"
	ch := c.OpenChat(id)
	e.loop.until(t, "subscribed", func() bool { return ch.subscribed })
	c.Send(id, "default", "clean up", nil)
	e.loop.until(t, "card", func() bool { cur := ch.Live.Current(); return cur != nil && cur.Prompt != nil })
	c.Answer(id, "deny")
	if ch.Live.Current().Prompt != nil {
		t.Fatal("answering hides the card at once")
	}
	e.loop.until(t, "respond", func() bool { return e.srv.CallCount("approval.respond") == 1 })
}

func TestMutationsAreOptimistic(t *testing.T) {
	e := setup(t, hermestest.Options{}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "sync", func() bool { return c.Synced })
	id := "20261122_060000_0a0b0c"
	c.Rename(id, "Greetings")
	if s, _ := c.Session(id); s.Title != "Greetings" {
		t.Fatal("rename shows at once")
	}
	c.SetPinned(id, true)
	e.loop.until(t, "server", func() bool { r := e.srv.SessionRow(id); return r["title"] == "Greetings" && r["pinned"] == true })
	c.Delete(id)
	if _, ok := c.Session(id); ok {
		t.Fatal("delete shows at once")
	}
	e.loop.until(t, "server delete", func() bool { return e.srv.SessionRow(id) == nil })

	var md string
	c.Export("20261122_080205_fb120f", func(s string, err error) { md = s })
	e.loop.until(t, "export", func() bool { return md != "" })
	if !strings.HasPrefix(md, "# Shorten morning news and surf links") || !strings.Contains(md, "Ran 11 commands and made 7 tool calls") {
		t.Fatalf("export:\n%s", md[:min(400, len(md))])
	}
}

func TestBadCredentialsSignOut(t *testing.T) {
	e := setup(t, hermestest.Options{Token: "right"}, "")
	e.app.Secrets.Set("i1", secrets.Credentials{Token: "wrong"})
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "signed out", func() bool { return c.Status == SignedOut })
}

func TestPasswordInstance(t *testing.T) {
	e := setup(t, hermestest.Options{Username: "jack", Password: "pw"}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "connected", func() bool { return c.Synced && c.Status == Connected })
	cr, _ := e.app.Secrets.Get("i1")
	if cr.Tokens == nil || cr.Tokens.AccessToken == "" {
		t.Fatal("the tokens must be kept in the keychain")
	}
}

func TestReconnectAfterDrop(t *testing.T) {
	e := setup(t, hermestest.Options{}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "connected", func() bool { return c.ws != nil && c.Status == Connected })
	first := c.wsGen
	e.srv.CloseSockets()
	e.loop.until(t, "offline", func() bool { return c.Status == Offline })
	e.loop.until(t, "reconnected", func() bool { return c.Status == Connected && c.wsGen > first })
}

func TestEventBurstIsOneUpdate(t *testing.T) {
	e := setup(t, hermestest.Options{}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "sync", func() bool { return c.Synced })
	ch := c.chat("x", "default")
	ch.Live.AddUser("u", "hi", time.Now())
	before := len(e.loop.ch)
	for i := range 100 {
		c.OnEvent(hermes.Event{Type: "message.delta", StoredID: "x", Seq: int64(i + 1), Payload: []byte(`{"text":"a"}`)})
	}
	if got := len(e.loop.ch) - before; got != 1 {
		t.Fatalf("100 tokens queued %d UI updates, want 1", got)
	}
	e.loop.until(t, "drain", func() bool { return len(ch.Live.Current().Text) == 100 })
}

func TestConnectExplainsMissingSessionToken(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Username: "jack", Password: "pw"}) // no session-token provider
	defer srv.Close()
	loop := newLoop()
	app := New(Deps{Post: loop.post, Secrets: secrets.NewWithBackend(&secrets.MemoryBackend{})}, settings.Instances{})
	var gotErr error
	done := false
	app.Connect(ConnectRequest{Address: srv.URL + "/", Mode: settings.AuthToken, Token: "some-api-server-key"}, func(_ settings.Instance, err error) { gotErr, done = err, true })
	loop.until(t, "answer", func() bool { return done })
	if gotErr == nil || !strings.Contains(gotErr.Error(), "Dashboard login") {
		t.Fatalf("error: %v", gotErr)
	}
	// Dashboard login works on the same instance, trailing slash and all.
	done = false
	var inst settings.Instance
	app.Connect(ConnectRequest{Address: srv.URL + "/", Mode: settings.AuthPassword, Username: "jack", Password: "pw"}, func(in settings.Instance, err error) { inst, gotErr, done = in, err, true })
	loop.until(t, "answer", func() bool { return done })
	if gotErr != nil || inst.ID == "" || strings.HasSuffix(inst.URL, "/") {
		t.Fatalf("password connect: %+v %v", inst, gotErr)
	}
}

func TestSyncPagesThenStopsEarly(t *testing.T) {
	e := setup(t, hermestest.Options{}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "first sync", func() bool { return c.Synced })
	// 245 sessions: a first page of 200, then the remaining 45.
	if n := e.srv.CallCount("GET /api/profiles/sessions"); n != 2 {
		t.Fatalf("first sync pages: %d", n)
	}
	if len(c.Sessions) != 245 || c.Totals["default"] != 239 {
		t.Fatalf("sessions %d totals %v", len(c.Sessions), c.Totals)
	}
	// A second sync with a warm, recently reconciled cache stops at the
	// first page that holds nothing new.
	c.Synced = false
	c.Refresh()
	e.loop.until(t, "second sync", func() bool { return c.Synced })
	if n := e.srv.CallCount("GET /api/profiles/sessions"); n != 3 {
		t.Fatalf("incremental sync should read one page, read %d", n-2)
	}
}
