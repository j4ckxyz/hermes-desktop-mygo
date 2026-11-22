package core

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
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
		fetch = FetchTitle
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
	titleHTTP = &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
)

// FetchTitle reads a web page's og:title or <title>.
func FetchTitle(ctx context.Context, url string) (string, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", errors.New("not a web link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "HermesDesktop/0.1 (link preview)")
	req.Header.Set("Accept", "text/html")
	resp, err := titleHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "html") {
		return "", errors.New("not an html page")
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
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
