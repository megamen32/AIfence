package market

import (
	catalog "agent/collector"
	"agent/internal/db"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/airlockrun/agentsdk"
)

// Staging preserves original queue/failure/response records in the same
// PostgreSQL tables, without starting a second network collector. The
// current legacy snapshot has exactly one generation and task per URL per
// source. Reject ambiguous identities rather than silently collapsing them.
type LegacyTask struct {
	Source     string `json:"source"`
	URL        string `json:"url"`
	Kind       string `json:"kind"`
	Generation string `json:"generation"`
	Priority   int    `json:"priority"`
	State      string `json:"state"`
	UpdatedAt  int64  `json:"updated_at"`
	EvidenceID int64  `json:"evidence_id"`
	Lane       string `json:"lane"`
}
type LegacyFailure struct {
	Source      string `json:"source"`
	URL         string `json:"url"`
	Kind        string `json:"kind"`
	Generation  string `json:"generation"`
	ErrorType   string `json:"error_type"`
	Attempts    int    `json:"attempts"`
	NextAttempt int64  `json:"next_attempt"`
	EvidenceID  int64  `json:"evidence_id"`
	State       string `json:"state"`
	Detail      string `json:"detail"`
	UpdatedAt   int64  `json:"updated_at"`
}
type LegacyTaskInput struct {
	Task    LegacyTask     `json:"task"`
	Failure *LegacyFailure `json:"failure,omitempty"`
}
type LegacyResponse struct {
	ID          int64  `json:"id"`
	Source      string `json:"source"`
	URL         string `json:"url"`
	ResponseURL string `json:"response_url"`
	CapturedAt  int64  `json:"captured_at"`
	Status      int    `json:"status"`
	Hash        string `json:"sha256"`
	Location    string `json:"location"`
	Bytes       int64  `json:"bytes"`
}
type LegacyStageInput struct {
	Source    string            `json:"source"`
	Snapshot  string            `json:"snapshot"`
	Tasks     []LegacyTaskInput `json:"tasks,omitempty"`
	Responses []LegacyResponse  `json:"responses,omitempty"`
}
type legacyStageEnvelope struct {
	Digest     string `json:"sha256"`
	Compressed string `json:"gzip_b64"`
}
type LegacyStageReceipt struct {
	Source    string `json:"source"`
	Snapshot  string `json:"snapshot"`
	Digest    string `json:"sha256"`
	Rows      int    `json:"rows"`
	Kind      string `json:"kind"`
	Duplicate bool   `json:"duplicate"`
}

