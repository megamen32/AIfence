package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliverySameOrigin(t *testing.T) {
	const publicHost = "product-manager.aifence.example"
	const internalHost = "app-runtime:8080"
	cases := []struct {
		name      string
		origin    string
		forwarded string
		want      int
	}{
		{"public browser origin", "https://" + publicHost, publicHost, http.StatusNoContent},
		{"direct local origin", "http://" + internalHost, "", http.StatusNoContent},
		{"authorized client without Origin", "", publicHost, http.StatusNoContent},
		{"different origin", "https://other.example", publicHost, http.StatusForbidden},
		{"internal host is not the public origin", "http://" + internalHost, publicHost, http.StatusForbidden},
		{"forwarded host list is not a host", "https://" + publicHost, publicHost + ", other.example", http.StatusForbidden},
		{"origin with a path", "https://" + publicHost + "/path", publicHost, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+internalHost+"/delivery/sync", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Forwarded-Host", tc.forwarded)
			resp := httptest.NewRecorder()
			handler := sameOrigin(func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusNoContent)
				return nil
			})
			if err := handler(resp, req); err != nil {
				t.Fatal(err)
			}
			if resp.Code != tc.want {
				t.Errorf("status = %d, want %d", resp.Code, tc.want)
			}
		})
	}
}
