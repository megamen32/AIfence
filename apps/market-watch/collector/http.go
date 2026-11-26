package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

const UserAgent = "AIfenceMarketWatch/0.1"
const maxBody = 32 * 1024 * 1024

type hostState struct {
	mu    sync.Mutex
	last  time.Time
	group *robotstxt.Group
}
type HTTP struct {
	mu    sync.Mutex
	hosts map[string]*hostState
}

func NewHTTP() *HTTP { return &HTTP{hosts: map[string]*hostState{}} }
func (h *HTTP) state(host string) *hostState {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hosts[host] == nil {
		h.hosts[host] = &hostState{}
	}
	return h.hosts[host]
}
func (h *HTTP) Fetch(ctx context.Context, s Source, raw string) ([]byte, error) {
	if !Allowed(s, raw) {
		return nil, fmt.Errorf("URL outside approved HTTPS host")
	}
	state := h.state(s.Host)
	state.mu.Lock()
	defer state.mu.Unlock()
	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, prev []*http.Request) error {
		// Do not follow redirects: even same-host destination needs a robots check.
		return http.ErrUseLastResponse
	}}
	get := func(raw string) ([]byte, error) {
		delay := time.Second
		if state.group != nil && state.group.CrawlDelay > delay {
			delay = state.group.CrawlDelay
		}
		if remaining := time.Until(state.last.Add(delay)); remaining > 0 {
			t := time.NewTimer(remaining)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-t.C:
			}
		}
		state.last = time.Now()
		req, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("User-Agent", UserAgent)
		res, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP %d (not bypassed or retried)", res.StatusCode)
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
		if e != nil {
			return nil, e
		}
		if len(b) > maxBody {
			return nil, fmt.Errorf("response exceeds %d bytes", maxBody)
		}
		return b, nil
	}
	if state.group == nil {
		b, e := get("https://" + s.Host + "/robots.txt")
		if e != nil {
			return nil, fmt.Errorf("robots unavailable: %w", e)
		}
		data, e := robotstxt.FromBytes(b)
		if e != nil {
			return nil, e
		}
		state.group = data.FindGroup(UserAgent)
	}
	u, _ := url.Parse(raw)
	path := u.EscapedPath()
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	if !state.group.Test(path) {
		return nil, fmt.Errorf("robots.txt disallows %s", path)
	}
	return get(raw)
}
