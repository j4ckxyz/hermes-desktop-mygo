package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one Hermes instance. It is safe for concurrent use.
type Client struct {
	base *url.URL
	http *http.Client
	auth Auth
	// UserAgent identifies the app; it carries no personal data.
	UserAgent string
}

// NormalizeURL turns what the user typed into a base URL: it adds a
// scheme (http for loopback and bare host:port on a private network,
// https otherwise) and drops a trailing slash, a query and a fragment.
func NormalizeURL(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, errors.New("enter the address of your Hermes instance")
	}
	if !strings.Contains(s, "://") {
		host := s
		if i := strings.IndexAny(host, "/"); i >= 0 {
			host = host[:i]
		}
		h := host
		if hh, _, err := net.SplitHostPort(host); err == nil {
			h = hh
		}
		if isLoopback(h) {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q is not a valid address", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("use an http:// or https:// address, not %s://", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u, nil
}

func isLoopback(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// New returns a client for the instance at base.
func New(base string, auth Auth) (*Client, error) {
	u, err := NormalizeURL(base)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	return &Client{
		base: u,
		auth: auth,
		http: &http.Client{
			Jar:     jar,
			Timeout: 0, // per request, by context
			// Never follow redirects: the auth routes answer with 302s
			// that a client must read, not follow (docs §2.3).
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
			},
		},
		UserAgent: "HermesDesktop/0.1",
	}, nil
}

// BaseURL is the instance's address.
func (c *Client) BaseURL() *url.URL { u := *c.base; return &u }

// Auth is the client's credential.
func (c *Client) Auth() Auth { return c.auth }

func (c *Client) url(path string, q url.Values) string {
	u := *c.base
	u.Path = c.base.Path + path
	u.RawQuery = q.Encode()
	return u.String()
}

// raw sends one request and returns the response as is.
func (c *Client) raw(ctx context.Context, method, path string, q url.Values, body any, authed bool) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path, q), rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	// No Origin and no Sec-Fetch-* headers: the server's CSRF and origin
	// guards pass a client that sends neither (docs §1.4).
	if authed && c.auth != nil {
		c.auth.Apply(req)
	}
	return c.http.Do(req)
}

// do sends a request, decodes a JSON answer into out, and on a 401
// refreshes the credential once and retries.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	return c.doAuth(ctx, method, path, q, body, out, true)
}

func (c *Client) post(ctx context.Context, path string, body, out any, authed bool) error {
	return c.doAuth(ctx, http.MethodPost, path, nil, body, out, authed)
}

func (c *Client) doAuth(ctx context.Context, method, path string, q url.Values, body, out any, authed bool) error {
	for attempt := 0; ; attempt++ {
		resp, err := c.raw(ctx, method, path, q, body, authed)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && authed && attempt == 0 && c.auth != nil {
			if ok, rerr := c.auth.Refresh(ctx, c); rerr != nil {
				return rerr
			} else if ok {
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return parseAPIError(resp.StatusCode, data)
		}
		if out == nil || len(data) == 0 {
			return nil
		}
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], data...)
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("hermes: %s %s: decode: %w", method, path, err)
		}
		return nil
	}
}

// Health is GET /api/health: public and cheap, the first probe.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.doAuth(ctx, http.MethodGet, "/api/health", nil, nil, &h, false)
	return h, err
}

// Status is GET /api/status: the auth shape of the instance.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.doAuth(ctx, http.MethodGet, "/api/status", nil, nil, &s, false)
	return s, err
}

// AuthProviders lists the interactive sign-in providers.
func (c *Client) AuthProviders(ctx context.Context) ([]AuthProvider, error) {
	var v struct {
		Providers []AuthProvider `json:"providers"`
	}
	err := c.doAuth(ctx, http.MethodGet, "/api/auth/providers", nil, nil, &v, false)
	return v.Providers, err
}

// Me is the signed-in identity: the authenticated probe of a connection.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var m Me
	err := c.do(ctx, http.MethodGet, "/api/auth/me", nil, nil, &m)
	return m, err
}

// Profiles lists the agents.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	var v struct {
		Profiles []Profile `json:"profiles"`
	}
	err := c.do(ctx, http.MethodGet, "/api/profiles", nil, nil, &v)
	return v.Profiles, err
}

// SessionQuery filters a session listing.
type SessionQuery struct {
	Profile string // "" = the default profile; "all" = every agent (AllSessions)
	Limit   int
	Offset  int
}

// Sessions lists one agent's sessions, most recent first.
func (c *Client) Sessions(ctx context.Context, sq SessionQuery) (SessionPage, error) {
	q := url.Values{
		"limit": {strconv.Itoa(max(1, min(sq.Limit, 100)))}, "offset": {strconv.Itoa(sq.Offset)},
		"min_messages": {"0"}, "archived": {"exclude"}, "order": {"recent"}, "full": {"false"},
	}
	if sq.Profile != "" {
		q.Set("profile", sq.Profile)
	}
	var p SessionPage
	err := c.do(ctx, http.MethodGet, "/api/sessions", q, nil, &p)
	return p, err
}

// AllSessions lists sessions across every agent (GET /api/profiles/sessions).
func (c *Client) AllSessions(ctx context.Context, limit, offset int) (SessionPage, error) {
	q := url.Values{
		"limit": {strconv.Itoa(max(1, min(limit, 500)))}, "offset": {strconv.Itoa(offset)},
		"order": {"recent"}, "profile": {"all"}, "min_messages": {"0"}, "archived": {"exclude"},
	}
	var p SessionPage
	err := c.do(ctx, http.MethodGet, "/api/profiles/sessions", q, nil, &p)
	return p, err
}

