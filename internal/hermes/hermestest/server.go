// Package hermestest is an in-process fake of a Hermes instance: the
// dashboard REST routes, the native and cookie sign-in flows, and the
// /api/ws JSON-RPC socket in either dialect. Prompts are answered by
// replaying recorded event streams (testdata/*.jsonl). Tests, the
// benchmarks and the screenshot runs use it; it is never shipped.
package hermestest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

//go:embed testdata/*.jsonl
var fixtures embed.FS

// Fixture returns a recorded stream: one event params object per line.
func Fixture(name string) []json.RawMessage {
	data, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	var out []json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// A line is either a whole frame or its params.
		var f struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &f) == nil && f.Method == "event" {
			line = f.Params
		}
		out = append(out, append(json.RawMessage(nil), line...))
	}
	return out
}

// Options configure the fake.
type Options struct {
	Token     string // session token; "" disables token auth
	Username  string // basic auth; "" disables password auth
	Password  string
	Canonical bool // speak the canonical wire (a gateway-served listener)
	// NoNative makes the server refuse the native flow, so password
	// sign-in falls back to cookies.
	NoNative bool
	// Reply is the fixture a prompt replays (default canonical_turn.jsonl
	// or legacy_turn.jsonl), and Delay the pause between its events.
	Reply string
	Delay time.Duration
	// Latency is added to every REST answer.
	Latency time.Duration
	// Seed fills the agents and sessions; nil uses DesignSeed.
	Seed *Data
	// Addr is the address to listen on ("" is a loopback port).
	Addr string
}

// Data is the instance's content.
type Data struct {
	Profiles []map[string]any
	Sessions []map[string]any            // with a "profile" key
	Messages map[string][]map[string]any // by session id
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	opt Options

	mu       sync.Mutex
	data     Data
	tickets  map[string]bool
	codes    map[string]string // native code -> challenge
	pkce     map[string]string // pkce cookie -> challenge
	access   map[string]bool
	refresh  map[string]bool
	cookies  map[string]bool
	revision map[string]int64
	sockets  []*socket
	// Calls counts requests by "METHOD path" (REST) and method (RPC).
	Calls map[string]int
	// Submitted records the prompts sent, by session.
	Submitted map[string][]string
	// Responses records the client's answers to server requests, by id.
	Responses map[string]json.RawMessage
	nextID    atomic.Int64
}

// New starts a fake on a loopback port.
func New(opt Options) *Server {
	if opt.Reply == "" {
		opt.Reply = "legacy_turn.jsonl"
		if opt.Canonical {
			opt.Reply = "canonical_turn.jsonl"
		}
	}
	s := &Server{
		opt: opt, tickets: map[string]bool{}, codes: map[string]string{}, pkce: map[string]string{},
		access: map[string]bool{}, refresh: map[string]bool{}, cookies: map[string]bool{}, revision: map[string]int64{},
		Calls: map[string]int{}, Submitted: map[string][]string{}, Responses: map[string]json.RawMessage{},
	}
	if opt.Seed != nil {
		s.data = *opt.Seed
	} else {
		s.data = DesignSeed(time.Now())
	}
	if opt.Addr == "" {
		s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
		return s
	}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	l, err := net.Listen("tcp", opt.Addr)
	if err != nil {
		panic(err)
	}
	s.Server.Listener.Close()
	s.Server.Listener = l
	s.Server.Start()
	return s
}

func (s *Server) count(k string) {
	s.mu.Lock()
	s.Calls[k]++
	s.mu.Unlock()
}

