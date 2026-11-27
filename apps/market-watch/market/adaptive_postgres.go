package market

import (
	catalog "agent/collector"
	"agent/internal/db"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/airlockrun/agentsdk"
)

// PostgreSQL is authoritative for adaptive state. The legacy JSON files
// are read only when a store has not yet been migrated by an actual job.
type adaptiveLeaseKey struct{}
type adaptiveLease struct {
	ID, Job string
	Attempt int32
}

func (s *Service) withAdaptiveLease(ctx context.Context, id, job string, attempt int, fn func(context.Context) (AdaptiveStoreView, error)) (AdaptiveStoreView, error) {
	if s.conn == nil {
		return fn(ctx)
	}
	owner, e := s.queries.ClaimAdaptiveLease(ctx, db.ClaimAdaptiveLeaseParams{StoreID: id, JobID: job, Attempt: int32(attempt), ExpiresAt: time.Now().UTC().Add(12 * time.Minute)})
	if errors.Is(e, sql.ErrNoRows) {
		v, err := s.readAdaptive(ctx, id)
		if err != nil {
			return AdaptiveStoreView{}, fmt.Errorf("store already has an active job")
		}
		out := v.view()
		out.Note = "Existing store job owns the lease; no duplicate processing started."
		return out, nil
	}
	if e != nil {
		return AdaptiveStoreView{}, e
	}
	if owner != job {
		return AdaptiveStoreView{}, fmt.Errorf("unexpected job ownership")
	}
	lease := adaptiveLease{id, job, int32(attempt)}
	defer func() {
		// Context cancellation leaves a bounded durable lease, not detached work.
		if ctx.Err() == nil {
			_ = s.queries.ReleaseAdaptiveLease(ctx, db.ReleaseAdaptiveLeaseParams{StoreID: id, JobID: job, Attempt: int32(attempt)})
		}
	}()
	return fn(context.WithValue(ctx, adaptiveLeaseKey{}, lease))
}
func (s *Service) pgIDs(ctx context.Context, legacy []string) ([]string, error) {
	if s.conn == nil {
		return legacy, nil
	}
	ids, e := s.queries.AdaptiveStoreIDs(ctx)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range legacy {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Strings(ids)
	return ids, nil
}
func receiptKey(r AdapterReceipt) string {
	return r.URL + "|" + r.At.UTC().Format(time.RFC3339Nano) + "|" + r.Hash
}

func (s *Service) readAdaptivePG(ctx context.Context, id string) (AdaptiveStore, error) {
	var a AdaptiveStore
	e := s.conn.Transaction(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		meta, e := q.AdaptiveStoreMeta(ctx, id)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(meta.Metadata, &a); e != nil {
			return e
		}
		if a.LegacySource != "" {
			return fmt.Errorf("legacy migration is staged and cannot be collected or learned before cutover")
		}
		a.Revision = meta.Revision
		a.Tasks = map[string]AdapterTask{}
		a.savedTasks = map[string]string{}
		a.savedReceipts = map[string]bool{}
		tasks, e := q.AdaptiveTaskRows(ctx, id)
		if e != nil {
			return e
		}
		for _, raw := range tasks {
			var t AdapterTask
			if e = json.Unmarshal(raw, &t); e != nil {
				return e
			}
			a.Tasks[t.URL] = t
			b, _ := json.Marshal(t)
			a.savedTasks[t.URL] = string(b)
		}
		receipts, e := q.AdaptiveResponseRows(ctx, id)
		if e != nil {
			return e
		}
		for _, raw := range receipts {
			var r AdapterReceipt
			if e = json.Unmarshal(raw, &r); e != nil {
				return e
			}
			a.Samples = append(a.Samples, r)
			a.savedReceipts[receiptKey(r)] = true
		}
		return nil
	})
	return a, e
}

