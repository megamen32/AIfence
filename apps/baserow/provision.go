package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type nativeSession struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Signed  string `json:"user_session"`
	User    struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Staff    bool   `json:"is_staff"`
	} `json:"user"`
	body    []byte
	expires time.Time
}
type provisioner struct {
	store    identityStore
	client   *http.Client
	base     string
	origin   func() (*url.URL, error)
	mu       sync.Mutex
	sessions map[string]nativeSession
}

func (p *provisioner) call(ctx context.Context, method, path string, body any, token string) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return 0, nil, e
		}
		r = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, p.base+path, r)
	if e != nil {
		return 0, nil, e
	}
	origin, e := p.origin()
	if e != nil {
		return 0, nil, e
	}
	req.Host = origin.Host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", origin.Scheme)
	if token != "" {
		req.Header.Set("Authorization", "JWT "+token)
	}
	resp, e := p.client.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp.StatusCode, b, e
}
func (p *provisioner) login(ctx context.Context, email, pw string) (nativeSession, int, error) {
	status, b, e := p.call(ctx, "POST", "/api/user/token-auth/", map[string]string{"email": email, "password": pw}, "")
	if e != nil {
		return nativeSession{}, status, e
	}
	if status != 200 {
		return nativeSession{}, status, fmt.Errorf("Baserow login HTTP %d", status)
	}
	var s nativeSession
	if e = json.Unmarshal(b, &s); e != nil || s.User.ID <= 0 || s.User.Username != email || s.Access == "" || s.Refresh == "" {
		return nativeSession{}, status, errors.New("Baserow identity/session mismatch")
	}
	s.body = b
	s.expires = time.Now().Add(3 * time.Minute)
	return s, status, nil
}
func (p *provisioner) register(ctx context.Context, email, pw, name string) error {
	code, _, e := p.call(ctx, "POST", "/api/user/", map[string]any{"name": name, "email": email, "password": pw, "language": "en", "authenticate": false}, "")
	if e != nil {
		return e
	}
	if code != 200 && code != 201 && code != 400 {
		return fmt.Errorf("Baserow signup HTTP %d", code)
	}
	// A conflicting registration is accepted only after successful login with the stable derived credentials.
	return nil
}
func (p *provisioner) prepareInstance(ctx context.Context, seed []byte) error {
	status, b, e := p.call(ctx, "GET", "/api/settings/", nil, "")
	if e != nil || status != 200 {
		return errors.New("Baserow settings unavailable")
	}
	var cfg struct {
		Fresh bool `json:"show_admin_signup_page"`
	}
	if e = json.Unmarshal(b, &cfg); e != nil {
		return e
	}
	if !cfg.Fresh {
		return nil
	}
	name, pw := credentials(seed, "baserow-system", "system")
	email := name + "@airlock.invalid"
	if e = p.register(ctx, email, pw, "Airlock service"); e != nil {
		return e
	}
	sess, _, e := p.login(ctx, email, pw)
	if e != nil || !sess.User.Staff {
		return errors.New("Baserow technical administrator not initialized")
	}
	return nil
}
func (p *provisioner) workspace(ctx context.Context, s nativeSession) error {
	code, b, e := p.call(ctx, "GET", "/api/workspaces/", nil, s.Access)
	if e != nil || code != 200 {
		return fmt.Errorf("Baserow workspace list HTTP %d", code)
	}
	var existing []json.RawMessage
	if e = json.Unmarshal(b, &existing); e != nil {
		return e
	}
	if len(existing) > 0 {
		return nil
	}
	code, _, e = p.call(ctx, "POST", "/api/workspaces/", map[string]string{"name": "Airlock"}, s.Access)
	if e != nil || !(code == 200 || code == 201) {
		return fmt.Errorf("Baserow workspace creation HTTP %d", code)
	}
	return nil
}
func (p *provisioner) session(ctx context.Context, force bool) (nativeSession, error) {
	uid, e := currentUser(ctx)
	if e != nil {
		return nativeSession{}, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[uid]; ok && !force && time.Now().Before(s.expires) {
		return s, nil
	}
	seed, e := p.store.seed(ctx)
	if e != nil {
		return nativeSession{}, e
	}
	name, pw := credentials(seed, "baserow-user", uid)
	email := name + "@airlock.invalid"
	s, code, e := p.login(ctx, email, pw)
	if e != nil {
		if code != 401 && code != 400 {
			return nativeSession{}, e
		}
		if e = p.prepareInstance(ctx, seed); e != nil {
			return nativeSession{}, e
		}
		if e = p.register(ctx, email, pw, "Airlock user"); e != nil {
			return nativeSession{}, e
		}
		s, _, e = p.login(ctx, email, pw)
		if e != nil {
			return nativeSession{}, e
		}
	}
	if s.User.Staff {
		return nativeSession{}, errors.New("Refusing to use a Baserow administrator as an Airlock user")
	}
	if e = p.workspace(ctx, s); e != nil {
		return nativeSession{}, e
	}
	for k, v := range p.sessions {
		if time.Now().After(v.expires) {
			delete(p.sessions, k)
		}
	}
	p.sessions[uid] = s
	return s, nil
}
func safeNativeClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
