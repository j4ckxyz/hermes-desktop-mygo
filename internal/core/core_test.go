package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
	srv     *hermestest.Server
	app     *App
	loop    *uiLoop
	inst    settings.Instance
	notes   []string
	notices []Notice
	mu      sync.Mutex
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
		Notify: func(n Notice) {
			e.mu.Lock()
			e.notes = append(e.notes, n.Title+": "+n.Body)
			e.notices = append(e.notices, n)
			e.mu.Unlock()
		},
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
	if cr.Password != "" {
		t.Fatal("once tokens are issued, the password must not be kept")
	}
	// The refresh token is refused (expired, revoked): no silent re-login
	// with a kept password; the instance asks to sign in again.
	e.srv.ExpireAll()
	// Synced shows before the first sync lets go of its slot; a refresh
	// meanwhile would be skipped (one sync at a time).
	e.loop.until(t, "the first sync to end", func() bool { return !c.syncing.Load() })
	c.Refresh()
	e.loop.until(t, "signed out", func() bool { return c.Status == SignedOut })
	if e.srv.CallCount("POST /auth/password-login") != 1 {
		t.Fatal("no password login after the first")
	}
}

func TestPasswordCookieFallbackKeepsPassword(t *testing.T) {
	e := setup(t, hermestest.Options{Username: "jack", Password: "pw", NoNative: true}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "connected", func() bool { return c.Synced && c.Status == Connected })
	cr, _ := e.app.Secrets.Get("i1")
	if cr.Tokens != nil || cr.Password != "pw" {
		t.Fatalf("a cookie session keeps the password (its cookies are not kept): %+v", cr.Tokens)
	}
}

func TestConnectStoresTokensNotPassword(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Username: "jack", Password: "pw"})
	defer srv.Close()
	loop := newLoop()
	app := New(Deps{Post: loop.post, Secrets: secrets.NewWithBackend(&secrets.MemoryBackend{})}, settings.Instances{})
	done := false
	var inst settings.Instance
	app.Connect(ConnectRequest{Address: srv.URL, Mode: settings.AuthPassword, Username: "jack", Password: "pw"}, func(in settings.Instance, err error) { inst, done = in, true })
	loop.until(t, "connected", func() bool { return done })
	cr, err := app.Secrets.Get(inst.ID)
	if err != nil || cr.Password != "" || cr.Tokens == nil || cr.Username != "jack" {
		t.Fatalf("stored %+v %v", cr, err)
	}
}

func TestOldPasswordEntryIsRewritten(t *testing.T) {
	e := setup(t, hermestest.Options{Username: "jack", Password: "pw"}, "")
	// An entry from before: password and tokens together.
	pa := &hermes.PasswordAuth{Username: "jack", Password: "pw"}
	cl, _ := hermes.New(e.srv.URL, pa)
	if err := pa.SignIn(context.Background(), cl); err != nil {
		t.Fatal(err)
	}
	tk := pa.Tokens()
	e.app.Secrets.Set("i1", secrets.Credentials{Username: "jack", Password: "pw", Tokens: &tk})
	e.app.Open(e.inst)
	e.loop.until(t, "connected", func() bool { return e.app.Conn.Synced })
	cr, _ := e.app.Secrets.Get("i1")
	if cr.Password != "" {
		t.Fatal("the old password must be dropped")
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

func TestLinkTitles(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/og" {
			w.Write([]byte(`<html><head><meta property="og:title" content="From OG"><title>Not this</title>`))
			return
		}
		w.Write([]byte("<html><head><title>\n  Hermes dashboard &middot; Cron jobs </title></head>"))
	}))
	defer page.Close()
	allow := []string{strings.TrimPrefix(page.URL, "http://")} // as if it were the instance
	loop := newLoop()
	app := New(Deps{Post: loop.post, FetchTitle: func(ctx context.Context, url string) (string, error) {
		return FetchTitleFrom(ctx, url, allow)
	}}, settings.Instances{})
	app.LinkPreviews = true
	if app.LinkTitle(page.URL+"/cron") != "" {
		t.Fatal("unknown at first")
	}
	loop.until(t, "title", func() bool { return app.LinkTitle(page.URL+"/cron") != "" })
	if got := app.LinkTitle(page.URL + "/cron"); got != "Hermes dashboard · Cron jobs" {
		t.Fatalf("title %q", got)
	}
	app.LinkTitle(page.URL + "/og")
	loop.until(t, "og", func() bool { return app.LinkTitle(page.URL+"/og") == "From OG" })
	app.LinkPreviews = false
	if app.LinkTitle("https://example.invalid/x") != "" || len(app.links.pending) != 0 {
		t.Fatal("previews off: no fetch")
	}
}

