package product

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"agent/internal/decision"
	"agent/internal/evidence"
	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
)

func TestBacklogRequiresApprovalRoute(t *testing.T) {
	u := wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	version := int64(1)
	in := decision.SendInput{HypothesisID: "proposal", Confirmed: true, ExpectedVersion: &version}
	caller := wire.Caller{Kind: "user", Access: wire.AccessUser, User: &u, Initiator: &u,
		Origin: wire.CallerOrigin{Interface: "chat", Execution: "request"}}
	ctx := testutil.WithCaller(t.Context(), caller)
	if _, err := (Service{}).Send(ctx, in); err == nil {
		t.Fatal("a model-supplied confirmed flag escaped the approval route")
	}
	entered := 0
	next := func(_ http.ResponseWriter, r *http.Request) error {
		if r.Context().Value(backlogApprovalKey{}) != true {
			t.Fatal("approval route did not mark the request")
		}
		entered++
		return nil
	}
	handler := evidence.LocalHTTP(BacklogApprovalHTTP(next))
	for _, tc := range []struct {
		surface, method string
		status          int
	}{
		{"chat", http.MethodPost, http.StatusForbidden},
		{"http", http.MethodGet, http.StatusForbidden},
		{"http", http.MethodPost, http.StatusOK},
	} {
		caller.Origin.Interface = tc.surface
		req := httptest.NewRequest(tc.method, "/approve", nil).WithContext(testutil.WithCaller(t.Context(), caller))
		w := httptest.NewRecorder()
		if err := handler(w, req); err != nil || w.Code != tc.status {
			t.Fatalf("%s %s: status %d, error %v", tc.surface, tc.method, w.Code, err)
		}
	}
	if entered != 1 {
		t.Fatalf("expected exactly one owner approval route call, got %d", entered)
	}
}
