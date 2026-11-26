package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent/internal/evidence"
	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
)

const alice = "00000000-0000-0000-0000-000000000001"
const bob = "00000000-0000-0000-0000-000000000002"

func reviewCaller(t *testing.T, id, surface, execution string) context.Context {
	t.Helper()
	u := &wire.CallerUser{ID: id, PlatformMember: true}
	return testutil.WithCaller(t.Context(), wire.Caller{Kind: "user", Access: wire.AccessUser, User: u, Initiator: u, Origin: wire.CallerOrigin{Interface: surface, Execution: execution}})
}

func TestPrivateReviewSignedInOwnerOnly(t *testing.T) {
	h := evidence.LocalHTTP(privatePage(func(ctx context.Context, owner string, offset int) ([]privateEntry, error) {
		if offset != 0 {
			t.Fatalf("offset %d", offset)
		}
		return []privateEntry{{Kind: "card", ID: "card&" + owner, Title: "<script>title-" + owner + "</script>", Date: "2025-01-01"}, {Kind: "snapshot", ID: "snapshot-" + owner, Date: "2025-01-01", Count: 3}}, nil
	}))
	for _, tc := range []struct{ self, other string }{{alice, bob}, {bob, alice}} {
		req := httptest.NewRequest(http.MethodGet, "/private-review?owner="+tc.other+"&id="+tc.other, nil).WithContext(reviewCaller(t, tc.self, "http", "request"))
		w := httptest.NewRecorder()
		if err := h(w, req); err != nil {
			t.Fatal(err)
		}
		body := w.Body.String()
		if w.Code != 200 || strings.Contains(body, tc.other) || !strings.Contains(body, "title-"+tc.self) || !strings.Contains(body, "./?id=card%26"+tc.self) || !strings.Contains(body, "./?snapshot=snapshot-"+tc.self) || strings.Contains(body, "<script>title") {
			t.Fatalf("owner-scoped page or output escaping failed; status=%d", w.Code)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("private response may be cached or forwarded as a referrer")
		}
	}
}
func TestPrivateBoardRejectsModelAndUnmarkedOriginsBeforeRead(t *testing.T) {
	for _, tc := range []struct {
		name, surface, execution string
		wrap, anonymous          bool
	}{
		{"model chat", "chat", "request", true, false},
		{"MCP", "mcp", "request", true, false},
		{"unmarked tool", "http", "request", false, false},
		{"job", "http", "job", true, false},
		{"no human", "", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := privatePage(func(context.Context, string, int) ([]privateEntry, error) {
				t.Fatal("unauthorized query")
				return nil, nil
			})
			if tc.wrap {
				h = evidence.LocalHTTP(h)
			}
			ctx := t.Context()
			if tc.anonymous {
				ctx = testutil.WithCaller(ctx, wire.Caller{Kind: "anonymous", Access: wire.AccessPublic, Origin: wire.CallerOrigin{Interface: "http", Execution: "request"}})
			} else {
				ctx = reviewCaller(t, alice, tc.surface, tc.execution)
			}
			w := httptest.NewRecorder()
			if err := h(w, httptest.NewRequest("GET", "/private-review", nil).WithContext(ctx)); err != nil {
				t.Fatal(err)
			}
			if w.Code != 403 || strings.Contains(w.Body.String(), alice) {
				t.Fatalf("invalid denial: %d", w.Code)
			}
		})
	}
}
func TestPrivateReviewPaginationAndErrors(t *testing.T) {
	for _, tc := range []struct {
		method, query    string
		wantCode, offset int
		fetchErr         bool
	}{
		{"GET", "?page=2", 200, privatePageSize, false}, {"GET", "?page=-3", 400, -1, false}, {"GET", "?page=a", 400, -1, false}, {"POST", "", 405, -1, false}, {"GET", "", 500, 0, true},
	} {
		t.Run(tc.method+tc.query, func(t *testing.T) {
			called := false
			h := evidence.LocalHTTP(privatePage(func(_ context.Context, _ string, offset int) ([]privateEntry, error) {
				called = true
				if offset != tc.offset {
					t.Fatalf("offset %d, want %d", offset, tc.offset)
				}
				if tc.fetchErr {
					return nil, errors.New("SQL-PRIVATE-ERROR")
				}
				out := make([]privateEntry, privatePageSize+1)
				out[len(out)-1] = privateEntry{Kind: "card", Title: "NEXT-PAGE-SENTINEL"}
				return out, nil
			}))
			w := httptest.NewRecorder()
			if err := h(w, httptest.NewRequest(tc.method, "/private-review"+tc.query, nil).WithContext(reviewCaller(t, alice, "http", "request"))); err != nil {
				t.Fatal(err)
			}
			b := w.Body.String()
			if w.Code != tc.wantCode || called != (tc.offset >= 0) || strings.Contains(b, "NEXT-PAGE-SENTINEL") || strings.Contains(b, "SQL-PRIVATE-ERROR") {
				t.Fatalf("status %d, database call %t", w.Code, called)
			}
			if w.Code == 200 && (!strings.Contains(b, "?page=1") || !strings.Contains(b, "?page=3")) {
				t.Error("missing pagination")
			}
		})
	}
}
