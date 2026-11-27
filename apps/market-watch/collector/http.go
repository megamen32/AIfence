package catalog

import (
	"context"
	"errors"
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

type ArchiveFailure struct{ Err error }

func (e *ArchiveFailure) Error() string { return "raw archive unavailable: " + e.Err.Error() }
func (e *ArchiveFailure) Unwrap() error { return e.Err }

type AdmissionPaused struct{ Status *HTTPStatus }

func (e *AdmissionPaused) Error() string { return "host paused: " + e.Status.Error() }
func (e *AdmissionPaused) Unwrap() error { return e.Status }

type hostState struct {
	robotsMu      sync.Mutex
	group         *robotstxt.Group
	paceMu        sync.Mutex
	next          time.Time
	pauseErr      *HTTPStatus
	inflight      int
	capacity      int
	adaptiveDelay time.Duration
	successes     int
	changed       chan struct{}
}
type HTTP struct {
	mu      sync.Mutex
	hosts   map[string]*hostState
	journal *Journal
}

func NewHTTP() *HTTP                   { return NewArchivedHTTP(nil) }
func NewArchivedHTTP(j *Journal) *HTTP { return &HTTP{hosts: map[string]*hostState{}, journal: j} }
func (h *HTTP) state(host string) *hostState {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hosts[host] == nil {
		h.hosts[host] = &hostState{capacity: 8, changed: make(chan struct{})}
	}
	return h.hosts[host]
}

// Reserve starts at least one second apart per store. Latency and body reads
// can overlap without increasing the site's request rate.
func (s *hostState) notifyLocked() {
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
}
func (s *hostState) wait(ctx context.Context, delay time.Duration) error {
	for {
		s.paceMu.Lock()
		if s.pauseErr != nil {
			e := s.pauseErr
			s.paceMu.Unlock()
			return &AdmissionPaused{e}
		}
		if s.capacity == 0 {
			s.capacity = 8
		}
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		if s.inflight >= s.capacity {
			changed := s.changed
			s.paceMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			}
			continue
		}
		remaining := time.Until(s.next)
		if remaining <= 0 {
			if e := ctx.Err(); e != nil {
				s.paceMu.Unlock()
				return e
			}
			s.inflight++
			s.next = time.Now().Add(max(delay, s.adaptiveDelay))
			s.paceMu.Unlock()
			return nil
		}
		changed := s.changed
		s.paceMu.Unlock()
		t := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-changed:
			t.Stop()
		case <-t.C:
		}
	}
}
func (s *hostState) release() {
	s.paceMu.Lock()
	defer s.paceMu.Unlock()
	s.inflight--
	s.notifyLocked()
}
func (s *hostState) feedback(status int, retry time.Time, url string) {
	s.paceMu.Lock()
	defer s.paceMu.Unlock()
	if status == 403 || status == 429 || status == 503 || (status >= 500 && retry.After(time.Now())) {
		// Latch the pause before reading/archiving a control response.
		// No further starts occur in this process for this host.
		if s.pauseErr == nil || retry.After(s.pauseErr.RetryAfter) {
			s.pauseErr = &HTTPStatus{Code: status, RetryAfter: retry, URL: url}
		}
	}
	if status >= 500 || status == 429 || status == 0 {
		s.successes = 0
		s.capacity = max(1, s.capacity/2)
		s.adaptiveDelay = min(30*time.Second, max(2*time.Second, 2*s.adaptiveDelay))
		if t := time.Now().Add(s.adaptiveDelay); t.After(s.next) {
			s.next = t
		}
		if retry.After(s.next) {
			s.next = retry
		}
	} else if status == 200 {
		s.successes++
		if s.successes >= 32 {
			s.capacity = min(16, s.capacity+1)
			s.adaptiveDelay = max(time.Second, s.adaptiveDelay*3/4)
			s.successes = 0
		}
	}
	s.notifyLocked()
}

type response struct {
	body     []byte
	location string
}

func (s *hostState) get(ctx context.Context, raw string, delay time.Duration, j *Journal, source, requested string) (response, error) {
	if e := s.wait(ctx, delay); e != nil {
		return response{}, e
	}
	defer s.release()
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
		if ctx.Err() == nil {
			s.feedback(0, time.Time{}, raw)
		}
		return response{}, e
	}
	defer res.Body.Close()
	at := time.Now().UTC()
	hint := res.Header.Get("Retry-After")
	when, _ := http.ParseTime(hint)
	if n, e := strconv.Atoi(hint); e == nil && n >= 0 && n <= 7*86400 {
		when = at.Add(time.Duration(n) * time.Second)
	}
	s.feedback(res.StatusCode, when, raw)
	body, e := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if e != nil {
		return response{}, e
	}
	if len(body) > maxBody {
		return response{}, fmt.Errorf("response exceeds %d bytes", maxBody)
	}
	location := ""
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		loc, err := res.Location()
		if err != nil {
			return response{}, err
		}
		location = loc.String()
	}
	if j != nil {
		if _, err := j.Archive(source, requested, raw, res.StatusCode, location, at, body); err != nil {
			if res.StatusCode == 403 || res.StatusCode == 429 || res.StatusCode == 503 || (res.StatusCode >= 500 && when.After(time.Now())) {
				return response{}, errors.Join(&HTTPStatus{Code: res.StatusCode, RetryAfter: when, URL: raw}, &ArchiveFailure{err})
			}
			return response{}, &ArchiveFailure{err}
		}
	}
	if location != "" {
		return response{location: location}, nil
	}
	if res.StatusCode != http.StatusOK {
		return response{}, &HTTPStatus{Code: res.StatusCode, RetryAfter: when, URL: raw}
	}
	return response{body: body}, nil
}

func (s *hostState) policy(ctx context.Context, host, source string, j *Journal) (*robotstxt.Group, error) {
	s.robotsMu.Lock()
	defer s.robotsMu.Unlock()
	if s.group != nil {
		return s.group, nil
	}
	res, e := s.get(ctx, "https://"+host+"/robots.txt", time.Second, j, source, "https://"+host+"/robots.txt")
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
	rules, e := s.policy(ctx, src.Host, src.ID, h.journal)
	if e != nil {
		return nil, e
	}
	delay := max(time.Second, rules.CrawlDelay)
	requested := raw
	for i := 0; i < 5; i++ {
		if !Allowed(src, raw) {
			return nil, fmt.Errorf("redirect outside approved HTTPS host")
		}
		u, _ := url.Parse(raw)
		if !rules.Test(u.RequestURI()) {
			return nil, fmt.Errorf("robots.txt disallows %s", u.RequestURI())
		}
		res, e := s.get(ctx, raw, delay, h.journal, src.ID, requested)
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
