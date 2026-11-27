package market

import (
	catalog "agent/collector"
	"agent/internal/db"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

type AdaptiveJobResult struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Storage        string `json:"storage"`
	ActiveVersion  string `json:"active_version"`
	Archived       int    `json:"archived_responses"`
	Queued         int    `json:"queued"`
	ObservedOffers int    `json:"observed_offers"`
	Note           string `json:"note"`
}

func jobResult(v AdaptiveStoreView) AdaptiveJobResult {
	return AdaptiveJobResult{ID: v.ID, Status: v.Status, Storage: v.Storage, ActiveVersion: v.ActiveVersion, Archived: v.Archived, Queued: v.Queued, ObservedOffers: v.ObservedOffers, Note: v.Note}
}
func (s *Service) AddAdaptiveJobResult(ctx context.Context, j agentsdk.JobContext, in AddStoreInput) (AdaptiveJobResult, error) {
	v, e := s.AddAdaptiveJob(ctx, j, in)
	return jobResult(v), e
}
func (s *Service) RepairAdaptiveJobResult(ctx context.Context, j agentsdk.JobContext, in AdapterActionInput) (AdaptiveJobResult, error) {
	v, e := s.RepairAdaptiveJob(ctx, j, in)
	return jobResult(v), e
}

type adaptiveProgressKey struct{}
type adaptiveOneSourceKey struct{}
type AdaptiveTickResult struct {
	Advanced int    `json:"advanced"`
	Status   string `json:"status"`
}