// CallCount returns how many times k was called.
func (s *Server) CallCount(k string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Calls[k]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) authed(r *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opt.Token != "" && r.Header.Get("X-Hermes-Session-Token") == s.opt.Token {
		return true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimPrefix(h, "Bearer ")
		return s.access[tok] || (s.opt.Token != "" && tok == s.opt.Token)
	}
	if c, err := r.Cookie("hermes_session_at"); err == nil && s.cookies[c.Value] {
		return true
	}
	return false
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if s.opt.Latency > 0 {
		time.Sleep(s.opt.Latency)
	}
	path := r.URL.Path
	s.count(r.Method + " " + routeKey(path))
	if r.Header.Get("Origin") != "" {
		http.Error(w, "unexpected Origin", http.StatusForbidden) // the client must send none
		return
	}
	switch {
	case path == "/api/ws":
		s.serveWS(w, r)
		return
	case path == "/api/health":
		writeJSON(w, 200, map[string]any{"ok": true, "version": "0.30.0", "displayVersion": "0.30.0", "commit": "fake", "auth_required": true})
		return
	case path == "/api/status":
		prov := []string{}
		if s.opt.Token != "" {
			prov = append(prov, "session-token")
		}
		if s.opt.Username != "" {
			prov = append(prov, "basic")
		}
		writeJSON(w, 200, map[string]any{"version": "0.30.0", "auth_required": true, "auth_providers": prov, "auth_flows": []string{"cookie", "native_pkce"}, "overall": "ok"})
		return
	case path == "/api/auth/providers":
		ps := []any{}
		if s.opt.Username != "" {
			ps = append(ps, map[string]any{"name": "basic", "display_name": "Username & Password", "supports_password": true})
		}
		writeJSON(w, 200, map[string]any{"providers": ps})
		return
	case path == "/auth/native/authorize":
		s.nativeAuthorize(w, r)
		return
	case path == "/auth/password-login":
		s.passwordLogin(w, r)
		return
	case path == "/auth/native/token":
		s.nativeToken(w, r)
		return
	case path == "/auth/native/refresh":
		s.nativeRefresh(w, r)
		return
	}
	if !s.authed(r) {
		writeJSON(w, 401, map[string]any{"error": "unauthenticated", "detail": "Unauthorized", "reason": "no_cookie", "login_url": "/login"})
		return
	}
	switch {
	case path == "/api/auth/me":
		writeJSON(w, 200, map[string]any{"user_id": "u1", "display_name": "Jack G", "provider": "basic"})
	case path == "/api/auth/ws-ticket" && r.Method == http.MethodPost:
		t := fmt.Sprintf("tk%d", s.nextID.Add(1))
		s.mu.Lock()
		s.tickets[t] = true
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ticket": t, "ttl_seconds": 30})
	case path == "/api/profiles":
		s.mu.Lock()
		writeJSON(w, 200, map[string]any{"profiles": s.data.Profiles})
		s.mu.Unlock()
	case path == "/api/profiles/sessions" || path == "/api/sessions":
		s.listSessions(w, r)
	case path == "/api/sessions/search":
		s.search(w, r)
	case strings.HasPrefix(path, "/api/sessions/"):
		s.session(w, r, strings.TrimPrefix(path, "/api/sessions/"))
	default:
		writeJSON(w, 404, map[string]any{"detail": "Not Found"})
	}
}

func routeKey(p string) string {
	if strings.HasPrefix(p, "/api/sessions/") && p != "/api/sessions/search" {
		parts := strings.Split(strings.TrimPrefix(p, "/api/sessions/"), "/")
		parts[0] = "{id}"
		return "/api/sessions/" + strings.Join(parts, "/")
	}
	return p
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit == 0 {
		limit = 20
	}
	profile := q.Get("profile")
	s.mu.Lock()
	var rows []map[string]any
	for _, row := range s.data.Sessions {
		if profile != "" && profile != "all" && row["profile"] != profile {
			continue
		}
		if row["archived"] == true {
			continue
		}
		rows = append(rows, row)
	}
	s.mu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool { return num(rows[i]["last_active"]) > num(rows[j]["last_active"]) })
	total := len(rows)
	rows = rows[min(offset, total):min(offset+limit, total)]
	writeJSON(w, 200, map[string]any{"sessions": rows, "total": total, "limit": limit, "offset": offset})
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	s.mu.Lock()
	defer s.mu.Unlock()
	res := []any{}
	for _, row := range s.data.Sessions {
		title, _ := row["title"].(string)
		if q != "" && strings.Contains(strings.ToLower(title), q) {
			res = append(res, map[string]any{"session_id": row["id"], "title": title, "snippet": title, "profile": row["profile"], "last_active": row["last_active"]})
		}
	}
	writeJSON(w, 200, map[string]any{"results": res})
}

