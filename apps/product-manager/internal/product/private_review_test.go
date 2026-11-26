package product

import (
	"encoding/json"
	"testing"

	"agent/internal/evidence"
)

func TestPrivateReviewURLHasOnlyApplicationLocation(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://pm.example.org", "https://pm.example.org/private-review"},
		{"https://pm.example.org/path/?snapshot=private-secret#another-secret", "https://pm.example.org/path/private-review"},
		{"http://product-manager.localhost:8080", "http://product-manager.localhost:8080/private-review"},
		{"http://127.0.0.1:8000", "http://127.0.0.1:8000/private-review"},
		{"https://name:password@pm.example.org", ""},
		{"http://external.example.org", ""},
		{"https://", ""},
	} {
		t.Run(tc.base, func(t *testing.T) {
			got, err := privateReviewURL(tc.base)
			if tc.want == "" {
				if err == nil || got.URL != "" {
					t.Fatalf("unexpected valid link: %+v", got)
				}
				return
			}
			if err != nil || got.URL != tc.want {
				t.Fatalf("got %q (%v), want %q", got.URL, err, tc.want)
			}
			raw, err := json.Marshal(got)
			if err != nil || string(raw) != `{"url":"`+tc.want+`"}` {
				t.Fatalf("unexpected fields %s", raw)
			}
		})
	}
	if v, err := PrivateReviewLink(t.Context(), evidence.Empty{}); err == nil || v.URL != "" {
		t.Fatalf("unexpected fallback: %+v", v)
	}
}