func adaptiveProgress(ctx context.Context, phase, message string, completed, total int64) error {
	if j, ok := ctx.Value(adaptiveProgressKey{}).(agentsdk.JobContext); ok {
		return j.ReportProgress(ctx, agentsdk.JobProgress{Phase: phase, Message: message, Completed: completed, Total: total})
	}
	return nil
}
func (s *Service) AddAdaptiveJob(ctx context.Context, j agentsdk.JobContext, in AddStoreInput) (AdaptiveStoreView, error) {
	root := strings.TrimSpace(in.URL)
	if e := catalog.PublicStoreURL(root); e != nil {
		return AdaptiveStoreView{}, e
	}
	u, e := url.Parse(root)
	if e != nil {
		return AdaptiveStoreView{}, e
	}
	ctx = context.WithValue(ctx, adaptiveProgressKey{}, j)
	return s.withAdaptiveLease(ctx, adapterID(u.Host), j.ID, j.Attempt, func(c context.Context) (AdaptiveStoreView, error) {
		if e := adaptiveProgress(c, "study", "Loading store and existing evidence", 0, 0); e != nil {
			return AdaptiveStoreView{}, e
		}
		return s.AddAdaptiveStore(c, in)
	})
}
func (s *Service) RepairAdaptiveJob(ctx context.Context, j agentsdk.JobContext, in AdapterActionInput) (AdaptiveStoreView, error) {
	if !validAdapterID(in.ID) {
		return AdaptiveStoreView{}, fmt.Errorf("invalid adapter ID")
	}
	ctx = context.WithValue(ctx, adaptiveProgressKey{}, j)
	return s.withAdaptiveLease(ctx, in.ID, j.ID, j.Attempt, func(c context.Context) (AdaptiveStoreView, error) {
		if e := adaptiveProgress(c, "resume", "Resuming the archived learning corpus", 0, 0); e != nil {
			return AdaptiveStoreView{}, e
		}
		return s.RepairAdaptiveStore(c, in)
	})
}
func (s *Service) RollbackAdaptiveOwned(ctx context.Context, in AdapterRollbackInput) (AdaptiveStoreView, error) {
	if !validAdapterID(in.ID) {
		return AdaptiveStoreView{}, fmt.Errorf("invalid adapter ID")
	}
	return s.withAdaptiveLease(ctx, in.ID, uuid.NewString(), 1, func(c context.Context) (AdaptiveStoreView, error) { return s.RollbackAdaptiveStore(c, in) })
}
func (s *Service) dueAdaptive(ctx context.Context, a AdaptiveStore, at time.Time) ([]AdapterTask, error) {
	rows, e := s.queries.DueAdaptiveTasks(ctx, db.DueAdaptiveTasksParams{StoreID: a.ID, AsOf: at, MaxRows: nativeWindowSize})
	if e != nil {
		return nil, e
	}
	out := make([]AdapterTask, 0, len(rows))
	for _, raw := range rows {
		var t AdapterTask
		if e = json.Unmarshal(raw, &t); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, nil
}

// This is a bounded step driven by the platform cron, not a long-lived
// goroutine. The app's DB owns URL due times, attempts and parser state.
func (s *Service) AdvanceAdaptiveJob(ctx context.Context, j agentsdk.JobContext, _ struct{}) (AdaptiveTickResult, error) {
	ctx = context.WithValue(ctx, adaptiveProgressKey{}, j)
	ids, e := s.readAdaptiveIDs(ctx)
	if one, ok := ctx.Value(adaptiveOneSourceKey{}).(string); ok {
		ids = []string{one}
	}
	if e != nil {
		return AdaptiveTickResult{}, e
	}
	if len(ids) == 0 {
		return AdaptiveTickResult{Status: "idle"}, nil
	}
	for _, id := range ids {
		a, e := s.readAdaptive(ctx, id)
		if e != nil {
			return AdaptiveTickResult{}, e
		}
		if a.PauseUntil.After(s.now()) {
			continue
		}
		if a.ActiveVersion == "" {
			// Resume a recorded, unfinished study after a bounded cooldown.
			// A rejected/exhausted candidate is not blindly retried by cron.
			if a.Revision == 0 || a.Study == nil || a.Study.Round >= maxLearningRounds || a.LastStudy.IsZero() || s.now().Sub(a.LastStudy) < 15*time.Minute {
				continue
			}
			_, e = s.RepairAdaptiveJob(ctx, j, AdapterActionInput{ID: id})
			if e != nil {
				return AdaptiveTickResult{}, e
			}
			return AdaptiveTickResult{Advanced: 1, Status: "resumed_study"}, nil
		}
		due, e := s.dueAdaptive(ctx, a, s.now())
		if e != nil {
			return AdaptiveTickResult{}, e
		}
		if len(due) == 0 && !a.ReplayPending {
			clusters := map[string]int{}
			for _, t := range a.Tasks {
				if t.State == "repair" {
					clusters[t.Template]++
				}
			}
			repeated := false
			for _, n := range clusters {
				if n >= 3 {
					repeated = true
				}
			}
			if repeated && studyKey(a) != a.LastStudyKey && s.now().Sub(a.LastStudy) >= time.Hour {
				_, e = s.RepairAdaptiveJob(ctx, j, AdapterActionInput{ID: id})
				if e != nil {
					return AdaptiveTickResult{}, e
				}
				return AdaptiveTickResult{Advanced: 1, Status: "repair_step"}, nil
			}
			continue
		}
		_, e = s.withAdaptiveLease(ctx, id, j.ID, j.Attempt, func(c context.Context) (AdaptiveStoreView, error) {
			if e := adaptiveProgress(c, "crawl", "Processing a bounded ready URL batch", 0, 0); e != nil {
				return a.view(), e
			}
			current, e := s.readAdaptive(c, id)
			if e != nil {
				return a.view(), e
			}
			if current.ReplayPending {
				return s.publishAdaptive(c, &current)
			}
			v, e := s.version(c, current, current.ActiveVersion)
			if e != nil {
				return current.view(), e
			}
			src := current.source()
			src.Adaptive = &v.Spec
			src.Adapter = "adaptive"
			r := s.refreshAdaptive(c, src, s.now())
			if len(r.Offers) > 0 {
				if e = s.storeOffers(c, r.Offers); e != nil {
					return current.view(), e
				}
			}
			latest, e := s.readAdaptive(c, id)
			if e != nil {
				return current.view(), e
			}
			out := latest.view()
			out.ObservedOffers = len(r.Offers)
			if len(r.Errors) > 0 {
				out.Note = strings.Join(r.Errors, "; ")
			}
			return out, nil
		})
		if e != nil {
			return AdaptiveTickResult{}, e
		}
		return AdaptiveTickResult{Advanced: 1, Status: "bounded_step"}, nil
	}
	return AdaptiveTickResult{Status: "idle"}, nil
}

// Version 2 uses one native Job for parallel independent source windows.
// Store-level SQL leases protect against overlapping old v1 deliveries.
func (s *Service) AdvanceAdaptiveJobV2(ctx context.Context, j agentsdk.JobContext, _ struct{}) (AdaptiveTickResult, error) {
	ids, e := s.readAdaptiveIDs(ctx)
	if e != nil {
		return AdaptiveTickResult{}, e
	}
	if len(ids) == 0 {
		return AdaptiveTickResult{Status: "idle"}, nil
	}
	type result struct {
		v AdaptiveTickResult
		e error
	}
	ch := make(chan result, len(ids))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				ch <- result{e: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			v, e := s.AdvanceAdaptiveJob(context.WithValue(ctx, adaptiveOneSourceKey{}, id), j, struct{}{})
			ch <- result{v, e}
		}()
	}
	wg.Wait()
	close(ch)
	out := AdaptiveTickResult{Status: "idle"}
	var errs []error
	for r := range ch {
		out.Advanced += r.v.Advanced
		if r.e != nil {
			errs = append(errs, r.e)
		}
	}
	if out.Advanced > 0 {
		out.Status = "bounded_parallel_steps"
	}
	return out, errors.Join(errs...)
}
