package hermes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/hermes/hermestest"
)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:9119":                     "http://127.0.0.1:9119",
		"localhost:9119/":                    "http://localhost:9119",
		"hermes-vps.your-tailnet.ts.net":     "https://hermes-vps.your-tailnet.ts.net",
		"https://h.example/hermes/?x=1#frag": "https://h.example/hermes",
		" http://[::1]:9120 ":                "http://[::1]:9120",
		"https://user:pw@h.example":          "https://h.example",
	}
	for in, want := range cases {
		u, err := hermes.NormalizeURL(in)
		if err != nil || u.String() != want {
			t.Errorf("NormalizeURL(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "://"} {
		if _, err := hermes.NormalizeURL(bad); err == nil {
			t.Errorf("NormalizeURL(%q) should fail", bad)
		}
	}
}

func TestTokenAuthREST(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Token: "secret-token"})
	defer srv.Close()
	c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "secret-token"})

	h, err := c.Health(ctx(t))
	if err != nil || !h.OK {
		t.Fatalf("health %+v %v", h, err)
	}
	st, err := c.Status(ctx(t))
	if err != nil || !st.AuthRequired || st.AuthProviders[0] != "session-token" {
		t.Fatalf("status %+v %v", st, err)
	}
	me, err := c.Me(ctx(t))
	if err != nil || me.DisplayName != "Jack G" {
		t.Fatalf("me %+v %v", me, err)
	}
	ps, err := c.Profiles(ctx(t))
	if err != nil || len(ps) != 3 || ps[0].Title() != "Hermes" || ps[0].Color() != "#B9566F" {
		t.Fatalf("profiles %+v %v", ps, err)
	}
	page, err := c.AllSessions(ctx(t), 500, 0)
	if err != nil || page.Total != 239+4+2 {
		t.Fatalf("all sessions total %d %v", page.Total, err)
	}
	if page.Sessions[0].ID != "20261122_080205_fb120f" || !page.Sessions[0].Pinned {
		t.Fatalf("most recent first: %+v", page.Sessions[0])
	}
	if page.Sessions[0].LastActive.IsZero() {
		t.Fatal("float timestamps must decode")
	}
	one, err := c.Sessions(ctx(t), hermes.SessionQuery{Profile: "helix", Limit: 10})
	if err != nil || one.Total != 2 {
		t.Fatalf("helix %+v %v", one, err)
	}
	msgs, err := c.Messages(ctx(t), "default", "20261122_080205_fb120f", 0)
	if err != nil || len(msgs) != 39 || msgs[1].ToolCalls[0].Function.Name != "terminal" {
		t.Fatalf("messages %d %v", len(msgs), err)
	}
	if _, err := c.Messages(ctx(t), "", "nope", 0); !errors.Is(err, hermes.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}

	bad, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "wrong"})
	_, err = bad.Profiles(ctx(t))
	var ae *hermes.APIError
	if !errors.Is(err, hermes.ErrUnauthorized) || !errors.As(err, &ae) || ae.Code != "unauthenticated" {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestFencedMutations(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Token: "t"})
	defer srv.Close()
	c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "t"})
	id := "20261122_060000_0a0b0c"
	title := "Greetings"
	pin := true
	if err := c.UpdateSession(ctx(t), "default", id, hermes.SessionPatch{Title: &title, Pinned: &pin}); err != nil {
		t.Fatal(err)
	}
	row := srv.SessionRow(id)
	if row["title"] != "Greetings" || row["pinned"] != true {
		t.Fatalf("row %+v", row)
	}
	// Another client moves the fence between our snapshot and write: the
	// client takes one new snapshot and succeeds.
	var once sync.Once
	wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			once.Do(func() { srv.BumpRevision(id) })
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer wrapped.Close()
	c2, _ := hermes.New(wrapped.URL, &hermes.TokenAuth{Token: "t"})
	title = "Again"
	if err := c2.UpdateSession(ctx(t), "default", id, hermes.SessionPatch{Title: &title}); err != nil {
		t.Fatalf("retry after revision_conflict: %v", err)
	}
	if srv.CallCount("GET /api/sessions/{id}/mutation-snapshot") != 3 {
		t.Fatalf("snapshots: %d", srv.CallCount("GET /api/sessions/{id}/mutation-snapshot"))
	}
	if err := c.DeleteSession(ctx(t), "default", id); err != nil {
		t.Fatal(err)
	}
	if srv.SessionRow(id) != nil {
		t.Fatal("not deleted")
	}
	if err := c.DeleteSession(ctx(t), "default", id); err != nil {
		t.Fatalf("deleting twice is fine: %v", err)
	}
}

