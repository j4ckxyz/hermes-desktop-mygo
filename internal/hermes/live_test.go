//go:build live

// The live end-to-end test runs against a real instance named in the
// gitignored .env.local (HERMES_URL, HERMES_API_KEY):
//
//	go test -tags live -run Live -v ./internal/hermes/
//
// It prints counts and event types only, never credentials or chat text.
// Raw frames are recorded to testdata-live/ (gitignored) for fixtures.
package hermes_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/transcript"
)

func liveEnv(t *testing.T) (string, string) {
	v := liveVals(t)
	return v["HERMES_URL"], v["HERMES_API_KEY"]
}

func liveVals(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", ".env.local"))
	if err != nil {
		t.Skip("no .env.local")
	}
	defer f.Close()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			vals[strings.TrimSpace(k)] = strings.TrimRight(v, "\r\n")
		}
	}
	if vals["HERMES_URL"] == "" {
		t.Skip("HERMES_URL missing")
	}
	return vals
}

type liveRec struct {
	mu     sync.Mutex
	events []hermes.Event
	reqs   []hermes.ServerRequest
	done   chan string
	sid    string
}

func (r *liveRec) OnEvent(e hermes.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	sid := r.sid
	r.mu.Unlock()
	if e.Type == "message.complete" && sid != "" && e.StoredID == sid {
		select {
		case r.done <- e.Type:
		default:
		}
	}
}
func (r *liveRec) OnRequest(q hermes.ServerRequest) {
	r.mu.Lock()
	r.reqs = append(r.reqs, q)
	r.mu.Unlock()
}
func (r *liveRec) OnClose(error) {}

