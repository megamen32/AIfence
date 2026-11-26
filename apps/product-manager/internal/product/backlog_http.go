package product

import (
	"context"
	"net/http"

	"agent/internal/evidence"
	"github.com/airlockrun/agentsdk"
)

type backlogApprovalKey struct{}

// BacklogApprovalHTTP is used only for the panel's separate approval POST.
// The SDK does not expose a trusted hosted chat approval receipt to app tools.
func BacklogApprovalHTTP(next agentsdk.RouteHandlerFunc) agentsdk.RouteHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if r.Method != http.MethodPost || !evidence.IsLocalHTTP(r.Context()) {
			http.Error(w, "Подтвердите задачу на карточке Product Manager.", http.StatusForbidden)
			return nil
		}
		ctx := context.WithValue(r.Context(), backlogApprovalKey{}, true)
		return next(w, r.WithContext(ctx))
	}
}
