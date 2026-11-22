package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"hermes-desktop/internal/hermes"
	"hermes-desktop/internal/secrets"
	"hermes-desktop/internal/settings"
)

// ConnectRequest is what the connect screen asks for.
type ConnectRequest struct {
	Name     string
	Address  string // "" on This computer tries the default ports
	Local    bool
	Mode     settings.AuthMode
	Token    string
	Username string
	Password string
	// ReplaceID signs an existing instance in again instead of adding one.
	ReplaceID string
}

// DefaultLocalPorts are the dashboard's and the gateway Webapp's ports.
var DefaultLocalPorts = []string{"9119", "9120"}

// Connect checks the address and the credentials off the UI thread, and
// on success keeps the credentials in the keychain and saves the instance.
func (a *App) Connect(req ConnectRequest, done func(settings.Instance, error)) {
	go func() {
		inst, err := a.verify(req)
		a.Post(func() {
			if err == nil {
				a.Instances.Put(inst)
				if a.SaveInstances != nil {
					list := settings.Instances{List: append([]settings.Instance(nil), a.Instances.List...)}
					go a.SaveInstances(list)
				}
			}
			done(inst, err)
		})
	}()
}

func (a *App) verify(req ConnectRequest) (settings.Instance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addrs := []string{req.Address}
	if strings.TrimSpace(req.Address) == "" {
		if !req.Local {
			return settings.Instance{}, errors.New("Enter the address of your Hermes server.")
		}
		addrs = nil
		for _, p := range DefaultLocalPorts {
			addrs = append(addrs, "http://127.0.0.1:"+p)
		}
	}
	var auth hermes.Auth
	var pa *hermes.PasswordAuth
	cred := secrets.Credentials{}
	switch req.Mode {
	case settings.AuthPassword:
		if req.Username == "" || req.Password == "" {
			return settings.Instance{}, errors.New("Enter your dashboard username and password.")
		}
		pa = &hermes.PasswordAuth{Username: req.Username, Password: req.Password}
		auth = pa
		cred.Username, cred.Password = req.Username, req.Password
	default:
		tok := strings.TrimSpace(req.Token)
		if tok == "" {
			return settings.Instance{}, errors.New("Paste the API key from the instance's .env file.")
		}
		auth = &hermes.TokenAuth{Token: tok}
		cred.Token = tok
	}
	var cl *hermes.Client
	var reachErr error
	for _, addr := range addrs {
		c, err := hermes.New(addr, auth)
		if err != nil {
			return settings.Instance{}, err
		}
		if _, err := c.Health(ctx); err != nil {
			reachErr = err
			continue
		}
		cl = c
		break
	}
	if cl == nil {
		return settings.Instance{}, friendly(reachErr, addrs[0])
	}
	// A gated instance takes an API key only when it runs the
	// session-token provider (HERMES_DASHBOARD_SESSION_TOKEN is set).
	if req.Mode != settings.AuthPassword {
		if st, err := cl.Status(ctx); err == nil && st.AuthRequired && !slices.Contains(st.AuthProviders, "session-token") {
			if _, err := cl.Me(ctx); err != nil {
				msg := "This instance does not accept API keys: HERMES_DASHBOARD_SESSION_TOKEN is not set where it runs."
				if slices.Contains(st.AuthProviders, "basic") {
					msg += " Use Dashboard login with your dashboard username and password, or set the token in the instance's .env and restart it."
				} else {
					msg += " Set it in the instance's .env and restart it."
				}
				return settings.Instance{}, errors.New(msg)
			}
		}
	}
	if pa != nil {
		if err := pa.SignIn(ctx, cl); err != nil {
			return settings.Instance{}, friendly(err, cl.BaseURL().String())
		}
		if t := pa.Tokens(); t.AccessToken != "" {
			// Bearer tokens: keep them, not the password.
			cred.Tokens, cred.Password = &t, ""
		}
	}
	if _, err := cl.Me(ctx); err != nil {
		return settings.Instance{}, friendly(err, cl.BaseURL().String())
	}
	inst := settings.Instance{ID: req.ReplaceID, Name: strings.TrimSpace(req.Name), URL: cl.BaseURL().String(), Local: req.Local, Auth: req.Mode, Username: req.Username}
	if inst.Auth == "" {
		inst.Auth = settings.AuthToken
	}
	if inst.ID == "" {
		// Signing in to an address already saved replaces it.
		for _, old := range a.instancesSnapshot() {
			if strings.EqualFold(old.URL, inst.URL) {
				inst.ID = old.ID
				if inst.Name == "" {
					inst.Name = old.Name
				}
			}
		}
	}
	if inst.ID == "" {
		inst.ID = settings.NewInstanceID()
	}
	if inst.Name == "" {
		inst.Name = defaultName(cl.BaseURL(), req.Local, len(a.instancesSnapshot()))
	}
	if err := a.Secrets.Set(inst.ID, cred); err != nil {
		return settings.Instance{}, fmt.Errorf("Signed in, but the keychain refused to keep the credentials: %w", err)
	}
	return inst, nil
}

