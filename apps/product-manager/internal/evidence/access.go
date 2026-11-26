package evidence

import (
	"context"
	"errors"
	"net/http"

	"github.com/airlockrun/agentsdk"
)

type localHTTPKey struct{}

// LocalHTTP marks a user-facing PM route. SDK tools do not inherit this marker
// merely because an invocation was transported over HTTP.
func LocalHTTP(next agentsdk.RouteHandlerFunc) agentsdk.RouteHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		caller := agentsdk.CallerFromContext(ctx)
		origin := caller.Origin()
		if _, ok := caller.User(); ok && origin.Interface == agentsdk.InterfaceHTTP && origin.Execution == agentsdk.ExecutionRequest {
			r = r.WithContext(context.WithValue(ctx, localHTTPKey{}, true))
		}
		return next(w, r)
	}
}

// IsLocalHTTP reports whether the request entered through an authenticated PM route.
func IsLocalHTTP(ctx context.Context) bool {
	return ctx.Value(localHTTPKey{}) == true
}

func (s *Service) CanReadLocal(ctx context.Context) bool {
	return s.localRead(ctx)
}

func (s *Service) localRead(ctx context.Context) bool {
	return localAccess(ctx, func(ctx context.Context) bool {
		if s.privateRoute == nil {
			return false
		}
		value, err := s.privateRoute.Get(ctx)
		return err == nil && value == "approved"
	})
}

func localAccess(ctx context.Context, approvedModel func(context.Context) bool) bool {
	caller := agentsdk.CallerFromContext(ctx)
	if _, ok := caller.User(); !ok {
		return false
	}
	switch caller.Origin().Interface {
	case agentsdk.InterfaceHTTP:
		return IsLocalHTTP(ctx) && caller.Origin().Execution == agentsdk.ExecutionRequest
	case agentsdk.InterfaceChat:
		return approvedModel(ctx)
	}
	return false
}

var ErrLocalOnly = errors.New("этот материал доступен в локальной веб-форме Product Manager или чате с разрешённым маршрутом модели")

func (s *Service) checkVisibility(ctx context.Context, policy string) error {
	switch policy {
	case "":
		return nil
	case "local_project_only":
		if s.localRead(ctx) {
			return nil
		}
		return ErrLocalOnly
	default:
		return errors.New("неизвестный режим доступа к данным")
	}
}
