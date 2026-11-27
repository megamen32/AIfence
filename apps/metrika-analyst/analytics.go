package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type Ref struct {
	SourceID string `json:"source_id"`
	Locator  string `json:"locator"`
	URL      string `json:"url,omitempty"`
	Revision string `json:"revision,omitempty"`
}
type Observation = EvidenceObservation[float64]
type Batch = EvidenceBatch[float64]

type EvidenceObservation[V any] struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	ProductKey    string         `json:"product_key"`
	MetricKey     string         `json:"metric_key"`
	Value         *V             `json:"value"`
	Unit          string         `json:"unit"`
	Period        Period         `json:"period"`
	Segment       string         `json:"segment,omitempty"`
	Currency      string         `json:"currency,omitempty"`
	SampleSize    *int           `json:"sample_size,omitempty"`
	Numerator     *float64       `json:"numerator,omitempty"`
	Denominator   *float64       `json:"denominator,omitempty"`
	SourceRef     Ref            `json:"source_ref"`
	Status        string         `json:"status"`
	Caveats       []string       `json:"caveats,omitempty"`
	ComparisonKey string         `json:"comparison_key,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}
type EvidenceBatch[V any] struct {
	SchemaVersion string                   `json:"schema_version"`
	Source        string                   `json:"source"`
	AsOf          string                   `json:"as_of"`
	Status        string                   `json:"status"`
	Observations  []EvidenceObservation[V] `json:"observations"`
	Warnings      []string                 `json:"warnings"`
}
type Query struct {
	ProductKey string   `json:"product_key"`
	MetricKeys []string `json:"metric_keys"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Segment    string   `json:"segment,omitempty"`
	SourcePath string   `json:"source_path,omitempty"`
}
type Compare struct {
	// SourceMode=live pins the counter independently of the dashboard file selection.
	SourceMode        string   `json:"source_mode,omitempty" jsonschema:"enum=live,description=Use live to compare the configured counter even when a historical dashboard file is selected. Omit to use the configured source."`
	IncludeIncomplete bool     `json:"include_incomplete,omitempty"`
	ProductKey        string   `json:"product_key"`
	MetricKeys        []string `json:"metric_keys"`
	BaselineFrom      string   `json:"baseline_from"`
	BaselineTo        string   `json:"baseline_to"`
	CurrentFrom       string   `json:"current_from"`
	CurrentTo         string   `json:"current_to"`
	Segment           string   `json:"segment,omitempty"`
	SourcePath        string   `json:"source_path,omitempty"`
}
type CheckInput struct {
	SourcePath string `json:"source_path,omitempty"`
}
type CheckResult struct {
	Status    string `json:"status"`
	Source    string `json:"source"`
	AsOf      string `json:"as_of"`
	Message   string `json:"message"`
	CounterID int64  `json:"counter_id,omitempty"`
}
type Settings struct {
	Counter      int64
	Ecommerce    bool
	EvidencePath string
}
type Service struct {
	call   func(context.Context, string, any) (json.RawMessage, error)
	config func(context.Context) Settings
	read   func(context.Context, string) ([]byte, error)
}

