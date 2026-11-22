package hermes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Auth signs requests. A Client calls Prepare before each authenticated
// request (to refresh credentials about to expire), and Refresh once after
// a 401 with the request that failed, retrying it when Refresh reports
// true.
type Auth interface {
	Apply(r *http.Request)
	Prepare(ctx context.Context, c *Client)
	Refresh(ctx context.Context, c *Client, failed *http.Request) (bool, error)
	// WSToken is a credential the socket accepts as ?token= when no ticket
	// can be minted, or "".
	WSToken() string
}

// TokenAuth is the "API key" mode: HERMES_DASHBOARD_SESSION_TOKEN from the
// instance's .env, sent as X-Hermes-Session-Token (docs §2.2a). It has no
// refresh: a 401 means the token was rotated.
type TokenAuth struct{ Token string }

func (a *TokenAuth) Apply(r *http.Request)            { r.Header.Set("X-Hermes-Session-Token", a.Token) }
func (a *TokenAuth) Prepare(context.Context, *Client) {}
func (a *TokenAuth) Refresh(context.Context, *Client, *http.Request) (bool, error) {
	return false, nil
}
func (a *TokenAuth) WSToken() string { return a.Token }

// Tokens are the bearer tokens of the native sign-in flow (docs §2.3.2).
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresAt    Time   `json:"expires_at"`
	Provider     string `json:"provider"`
	UserID       string `json:"user_id"`
}

// PasswordAuth is the "dashboard login" mode. It signs in with the
// headless native flow and uses bearer tokens; when the server will not
// hand out tokens it falls back to the cookie session the login set
// (docs §2.3.1). On a 401 it refreshes the tokens, and when that fails it
// signs in again with the password.
type PasswordAuth struct {
	Username string
	Password string
	Provider string // "" picks the first password provider

	// OnTokens is called, from the goroutine that got them, when new
	// tokens arrive, so the app can keep them in the keychain.
	OnTokens func(Tokens)

	mu      sync.Mutex
	tokens  Tokens
	cookies bool // signed in by cookie (the client's jar holds the session)
	// refreshing lets one refresh run at a time; the others wait for it.
	refreshing chan struct{}
}

// refreshEarly is how long before expires_at the tokens are refreshed.
const refreshEarly = time.Minute

// Prepare refreshes the tokens before a request when they expire within a
// minute, so the request does not fail on them.
func (a *PasswordAuth) Prepare(ctx context.Context, c *Client) {
	a.mu.Lock()
	t := a.tokens
	a.mu.Unlock()
	if t.RefreshToken == "" || t.ExpiresAt.IsZero() || time.Until(t.ExpiresAt.Time) > refreshEarly {
		return
	}
	a.refresh(ctx, c, func(cur Tokens) bool {
		return cur.AccessToken != "" && time.Until(cur.ExpiresAt.Time) > refreshEarly
	})
}

// Refresh renews the tokens after a 401. When another request refreshed
// them while this one waited, it only asks for a retry.
func (a *PasswordAuth) Refresh(ctx context.Context, c *Client, failed *http.Request) (bool, error) {
	used := ""
	if failed != nil {
		used = strings.TrimPrefix(failed.Header.Get("Authorization"), "Bearer ")
	}
	return a.refresh(ctx, c, func(cur Tokens) bool {
		return cur.AccessToken != "" && cur.AccessToken != used
	})
}

// refresh runs one refresh at a time. fresh reports whether the tokens no
// longer need it (another refresh finished meanwhile).
func (a *PasswordAuth) refresh(ctx context.Context, c *Client, fresh func(Tokens) bool) (bool, error) {
	a.mu.Lock()
	if a.refreshing == nil {
		a.refreshing = make(chan struct{}, 1)
	}
	sem := a.refreshing
	a.mu.Unlock()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-sem }()
	a.mu.Lock()
	cur := a.tokens
	a.mu.Unlock()
	if fresh(cur) {
		return true, nil
	}
	return a.renew(ctx, c)
}

// SetTokens restores tokens saved from an earlier launch.
func (a *PasswordAuth) SetTokens(t Tokens) {
	a.mu.Lock()
	a.tokens = t
	a.mu.Unlock()
}

// Tokens returns the current tokens.
func (a *PasswordAuth) Tokens() Tokens {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tokens
}

func (a *PasswordAuth) Apply(r *http.Request) {
	a.mu.Lock()
	tok := a.tokens.AccessToken
	a.mu.Unlock()
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
}

