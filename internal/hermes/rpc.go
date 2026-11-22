package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Event is one "event" notification from the socket (docs §6.1, §6.5).
type Event struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"` // the runtime id
	Payload   json.RawMessage `json:"payload"`
	Seq       int64           `json:"seq"`
	// Canonical stamps.
	ReplayEpoch         string `json:"replay_epoch,omitempty"`
	ExecutionGeneration int64  `json:"execution_generation,omitempty"`
	AdmissionID         string `json:"admission_id,omitempty"`

	// StoredID is the stored session id the runtime id maps to, filled by
	// the connection when it knows it.
	StoredID string `json:"-"`
}

// Decode unmarshals the payload into v.
func (e Event) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

// ServerRequest is a JSON-RPC request the server sends the client: an
// approval (legacy), clarify batch, sudo or secret prompt (docs §7.3).
type ServerRequest struct {
	ID       string          `json:"id"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
	StoredID string          `json:"-"`
}

// RPCError is a JSON-RPC error answer.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("hermes rpc %d: %s", e.Code, e.Message) }

// Reason is data.reason of a canonical refusal.
func (e *RPCError) Reason() string {
	var d struct {
		Reason string `json:"reason"`
	}
	json.Unmarshal(e.Data, &d)
	return d.Reason
}

// Handler receives what the server pushes. Its methods run on the
// connection's reader goroutine and must not block.
type Handler interface {
	OnEvent(Event)
	OnRequest(ServerRequest)
	// OnClose is called once, when the socket ends; err is nil after Close.
	OnClose(err error)
}

// HandlerFuncs adapts functions to Handler; nil fields are ignored.
type HandlerFuncs struct {
	Event   func(Event)
	Request func(ServerRequest)
	Close   func(error)
}

func (h HandlerFuncs) OnEvent(e Event) {
	if h.Event != nil {
		h.Event(e)
	}
}
func (h HandlerFuncs) OnRequest(r ServerRequest) {
	if h.Request != nil {
		h.Request(r)
	}
}
func (h HandlerFuncs) OnClose(err error) {
	if h.Close != nil {
		h.Close(err)
	}
}

// Ready is the gateway.ready payload, the first frame (docs §6.2).
type Ready struct {
	SessionAuthority bool   `json:"session_authority"`
	ReplayEpoch      string `json:"replay_epoch"`
	ChangeEvents     bool   `json:"change_events"`
	Heartbeat        bool   `json:"heartbeat"`
}

// Conn is a JSON-RPC 2.0 connection to /api/ws.
type Conn struct {
	ws      *websocket.Conn
	handler Handler
	ready   Ready

	next    atomic.Int64
	mu      sync.Mutex
	pending map[string]chan rpcReply
	// runtime session id -> stored id, and back.
	stored  map[string]string
	runtime map[string]string

	writeMu sync.Mutex
	done    chan struct{}
	closeMu sync.Once
	err     error

	// HeartbeatEvery and DeadAfter default to 15 s and 45 s (Desktop's).
	heartbeatEvery, deadAfter time.Duration
	lastHeard                 atomic.Int64
	record                    func([]byte)
}

type rpcReply struct {
	result json.RawMessage
	err    *RPCError
}

// DialOptions tune a socket; zero values are the defaults.
type DialOptions struct {
	HeartbeatEvery time.Duration
	DeadAfter      time.Duration
	ReadyTimeout   time.Duration
	// Record, when set, gets every frame the server sends, as is (for
	// recording fixtures). It runs on the reader goroutine.
	Record func(frame []byte)
}

// Canonical reports whether the instance speaks the canonical wire (a
// gateway-served listener) rather than the legacy one.
func (c *Conn) Canonical() bool { return c.ready.SessionAuthority }

// Ready returns the gateway.ready payload.
func (c *Conn) Ready() Ready { return c.ready }

// Done is closed when the socket has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the socket ended.
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// wsURL builds ws(s)://host/prefix/api/ws with the given credential.
func (cl *Client) wsURL(q url.Values) string {
	u := *cl.base
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = cl.base.Path + "/api/ws"
	u.RawQuery = q.Encode()
	return u.String()
}

// Dial opens the socket: it mints a single-use ticket (or falls back to
// the token in the query), waits for gateway.ready, and announces that
// it answers server requests (client.capabilities).
func (cl *Client) Dial(ctx context.Context, h Handler, o DialOptions) (*Conn, error) {
	if o.HeartbeatEvery == 0 {
		o.HeartbeatEvery = 15 * time.Second
	}
	if o.DeadAfter == 0 {
		o.DeadAfter = 45 * time.Second
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 15 * time.Second
	}
	q := url.Values{}
	ticket, terr := cl.WSTicket(ctx)
	switch {
	case terr == nil:
		q.Set("ticket", ticket)
	case cl.auth != nil && cl.auth.WSToken() != "":
		q.Set("token", cl.auth.WSToken())
	case errors.Is(terr, ErrUnauthorized):
		return nil, terr
	default:
		// No ticket route and no token (a cookie session): try the cookies.
	}
	hdr := http.Header{}
	hdr.Set("User-Agent", cl.UserAgent)
	dctx, cancel := context.WithTimeout(ctx, o.ReadyTimeout)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, cl.wsURL(q), &websocket.DialOptions{
		HTTPClient: cl.http,
		HTTPHeader: hdr,
	})
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return nil, ErrUnauthorized
			case http.StatusForbidden:
				return nil, ErrForbidden
			}
		}
		return nil, fmt.Errorf("hermes: connect: %w", err)
	}
	ws.SetReadLimit(64 << 20)

	c := &Conn{
		ws: ws, handler: h,
		pending: map[string]chan rpcReply{}, stored: map[string]string{}, runtime: map[string]string{},
		done:           make(chan struct{}),
		heartbeatEvery: o.HeartbeatEvery, deadAfter: o.DeadAfter, record: o.Record,
	}
	// The first frame is gateway.ready, or a close (4401/4403) sent before
	// the server accepted.
	if err := c.awaitReady(dctx); err != nil {
		ws.Close(websocket.StatusNormalClosure, "")
		return nil, err
	}
	c.lastHeard.Store(time.Now().UnixNano())
	go c.readLoop()
	go c.heartbeat()

	cctx, ccancel := context.WithTimeout(ctx, 15*time.Second)
	defer ccancel()
	if err := c.Call(cctx, "client.capabilities", map[string]any{"server_requests": true}, nil); err != nil {
		var re *RPCError
		if !errors.As(err, &re) { // an old server may not know it; the socket is fine
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

func closeError(err error) error {
	switch websocket.CloseStatus(err) {
	case 4401:
		return ErrUnauthorized
	case 4403:
		return ErrForbidden
	case websocket.StatusNormalClosure, websocket.StatusGoingAway:
		return fmt.Errorf("hermes: the instance closed the connection: %w", err)
	}
	return err
}

func (c *Conn) awaitReady(ctx context.Context) error {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return closeError(err)
		}
		var f frame
		if json.Unmarshal(data, &f) != nil || f.Method != "event" {
			continue
		}
		var ev Event
		if json.Unmarshal(f.Params, &ev) == nil && ev.Type == "gateway.ready" {
			json.Unmarshal(ev.Payload, &c.ready)
			return nil
		}
	}
}

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func idKey(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return string(raw)
}

func (c *Conn) readLoop() {
	var err error
	for {
		var data []byte
		_, data, err = c.ws.Read(context.Background())
		if err != nil {
			break
		}
		c.lastHeard.Store(time.Now().UnixNano())
		if c.record != nil {
			c.record(data)
		}
		c.dispatch(data)
	}
	c.finish(closeError(err))
}

func (c *Conn) dispatch(data []byte) {
	var f frame
	if json.Unmarshal(data, &f) != nil {
		return
	}
	switch {
	case f.Method == "event":
		var ev Event
		if json.Unmarshal(f.Params, &ev) != nil {
			return
		}
		ev.StoredID = c.StoredID(ev.SessionID)
		if ev.Type == "session.replay_gap" || ev.Type == "gateway.ready" {
			var r Ready
			if json.Unmarshal(ev.Payload, &r) == nil && r.ReplayEpoch != "" {
				c.mu.Lock()
				c.ready.ReplayEpoch = r.ReplayEpoch
				c.mu.Unlock()
			}
		}
		c.handler.OnEvent(ev)
	case f.Method != "" && len(f.ID) > 0:
		req := ServerRequest{ID: idKey(f.ID), Method: f.Method, Params: f.Params}
		var p struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(f.Params, &p)
		req.StoredID = c.StoredID(p.SessionID)
		c.handler.OnRequest(req)
	case len(f.ID) > 0:
		key := idKey(f.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- rpcReply{result: f.Result, err: f.Error}
		}
	}
}

func (c *Conn) heartbeat() {
	t := time.NewTicker(c.heartbeatEvery)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		if time.Since(time.Unix(0, c.lastHeard.Load())) > c.deadAfter {
			c.finish(errors.New("hermes: the connection stopped answering"))
			c.ws.CloseNow()
			return
		}
		n++
		// The server answers gateway.ping ahead of its queue (ws.py:710).
		c.send(map[string]any{"jsonrpc": "2.0", "id": "heartbeat-" + strconv.Itoa(n), "method": "gateway.ping", "params": map[string]any{}})
	}
}

func (c *Conn) finish(err error) {
	c.closeMu.Do(func() {
		c.err = err
		close(c.done)
		c.mu.Lock()
		for k, ch := range c.pending {
			delete(c.pending, k)
			ch <- rpcReply{err: &RPCError{Code: -32000, Message: "connection closed"}}
		}
		c.mu.Unlock()
		c.handler.OnClose(err)
	})
}

func (c *Conn) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

// Call sends a request and decodes its result into out (which may be nil).
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = map[string]any{}
	}
	id := c.next.Add(1)
	key := strconv.FormatInt(id, 10)
	ch := make(chan rpcReply, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return c.closedErr()
	default:
	}
	c.pending[key] = ch
	c.mu.Unlock()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			if r.err.Code == -32000 && r.err.Message == "connection closed" {
				return c.closedErr()
			}
			return r.err
		}
		if out != nil && len(r.result) > 0 {
			return json.Unmarshal(r.result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *Conn) closedErr() error {
	if c.err != nil {
		return c.err
	}
	return errors.New("hermes: connection closed")
}

// Respond answers a server request with a result.
func (c *Conn) Respond(id string, result any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// Decline answers a server request the app cannot show (-32601).
func (c *Conn) Decline(id string) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "not supported by this client"}})
}

// Close ends the socket.
func (c *Conn) Close() error {
	c.finish(nil)
	return c.ws.Close(websocket.StatusNormalClosure, "")
}

// bind records that runtime id rt is stored session st.
func (c *Conn) bind(rt, st string) {
	if rt == "" || st == "" {
		return
	}
	c.mu.Lock()
	c.stored[rt] = st
	c.runtime[st] = rt
	c.mu.Unlock()
}

// StoredID maps a runtime session id to its stored id (itself if unknown).
func (c *Conn) StoredID(rt string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, ok := c.stored[rt]; ok {
		return st
	}
	return rt
}

// RuntimeID maps a stored session id to the runtime id this socket
// attached it as, or "" when it has not.
func (c *Conn) RuntimeID(stored string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtime[stored]
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
