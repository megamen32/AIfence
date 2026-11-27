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
	robotsMu          sync.Mutex
	group             *robotstxt.Group
	robotsErr         error
	paceMu            sync.Mutex
	next              time.Time
	pauseErr          *HTTPStatus
	inflight          int
	capacity          int
	adaptiveDelay     time.Duration
	successes         int
	transportFailures int
	changed           chan struct{}
	transport         http.RoundTripper
}

// Raw response sink is storage-agnostic: the native app uses PostgreSQL
// receipts and Airlock Storage; standalone legacy workers use their journal.
type HTTPArchive func(context.Context, string, string, string, int, string, time.Time, []byte) error
type HTTP struct {
	mu         sync.Mutex
	hosts      map[string]*hostState
	archive    HTTPArchive
	publicOnly bool
	transport  http.RoundTripper
}

func NewHTTP() *HTTP { return newHTTPWithArchive(nil) }
func newHTTPWithArchive(archive HTTPArchive) *HTTP {
	return &HTTP{hosts: map[string]*hostState{}, archive: archive}
}
func (h *HTTP) state(host string) *hostState {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hosts[host] == nil {
		h.hosts[host] = &hostState{capacity: 8, changed: make(chan struct{}), transport: h.transport}
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
	if status == 0 {
		// A single timed-out URL is retried by its own queue entry. Only a burst
		// of transport failures should slow the entire host.
		s.transportFailures++
		s.successes = 0
		if s.transportFailures >= 3 {
			if s.capacity > 2 {
				s.capacity = max(2, s.capacity/2)
			}
			s.adaptiveDelay = max(s.adaptiveDelay, 2*time.Second)
			if t := time.Now().Add(s.adaptiveDelay); t.After(s.next) {
				s.next = t
			}
		}
	} else if status >= 500 || status == 429 {
		s.transportFailures = 0
		s.successes = 0
		s.capacity = max(1, s.capacity/2)
		s.adaptiveDelay = min(30*time.Second, max(2*time.Second, 2*s.adaptiveDelay))
		if t := time.Now().Add(s.adaptiveDelay); t.After(s.next) {
			s.next = t
		}
		if retry.After(s.next) {
			s.next = retry
		}
	} else if status >= 200 && status < 400 || status == 404 || status == 410 {
		s.transportFailures = 0
		// A valid no-price/unavailable result still proves transport health.
		s.successes++
		if s.successes >= 8 {
			s.capacity = min(16, s.capacity+1)
			s.adaptiveDelay = max(time.Second, s.adaptiveDelay/2)
			s.successes = 0
		}
	}
	s.notifyLocked()
}

type response struct {
	body     []byte
	location string
}

func (s *hostState) get(ctx context.Context, raw string, delay time.Duration, archive HTTPArchive, source, requested string) (response, error) {
	if e := s.wait(ctx, delay); e != nil {
		return response{}, e
	}
	defer s.release()
	timeout := 40 * time.Second
	if strings.HasSuffix(raw, ".xml") {
		timeout = 3 * time.Minute
	}
	client := http.Client{Transport: s.transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
	if archive != nil {
		if err := archive(ctx, source, requested, raw, res.StatusCode, location, at, body); err != nil {
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

func (s *hostState) policy(ctx context.Context, host, source string, archive HTTPArchive) (*robotstxt.Group, error) {
	s.robotsMu.Lock()
	defer s.robotsMu.Unlock()
	if s.group != nil {
		return s.group, nil
	}
	if s.robotsErr != nil {
		return nil, s.robotsErr
	}
	res, e := s.get(ctx, "https://"+host+"/robots.txt", time.Second, archive, source, "https://"+host+"/robots.txt")
	if e != nil {
		s.robotsErr = fmt.Errorf("robots unavailable: %w", e)
		return nil, s.robotsErr
	}
	if res.location != "" {
		s.robotsErr = fmt.Errorf("robots unavailable: redirect unsupported")
		return nil, s.robotsErr
	}
	rules, e := robotstxt.FromBytes(res.body)
	if e != nil {
		s.robotsErr = fmt.Errorf("robots unavailable: %w", e)
		return nil, s.robotsErr
	}
	s.group = rules.FindGroup(UserAgent)
	return s.group, nil
}
func (h *HTTP) Fetch(ctx context.Context, src Source, raw string) ([]byte, error) {
	if h.publicOnly {
		if e := PublicStoreURL(raw); e != nil {
			return nil, e
		}
	}

	if !Allowed(src, raw) {
		return nil, fmt.Errorf("URL outside approved HTTPS host")
	}
	s := h.state(src.Host)
	rules, e := s.policy(ctx, src.Host, src.ID, h.archive)
	if e != nil {
		return nil, e
	}
	delay := max(time.Second, rules.CrawlDelay)
	requested := raw
	for i := 0; i < 5; i++ {
		if h.publicOnly {
			if e := PublicStoreURL(raw); e != nil {
				return nil, e
			}
		}
		if !Allowed(src, raw) {
			return nil, fmt.Errorf("redirect outside approved HTTPS host")
		}
		u, _ := url.Parse(raw)
		if !rules.Test(u.RequestURI()) {
			return nil, fmt.Errorf("robots.txt disallows %s", u.RequestURI())
		}
		res, e := s.get(ctx, raw, delay, h.archive, src.ID, requested)
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