func (a *PasswordAuth) WSToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tokens.AccessToken
}

// renew uses the refresh token, or the password in a cookie session.
func (a *PasswordAuth) renew(ctx context.Context, c *Client) (bool, error) {
	a.mu.Lock()
	t := a.tokens
	a.mu.Unlock()
	if t.RefreshToken != "" {
		var nt Tokens
		err := c.post(ctx, "/auth/native/refresh", map[string]string{"refresh_token": t.RefreshToken, "provider": t.Provider}, &nt, false)
		if err == nil && nt.AccessToken != "" {
			a.store(nt)
			return true, nil
		}
		if !errors.Is(err, ErrUnauthorized) {
			return false, err // unreachable: don't burn a password login
		}
	}
	a.mu.Lock()
	pw := a.Password
	a.mu.Unlock()
	if pw == "" {
		// Tokens-only: the refresh token was refused, so the user signs
		// in again (the caller sees ErrUnauthorized).
		return false, nil
	}
	if err := a.SignIn(ctx, c); err != nil {
		return false, err
	}
	return true, nil
}

// store keeps new tokens. With tokens the password is no longer needed,
// and is forgotten: a refused refresh token asks the user again.
func (a *PasswordAuth) store(t Tokens) {
	a.mu.Lock()
	a.tokens = t
	a.cookies = false
	a.Password = ""
	a.mu.Unlock()
	if a.OnTokens != nil {
		a.OnTokens(t)
	}
}

// ErrBadCredentials is a refused username or password.
var ErrBadCredentials = errors.New("hermes: invalid username or password")

// ErrTooManyAttempts is the login rate limit (10 per minute per IP).
var ErrTooManyAttempts = errors.New("hermes: too many login attempts, try again shortly")

// SignIn signs in with the username and password.
func (a *PasswordAuth) SignIn(ctx context.Context, c *Client) error {
	a.mu.Lock()
	username, password := a.Username, a.Password
	a.mu.Unlock()
	provider := a.Provider
	if provider == "" {
		provider = "basic"
		if ps, err := c.AuthProviders(ctx); err == nil {
			for _, p := range ps {
				if p.SupportsPassword {
					provider = p.Name
					break
				}
			}
		}
	}

	// 1. Authorize: the server stashes a broker handle in a PKCE cookie.
	verifier := randomString(32)
	sum := sha256.Sum256([]byte(verifier))
	state := randomString(16)
	q := url.Values{
		"provider":              {provider},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"http://127.0.0.1:53682/cb"},
		"state":                 {state},
	}
	native := false
	if resp, err := c.raw(ctx, http.MethodGet, "/auth/native/authorize", q, nil, false); err == nil {
		resp.Body.Close()
		native = resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusOK
	}

	// 2. Password login. With the broker handle it answers with the
	// loopback redirect carrying a one-time code; without, it sets the
	// session cookies.
	var login struct {
		OK   bool   `json:"ok"`
		Next string `json:"next"`
	}
	err := c.post(ctx, "/auth/password-login", map[string]string{
		"provider": provider, "username": username, "password": password, "next": "",
	}, &login, false)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			switch ae.Status {
			case http.StatusUnauthorized:
				return ErrBadCredentials
			case http.StatusTooManyRequests:
				return ErrTooManyAttempts
			}
		}
		return err
	}
	if native {
		if u, err := url.Parse(login.Next); err == nil && u.Query().Get("code") != "" {
			if u.Query().Get("state") != state {
				return errors.New("hermes: sign-in state mismatch")
			}
			// 3. Exchange the code for tokens.
			var t Tokens
			if err := c.post(ctx, "/auth/native/token", map[string]string{
				"code": u.Query().Get("code"), "code_verifier": verifier,
			}, &t, false); err != nil {
				return fmt.Errorf("hermes: token exchange: %w", err)
			}
			a.store(t)
			return nil
		}
	}
	// Cookie session: the jar holds it, and the gate refreshes it.
	a.mu.Lock()
	a.tokens = Tokens{}
	a.cookies = true
	a.mu.Unlock()
	return nil
}

// UsesCookies reports whether the session lives in cookies, not tokens.
func (a *PasswordAuth) UsesCookies() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cookies
}

// Expired reports whether the access token is past its expiry.
func (t Tokens) Expired(now time.Time) bool {
	return t.AccessToken == "" || (!t.ExpiresAt.IsZero() && now.After(t.ExpiresAt.Time))
}

func randomString(n int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(randomBytes(n)))
}

func randomBytes(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return string(b)
}
