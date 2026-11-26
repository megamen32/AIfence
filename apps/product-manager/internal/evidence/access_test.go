package evidence

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
)

func TestLocalEvidenceAccess(t *testing.T) {
	user := &wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	for _, tc := range []struct {
		name, surface, execution string
		route, approved, want    bool
	}{
		{"PM web route", "http", "request", true, false, true},
		{"HTTP-origin tool", "http", "request", false, true, false},
		{"HTTP background job", "http", "job", true, true, false},
		{"ordinary web chat", "chat", "request", false, false, false},
		{"approved chat", "chat", "request", false, true, true},
		{"MCP", "mcp", "request", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testutil.WithCaller(t.Context(), wire.Caller{Kind: "user", Access: wire.AccessUser, User: user, Initiator: user,
				Origin: wire.CallerOrigin{Interface: tc.surface, Execution: tc.execution}})
			handler := func(_ http.ResponseWriter, r *http.Request) error {
				if got := localAccess(r.Context(), func(context.Context) bool { return tc.approved }); got != tc.want {
					t.Errorf("local access = %t, want %t", got, tc.want)
				}
				return nil
			}
			r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
			if tc.route {
				_ = LocalHTTP(handler)(httptest.NewRecorder(), r)
			} else {
				_ = handler(httptest.NewRecorder(), r)
			}
		})
	}
}

func TestPrivateImportRequiresLocalAccess(t *testing.T) {
	user := &wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	ctx := testutil.WithCaller(t.Context(), wire.Caller{Kind: "user", Access: wire.AccessUser, User: user, Initiator: user,
		Origin: wire.CallerOrigin{Interface: "chat", Execution: "request"}})
	input := ImportInput{Document: `{"schema_version":"aifence.case.v1","source_policy":"local_project_only","as_of":"2025-01-02T00:00:00Z","batches":[{"schema_version":"aifence.v1","source":"sales","as_of":"2025-01-02T00:00:00Z","status":"ok","observations":[{"id":"x","product_key":"kaspersky_small_office_security","metric_key":"orders","kind":"metric","period":{"from":"2025-01-01","to":"2025-01-01"},"source_ref":{"source_id":"example","locator":"row"},"status":"observed","value":1}]}]}`}
	if _, err := (&Service{}).Import(ctx, input); !errors.Is(err, ErrLocalOnly) {
		t.Fatalf("private import came through normal chat: %v", err)
	}
}
