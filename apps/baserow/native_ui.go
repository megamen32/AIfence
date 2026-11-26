package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Keep these upstream-only names in step with BASEROW_FRONTEND_COOKIE_PREFIX
// in compose.yml. Nuxt concatenates the prefix directly, without a separator.
const nativeCookiePrefix = "aifence_baserow"
const nativeRefreshCookie = nativeCookiePrefix + "jwt_token"
const nativeSignedCookie = nativeCookiePrefix + "user_session"

func setNativeAuthentication(req *http.Request, session nativeSession) {
	req.Header.Del("Cookie")
	req.Header.Set("Authorization", "JWT "+session.Access)
	req.AddCookie(&http.Cookie{Name: nativeRefreshCookie, Value: session.Refresh})
	if session.Signed != "" {
		req.AddCookie(&http.Cookie{Name: nativeSignedCookie, Value: session.Signed})
	}

	query := req.URL.Query()
	if strings.HasPrefix(req.URL.Path, "/ws/") && strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		// Baserow's websocket middleware takes its identity from this query key.
		query.Set("jwt_token", session.Access)
	} else if !strings.HasPrefix(req.URL.Path, "/api/") {
		// These optional Nuxt login inputs would take priority over its cookie.
		query.Del("token")
		query.Del("user_session")
	}
	req.URL.RawQuery = query.Encode()
}

// During SSR Nuxt exchanges the injected cookie with the native backend and
// can rotate it. Keep the successor in the same caller's server-side cache.
func (p *provisioner) acceptRotation(ctx context.Context, previous nativeSession, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	uid, err := currentUser(ctx)
	if err != nil {
		return
	}
	p.rememberRotation(uid, previous, cookies)
}

func (p *provisioner) rememberRotation(uid string, previous nativeSession, cookies []*http.Cookie) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[uid]
	if !ok || s.Refresh != previous.Refresh || s.User.ID != previous.User.ID {
		return
	}
	for _, c := range cookies {
		switch c.Name {
		case nativeRefreshCookie:
			if c.MaxAge < 0 || c.Value == "" {
				delete(p.sessions, uid)
				return
			}
			s.Refresh = c.Value
		case nativeSignedCookie:
			s.Signed = c.Value
		}
	}
	if s.Refresh == previous.Refresh && s.Signed == previous.Signed {
		return
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(s.body, &data) != nil || data == nil {
		delete(p.sessions, uid)
		return
	}
	data["refresh_token"], _ = json.Marshal(s.Refresh)
	data["user_session"], _ = json.Marshal(s.Signed)
	s.body, _ = json.Marshal(data)
	p.sessions[uid] = s
}
