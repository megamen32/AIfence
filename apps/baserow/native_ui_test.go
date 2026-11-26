package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeSSRSession(t *testing.T) {
	session := nativeSession{Access: "synthetic-access", Refresh: "synthetic-refresh", Signed: "synthetic-signed"}
	req := httptest.NewRequest("GET", "http://example.test/database/1/table/2/3?filter=foo", nil)
	setNativeAuthentication(req, session)
	cookies := req.Cookies()
	if len(cookies) != 2 || cookies[0].Name != "aifence_baserowjwt_token" || cookies[0].Value != session.Refresh || cookies[1].Name != "aifence_baserowuser_session" || cookies[1].Value != session.Signed || req.URL.Query().Get("filter") != "foo" {
		t.Fatal("Nuxt SSR session cookies are missing")
	}
	socket := httptest.NewRequest("GET", "http://example.test/ws/core/?web_socket_id=dummy", nil)
	socket.Header.Set("Upgrade", "websocket")
	setNativeAuthentication(socket, session)
	if socket.URL.Query().Get("jwt_token") != session.Access || socket.URL.Query().Get("web_socket_id") != "dummy" {
		t.Fatal("wrong scoped WebSocket session")
	}
}
func TestServerSideRefreshRotation(t *testing.T) {
	s := nativeSession{Access: "sample-access", Refresh: "sample-a", Signed: "sample-signed", body: []byte(`{"access_token":"sample-access","refresh_token":"sample-a","value":1}`)}
	s.User.ID = 7
	p := &provisioner{sessions: map[string]nativeSession{"alice": s, "bob": {Refresh: "sample-b"}}}
	p.rememberRotation("alice", s, []*http.Cookie{{Name: nativeRefreshCookie, Value: "sample-next"}})
	p.rememberRotation("alice", s, []*http.Cookie{{Name: nativeRefreshCookie, Value: "sample-stale"}})
	var payload struct {
		Refresh string `json:"refresh_token"`
		Value   int    `json:"value"`
	}
	if json.Unmarshal(p.sessions["alice"].body, &payload) != nil || payload.Refresh != "sample-next" || payload.Value != 1 || p.sessions["bob"].Refresh != "sample-b" {
		t.Fatal("invalid rotated caller session")
	}
}