func TestLinkPreviewsStayOffPrivateAddresses(t *testing.T) {
	for addr, blocked := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.20.0.1": true, "192.168.1.10": true, "169.254.169.254": true,
		"100.100.100.100": true, "0.0.0.0": true, "224.0.0.1": true, "::1": true, "fe80::1": true, "fd7a:115c:a1e0::1": true,
		"::ffff:127.0.0.1": true, "8.8.8.8": false, "1.1.1.1": false, "2606:4700::1111": false, "100.63.255.255": false,
	} {
		if got := BlockedAddress(netip.MustParseAddr(addr)); got != blocked {
			t.Errorf("BlockedAddress(%s) = %v, want %v", addr, got, blocked)
		}
	}
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<title>secret router page</title>"))
	}))
	defer inner.Close()
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hop":
			http.Redirect(w, r, inner.URL+"/admin", http.StatusFound)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"title":"x"}`))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<title>Instance</title>"))
		}
	}))
	defer instance.Close()
	ctx := context.Background()
	allow := []string{strings.TrimPrefix(instance.URL, "http://")}
	if _, err := FetchTitleFrom(ctx, inner.URL, nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("a loopback page: %v", err)
	}
	if got, err := FetchTitleFrom(ctx, instance.URL, allow); err != nil || got != "Instance" {
		t.Fatalf("the instance's own page: %q %v", got, err)
	}
	if _, err := FetchTitleFrom(ctx, instance.URL+"/hop", allow); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("a redirect from the instance to another private address: %v", err)
	}
	if _, err := FetchTitleFrom(ctx, instance.URL+"/json", allow); err == nil {
		t.Fatal("only HTML pages")
	}
	if _, err := FetchTitleFrom(ctx, "file:///etc/passwd", nil); err == nil {
		t.Fatal("only web links")
	}
}

func TestServerRequestCards(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		t.Run(map[bool]string{true: "canonical", false: "legacy"}[canonical], func(t *testing.T) {
			e := setup(t, hermestest.Options{Canonical: canonical, Reply: "design_turn.jsonl", Delay: time.Millisecond}, "")
			e.app.Open(e.inst)
			c := e.app.Conn
			e.loop.until(t, "connected", func() bool { return c.Synced && c.ws != nil })
			id := "20261122_060000_0a0b0c"
			ch := c.OpenChat(id)
			e.loop.until(t, "subscribed", func() bool { return ch.Fetched && ch.subscribed })
			c.Send(id, "default", "install it", nil)
			e.loop.until(t, "running", func() bool { cur := ch.Live.Current(); return cur != nil && len(cur.Tools) == 3 })
			card := func() *hermes.Prompt {
				if cur := ch.Live.Current(); cur != nil {
					return cur.Prompt
				}
				return nil
			}

			// sudo: the password goes back as {value}.
			rid := e.srv.Ask(id, "sudo", map[string]any{"command": "apt install jq"})
			e.loop.until(t, "sudo card", func() bool { return card() != nil && card().Kind == "sudo" })
			if card().Command != "apt install jq" {
				t.Fatalf("%+v", card())
			}
			c.Answer(id, "hunter2")
			e.loop.until(t, "sudo reply", func() bool { return e.srv.Response(rid) != nil })
			if got := string(e.srv.Response(rid)); got != `{"value":"hunter2"}` {
				t.Fatalf("sudo reply %s", got)
			}

			// secret, skipped: {value:""}.
			rid = e.srv.Ask(id, "secret", map[string]any{"env_var": "NOTION_API_KEY", "prompt": "Paste your Notion key"})
			e.loop.until(t, "secret card", func() bool { return card() != nil && card().Kind == "secret" })
			if card().EnvVar != "NOTION_API_KEY" || card().Question != "Paste your Notion key" {
				t.Fatalf("%+v", card())
			}
			c.Answer(id, "")
			e.loop.until(t, "secret reply", func() bool { return e.srv.Response(rid) != nil })
			if got := string(e.srv.Response(rid)); got != `{"value":""}` {
				t.Fatalf("secret reply %s", got)
			}

			// A clarify batch: answers by question id, nil for one skipped.
			rid = e.srv.Ask(id, "clarify", map[string]any{"questions": []any{
				map[string]any{"qid": "q1", "question": "Which list?", "choices": []string{"Inbox", "School"}},
				map[string]any{"qid": "q2", "question": "Any notes?"},
			}})
			e.loop.until(t, "questions card", func() bool { return card() != nil && card().Kind == "questions" })
			if len(card().Questions) != 2 || card().Questions[0].Choices[1] != "School" {
				t.Fatalf("%+v", card())
			}
			school := "School"
			c.AnswerQuestions(id, map[string]*string{"q1": &school, "q2": nil})
			e.loop.until(t, "clarify reply", func() bool { return e.srv.Response(rid) != nil })
			if got := string(e.srv.Response(rid)); got != `{"answers":{"q1":"School","q2":null},"outcome":"submitted"}` {
				t.Fatalf("clarify reply %s", got)
			}

			// The server withdraws a request: its card goes.
			rid = e.srv.Ask(id, "sudo", map[string]any{"command": "reboot"})
			e.loop.until(t, "card", func() bool { return card() != nil && card().Command == "reboot" })
			e.srv.Withdraw(id, rid)
			e.loop.until(t, "withdrawn", func() bool { return card() == nil })

			// A desktop-only request is declined, not shown.
			rid = e.srv.Ask(id, "preview.open", map[string]any{"url": "x"})
			e.loop.until(t, "declined", func() bool { return e.srv.Response(rid) != nil })
			if card() != nil {
				t.Fatal("no card for desktop-only requests")
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			if len(e.notes) < 3 {
				t.Fatalf("each card notifies: %v", e.notes)
			}
		})
	}
}

func TestNoticesNameTheirChat(t *testing.T) {
	e := setup(t, hermestest.Options{Delay: time.Millisecond}, "")
	e.app.Open(e.inst)
	c := e.app.Conn
	e.loop.until(t, "connected", func() bool { return c.Synced && c.ws != nil })
	id := "20261122_060000_0a0b0c"
	ch := c.OpenChat(id)
	e.loop.until(t, "subscribed", func() bool { return ch.subscribed })
	c.Send(id, "default", "hi", nil)
	e.loop.until(t, "notice", func() bool { e.mu.Lock(); defer e.mu.Unlock(); return len(e.notices) > 0 })
	e.mu.Lock()
	n := e.notices[0]
	e.mu.Unlock()
	if n.Kind != NoticeFinished || n.Instance != "i1" || n.Chat != id {
		t.Fatalf("%+v", n)
	}
	inst, chat, ok := ParseNoticeID(n.ID())
	if !ok || inst != "i1" || chat != id {
		t.Fatalf("round trip %q", n.ID())
	}
	if _, _, ok := ParseNoticeID("mygo-abc"); ok {
		t.Fatal("foreign ids are not ours")
	}
}