func stamp() string          { return time.Now().UTC().Format(time.RFC3339) }
func batch() Batch           { return Batch{"aifence.v1", "metrika", stamp(), "ok", []Observation{}, []string{}} }
func num(v float64) *float64 { return &v }
func hash(v any) string      { b, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func days(p Period) (int, error) {
	a, e := time.Parse(time.DateOnly, p.From)
	if e != nil {
		return 0, fmt.Errorf("invalid start date")
	}
	b, e := time.Parse(time.DateOnly, p.To)
	if e != nil || b.Before(a) {
		return 0, fmt.Errorf("invalid end date")
	}
	return int(b.Sub(a).Hours()/24) + 1, nil
}
func normalize(q Query) (Query, error) {
	if _, e := days(Period{q.From, q.To}); e != nil {
		return q, e
	}
	if q.ProductKey == "" {
		q.ProductKey = "store"
	}
	if q.Segment == "" {
		q.Segment = "all"
		if len(q.MetricKeys) == 1 && strings.TrimSpace(q.MetricKeys[0]) == "orders_index" {
			q.Segment = "self_service"
		}
	}
	if len(q.MetricKeys) == 0 || len(q.MetricKeys) > 12 {
		return q, fmt.Errorf("specify 1–12 metric keys")
	}
	seen := map[string]bool{}
	for i, k := range q.MetricKeys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			return q, fmt.Errorf("empty or duplicate metric key")
		}
		seen[k] = true
		q.MetricKeys[i] = k
	}
	return q, nil
}
func chosen(p, def string) string {
	if p != "" {
		return p
	}
	return def
}
func missing(q Query, k string, ref Ref, reason string) Observation {
	return Observation{ID: hash([]any{q, k, ref}), Kind: "metric", ProductKey: q.ProductKey, MetricKey: k, Unit: "unknown", Period: Period{q.From, q.To}, Segment: q.Segment, SourceRef: ref, Status: "missing", Caveats: []string{reason}}
}
func (s Service) historical(ctx context.Context, p string) (Batch, error) {
	return s.historicalWithDigest(ctx, p, nil)
}
func (s Service) historicalWithDigest(ctx context.Context, p string, digest *string) (Batch, error) {
	if !strings.HasSuffix(strings.ToLower(p), ".json") || strings.Contains(p, "..") {
		return Batch{}, fmt.Errorf("select an authorized evidence-v1 JSON file")
	}
	b, e := s.read(ctx, p)
	if e != nil {
		return Batch{}, fmt.Errorf("historical file unavailable; check permissions/path")
	}
	if len(b) > 8<<20 {
		return Batch{}, fmt.Errorf("file too large")
	}
	var x Batch
	if json.Unmarshal(b, &x) != nil || x.SchemaVersion != "aifence.v1" || (x.Source != "metrika" && x.Source != "sales") || x.Observations == nil {
		return Batch{}, fmt.Errorf("not a numeric metrika or sales evidence-v1 batch")
	}
	if _, e := time.Parse(time.RFC3339, x.AsOf); e != nil {
		return Batch{}, fmt.Errorf("invalid evidence timestamp")
	}
	for i := range x.Observations {
		o := &x.Observations[i]
		if o.ID == "" || o.Kind != "metric" || o.MetricKey == "" || o.ProductKey == "" || o.Unit == "" || o.SourceRef.SourceID == "" || o.SourceRef.Locator == "" {
			return Batch{}, fmt.Errorf("invalid observation/provenance")
		}
		if _, e := days(o.Period); e != nil {
			return Batch{}, e
		}
		if o.Status != "observed" && o.Status != "missing" && o.Status != "incomparable" && o.Status != "estimated" {
			return Batch{}, fmt.Errorf("invalid observation status")
		}
		if o.Status == "observed" && o.Value == nil || o.Status == "missing" && o.Value != nil {
			return Batch{}, fmt.Errorf("inconsistent observation value")
		}
		if o.Details == nil {
			o.Details = map[string]any{}
		}
		o.Details["evidence_file"] = p
	}
	if digest != nil {
		*digest = fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	}
	return x, nil
}
func (s Service) Query(ctx context.Context, q Query) (Batch, error) {
	q, e := normalize(q)
	if e != nil {
		return Batch{}, e
	}
	return s.query(ctx, q, s.config(ctx))
}
func (s Service) query(ctx context.Context, q Query, c Settings) (Batch, error) {
	out := batch()
	if p := chosen(q.SourcePath, c.EvidencePath); p != "" {
		x, e := s.historical(ctx, p)
		if e != nil {
			return Batch{}, e
		}
		out.AsOf = x.AsOf
		out.Warnings = append(out.Warnings, x.Warnings...)
		out.Warnings = append(out.Warnings, "Historical evidence: exact aggregate windows, no automatic live fallback.")
		if x.Status != "ok" {
			out.Status = "partial"
		}
		start, _ := time.Parse(time.DateOnly, q.From)
		end, _ := time.Parse(time.DateOnly, q.To)
		for _, k := range q.MetricKeys {
			if k == "orders_index" && start.Weekday() == time.Monday && end.Weekday() == time.Sunday {
				o, err := weeklyAverage(x, q, p, false)
				if err != nil {
					return Batch{}, err
				}
				out.Observations = append(out.Observations, o)
				if o.Status != "observed" {
					out.Status = "partial"
				}
				continue
			}
			var matches []Observation
			for _, o := range x.Observations {
				seg := chosen(o.Segment, "all")
				if o.ProductKey == q.ProductKey && o.MetricKey == k && o.Period == (Period{q.From, q.To}) && seg == q.Segment {
					matches = append(matches, o)
				}
			}
			if len(matches) != 1 {
				out.Observations = append(out.Observations, missing(q, k, Ref{SourceID: "historical-file", Locator: p}, "No unique exact aggregate; unknown is not zero."))
				out.Status = "partial"
				continue
			}
			o := matches[0]
			o.Segment = q.Segment
			out.Observations = append(out.Observations, o)
			if o.Status != "observed" {
				out.Status = "partial"
			}
		}
		return out, nil
	}
	if c.Counter <= 0 {
		out.Status = "unavailable"
		out.Warnings = append(out.Warnings, "Configure counter_id and private read-only MCP, or explicitly choose historical evidence.")
		for _, k := range q.MetricKeys {
			out.Observations = append(out.Observations, missing(q, k, Ref{SourceID: "metrika", Locator: "counter not configured"}, "counter not configured"))
		}
		return out, nil
	}
	if q.ProductKey != "store" {
		return Batch{}, fmt.Errorf("live MVP supports store-level metrics; SKU needs explicit mapping")
	}
	filter, err := segmentFilter(q.Segment)
	if err != nil {
		return Batch{}, err
	}
	if q.To >= time.Now().UTC().Format(time.DateOnly) {
		return Batch{}, fmt.Errorf("choose completed days; verify counter timezone")
	}
	for _, k := range q.MetricKeys {
		d, ok := metrics[k]
		ref := Ref{SourceID: "yandex-metrika:" + strconv.FormatInt(c.Counter, 10), Locator: fmt.Sprintf("metric=%s; %s..%s; %s", k, q.From, q.To, filter)}
		if !ok || d.ec && !c.Ecommerce {
			o := missing(q, k, ref, "Unsupported metric or ecommerce instrumentation not confirmed.")
			if ok {
				o.Unit = d.unit
			}
			out.Observations = append(out.Observations, o)
			out.Status = "partial"
			continue
		}
		args := map[string]any{"counterId": c.Counter, "metrics": []string{d.api}, "date1": q.From, "date2": q.To, "limit": 1, "accuracy": "full"}
		if filter != "" {
			args["filters"] = filter
		}
		ab, _ := json.Marshal(args)
		ref.Locator = "run_report " + string(ab)
		raw, e := s.call(ctx, "run_report", args)
		if e != nil {
			out.Observations = append(out.Observations, missing(q, k, ref, "MCP query failed; verify authorization/metric."))
			out.Status = "partial"
			continue
		}
		o := report(raw, q, k, d, ref)
		if o.Status != "observed" {
			out.Status = "partial"
		}
		out.Observations = append(out.Observations, o)
	}
	return out, nil
}