func (s *Server) findSession(id string) (map[string]any, int) {
	for i, row := range s.data.Sessions {
		if row["id"] == id {
			return row, i
		}
	}
	return nil, -1
}

func (s *Server) session(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.SplitN(rest, "/", 2)
	id, _ := url.PathUnescape(parts[0])
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, idx := s.findSession(id)
	switch {
	case sub == "mutation-snapshot":
		writeJSON(w, 200, map[string]any{"session_id": id, "exists": row != nil, "runtime_revision": s.revision[id], "runtime_generation": 1})
	case sub == "messages":
		if row == nil {
			writeJSON(w, 404, map[string]any{"detail": "Session not found"})
			return
		}
		msgs := s.data.Messages[id]
		if l, _ := strconv.Atoi(r.URL.Query().Get("limit")); l > 0 && len(msgs) > l {
			msgs = msgs[len(msgs)-l:]
		}
		if msgs == nil {
			msgs = []map[string]any{}
		}
		writeJSON(w, 200, map[string]any{"session_id": id, "profile": row["profile"], "messages": msgs})
	case sub == "export":
		out := map[string]any{}
		for k, v := range row {
			out[k] = v
		}
		out["messages"] = s.data.Messages[id]
		writeJSON(w, 200, out)
	case sub == "" && r.Method == http.MethodGet:
		if row == nil {
			writeJSON(w, 404, map[string]any{"detail": "Session not found"})
			return
		}
		writeJSON(w, 200, row)
	case sub == "" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete):
		var body map[string]any
		if r.Method == http.MethodPatch {
			json.NewDecoder(r.Body).Decode(&body)
		} else {
			body = map[string]any{}
			for k, v := range r.URL.Query() {
				body[k] = v[0]
			}
		}
		if body["request_id"] == nil || body["expected_revision"] == nil {
			writeJSON(w, 409, map[string]any{"detail": "mutation_identity_required"})
			return
		}
		if fmt.Sprint(body["expected_revision"]) != strconv.FormatInt(s.revision[id], 10) {
			writeJSON(w, 409, map[string]any{"detail": "revision_conflict"})
			return
		}
		if r.Method == http.MethodDelete {
			if row == nil {
				writeJSON(w, 200, map[string]any{"ok": true, "already_absent": true})
				return
			}
			s.data.Sessions = append(s.data.Sessions[:idx], s.data.Sessions[idx+1:]...)
			delete(s.data.Messages, id)
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
		if row == nil {
			writeJSON(w, 404, map[string]any{"detail": "not_found"})
			return
		}
		for _, k := range []string{"title", "pinned", "archived", "unread"} {
			if v, ok := body[k]; ok {
				row[k] = v
			}
		}
		s.revision[id]++
		writeJSON(w, 200, map[string]any{"ok": true, "revision": s.revision[id]})
	default:
		writeJSON(w, 404, map[string]any{"detail": "Not Found"})
	}
}

// BumpRevision moves a session's fence, as another client's write would.
func (s *Server) BumpRevision(id string) {
	s.mu.Lock()
	s.revision[id]++
	s.mu.Unlock()
}

// SessionRow returns a copy of a session's row.
func (s *Server) SessionRow(id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, _ := s.findSession(id)
	if row == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range row {
		out[k] = v
	}
	return out
}

// ---- sign-in ----