func TestPasswordNativeFlowAndRefresh(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Username: "jack", Password: "pw"})
	defer srv.Close()
	var saved []hermes.Tokens
	auth := &hermes.PasswordAuth{Username: "jack", Password: "pw", OnTokens: func(tk hermes.Tokens) { saved = append(saved, tk) }}
	c, _ := hermes.New(srv.URL, auth)
	if err := auth.SignIn(ctx(t), c); err != nil {
		t.Fatal(err)
	}
	if auth.UsesCookies() || len(saved) != 1 || saved[0].AccessToken == "" || saved[0].RefreshToken == "" {
		t.Fatalf("expected bearer tokens, got cookies=%v saved=%+v", auth.UsesCookies(), saved)
	}
	if _, err := c.Profiles(ctx(t)); err != nil {
		t.Fatal(err)
	}
	// Access expires: one refresh, then the call succeeds.
	srv.ExpireAccess()
	if _, err := c.Profiles(ctx(t)); err != nil {
		t.Fatalf("after refresh: %v", err)
	}
	if srv.CallCount("POST /auth/native/refresh") != 1 || len(saved) != 2 {
		t.Fatalf("refreshes %d saved %d", srv.CallCount("POST /auth/native/refresh"), len(saved))
	}
	// Everything expires: refresh fails, the password signs in again.
	srv.ExpireAll()
	if _, err := c.Profiles(ctx(t)); err != nil {
		t.Fatalf("after re-login: %v", err)
	}
	if srv.CallCount("POST /auth/password-login") != 2 {
		t.Fatalf("logins %d", srv.CallCount("POST /auth/password-login"))
	}
}

func TestPasswordCookieFallbackAndBadPassword(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Username: "jack", Password: "pw", NoNative: true})
	defer srv.Close()
	auth := &hermes.PasswordAuth{Username: "jack", Password: "pw"}
	c, _ := hermes.New(srv.URL, auth)
	if err := auth.SignIn(ctx(t), c); err != nil {
		t.Fatal(err)
	}
	if !auth.UsesCookies() {
		t.Fatal("should fall back to the cookie session")
	}
	if _, err := c.Me(ctx(t)); err != nil {
		t.Fatalf("cookie session: %v", err)
	}
	// The socket rides the cookies too.
	conn, err := c.Dial(ctx(t), hermes.HandlerFuncs{}, hermes.DialOptions{})
	if err != nil {
		t.Fatalf("dial with cookies: %v", err)
	}
	conn.Close()

	bad := &hermes.PasswordAuth{Username: "jack", Password: "nope"}
	c2, _ := hermes.New(srv.URL, bad)
	if err := bad.SignIn(ctx(t), c2); !errors.Is(err, hermes.ErrBadCredentials) {
		t.Fatalf("bad password: %v", err)
	}
}

type recorder struct {
	mu     sync.Mutex
	events []hermes.Event
	reqs   []hermes.ServerRequest
	closed chan error
}

func newRecorder() *recorder { return &recorder{closed: make(chan error, 1)} }
func (r *recorder) OnEvent(e hermes.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}
func (r *recorder) OnRequest(q hermes.ServerRequest) {
	r.mu.Lock()
	r.reqs = append(r.reqs, q)
	r.mu.Unlock()
}
func (r *recorder) OnClose(err error) { r.closed <- err }
func (r *recorder) waitFor(t *testing.T, typ string) hermes.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, e := range r.events {
			if e.Type == typ {
				r.mu.Unlock()
				return e
			}
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s event", typ)
	return hermes.Event{}
}

func TestSocketBothDialects(t *testing.T) {
	for _, canonical := range []bool{true, false} {
		name := map[bool]string{true: "canonical", false: "legacy"}[canonical]
		t.Run(name, func(t *testing.T) {
			srv := hermestest.New(hermestest.Options{Token: "t", Canonical: canonical})
			defer srv.Close()
			c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "t"})
			rec := newRecorder()
			conn, err := c.Dial(ctx(t), rec, hermes.DialOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if conn.Canonical() != canonical {
				t.Fatalf("dialect: %v", conn.Canonical())
			}
			if srv.CallCount("client.capabilities") != 1 {
				t.Fatal("must announce server_requests")
			}
			if srv.CallCount("POST /api/auth/ws-ticket") != 1 {
				t.Fatal("must dial with a ticket, not the token")
			}
			snap, err := conn.CreateSession(ctx(t), "helix", "")
			if err != nil {
				t.Fatal(err)
			}
			stored := snap.Stored()
			if !canonical && snap.SessionID == stored {
				t.Fatal("legacy runtime id differs from the stored id")
			}
			if _, err := conn.Submit(ctx(t), "helix", stored, "hello", false); err != nil {
				t.Fatal(err)
			}
			done := rec.waitFor(t, "message.complete")
			if done.StoredID != stored {
				t.Fatalf("events must map to the stored id: %q vs %q", done.StoredID, stored)
			}
			if got := srv.Submitted[stored]; len(got) != 1 || got[0] != "hello" {
				t.Fatalf("submitted %v", got)
			}
		})
	}
}