type metricDef struct {
	api, unit, currency string
	scale               float64
	ec                  bool
}

var metrics = map[string]metricDef{
	"visits": {"ym:s:visits", "count", "", 1, false}, "users": {"ym:s:users", "count", "", 1, false}, "pageviews": {"ym:s:pageviews", "count", "", 1, false},
	"orders": {"ym:s:ecommercePurchases", "count", "", 1, true}, "revenue": {"ym:s:ecommerceRUBConvertedRevenue", "RUB", "RUB", 1, true},
	"product_views": {"ym:s:productImpressions", "count", "", 1, true}, "add_to_cart": {"ym:s:productBasketsQuantity", "count", "", 1, true}, "purchase_conversion": {"ym:s:usersPurchasePercentage", "ratio", "", .01, true}, "average_order_value": {"ym:s:ecommerceRUBConvertedRevenuePerPurchase", "RUB", "RUB", 1, true},
}

func unwrap(raw json.RawMessage) json.RawMessage {
	var w struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &w) == nil && len(w.Result) > 0 {
		return w.Result
	}
	return raw
}
func report(raw json.RawMessage, q Query, k string, d metricDef, ref Ref) Observation {
	o := missing(q, k, ref, "Missing report totals.")
	o.Unit = d.unit
	o.Currency = d.currency
	var r struct {
		Totals map[string]*float64 `json:"totals"`
		Meta   reportMeta          `json:"meta"`
	}
	if json.Unmarshal(unwrap(raw), &r) != nil {
		return o
	}
	return observed(o, r.Totals[d.api], d.scale, r.Meta)
}

