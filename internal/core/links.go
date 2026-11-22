package core

import (
	"context"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	neturl "net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Link previews: a link card shows the page's own title (the design's
// "Hermes dashboard · Cron jobs"), fetched once per URL off the UI thread,
// without cookies, reading at most 256 KB. LinkPreviews turns it off.
type linkTitles struct {
	mu      sync.Mutex
	titles  map[string]string
	pending map[string]bool
}

// LinkTitle returns the title of the page at url, or "" while it is not
// known (it starts fetching it). Call it from the UI thread.
func (a *App) LinkTitle(url string) string {
	a.links.mu.Lock()
	defer a.links.mu.Unlock()
	if a.links.titles == nil {
		a.links.titles, a.links.pending = map[string]string{}, map[string]bool{}
	}
	if t, ok := a.links.titles[url]; ok {
		return t
	}
	if !a.LinkPreviews || a.links.pending[url] {
		return ""
	}
	a.links.pending[url] = true
	fetch := a.FetchTitle
	if fetch == nil {
		// The instance itself may sit on a private address (a tailnet's
		// CGNAT range, a LAN): its own links still get titles.
		var allow []string
		if a.Conn != nil {
			if u, err := neturl.Parse(a.Conn.Instance.URL); err == nil {
				allow = append(allow, hostPort(u))
			}
		}
		fetch = func(ctx context.Context, url string) (string, error) { return FetchTitleFrom(ctx, url, allow) }
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		title, _ := fetch(ctx, url)
		a.Post(func() { a.SetLinkTitle(url, title) })
	}()
	return ""
}

// SetLinkTitle records a page title ("" when it has none).
func (a *App) SetLinkTitle(url, title string) {
	a.links.mu.Lock()
	defer a.links.mu.Unlock()
	if a.links.titles == nil {
		a.links.titles, a.links.pending = map[string]string{}, map[string]bool{}
	}
	a.links.titles[url] = title
	delete(a.links.pending, url)
}

var (
	ogTitle   = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]*content=["']([^"']+)["']`)
	htmlTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// ErrBlockedAddress is a link preview that would reach a private, loopback
// or link-local address.
var ErrBlockedAddress = errors.New("link preview: address not allowed")

// blockedPrefixes are the ranges a link preview never connects to: they
// reach the user's machine or network, not the web (an agent's reply could
// otherwise make the app probe them).
var blockedPrefixes = func() []netip.Prefix {
	var ps []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		ps = append(ps, netip.MustParsePrefix(s))
	}
	return ps
}()

// BlockedAddress reports whether a link preview may not connect to ip.
func BlockedAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func hostPort(u *neturl.URL) string {
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// FetchTitleFrom reads a web page's og:title or <title>. It connects only
// to public addresses, checked on every connection (so on every redirect,
// and whatever a name resolves to), except the host:ports in allow.
func FetchTitleFrom(ctx context.Context, url string, allow []string) (string, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", errors.New("not a web link")
	}
	allowed := map[string]bool{}
	for _, a := range allow {
		allowed[strings.ToLower(a)] = true
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		Proxy: nil, // a proxy would connect for us, past these checks
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if allowed[strings.ToLower(addr)] {
				return dialer.DialContext(ctx, network, addr)
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if BlockedAddress(ip) {
					return nil, ErrBlockedAddress
				}
			}
			// Connect to the address just checked, not a fresh lookup.
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		Timeout:   8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return errors.New("not a web link")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "HermesDesktop/0.1 (link preview)")
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || !(strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml+xml")) {
		return "", errors.New("not an html page")
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxTitleBytes))
	m := ogTitle.FindSubmatch(body)
	if m == nil {
		m = htmlTitle.FindSubmatch(body)
	}
	if m == nil {
		return "", nil
	}
	t := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
	if r := []rune(t); len(r) > 120 {
		t = string(r[:120]) + "…"
	}
	return t, nil
}

// maxTitleBytes is how much of a page a preview reads for its title.
const maxTitleBytes = 256 << 10