// Session is one session's row.
func (c *Client) Session(ctx context.Context, profile, id string) (Session, error) {
	var s Session
	err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(id), profileQuery(profile), nil, &s)
	return s, err
}

// Messages reads a session's transcript, oldest first: the latest limit
// rows (≤500), or all when limit is 0. Images come as "[image]".
func (c *Client) Messages(ctx context.Context, profile, id string, limit int) ([]Message, error) {
	q := profileQuery(profile)
	q.Set("inline_images", "false")
	if limit > 0 {
		q.Set("limit", strconv.Itoa(min(limit, 500)))
		q.Set("order", "latest")
	}
	var p MessagePage
	err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(id)+"/messages", q, nil, &p)
	return p.Messages, err
}

// Search finds sessions by ID or full text.
func (c *Client) Search(ctx context.Context, profile, query string, limit int) ([]SearchResult, error) {
	q := profileQuery(profile)
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(max(1, min(limit, 100))))
	var v struct {
		Results []SearchResult `json:"results"`
	}
	err := c.do(ctx, http.MethodGet, "/api/sessions/search", q, nil, &v)
	return v.Results, err
}

// Export returns the session row with every message, as JSON.
func (c *Client) Export(ctx context.Context, profile, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(id)+"/export", profileQuery(profile), nil, &raw)
	return raw, err
}

// MutationSnapshot is the fence a session write must name (docs §5.4).
type MutationSnapshot struct {
	SessionID         string `json:"session_id"`
	Exists            bool   `json:"exists"`
	RuntimeRevision   int64  `json:"runtime_revision"`
	RuntimeGeneration int64  `json:"runtime_generation"`
}

func (c *Client) mutationSnapshot(ctx context.Context, profile, id string) (MutationSnapshot, error) {
	var s MutationSnapshot
	err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(id)+"/mutation-snapshot", profileQuery(profile), nil, &s)
	return s, err
}

// SessionPatch changes a session's metadata; nil fields stay.
type SessionPatch struct {
	Title    *string `json:"title,omitempty"`
	Pinned   *bool   `json:"pinned,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
	Unread   *bool   `json:"unread,omitempty"`
}

// UpdateSession applies a fenced PATCH, taking a fresh snapshot first and
// once more if the fence moved under it.
func (c *Client) UpdateSession(ctx context.Context, profile, id string, p SessionPatch) error {
	return c.fenced(ctx, profile, id, func(snap MutationSnapshot, reqID string) error {
		body := map[string]any{"request_id": reqID, "expected_revision": snap.RuntimeRevision}
		if snap.Exists {
			body["expected_generation"] = snap.RuntimeGeneration
		}
		if profile != "" {
			body["profile"] = profile
		}
		if p.Title != nil {
			body["title"] = *p.Title
		}
		if p.Pinned != nil {
			body["pinned"] = *p.Pinned
		}
		if p.Archived != nil {
			body["archived"] = *p.Archived
		}
		if p.Unread != nil {
			body["unread"] = *p.Unread
		}
		return c.do(ctx, http.MethodPatch, "/api/sessions/"+url.PathEscape(id), nil, body, nil)
	})
}

// DeleteSession deletes a session. Deleting one already gone succeeds.
func (c *Client) DeleteSession(ctx context.Context, profile, id string) error {
	return c.fenced(ctx, profile, id, func(snap MutationSnapshot, reqID string) error {
		q := profileQuery(profile)
		q.Set("request_id", reqID)
		q.Set("expected_revision", strconv.FormatInt(snap.RuntimeRevision, 10))
		if snap.Exists {
			q.Set("expected_generation", strconv.FormatInt(snap.RuntimeGeneration, 10))
		}
		return c.do(ctx, http.MethodDelete, "/api/sessions/"+url.PathEscape(id), q, nil, nil)
	})
}

func (c *Client) fenced(ctx context.Context, profile, id string, write func(MutationSnapshot, string) error) error {
	var err error
	for range 2 {
		var snap MutationSnapshot
		snap, err = c.mutationSnapshot(ctx, profile, id)
		if err != nil {
			return err
		}
		reqID := randomUUID()
		err = write(snap, reqID)
		var ae *APIError
		if errors.As(err, &ae) && (ae.Code == "revision_conflict" || ae.Code == "stale_generation") {
			continue // the session moved: take a new snapshot
		}
		return err
	}
	return err
}

// WSTicket mints a single-use, 30-second socket ticket (docs §2.4).
func (c *Client) WSTicket(ctx context.Context) (string, error) {
	var v struct {
		Ticket string `json:"ticket"`
	}
	err := c.do(ctx, http.MethodPost, "/api/auth/ws-ticket", nil, struct{}{}, &v)
	if err == nil && v.Ticket == "" {
		err = errors.New("hermes: empty ws ticket")
	}
	return v.Ticket, err
}

// Logout ends a cookie session (best effort; tokens are just dropped).
func (c *Client) Logout(ctx context.Context) error {
	resp, err := c.raw(ctx, http.MethodPost, "/auth/logout", nil, nil, true)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func profileQuery(profile string) url.Values {
	q := url.Values{}
	if profile != "" {
		q.Set("profile", profile)
	}
	return q
}

func randomUUID() string {
	b := []byte(randomBytes(16))
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