func (s *Server) nativeAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.opt.NoNative {
		writeJSON(w, 400, map[string]any{"detail": "provider does not support native sign-in"})
		return
	}
	q := r.URL.Query()
	ru := q.Get("redirect_uri")
	if !strings.HasPrefix(ru, "http://127.0.0.1") || q.Get("code_challenge_method") != "S256" {
		writeJSON(w, 400, map[string]any{"detail": "bad redirect_uri"})
		return
	}
	h := fmt.Sprintf("pk%d", s.nextID.Add(1))
	s.mu.Lock()
	s.pkce[h] = q.Get("code_challenge") + "|" + ru + "|" + q.Get("state")
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "hermes_session_pkce", Value: h, Path: "/", HttpOnly: true})
	w.Header().Set("Location", "/login")
	w.WriteHeader(http.StatusFound)
}

func (s *Server) passwordLogin(w http.ResponseWriter, r *http.Request) {
	var b struct{ Provider, Username, Password string }
	json.NewDecoder(r.Body).Decode(&b)
	if s.opt.Username == "" || b.Provider != "basic" {
		writeJSON(w, 404, map[string]any{"detail": "Unknown provider"})
		return
	}
	if b.Username != s.opt.Username || b.Password != s.opt.Password {
		writeJSON(w, 401, map[string]any{"detail": "Invalid credentials"})
		return
	}
	if c, err := r.Cookie("hermes_session_pkce"); err == nil {
		s.mu.Lock()
		v := s.pkce[c.Value]
		delete(s.pkce, c.Value)
		s.mu.Unlock()
		if v != "" {
			parts := strings.SplitN(v, "|", 3)
			code := fmt.Sprintf("gc%d", s.nextID.Add(1))
			s.mu.Lock()
			s.codes[code] = parts[0]
			s.mu.Unlock()
			writeJSON(w, 200, map[string]any{"ok": true, "next": parts[1] + "?code=" + code + "&state=" + url.QueryEscape(parts[2])})
			return
		}
	}
	at := fmt.Sprintf("ck%d", s.nextID.Add(1))
	s.mu.Lock()
	s.cookies[at] = true
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "hermes_session_at", Value: at, Path: "/", HttpOnly: true})
	writeJSON(w, 200, map[string]any{"ok": true, "next": "/"})
}

func (s *Server) mint() map[string]any {
	at := fmt.Sprintf("at%d", s.nextID.Add(1))
	rt := fmt.Sprintf("rt%d", s.nextID.Add(1))
	s.mu.Lock()
	s.access[at], s.refresh[rt] = true, true
	s.mu.Unlock()
	return map[string]any{"access_token": at, "refresh_token": rt, "token_type": "Bearer", "expires_at": time.Now().Add(12 * time.Hour).Unix(), "provider": "basic", "user_id": "u1"}
}

func (s *Server) nativeToken(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	json.NewDecoder(r.Body).Decode(&b)
	sum := sha256.Sum256([]byte(b.CodeVerifier))
	s.mu.Lock()
	want, ok := s.codes[b.Code]
	delete(s.codes, b.Code)
	s.mu.Unlock()
	if !ok || want != base64.RawURLEncoding.EncodeToString(sum[:]) {
		writeJSON(w, 400, map[string]any{"detail": "Invalid or expired authorization code."})
		return
	}
	writeJSON(w, 200, s.mint())
}

func (s *Server) nativeRefresh(w http.ResponseWriter, r *http.Request) {
	var b struct {
		RefreshToken string `json:"refresh_token"`
	}
	json.NewDecoder(r.Body).Decode(&b)
	s.mu.Lock()
	ok := s.refresh[b.RefreshToken]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "session_expired", "detail": "Refresh token expired or invalid; start a new sign-in."})
		return
	}
	writeJSON(w, 200, s.mint())
}

// ExpireAccess revokes every access token, as an expiry would.
func (s *Server) ExpireAccess() {
	s.mu.Lock()
	s.access = map[string]bool{}
	s.mu.Unlock()
}

// ExpireAll revokes access and refresh tokens and cookie sessions.
func (s *Server) ExpireAll() {
	s.mu.Lock()
	s.access, s.refresh, s.cookies = map[string]bool{}, map[string]bool{}, map[string]bool{}
	s.mu.Unlock()
}

