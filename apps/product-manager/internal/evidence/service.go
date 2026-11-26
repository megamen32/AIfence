package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agent/internal/db"
	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

const MaxSize = 1024 * 1024

type Service struct {
	db           *agentsdk.AgentDB
	changes      *agentsdk.UIChangeTopic
	privateRoute *agentsdk.EnvVarHandle
}

func New(database *agentsdk.AgentDB, changes *agentsdk.UIChangeTopic, privateRoute *agentsdk.EnvVarHandle) *Service {
	return &Service{database, changes, privateRoute}
}

type Empty struct{}
type ImportInput struct {
	Document string `json:"document" jsonschema:"description=JSON text of an authorized evidence batch or a whole case/workflow snapshot. Never include credentials or a held-out answer."`
}
type Summary struct {
	SnapshotID string `json:"snapshot_id"`
	AsOf       string `json:"as_of"`
	Count      int    `json:"count"`
}
type Index struct {
	Snapshots []Summary `json:"snapshots"`
}
type Query struct {
	SnapshotID string   `json:"snapshot_id"`
	Source     string   `json:"source,omitempty"`
	MetricKey  string   `json:"metric_key,omitempty"`
	ProductKey string   `json:"product_key,omitempty"`
	Status     string   `json:"status,omitempty"`
	Value      string   `json:"value,omitempty"`
	Offset     int      `json:"offset,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	IDs        []string `json:"ids,omitempty"`
}
type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type SourceRef struct {
	SourceID string `json:"source_id"`
	Locator  string `json:"locator"`
	URL      string `json:"url,omitempty"`
	Revision string `json:"revision,omitempty"`
}
type Observation struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	ProductKey string          `json:"product_key"`
	MetricKey  string          `json:"metric_key"`
	Value      json.RawMessage `json:"value"`
	Unit       string          `json:"unit"`
	Period     Period          `json:"period"`
	Ref        SourceRef       `json:"source_ref"`
	Status     string          `json:"status"`
}
type Batch struct {
	Schema       string            `json:"schema_version"`
	Source       string            `json:"source"`
	Status       string            `json:"status"`
	AsOf         string            `json:"as_of"`
	Observations []json.RawMessage `json:"observations"`
	Warnings     []string          `json:"warnings"`
}
type Document struct {
	SourcePolicy string   `json:"source_policy,omitempty"`
	Schema       string   `json:"schema_version"`
	AsOf         string   `json:"as_of"`
	Batches      []Batch  `json:"batches"`
	Warnings     []string `json:"warnings,omitempty"`
}
type Row struct {
	WindowContrast *WindowContrast `json:"window_contrast,omitempty"`
	BatchStatus    string          `json:"-"`
	BatchAsOf      string          `json:"-"`
	Kind           string          `json:"kind"`
	Segment        string          `json:"segment,omitempty"`
	Source         string          `json:"source"`
	ID             string          `json:"id"`
	Product        string          `json:"product"`
	Metric         string          `json:"metric"`
	Value          string          `json:"value"`
	Unit           string          `json:"unit"`
	Period         Period          `json:"period"`
	Ref            SourceRef       `json:"source_ref"`
	Status         string          `json:"status"`
	Details        string          `json:"details_json,omitempty"`
	Caveats        []string        `json:"caveats,omitempty"`
	SampleSize     *int            `json:"sample_size,omitempty"`
}
type Result struct {
	Summary
	ImportedAt *time.Time `json:"imported_at,omitempty"`
	Total      int        `json:"total_matched"`
	Rows       []Row      `json:"rows"`
	Warnings   []string   `json:"warnings"`
}

func human(ctx context.Context) (uuid.UUID, error) {
	u, ok := agentsdk.CallerFromContext(ctx).User()
	if !ok {
		return uuid.Nil, errors.New("требуется вход в Airlock")
	}
	return uuid.Parse(u.ID)
}
func validDate(s string) bool { _, e := time.Parse("2006-01-02", s); return e == nil }
func in(value string, options ...string) bool {
	for _, o := range options {
		if o == value {
			return true
		}
	}
	return false
}

func parse(raw []byte) (Document, int, error) {
	var d Document
	if len(raw) > MaxSize {
		return d, 0, errors.New("файл превышает 1 МиБ")
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, 0, err
	}
	if d.SourcePolicy != "" && d.SourcePolicy != "local_project_only" {
		return d, 0, errors.New("неизвестный режим доступа к данным")
	}
	policy := d.SourcePolicy
	switch d.Schema {
	case "aifence.case.v1":
	case "aifence.v1":
		var b Batch
		if err := json.Unmarshal(raw, &b); err != nil {
			return d, 0, err
		}
		d = Document{Schema: "aifence.case.v1", AsOf: b.AsOf, Batches: []Batch{b}}
	case "aifence.workflow.evidence.v1":
		var w struct {
			CollectedAt string `json:"collected_at"`
			Sources     []struct {
				Source string
				Batch  *Batch
				Reason string
			}
			Warnings []string
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return d, 0, err
		}
		d = Document{Schema: "aifence.case.v1", AsOf: w.CollectedAt, Warnings: w.Warnings}
		for _, s := range w.Sources {
			if s.Batch != nil {
				d.Batches = append(d.Batches, *s.Batch)
			} else {
				d.Warnings = append(d.Warnings, s.Source+": "+s.Reason)
			}
		}
	default:
		return d, 0, errors.New("неизвестная схема источника")
	}
	d.SourcePolicy = policy
	if len(d.Batches) == 0 || len(d.Batches) > 64 || d.AsOf == "" {
		return d, 0, errors.New("нет даты или набора наблюдений")
	}
	ids := map[string]bool{}
	for _, b := range d.Batches {
		if b.Schema != "aifence.v1" || !in(b.Source, "sales", "market", "metrika", "research", "backlog", "release", "economics", "browser") || !in(b.Status, "ok", "partial", "unavailable") || b.AsOf == "" {
			return d, 0, errors.New("некорректный набор наблюдений")
		}
		for _, rawObs := range b.Observations {
			var o Observation
			if err := json.Unmarshal(rawObs, &o); err != nil {
				return d, 0, err
			}
			if o.ID == "" || ids[o.ID] || o.ProductKey == "" || o.MetricKey == "" || o.Ref.SourceID == "" || o.Ref.Locator == "" || !validDate(o.Period.From) || !validDate(o.Period.To) || o.Period.From > o.Period.To || !in(o.Status, "observed", "estimated", "missing", "incomparable") || len(o.Value) == 0 || (o.Status == "missing" && string(o.Value) != "null") {
				return d, 0, fmt.Errorf("проверьте ID, источник, дату и значение наблюдения %q", o.ID)
			}
			if b.Source == "sales" && o.MetricKey == "watchdog.alert" && o.Status == "observed" &&
				string(o.Value) != "true" && string(o.Value) != "false" {
				return d, 0, fmt.Errorf("наблюдаемый сигнал %q должен содержать JSON boolean", o.ID)
			}
			ids[o.ID] = true
		}
	}
	if len(ids) == 0 {
		return d, 0, errors.New("нет наблюдений для импорта")
	}
	return d, len(ids), nil
}

func (s *Service) Import(ctx context.Context, input ImportInput) (Summary, error) {
	u, err := human(ctx)
	if err != nil {
		return Summary{}, err
	}
	d, n, err := parse([]byte(input.Document))
	if err != nil {
		return Summary{}, err
	}
	if err := s.checkVisibility(ctx, d.SourcePolicy); err != nil {
		return Summary{}, err
	}
	// Canonical object keys make whitespace and formatting irrelevant to the snapshot ID.
	data, err := json.Marshal(d)
	if err != nil {
		return Summary{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var obj any
	if err = decoder.Decode(&obj); err != nil {
		return Summary{}, err
	}
	data, err = json.Marshal(obj)
	if err != nil {
		return Summary{}, err
	}
	digest := sha256.Sum256(data)
	id := "ev-" + hex.EncodeToString(digest[:])
	err = s.db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		if err := db.New(tx).StoreEvidence(ctx, db.StoreEvidenceParams{UserID: u, SnapshotID: id, AsOf: d.AsOf, Observations: int32(n), Document: data}); err != nil {
			return err
		}
		return s.changes.Publish(ctx, tx, u.String())
	})
	return Summary{id, d.AsOf, n}, err
}
func (s *Service) List(ctx context.Context, _ Empty) (Index, error) {
	u, err := human(ctx)
	if err != nil {
		return Index{}, err
	}
	rows, err := db.New(s.db).EvidenceIndex(ctx, db.EvidenceIndexParams{UserID: u, LocalAllowed: s.localRead(ctx)})
	if err != nil {
		return Index{}, err
	}
	out := Index{Snapshots: []Summary{}}
	for _, r := range rows {
		out.Snapshots = append(out.Snapshots, Summary{r.SnapshotID, r.AsOf, int(r.Observations)})
	}
	return out, nil
}

// The browsing index stops at 100 recent imports. An older open alert or its
// recovery must still participate in the monitor after unrelated imports.
func (s *Service) MonitorSnapshotIDs(ctx context.Context, recent []Summary) ([]string, error) {
	ids := make([]string, 0, len(recent))
	for _, entry := range recent {
		ids = append(ids, entry.SnapshotID)
	}
	if len(recent) < 100 {
		return ids, nil
	}
	u, err := human(ctx)
	if err != nil {
		return nil, err
	}
	watched, err := db.New(s.db).WatchdogSnapshotIDs(ctx, db.WatchdogSnapshotIDsParams{
		UserID: u, LocalAllowed: s.localRead(ctx),
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range watched {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *Service) Read(ctx context.Context, q Query) (Result, error) {
	u, err := human(ctx)
	if err != nil {
		return Result{}, err
	}
	stored, err := db.New(s.db).GetEvidence(ctx, db.GetEvidenceParams{UserID: u, SnapshotID: q.SnapshotID})
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, errors.New("снимок не найден")
	}
	if err != nil {
		return Result{}, err
	}
	var d Document
	if err = json.Unmarshal(stored.Document, &d); err != nil {
		return Result{}, err
	}
	if err := s.checkVisibility(ctx, d.SourcePolicy); err != nil {
		return Result{}, err
	}
	result, err := selectRows(d, q)
	if err != nil {
		return Result{}, err
	}
	result.ImportedAt = &stored.ImportedAt
	return result, nil
}
func selectRows(d Document, q Query) (Result, error) {
	if q.Offset < 0 || q.Limit < 0 || q.Limit > 100 {
		return Result{}, errors.New("limit 1..100, offset >= 0")
	}
	if q.Limit == 0 {
		q.Limit = 24
	}
	ids := map[string]bool{}
	for _, id := range q.IDs {
		ids[id] = true
	}
	out := Result{Summary: Summary{SnapshotID: q.SnapshotID, AsOf: d.AsOf}, Rows: []Row{}, Warnings: append([]string{}, d.Warnings...)}
	for _, b := range d.Batches {
		out.Count += len(b.Observations)
		if q.Source != "" && b.Source != q.Source {
			continue
		}
		out.Warnings = append(out.Warnings, b.Warnings...)
		for _, raw := range b.Observations {
			var o struct {
				Observation
				Segment    string          `json:"segment"`
				Details    json.RawMessage `json:"details"`
				Caveats    []string        `json:"caveats"`
				SampleSize *int            `json:"sample_size"`
			}
			if err := json.Unmarshal(raw, &o); err != nil {
				return Result{}, err
			}
			if (len(ids) > 0 && !ids[o.ID]) || (q.MetricKey != "" && q.MetricKey != o.MetricKey) || (q.ProductKey != "" && q.ProductKey != o.ProductKey) ||
				(q.Status != "" && q.Status != o.Status) || (q.Value != "" && q.Value != string(o.Value)) {
				continue
			}
			out.Total++
			if out.Total <= q.Offset || len(out.Rows) >= q.Limit {
				continue
			}
			value := string(o.Value)
			if strings.HasPrefix(value, "\"") {
				_ = json.Unmarshal(o.Value, &value)
			}
			row := Row{
				BatchStatus: b.Status, BatchAsOf: b.AsOf, Kind: o.Kind, Segment: o.Segment, Source: b.Source,
				ID: o.ID, Product: o.ProductKey, Metric: o.MetricKey, Value: value, Unit: o.Unit,
				Period: o.Period, Ref: o.Ref, Status: o.Status, Details: string(o.Details), Caveats: o.Caveats,
				SampleSize: o.SampleSize,
			}
			row = projectWatchdogWindow(row)
			out.Rows = append(out.Rows, row)
		}
	}
	return out, nil
}
func (s *Service) Verify(ctx context.Context, snapshotID string, ids []string) ([]Row, error) {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	if len(wanted) == 0 || len(wanted) > 100 {
		return nil, errors.New("необходимы ссылки на 1..100 наблюдений")
	}
	q := Query{SnapshotID: snapshotID, IDs: ids, Limit: 100}
	r, err := s.Read(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, row := range r.Rows {
		delete(wanted, row.ID)
	}
	if len(wanted) > 0 {
		missing := []string{}
		for id := range wanted {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("в снимке отсутствуют наблюдения: %s", strings.Join(missing, ", "))
	}
	return r.Rows, nil
}

func (s *Service) Policy(ctx context.Context, id string) (string, error) {
	u, err := human(ctx)
	if err != nil {
		return "", err
	}
	policy, err := db.New(s.db).EvidencePolicy(ctx, db.EvidencePolicyParams{UserID: u, SnapshotID: id})
	return policy, err
}
func (s *Service) Access(ctx context.Context, id string) error {
	policy, err := s.Policy(ctx, id)
	if err != nil {
		return err
	}
	return s.checkVisibility(ctx, policy)
}