func TestLiveEndToEnd(t *testing.T) {
	vals := liveVals(t)
	url := vals["HERMES_URL"]
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	var auth hermes.Auth = &hermes.TokenAuth{Token: vals["HERMES_API_KEY"]}
	var pa *hermes.PasswordAuth
	if vals["HERMES_USERNAME"] != "" {
		pa = &hermes.PasswordAuth{Username: vals["HERMES_USERNAME"], Password: vals["HERMES_PASSWORD"]}
		auth = pa
	}
	c, err := hermes.New(url, auth)
	if err != nil {
		t.Fatal(err)
	}
	if pa != nil {
		if err := pa.SignIn(ctx, c); err != nil {
			t.Fatalf("dashboard sign-in: %v", err)
		}
		tk := pa.Tokens()
		t.Logf("signed in: bearer tokens=%v (cookie session=%v), refresh token=%v", tk.AccessToken != "", pa.UsesCookies(), tk.RefreshToken != "")
	}

	h, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	t.Logf("health: ok=%v version=%s auth_required=%v", h.OK, h.Version, h.AuthRequired)
	st, err := c.Status(ctx)
	if err != nil {
		t.Logf("status: %v", err)
	} else {
		t.Logf("status: auth_required=%v providers=%v flows=%v", st.AuthRequired, st.AuthProviders, st.AuthFlows)
	}
	me, err := c.Me(ctx)
	if err != nil {
		t.Fatalf("me (the API key was refused?): %v", err)
	}
	t.Logf("me: provider=%s has_name=%v", me.Provider, me.DisplayName != "")

	ps, err := c.Profiles(ctx)
	if err != nil {
		t.Fatalf("profiles: %v", err)
	}
	t.Logf("profiles: %d (default first: %v)", len(ps), len(ps) > 0 && ps[0].IsDefault)

	page, err := c.AllSessions(ctx, 500, 0)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	t.Logf("sessions: %d of %d", len(page.Sessions), page.Total)
	if len(page.Sessions) > 0 {
		s := page.Sessions[0]
		msgs, err := c.Messages(ctx, s.Profile, s.ID, 100)
		if err != nil {
			t.Fatalf("messages: %v", err)
		}
		roles := map[string]int{}
		calls := 0
		for _, m := range msgs {
			roles[m.Role]++
			calls += len(m.ToolCalls)
		}
		turns := transcript.FromMessages(msgs)
		t.Logf("latest session: %d messages %v, %d tool calls, %d turns", len(msgs), roles, calls, len(turns))
	}

	// The socket: dial, create a chat, send, stream, render the tools.
	os.MkdirAll(filepath.Join("..", "..", "testdata-live"), 0o700)
	recPath := filepath.Join("..", "..", "testdata-live", "live-"+time.Now().Format("20060102-150405")+".jsonl")
	rf, _ := os.Create(recPath)
	defer rf.Close()
	var rmu sync.Mutex
	rec := &liveRec{done: make(chan string, 1)}
	conn, err := c.Dial(ctx, rec, hermes.DialOptions{Record: func(b []byte) {
		rmu.Lock()
		rf.Write(append(append([]byte(nil), b...), '\n'))
		rmu.Unlock()
	}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	t.Logf("socket: canonical=%v replay_epoch_set=%v", conn.Canonical(), conn.Ready().ReplayEpoch != "")

	profile := ""
	if len(ps) > 0 {
		profile = ps[0].Name
	}
	snap, err := conn.CreateSession(ctx, profile, "Hermes Desktop live test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored := snap.Stored()
	// Leave nothing behind on the instance.
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), time.Minute)
		defer dcancel()
		if err := c.DeleteSession(dctx, profile, stored); err != nil {
			t.Logf("could not delete the test chat: %v", err)
		}
	})
	rec.mu.Lock()
	rec.sid = stored
	rec.mu.Unlock()
	t.Logf("created session (runtime id differs: %v)", snap.SessionID != stored)

	adm, err := conn.Submit(ctx, profile, stored, "This is an automated test from the Hermes Desktop client. Please run the shell command `echo hermes-desktop-live-test` with your terminal tool, then reply with one short sentence saying what it printed.", false)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	t.Logf("submit: status=%s", adm.Status)

	var live transcript.Live
	live.AddUser("u", "test", time.Now())
	select {
	case <-rec.done:
	case <-time.After(5 * time.Minute):
		t.Error("no message.complete within 5 minutes")
	}
	time.Sleep(500 * time.Millisecond)
	rec.mu.Lock()
	types := map[string]int{}
	for _, e := range rec.events {
		if e.StoredID == stored {
			types[e.Type]++
			live.Apply(e, time.Now())
		}
	}
	reqs := len(rec.reqs)
	rec.mu.Unlock()
	keys := make([]string, 0, len(types))
	for k := range types {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + itoa(types[k]) + " ")
	}
	t.Logf("events for the chat: %s", b.String())
	t.Logf("server requests: %d", reqs)
	turn := live.Turns[0]
	t.Logf("turn: status=%s tools=%d reply_chars=%d reasoning_chars=%d", turn.Status, len(turn.Tools), len(turn.Text), len(turn.Reasoning))
	for _, tl := range turn.Tools {
		l := transcript.LabelOf(tl)
		t.Logf("  tool pill: %q status=%d result_chars=%d", l.Title, tl.Status, len(tl.Result))
	}
	if types["message.delta"] == 0 && types["message.complete"] == 0 {
		t.Error("no streamed reply")
	}
	// The stored transcript has the turn too.
	msgs, err := c.Messages(ctx, profile, stored, 0)
	if err != nil {
		t.Fatalf("messages of the new chat: %v", err)
	}
	stTurns := transcript.FromMessages(msgs)
	t.Logf("stored: %d messages, %d turns, tools in last turn: %d", len(msgs), len(stTurns), func() int {
		if len(stTurns) == 0 {
			return -1
		}
		return len(stTurns[len(stTurns)-1].Tools)
	}())
	raw, _ := json.Marshal(types)
	_ = raw
	t.Logf("recorded frames to %s", filepath.Base(recPath))
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
