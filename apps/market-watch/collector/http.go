package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

const UserAgent = "AIfenceMarketWatch/0.1"
const maxBody = 32 * 1024 * 1024

type HTTPStatus struct {
	Code       int
	RetryAfter time.Time
	URL        string
}

func (e *HTTPStatus) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

type hostState struct {
	robotsMu sync.Mutex
	group    *robotstxt.Group
	paceMu   sync.Mutex
	next     time.Time
	pauseErr *HTTPStatus
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

// Reserve starts at least one second apart per store. Latency and body reads
// can overlap without increasing the site's request rate.
func (s *hostState) wait(ctx context.Context, delay time.Duration) error {
	for {
		s.paceMu.Lock()
		if s.pauseErr != nil {
			err := s.pauseErr
			s.paceMu.Unlock()
			return err
		}
		remaining := time.Until(s.next)
		if remaining <= 0 {
			s.next = time.Now().Add(delay)
			s.paceMu.Unlock()
			return nil
		}
		s.paceMu.Unlock()
		t := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

type response struct {
	body     []byte
	location string
}

func (s *hostState) get(ctx context.Context, raw string, delay time.Duration) (response, error) {
	if e := s.wait(ctx, delay); e != nil {
		return response{}, e
	}
	timeout := 40 * time.Second
	if strings.HasSuffix(raw, ".xml") {
		timeout = 3 * time.Minute
	}
	client := http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return response{}, e
	}
	req.Header.Set("User-Agent", UserAgent)
	res, e := client.Do(req)
	if e != nil {
		return response{}, e
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		loc, e := res.Location()
		if e != nil {
			return response{}, e
		}
		return response{location: loc.String()}, nil
	}
	if res.StatusCode != http.StatusOK {
		hint := res.Header.Get("Retry-After")
		when, _ := http.ParseTime(hint)
		if n, e := strconv.Atoi(hint); e == nil && n >= 0 && n <= 7*86400 {
			when = time.Now().Add(time.Duration(n) * time.Second)
		}
		status := &HTTPStatus{Code: res.StatusCode, RetryAfter: when, URL: raw}
		if status.Code == 403 || status.Code == 429 || status.Code == 503 {
			s.paceMu.Lock()
			s.pauseErr = status
			s.paceMu.Unlock()
		}
		return response{}, status
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if e != nil {
		return response{}, e
	}
	if len(body) > maxBody {
		return response{}, fmt.Errorf("response exceeds %d bytes", maxBody)
	}
	return response{body: body}, nil
}
func (s *hostState) policy(ctx context.Context, host string) (*robotstxt.Group, error) {
	s.robotsMu.Lock()
	defer s.robotsMu.Unlock()
	if s.group != nil {
		return s.group, nil
	}
	res, e := s.get(ctx, "https://"+host+"/robots.txt", time.Second)
	if e != nil {
		return nil, fmt.Errorf("robots unavailable: %w", e)
	}
	if res.location != "" {
		return nil, fmt.Errorf("robots redirect unsupported")
	}
	rules, e := robotstxt.FromBytes(res.body)
	if e != nil {
		return nil, e
	}
	s.group = rules.FindGroup(UserAgent)
	return s.group, nil
}
func (h *HTTP) Fetch(ctx context.Context, src Source, raw string) ([]byte, error) {
	if !Allowed(src, raw) {
		return nil, fmt.Errorf("URL outside approved HTTPS host")
	}
	s := h.state(src.Host)
	rules, e := s.policy(ctx, src.Host)
	if e != nil {
		return nil, e
	}
	delay := max(time.Second, rules.CrawlDelay)
	for i := 0; i < 5; i++ {
		if !Allowed(src, raw) {
			return nil, fmt.Errorf("redirect outside approved HTTPS host")
		}
		u, _ := url.Parse(raw)
		if !rules.Test(u.RequestURI()) {
			return nil, fmt.Errorf("robots.txt disallows %s", u.RequestURI())
		}
		res, e := s.get(ctx, raw, delay)
		if e != nil {
			return nil, e
		}
		if res.location == "" {
			return res.body, nil
		}
		raw = res.location
	}
	return nil, fmt.Errorf("too many redirects")
}