// ---- socket ----

type socket struct {
	s       *Server
	c       *websocket.Conn
	wmu     sync.Mutex
	seq     map[string]int64
	running map[string]context.CancelFunc
	mu      sync.Mutex
	gen     int64
	prompts map[string]string // prompt id -> session
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ok := false
	s.mu.Lock()
	if t := q.Get("ticket"); t != "" && s.tickets[t] {
		delete(s.tickets, t) // single use
		ok = true
	}
	if t := q.Get("token"); t != "" && (t == s.opt.Token || s.access[t]) {
		ok = true
	}
	if c, err := r.Cookie("hermes_session_at"); err == nil && s.cookies[c.Value] {
		ok = true
	}
	s.mu.Unlock()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	if !ok {
		c.Close(4401, "unauthorized")
		return
	}
	k := &socket{s: s, c: c, seq: map[string]int64{}, running: map[string]context.CancelFunc{}, prompts: map[string]string{}, gen: 1}
	s.mu.Lock()
	s.sockets = append(s.sockets, k)
	s.mu.Unlock()
	ready := map[string]any{"skin": map[string]any{}, "change_events": true, "replay_epoch": "c0ffee", "heartbeat": true}
	if s.opt.Canonical {
		ready["session_authority"] = true
	}
	k.event("gateway.ready", "", ready)
	k.serve()
}

// CloseSockets drops every open socket, as a network blip would.
func (s *Server) CloseSockets() {
	s.mu.Lock()
	ks := s.sockets
	s.sockets = nil
	s.mu.Unlock()
	for _, k := range ks {
		k.c.CloseNow()
	}
}

func (k *socket) write(v any) {
	data, _ := json.Marshal(v)
	k.wmu.Lock()
	defer k.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	k.c.Write(ctx, websocket.MessageText, data)
}

func (k *socket) event(typ, sid string, payload any) {
	params := map[string]any{"type": typ, "session_id": sid, "payload": payload}
	if sid != "" {
		k.mu.Lock()
		k.seq[sid]++
		params["seq"] = k.seq[sid]
		k.mu.Unlock()
	}
	k.write(map[string]any{"jsonrpc": "2.0", "method": "event", "params": params})
}

func (k *socket) serve() {
	for {
		_, data, err := k.c.Read(context.Background())
		if err != nil {
			return
		}
		var f struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		if f.Method == "" { // a response to a server request
			var id string
			json.Unmarshal(f.ID, &id)
			reply := f.Result
			if len(reply) == 0 {
				reply = json.RawMessage(`{"error":true}`)
			}
			k.s.mu.Lock()
			k.s.Responses[id] = append(json.RawMessage(nil), reply...)
			k.s.mu.Unlock()
			continue
		}
		k.s.count(f.Method)
		res, rerr := k.handle(f.Method, f.Params)
		reply := map[string]any{"jsonrpc": "2.0", "id": f.ID}
		if rerr != nil {
			reply["error"] = rerr
		} else {
			reply["result"] = res
		}
		k.write(reply)
	}
}