type reportMeta struct {
	Sampled     *bool    `json:"sampled"`
	SampleShare *float64 `json:"sample_share"`
	DataLag     *int     `json:"data_lag_seconds"`
}

func observed(o Observation, value *float64, scale float64, meta reportMeta) Observation {
	if value == nil {
		return o
	}
	v := *value * scale
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return o
	}
	o.Value = num(v)
	o.Status = "observed"
	o.Caveats = []string{"Recorded events, not accounting or proof of causality. Verify collection and timezone."}
	o.Details = map[string]any{"origin": "live_mcp", "sampled": meta.Sampled, "sample_share": meta.SampleShare, "data_lag": meta.DataLag}
	if meta.Sampled == nil || *meta.Sampled {
		o.Status = "estimated"
		o.Caveats = append(o.Caveats, "Sampled or unknown sampling status.")
	}
	return o
}
func (s Service) Compare(ctx context.Context, in Compare) (Batch, error) {
	cfg := s.config(ctx)
	switch in.SourceMode {
	case "":
	case "live":
		if in.SourcePath != "" || (in.ProductKey != "" && in.ProductKey != "store") {
			return Batch{}, fmt.Errorf("live source requires store metrics and no historical source_path")
		}
		cfg.EvidencePath = ""
	default:
		return Batch{}, fmt.Errorf("source_mode must be live or omitted")
	}
	path := chosen(in.SourcePath, cfg.EvidencePath)
	weekly := path != "" && len(in.MetricKeys) == 1 && strings.TrimSpace(in.MetricKeys[0]) == "orders_index"
	if weekly && in.Segment == "" {
		in.Segment = "self_service"
	}
	if in.IncludeIncomplete && !weekly {
		return Batch{}, fmt.Errorf("include_incomplete applies only to historical orders_index")
	}
	a, e := normalize(Query{in.ProductKey, in.MetricKeys, in.BaselineFrom, in.BaselineTo, in.Segment, in.SourcePath})
	if e != nil {
		return Batch{}, e
	}
	b, e := normalize(Query{in.ProductKey, in.MetricKeys, in.CurrentFrom, in.CurrentTo, in.Segment, in.SourcePath})
	if e != nil {
		return Batch{}, e
	}
	ad, _ := days(Period{a.From, a.To})
	bd, _ := days(Period{b.From, b.To})
	if a.To >= b.From || !weekly && ad != bd {
		return Batch{}, fmt.Errorf("require non-overlapping chronological periods, equal except for weekly index means")
	}
	if weekly {
		return s.compareWeekly(ctx, in, path, a, b)
	}
	if cfg.Counter > 0 && chosen(a.SourcePath, cfg.EvidencePath) == "" {
		return s.compareLive(ctx, a, b, cfg)
	}
	x, e := s.query(ctx, a, cfg)
	if e != nil {
		return Batch{}, e
	}
	y, e := s.query(ctx, b, cfg)
	if e != nil {
		return Batch{}, e
	}
	out := batch()
	out.Warnings = append(append(out.Warnings, x.Warnings...), y.Warnings...)
	out.Warnings = append(out.Warnings, "Temporal association is not causation; check weekdays, seasonality and collection breaks.")
	if x.Status == "unavailable" && y.Status == "unavailable" {
		out.Status = "unavailable"
	} else if x.Status != "ok" || y.Status != "ok" {
		out.Status = "partial"
	}
	for i, old := range x.Observations {
		cur := y.Observations[i]
		out.Observations = append(out.Observations, old, cur)
		for _, d := range deltas(old, cur) {
			out.Observations = append(out.Observations, d)
			if d.Status != "observed" && out.Status == "ok" {
				out.Status = "partial"
			}
		}
	}
	return out, nil
}
func deltas(a, b Observation) []Observation {
	ref := Ref{SourceID: "metrika-comparison", Locator: a.ID + " -> " + b.ID}
	create := func(s, u string) Observation {
		return Observation{ID: hash([]string{a.ID, b.ID, s}), Kind: "metric", ProductKey: b.ProductKey, MetricKey: b.MetricKey + s, Unit: u, Period: b.Period, Segment: b.Segment, SourceRef: ref, Status: "missing", ComparisonKey: b.MetricKey, Details: map[string]any{"baseline_id": a.ID, "current_id": b.ID, "baseline_period": a.Period}}
	}
	abs := create(".absolute_change", b.Unit)
	pct := create(".relative_change_pct", "percent")
	if a.Unit == "ratio" && b.Unit == "ratio" {
		abs.MetricKey = b.MetricKey + ".change_pp"
		abs.Unit = "pp"
	}
	if a.Status != "observed" || b.Status != "observed" || a.Value == nil || b.Value == nil || a.Unit != b.Unit || a.Currency != b.Currency || a.ComparisonKey != b.ComparisonKey {
		abs.Caveats = []string{"Missing, estimated or incompatible values."}
		pct.Caveats = abs.Caveats
		return []Observation{abs, pct}
	}
	d := *b.Value - *a.Value
	if abs.Unit == "pp" {
		d *= 100
	}
	abs.Value = num(d)
	abs.Status = "observed"
	abs.Currency = b.Currency
	if *a.Value == 0 {
		pct.Caveats = []string{"Zero baseline: percentage undefined."}
	} else {
		pct.Value = num((*b.Value - *a.Value) / math.Abs(*a.Value) * 100)
		pct.Status = "observed"
	}
	return []Observation{abs, pct}
}
func (s Service) Check(ctx context.Context, in CheckInput) (CheckResult, error) {
	c := s.config(ctx)
	r := CheckResult{"unavailable", "live_mcp", stamp(), "", c.Counter}
	if p := chosen(in.SourcePath, c.EvidencePath); p != "" {
		r.Source = "historical_file"
		b, e := s.historical(ctx, p)
		if e != nil {
			r.Message = e.Error()
		} else {
			r.Status = "ok"
			r.Message = fmt.Sprintf("Historical evidence: %d rows; not a live counter check.", len(b.Observations))
		}
		return r, nil
	}
	if c.Counter <= 0 {
		r.Message = "Configure counter_id"
		return r, nil
	}
	raw, e := s.call(ctx, "get_metadata", nil)
	if e != nil {
		r.Message = "MCP unavailable or unauthorized"
		return r, nil
	}
	var metadata struct {
		Counters []struct {
			ID int64 `json:"id"`
		} `json:"counters"`
	}
	if json.Unmarshal(unwrap(raw), &metadata) != nil {
		r.Message = "Unexpected counter list response"
		return r, nil
	}
	for _, v := range metadata.Counters {
		if v.ID == c.Counter {
			r.Status = "ok"
			r.Message = "Configured counter listed; ecommerce instrumentation not proven."
			return r, nil
		}
	}
	r.Message = "Counter not present in authorized list"
	return r, nil
}