// instancesSnapshot copies the instance list from a background goroutine.
func (a *App) instancesSnapshot() []settings.Instance {
	done := make(chan []settings.Instance, 1)
	a.Post(func() { done <- append([]settings.Instance(nil), a.Instances.List...) })
	select {
	case l := <-done:
		return l
	case <-time.After(5 * time.Second):
		return nil
	}
}

func defaultName(u *url.URL, local bool, n int) string {
	if local {
		return "This computer"
	}
	if n == 0 {
		return "Main"
	}
	h := u.Hostname()
	if i := strings.IndexByte(h, '.'); i > 0 && net.ParseIP(h) == nil {
		h = h[:i]
	}
	return h
}

// friendly turns a connection error into a sentence for the connect screen.
func friendly(err error, addr string) error {
	var ae *hermes.APIError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, hermes.ErrBadCredentials):
		return errors.New("That username or password was not accepted.")
	case errors.Is(err, hermes.ErrTooManyAttempts):
		return errors.New("Too many sign-in attempts. Wait a minute and try again.")
	case errors.Is(err, hermes.ErrUnauthorized):
		return errors.New("The instance refused that API key. Check HERMES_DASHBOARD_SESSION_TOKEN in its .env file.")
	case errors.As(err, &ae) && ae.Status == 400 && strings.Contains(ae.Detail, "Host"):
		return errors.New("The instance only answers on its own hostname. Use the address it is configured for (dashboard.public_url).")
	case errors.As(err, &ae):
		return fmt.Errorf("The instance answered with an error: %s", ae.Error())
	}
	var ne net.Error
	if errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
		return fmt.Errorf("Could not reach Hermes at %s. Check the address and that `hermes dashboard` or `hermes gateway` is running.", addr)
	}
	return err
}

// ReadLocalToken reads HERMES_DASHBOARD_SESSION_TOKEN from ~/.hermes/.env
// (or $HERMES_HOME/.env), for "This computer". Call it off the UI thread.
func ReadLocalToken() (string, error) {
	home := os.Getenv("HERMES_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".hermes")
	}
	f, err := os.Open(filepath.Join(home, ".env"))
	if err != nil {
		return "", fmt.Errorf("no .env in %s", home)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "HERMES_DASHBOARD_SESSION_TOKEN" {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if p, ok := strings.CutPrefix(v, "file:"); ok {
			data, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			v = strings.TrimSpace(string(data))
		}
		if v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("HERMES_DASHBOARD_SESSION_TOKEN is not set in %s", filepath.Join(home, ".env"))
}

// Forget removes a saved instance and its credentials and cache.
func (a *App) Forget(id string) {
	if a.Conn != nil && a.Conn.Instance.ID == id {
		a.Conn.Close()
	}
	a.Instances.Remove(id)
	list := settings.Instances{List: append([]settings.Instance(nil), a.Instances.List...)}
	go func() {
		a.Secrets.Delete(id)
		if a.Cache != nil {
			a.Cache.ForgetInstance(context.Background(), id)
		}
		if a.SaveInstances != nil {
			a.SaveInstances(list)
		}
	}()
}