func (k *socket) handle(method string, p map[string]any) (any, map[string]any) {
	canon := k.s.opt.Canonical
	sid, _ := p["session_id"].(string)
	switch method {
	case "gateway.ping":
		return map[string]any{"ok": true}, nil
	case "client.capabilities":
		return map[string]any{"server_requests": []string{"approval", "clarify", "sudo", "secret"}, "declines_not_shown": true}, nil
	case "session.create":
		id := fmt.Sprintf("2026112%d_%06d_new", 2, k.s.nextID.Add(1))
		profile, _ := p["profile"].(string)
		if profile == "" {
			profile = "default"
		}
		now := float64(time.Now().Unix())
		k.s.mu.Lock()
		k.s.data.Sessions = append(k.s.data.Sessions, map[string]any{"id": id, "title": "", "profile": profile, "started_at": now, "last_active": now, "message_count": 0, "source": "gui"})
		k.s.mu.Unlock()
		rt := id
		if !canon {
			rt = "rt-" + id
		}
		return k.snapshot(rt, id), nil
	case "session.resume":
		k.s.mu.Lock()
		row, _ := k.s.findSession(sid)
		k.s.mu.Unlock()
		if row == nil {
			return nil, map[string]any{"code": 4001, "message": "not_found", "data": map[string]any{"reason": "not_found"}}
		}
		rt := sid
		if !canon {
			rt = "rt-" + sid
		}
		return k.snapshot(rt, sid), nil
	case "prompt.submit":
		text, _ := p["text"].(string)
		stored := strings.TrimPrefix(sid, "rt-")
		k.s.mu.Lock()
		k.s.Submitted[stored] = append(k.s.Submitted[stored], text)
		k.s.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		k.mu.Lock()
		k.running[sid] = cancel
		k.gen++
		gen := k.gen
		k.mu.Unlock()
		go k.replay(ctx, sid, k.s.opt.Reply, gen)
		if canon {
			return map[string]any{"admission_id": "adm_1", "ref": map[string]any{"session_id": sid}, "sequence": 1, "status": "started", "outcome": nil, "authority_epoch": 7, "execution_generation": gen}, nil
		}
		return map[string]any{"status": "streaming", "user_row_id": 1}, nil
	case "session.interrupt":
		if canon {
			if _, ok := p["execution_generation"]; !ok {
				return nil, map[string]any{"code": -32602, "message": "execution_generation required"}
			}
		}
		k.mu.Lock()
		cancel := k.running[sid]
		k.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return map[string]any{"status": "interrupted"}, nil
	case "approval.respond", "clarify.respond":
		pid, _ := p["prompt_id"].(string)
		k.event(strings.TrimSuffix(method, ".respond")+".settled", sid, map[string]any{"prompt_id": pid, "execution_generation": p["execution_generation"]})
		return map[string]any{"status": "resolved", "prompt_id": pid}, nil
	case "session.events.since":
		return map[string]any{"events": []any{}, "latest_seq": k.seq[sid], "truncated": false, "count": 0, "epoch": "c0ffee"}, nil
	case "session.detach":
		return map[string]any{"ok": true}, nil
	}
	return nil, map[string]any{"code": -32601, "message": "unknown method", "data": map[string]any{"reason": "unknown_method"}}
}

func (k *socket) snapshot(rt, stored string) map[string]any {
	return map[string]any{
		"session_id": rt, "stored_session_id": stored, "messages": []any{}, "message_count": 0, "running": false,
		"authority_epoch": 7, "replay_epoch": "c0ffee", "last_sequence": 0, "subscription_id": "sub1", "revision": 0,
		"execution_generation": k.gen, "pending": []any{}, "prompts": []any{}, "info": map[string]any{"model": "anthropic/claude-sonnet-4.6"},
	}
}

// replay streams a fixture into session sid, renumbering seq and
// re-addressing session_id; an approval.request pauses until answered
// (the fake answers settle it); cancel ends the turn as interrupted.
func (k *socket) replay(ctx context.Context, sid, fixture string, gen int64) {
	defer func() {
		k.mu.Lock()
		delete(k.running, sid)
		k.mu.Unlock()
	}()
	var finalText strings.Builder
	completed := false
	for _, raw := range Fixture(fixture) {
		var ev map[string]any
		json.Unmarshal(raw, &ev)
		select {
		case <-ctx.Done():
			k.event("message.complete", sid, map[string]any{"text": finalText.String(), "status": "interrupted", "outcome": "cancelled"})
			return
		case <-time.After(k.s.opt.Delay):
		}
		typ, _ := ev["type"].(string)
		if typ == "message.delta" {
			if pl, ok := ev["payload"].(map[string]any); ok {
				if t, ok := pl["text"].(string); ok {
					finalText.WriteString(t)
				}
			}
		}
		params := map[string]any{"type": typ, "session_id": sid, "payload": ev["payload"]}
		k.mu.Lock()
		k.seq[sid]++
		params["seq"] = k.seq[sid]
		k.mu.Unlock()
		if k.s.opt.Canonical {
			params["execution_generation"] = gen
			params["replay_epoch"] = "c0ffee"
		}
		if typ == "message.complete" {
			completed = true
			k.s.persist(strings.TrimPrefix(sid, "rt-"), k.lastPrompt(sid), finalText.String())
		}
		k.write(map[string]any{"jsonrpc": "2.0", "method": "event", "params": params})
	}
	if !completed {
		// A stream that stops mid-turn (design_turn, approval_turn) stays
		// running, as a real turn does, until it is interrupted.
		<-ctx.Done()
		k.event("message.complete", sid, map[string]any{"text": finalText.String(), "status": "interrupted", "outcome": "cancelled"})
	}
}