func (s *Service) saveAdaptivePG(ctx context.Context, a *AdaptiveStore) error {
	return s.saveAdaptivePGWithOffers(ctx, a, nil)
}
func (s *Service) saveAdaptivePGWithOffers(ctx context.Context, a *AdaptiveStore, offers []catalog.Offer) error {
	lease, ok := ctx.Value(adaptiveLeaseKey{}).(adaptiveLease)
	if !ok || lease.ID != a.ID {
		return fmt.Errorf("adaptive write requires an owned native job lease")
	}
	// Store small metadata separately, write only changed tasks/new response
	// receipts. Existing HTML gzip content remains in native file storage.
	meta := *a
	meta.Tasks = nil
	meta.Samples = nil
	b, e := json.Marshal(meta)
	if e != nil {
		return e
	}
	tasks := []AdapterTask{}
	receipts := []AdapterReceipt{}
	taskSnapshot := map[string]string{}
	receiptSnapshot := map[string]bool{}
	for u, t := range a.Tasks {
		raw, e := json.Marshal(t)
		if e != nil {
			return e
		}
		taskSnapshot[u] = string(raw)
		if a.savedTasks[u] != string(raw) {
			tasks = append(tasks, t)
		}
	}
	for _, r := range a.Samples {
		k := receiptKey(r)
		receiptSnapshot[k] = true
		if !a.savedReceipts[k] {
			receipts = append(receipts, r)
		}
	}
	taskBytes, e := json.Marshal(tasks)
	if e != nil {
		return e
	}
	responseBytes, e := json.Marshal(receipts)
	if e != nil {
		return e
	}
	var newRev int64
	e = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		if _, e := q.CheckAdaptiveLease(ctx, db.CheckAdaptiveLeaseParams{StoreID: lease.ID, JobID: lease.Job, Attempt: lease.Attempt}); e != nil {
			return fmt.Errorf("adaptive job lost its lease: %w", e)
		}
		var err error
		if a.Revision == 0 {
			newRev, err = q.CreateAdaptiveStore(ctx, db.CreateAdaptiveStoreParams{ID: a.ID, Host: a.Host, RootUrl: a.Root, Metadata: b})
		} else {
			newRev, err = q.UpdateAdaptiveStore(ctx, db.UpdateAdaptiveStoreParams{ID: a.ID, ExpectedRevision: a.Revision, Metadata: b})
		}
		if err != nil {
			return fmt.Errorf("adaptive state revision conflict: %w", err)
		}
		if len(tasks) > 0 {
			if e = q.UpsertAdaptiveTasks(ctx, db.UpsertAdaptiveTasksParams{StoreID: a.ID, Tasks: taskBytes}); e != nil {
				return e
			}
		}
		if len(receipts) > 0 {
			if e = q.InsertAdaptiveResponses(ctx, db.InsertAdaptiveResponsesParams{StoreID: a.ID, Receipts: responseBytes}); e != nil {
				return e
			}
		}
		if len(offers) > 0 {
			raw, e := json.Marshal(offers)
			if e != nil {
				return e
			}
			if e = q.ImportObservations(ctx, raw); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	a.Revision = newRev
	a.savedTasks = taskSnapshot
	a.savedReceipts = receiptSnapshot
	return nil
}
func (s *Service) persistAdaptiveVersion(ctx context.Context, a AdaptiveStore, r AdapterVersionRecord) error {
	if s.conn == nil {
		return s.writeJSON(ctx, adapterPath(a.ID)+"/versions/"+r.Version+".json", r)
	}
	if !r.Decision.Accepted {
		return fmt.Errorf("unvalidated version")
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	lease, ok := ctx.Value(adaptiveLeaseKey{}).(adaptiveLease)
	if !ok || lease.ID != a.ID {
		return fmt.Errorf("version write requires store lease")
	}
	return s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		if _, e := q.CheckAdaptiveLease(ctx, db.CheckAdaptiveLeaseParams{StoreID: lease.ID, JobID: lease.Job, Attempt: lease.Attempt}); e != nil {
			return e
		}
		return q.InsertAdaptiveVersion(ctx, db.InsertAdaptiveVersionParams{StoreID: a.ID, Version: r.Version, Payload: b})
	})
}