func legacySource(id string) (catalog.Source, error) {
	sources, e := catalog.Sources()
	if e != nil {
		return catalog.Source{}, e
	}
	for _, s := range sources {
		if s.ID == id {
			return s, nil
		}
	}
	return catalog.Source{}, fmt.Errorf("not an approved legacy source")
}
func normalizeLegacyStage(in LegacyStageInput) (catalog.Source, []byte, []byte, int, string, error) {
	src, e := legacySource(in.Source)
	if e != nil {
		return src, nil, nil, 0, "", e
	}
	if !validHash(in.Snapshot) {
		return src, nil, nil, 0, "", fmt.Errorf("invalid snapshot digest")
	}
	if (len(in.Tasks) == 0) == (len(in.Responses) == 0) || len(in.Tasks)+len(in.Responses) > 2000 {
		return src, nil, nil, 0, "", fmt.Errorf("need 1..2000 rows of exactly one kind")
	}
	if len(in.Tasks) > 0 {
		rows := []map[string]any{}
		seen := map[string]bool{}
		for _, x := range in.Tasks {
			t := x.Task
			if t.Source != src.ID || !catalog.Allowed(src, t.URL) || seen[t.URL] || t.Kind == "" || t.Generation == "" || t.EvidenceID < 0 {
				return src, nil, nil, 0, "", fmt.Errorf("invalid or duplicate task URL")
			}
			if _, e = time.Parse(time.RFC3339, t.Generation); e != nil {
				return src, nil, nil, 0, "", fmt.Errorf("invalid task generation")
			}
			switch t.State {
			case "queued", "running", "retry", "repair", "done", "blocked", "exhausted":
			default:
				return src, nil, nil, 0, "", fmt.Errorf("unknown task state %q", t.State)
			}
			seen[t.URL] = true
			attempts := 0
			next := time.Time{}
			p := map[string]any{"url": t.URL, "state": t.State, "legacy": t, "attempts": attempts, "next_attempt": next}
			if f := x.Failure; f != nil {
				if f.Source != t.Source || f.URL != t.URL || f.Kind != t.Kind || f.Generation != t.Generation || f.Attempts < 0 || f.EvidenceID < 0 {
					return src, nil, nil, 0, "", fmt.Errorf("failure identity mismatch")
				}
				p["attempts"] = f.Attempts
				p["next_attempt"] = time.Unix(0, f.NextAttempt).UTC()
				p["failure"] = f
			}
			rows = append(rows, p)
		}
		b, e := json.Marshal(rows)
		return src, b, nil, len(rows), "tasks", e
	}
	rows := []map[string]any{}
	seen := map[int64]bool{}
	for _, r := range in.Responses {
		if r.Source != src.ID || r.ID <= 0 || seen[r.ID] || !catalog.Allowed(src, r.URL) || !catalog.Allowed(src, r.ResponseURL) || !validHash(r.Hash) || r.Bytes < 0 || r.CapturedAt <= 0 || r.Status < 100 || r.Status > 599 {
			return src, nil, nil, 0, "", fmt.Errorf("invalid response receipt")
		}
		seen[r.ID] = true
		rows = append(rows, map[string]any{"url": r.URL, "at": time.Unix(0, r.CapturedAt).UTC(), "sha256": r.Hash, "holdout": false, "html": false, "archive_state": "local_pending", "legacy": r})
	}
	b, e := json.Marshal(rows)
	return src, nil, b, len(rows), "responses", e
}
func (s *Service) stageLegacy(ctx context.Context, in LegacyStageInput, digest string) (LegacyStageReceipt, error) {
	src, tasks, responses, n, kind, e := normalizeLegacyStage(in)
	receipt := LegacyStageReceipt{Source: in.Source, Snapshot: in.Snapshot, Digest: digest, Rows: n, Kind: kind}
	if e != nil {
		return receipt, e
	}
	if s.conn == nil {
		return receipt, fmt.Errorf("native database required")
	}
	id := adapterID(src.Host)
	meta := AdaptiveStore{ID: id, Host: src.Host, Root: "https://" + src.Host + "/", Status: "migration_staged", LegacySource: src.ID, MigrationSnapshot: in.Snapshot}
	raw, e := json.Marshal(meta)
	if e != nil {
		return receipt, e
	}
	e = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		if _, e := q.CreateAdaptiveStore(ctx, db.CreateAdaptiveStoreParams{ID: id, Host: src.Host, RootUrl: meta.Root, Metadata: raw}); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		b, e := q.LockMigrationStore(ctx, id)
		if e != nil {
			return e
		}
		var old AdaptiveStore
		if e = json.Unmarshal(b, &old); e != nil {
			return e
		}
		if old.LegacySource != src.ID || old.MigrationSnapshot != in.Snapshot || old.Status != "migration_staged" || old.ActiveVersion != "" {
			return fmt.Errorf("store already has an incompatible or active migration")
		}
		count, e := q.InsertMigrationBatch(ctx, db.InsertMigrationBatchParams{StoreID: id, Snapshot: in.Snapshot, Digest: digest, RowCount: int32(n), Kind: kind})
		if e != nil {
			return e
		}
		if count == 0 {
			receipt.Duplicate = true
			return nil
		}
		if len(tasks) > 0 {
			return q.UpsertAdaptiveTasks(ctx, db.UpsertAdaptiveTasksParams{StoreID: id, Tasks: tasks})
		}
		return q.InsertAdaptiveResponses(ctx, db.InsertAdaptiveResponsesParams{StoreID: id, Receipts: responses})
	})
	return receipt, e
}
func (s *Service) StageLegacyMigration(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	defer r.Body.Close()
	var envelope legacyStageEnvelope
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(&envelope); e != nil {
		return agentsdk.NewHTTPError(400, "invalid migration envelope", nil)
	}
	compressed, e := base64.StdEncoding.DecodeString(envelope.Compressed)
	if e != nil {
		return agentsdk.NewHTTPError(400, "invalid compressed migration payload", nil)
	}
	z, e := gzip.NewReader(bytes.NewReader(compressed))
	if e != nil {
		return agentsdk.NewHTTPError(400, "invalid gzip", nil)
	}
	defer z.Close()
	raw, e := io.ReadAll(io.LimitReader(z, (4<<20)+1))
	if e != nil || len(raw) > 4<<20 || sampleHash(raw) != envelope.Digest {
		return agentsdk.NewHTTPError(400, "migration digest or size mismatch", nil)
	}
	var in LegacyStageInput
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&in); e != nil {
		return agentsdk.NewHTTPError(400, "invalid migration records: "+e.Error(), nil)
	}
	out, e := s.stageLegacy(r.Context(), in, envelope.Digest)
	if e != nil {
		return agentsdk.NewHTTPError(409, "stage migration: "+e.Error(), nil)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}
func (s *Service) LegacyMigrationStatus(ctx context.Context, _ struct{}) ([]db.LegacyMigrationCountsRow, error) {
	if s.conn == nil {
		return nil, fmt.Errorf("native database required")
	}
	return s.queries.LegacyMigrationCounts(ctx)
}