func (k *socket) lastPrompt(sid string) string {
	k.s.mu.Lock()
	defer k.s.mu.Unlock()
	ps := k.s.Submitted[strings.TrimPrefix(sid, "rt-")]
	if len(ps) == 0 {
		return ""
	}
	return ps[len(ps)-1]
}

// persist stores a finished turn, as the real instance's store does.
func (s *Server) persist(id, user, reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := float64(time.Now().UnixNano()) / 1e9
	n := int(s.nextID.Add(2))
	s.data.Messages[id] = append(s.data.Messages[id],
		map[string]any{"id": n, "role": "user", "content": user, "timestamp": now},
		map[string]any{"id": n + 1, "role": "assistant", "content": reply, "timestamp": now})
	if row, _ := s.findSession(id); row != nil {
		row["last_active"] = now
		row["is_active"] = false
	}
}

// Run replays a fixture into a stored session on the newest socket, as an
// agent run started elsewhere (another device, a cron job) would.
func (s *Server) Run(stored, fixture string, delay time.Duration) bool {
	s.mu.Lock()
	var k *socket
	if n := len(s.sockets); n > 0 {
		k = s.sockets[n-1]
	}
	s.mu.Unlock()
	if k == nil {
		return false
	}
	rt := stored
	if !s.opt.Canonical {
		rt = "rt-" + stored
	}
	old := s.opt.Delay
	s.opt.Delay = delay
	ctx, cancel := context.WithCancel(context.Background())
	k.mu.Lock()
	k.running[rt] = cancel
	k.mu.Unlock()
	go func() {
		k.replay(ctx, rt, fixture, k.gen)
		s.opt.Delay = old
	}()
	return true
}

// Ask sends a server request (sudo, secret, clarify, approval…) about a
// stored session on the newest socket and returns its id.
func (s *Server) Ask(stored, method string, params map[string]any) string {
	s.mu.Lock()
	var k *socket
	if n := len(s.sockets); n > 0 {
		k = s.sockets[n-1]
	}
	s.mu.Unlock()
	if k == nil {
		return ""
	}
	sid := stored
	if !s.opt.Canonical {
		sid = "rt-" + stored
	}
	id := fmt.Sprintf("srq-%012x", s.nextID.Add(1))
	p := map[string]any{"session_id": sid}
	for key, v := range params {
		p[key] = v
	}
	k.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": p})
	return id
}

// Withdraw sends request.cancel for an open server request.
func (s *Server) Withdraw(stored, id string) {
	s.mu.Lock()
	var k *socket
	if n := len(s.sockets); n > 0 {
		k = s.sockets[n-1]
	}
	s.mu.Unlock()
	if k == nil {
		return
	}
	sid := stored
	if !s.opt.Canonical {
		sid = "rt-" + stored
	}
	k.event("request.cancel", sid, map[string]any{"id": id, "method": "sudo", "reason": "timeout"})
}

// Response returns the client's answer to a server request.
func (s *Server) Response(id string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Responses[id]
}