func TestSocketInterruptAndApproval(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Token: "t", Canonical: true, Reply: "approval_turn.jsonl"})
	defer srv.Close()
	c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "t"})
	rec := newRecorder()
	conn, err := c.Dial(ctx(t), rec, hermes.DialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	snap, _ := conn.Resume(ctx(t), "default", "20261122_060000_0a0b0c")
	conn.Submit(ctx(t), "default", snap.Stored(), "clean up", false)
	ev := rec.waitFor(t, "approval.request")
	var p hermes.Prompt
	json.Unmarshal(ev.Payload, &p)
	if err := conn.AnswerPrompt(ctx(t), snap.Stored(), p, "deny"); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "approval.settled")
	if err := conn.Interrupt(ctx(t), snap.Stored(), ev.ExecutionGeneration); err != nil {
		t.Fatal(err)
	}
}

func TestSocketRefusesBadCredential(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Token: "t"})
	defer srv.Close()
	c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "wrong"})
	if _, err := c.Dial(ctx(t), hermes.HandlerFuncs{}, hermes.DialOptions{}); !errors.Is(err, hermes.ErrUnauthorized) {
		t.Fatalf("dial with a wrong token: %v", err)
	}
}

func TestSocketDropIsReported(t *testing.T) {
	srv := hermestest.New(hermestest.Options{Token: "t"})
	defer srv.Close()
	c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "t"})
	rec := newRecorder()
	conn, err := c.Dial(ctx(t), rec, hermes.DialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	srv.CloseSockets()
	select {
	case err := <-rec.closed:
		if err == nil {
			t.Fatal("a drop must carry an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnClose not called")
	}
	if err := conn.Call(ctx(t), "gateway.ping", nil, nil); err == nil {
		t.Fatal("calls on a dead socket must fail")
	}
}

func TestHeartbeatDetectsDeadSocket(t *testing.T) {
	// A server that accepts, says ready, and then never answers anything.
	srv := hermestest.New(hermestest.Options{Token: "t"})
	defer srv.Close()
	c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "t"})
	rec := newRecorder()
	conn, err := c.Dial(ctx(t), rec, hermes.DialOptions{HeartbeatEvery: 10 * time.Millisecond, DeadAfter: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(60 * time.Millisecond)
	if srv.CallCount("gateway.ping") < 2 {
		t.Fatalf("heartbeats: %d", srv.CallCount("gateway.ping"))
	}
}

func TestAPIErrorShapes(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
	}{
		{409, `{"detail":"revision_conflict"}`, "revision_conflict"},
		{401, `{"error":"session_expired","detail":"Unauthorized","reason":"invalid_or_expired_session"}`, "session_expired"},
		{401, `{"error":{"message":"Invalid gateway API key (API_SERVER_KEY)","type":"gateway_auth_error","code":"gateway_auth_failed"}}`, "gateway_auth_failed"},
		{429, `{"detail":"Too many login attempts. Try again shortly."}`, ""},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		c, _ := hermes.New(srv.URL, &hermes.TokenAuth{Token: "t"})
		_, err := c.Profiles(ctx(t))
		srv.Close()
		var ae *hermes.APIError
		if !errors.As(err, &ae) || ae.Code != tc.code || ae.Status != tc.status {
			t.Errorf("%s: got %#v", tc.body, err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Error("errors must not echo credentials")
		}
	}
}

// Hermes 0.23.3 sends compacted as 0/1 (seen live).
func TestMessageFlagsAcceptIntegers(t *testing.T) {
	var ms []hermes.Message
	if err := json.Unmarshal([]byte(`[{"id":1,"role":"user","content":"a","compacted":0,"timestamp":1795370100.5},{"id":2,"role":"user","content":"b","compacted":1},{"id":3,"role":"user","content":"c","compacted":true,"_compressed_summary":null}]`), &ms); err != nil {
		t.Fatal(err)
	}
	if ms[0].Compacted || !ms[1].Compacted || !ms[2].Compacted || ms[2].Summary {
		t.Fatalf("%+v", ms)
	}
}
