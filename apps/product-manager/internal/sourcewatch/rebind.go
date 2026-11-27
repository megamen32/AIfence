package sourcewatch

import (
	"agent/internal/db"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// RebindInput replaces only an already configured source's bound operation.
// Empty fields keep their current binding; adding/removing a source is not a rebind.
type RebindInput struct {
	WatchID         string `json:"watch_id"`
	MetrikaBinding  string `json:"metrika_binding,omitempty"`
	MarketBinding   string `json:"market_binding,omitempty"`
	WordstatBinding string `json:"wordstat_binding,omitempty"`
	TrendsBinding   string `json:"trends_binding,omitempty"`
}

type bindingRepository interface {
	GetSourceWatch(context.Context, db.GetSourceWatchParams) (db.PmSourceWatch, error)
	RebindSourceWatch(context.Context, db.RebindSourceWatchParams) (db.PmSourceWatch, error)
}

// Rebind changes no schedule, owner, status or historical signal. The next
// already queued delivery loads the current bindings by the unchanged watch ID.
func (s *Service) Rebind(ctx context.Context, in RebindInput) (WatchState, error) {
	return rebind(ctx, s.queries, in)
}

func rebind(ctx context.Context, repo bindingRepository, in RebindInput) (WatchState, error) {
	owner, err := human(ctx)
	if err != nil {
		return WatchState{}, err
	}
	if in.WatchID == "" {
		return WatchState{}, errors.New("watch_id is required")
	}
	row, err := repo.GetSourceWatch(ctx, db.GetSourceWatchParams{OwnerID: owner, WatchID: in.WatchID})
	if err != nil {
		return WatchState{}, err
	}
	var config Window
	if err := json.Unmarshal(row.Config, &config); err != nil {
		return WatchState{}, err
	}
	patch := map[string]string{}
	for _, source := range []struct {
		key     string
		current *string
		next    string
	}{
		{"metrika_binding", &config.MetrikaBinding, in.MetrikaBinding},
		{"market_binding", &config.MarketBinding, in.MarketBinding},
		{"wordstat_binding", &config.WordstatBinding, in.WordstatBinding},
		{"trends_binding", &config.TrendsBinding, in.TrendsBinding},
	} {
		if source.next == "" {
			continue
		}
		if *source.current == "" {
			return WatchState{}, errors.New("cannot rebind a source that this watch does not use")
		}
		*source.current = source.next
		patch[source.key] = source.next
	}
	if len(patch) == 0 {
		return WatchState{}, errors.New("provide at least one replacement binding ID")
	}
	if _, err := config.Validate(); err != nil {
		return WatchState{}, err
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return WatchState{}, err
	}
	updated, err := repo.RebindSourceWatch(ctx, db.RebindSourceWatchParams{
		OwnerID: owner, WatchID: in.WatchID,
		BindingPatch: raw, ExpectedConfig: row.Config,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return WatchState{}, errors.New("source watch changed; inspect it and retry rebinding")
	}
	if err != nil {
		return WatchState{}, err
	}
	return state(updated)
}
