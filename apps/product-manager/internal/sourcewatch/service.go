// Package sourcewatch checks completed store-level periods against an owner-selected threshold.
package sourcewatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Window is one owner-configured weekly check. The store metric is required;
// the optional sources add demand and comparable price evidence to the signal.
type Window struct {
	MetrikaBinding  string  `json:"metrika_binding"`
	MarketBinding   string  `json:"market_binding,omitempty"`
	WordstatBinding string  `json:"wordstat_binding,omitempty"`
	TrendsBinding   string  `json:"trends_binding,omitempty"`
	TrendsGeo       string  `json:"trends_geo,omitempty"`
	Phrase          string  `json:"phrase,omitempty"`
	DemandDropPct   float64 `json:"demand_drop_pct,omitempty"`
	SKU             string  `json:"sku,omitempty"`
	Metric          string  `json:"metric"`
	DropPct         float64 `json:"drop_pct"`
	Monday          string  `json:"monday"`
}

func (w Window) Validate() (time.Time, error) {
	if w.MarketBinding == "" && w.WordstatBinding == "" && w.TrendsBinding == "" {
		return time.Time{}, errors.New("configure Wordstat, Google Trends or Market Watch as a second source")
	}
	bindings := []string{w.MetrikaBinding}
	for _, v := range []string{w.MarketBinding, w.WordstatBinding, w.TrendsBinding} {
		if v != "" {
			bindings = append(bindings, v)
		}
	}
	seen := map[string]bool{}
	for _, v := range bindings {
		id, e := uuid.Parse(v)
		if e != nil || id == uuid.Nil || id.String() != v || seen[v] {
			return time.Time{}, errors.New("select distinct canonical binding UUIDs")
		}
		seen[v] = true
	}
	if (w.MarketBinding != "" && (!bounded(w.SKU, 128) || strings.TrimSpace(w.SKU) != w.SKU)) || (w.MarketBinding == "" && w.SKU != "") {
		return time.Time{}, errors.New("set an exact SKU with the market binding")
	}
	if w.TrendsBinding != "" && w.TrendsGeo == "" {
		w.TrendsGeo = "RU"
	}
	usesPhrase := w.WordstatBinding != "" || w.TrendsBinding != ""
	if (usesPhrase && (!bounded(w.Phrase, 256) || strings.TrimSpace(w.Phrase) != w.Phrase)) || (!usesPhrase && (w.Phrase != "" || w.DemandDropPct != 0)) {
		return time.Time{}, errors.New("set a search phrase with the demand binding")
	}
	if w.DemandDropPct < 0 || w.DemandDropPct > 100 || math.IsNaN(w.DemandDropPct) || math.IsInf(w.DemandDropPct, 0) {
		return time.Time{}, errors.New("demand drop threshold must be between zero and 100 percent")
	}
	switch w.Metric {
	case "visits", "users", "pageviews", "orders", "revenue", "purchase_conversion", "average_order_value", "product_views", "add_to_cart":
	default:
		return time.Time{}, errors.New("select one supported live store metric")
	}
	if w.DropPct <= 0 || w.DropPct > 100 || math.IsNaN(w.DropPct) || math.IsInf(w.DropPct, 0) {
		return time.Time{}, errors.New("set a drop threshold greater than zero and at most 100 percent")
	}
	monday, err := time.Parse(time.DateOnly, w.Monday)
	if err != nil || monday.Weekday() != time.Monday {
		return time.Time{}, errors.New("monday must be a UTC Monday in YYYY-MM-DD")
	}
	return monday, nil
}

// ID scopes the host's idempotency key to the authenticated owner and typed fields.
func ID(owner, kind string, fields any) string {
	raw, err := json.Marshal([]any{"aifence.source-watch.v1", owner, kind, fields})
	if err != nil {
		panic(err)
	}
	id := uuid.NewHash(sha256.New(), uuid.NameSpaceURL, raw, 8)
	return id.String()
}

type period struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type sourceRef struct {
	SourceID string `json:"source_id"`
	Locator  string `json:"locator"`
	URL      string `json:"url,omitempty"`
}
type offerDetails struct {
	MatchStatus string   `json:"match_status"`
	CapturedAt  string   `json:"captured_at"`
	PriceBasis  string   `json:"price_basis"`
	Seller      string   `json:"seller"`
	Name        string   `json:"name"`
	Quantity    *int64   `json:"quantity"`
	MaxQuantity *int64   `json:"max_quantity"`
	OldPrice    *float64 `json:"old_price,omitempty"`
}
type observation struct {
	ID         string       `json:"id"`
	Kind       string       `json:"kind"`
	ProductKey string       `json:"product_key"`
	MetricKey  string       `json:"metric_key"`
	Status     string       `json:"status"`
	Unit       string       `json:"unit"`
	Value      *float64     `json:"value"`
	Period     period       `json:"period"`
	SourceRef  sourceRef    `json:"source_ref"`
	Details    offerDetails `json:"details"`
}
type batch struct {
	AsOf          string        `json:"as_of"`
	Warnings      []string      `json:"warnings"`
	SchemaVersion string        `json:"schema_version"`
	Source        string        `json:"source"`
	Status        string        `json:"status"`
	Observations  []observation `json:"observations"`
}

// weekRead is the latest stored observation of one series inside one week.
type weekRead struct {
	CapturedAt string  `json:"captured_at"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	EvidenceID string  `json:"evidence_id"`
	URL        string  `json:"url"`
}
type weekDeltaSeries struct {
	Seller      string    `json:"seller"`
	URL         string    `json:"url"`
	PriceBasis  string    `json:"price_basis"`
	Currency    string    `json:"currency"`
	Quantity    *int64    `json:"quantity,omitempty"`
	MaxQuantity *int64    `json:"max_quantity,omitempty"`
	Name        string    `json:"name,omitempty"`
	Baseline    *weekRead `json:"baseline,omitempty"`
	Current     *weekRead `json:"current,omitempty"`
	Delta       *float64  `json:"delta,omitempty"`
	DeltaPct    *float64  `json:"delta_pct,omitempty"`
	Comparable  bool      `json:"comparable"`
	Reason      string    `json:"reason"`
	EvidenceIDs []string  `json:"evidence_ids"`
}
type weekDeltaSource struct {
	Seller  string `json:"seller"`
	Reads   int    `json:"reads"`
	Series  int    `json:"series"`
	Missing int    `json:"missing"`
}
type missingPeriod struct {
	Seller string `json:"seller"`
	Period string `json:"period"`
	Reason string `json:"reason"`
}

// weekDelta mirrors market-watch price_week_delta, which owns the series
// grouping and comparability rules. This package only consumes its verdict.
type weekDelta struct {
	SKU             string            `json:"sku"`
	BaselineWeek    period            `json:"baseline_week"`
	CurrentWeek     period            `json:"current_week"`
	ComparablePairs int               `json:"comparable_pairs"`
	MissingPeriods  []missingPeriod   `json:"missing_periods"`
	Sources         []weekDeltaSource `json:"sources"`
	Series          []weekDeltaSeries `json:"series"`
	Status          string            `json:"status"`
	AsOf            string            `json:"as_of"`
	Truncated       bool              `json:"truncated"`
	Warnings        []string          `json:"warnings"`
}

// PriceFamily is one comparable seller series with a two-week price change.
type PriceFamily struct {
	SKU         string   `json:"sku"`
	Seller      string   `json:"seller"`
	PriceBasis  string   `json:"price_basis"`
	Name        string   `json:"name,omitempty"`
	BaselineID  string   `json:"baseline_observation_id"`
	CurrentID   string   `json:"current_observation_id"`
	BaselineAt  string   `json:"baseline_captured_at"`
	CurrentAt   string   `json:"current_captured_at"`
	BaselineRUB float64  `json:"baseline_rub"`
	CurrentRUB  float64  `json:"current_rub"`
	ChangePct   *float64 `json:"relative_change_pct,omitempty"`
	Locator     string   `json:"locator"`
	Quantity    *int64   `json:"quantity,omitempty"`
	MaxQuantity *int64   `json:"max_quantity,omitempty"`
}

type Signal struct {
	SignalID       string          `json:"signal_id"`
	Trigger        string          `json:"trigger"`
	Metric         string          `json:"store_metric"`
	ChangePct      float64         `json:"change_pct"`
	BaselineFrom   string          `json:"baseline_from"`
	BaselineTo     string          `json:"baseline_to"`
	CurrentFrom    string          `json:"current_from"`
	CurrentTo      string          `json:"current_to"`
	ObservationID  string          `json:"observation_id"`
	ObservationRef string          `json:"observation_ref,omitempty"`
	Demand         *Demand         `json:"demand,omitempty"`
	Trends         *Trends         `json:"trends,omitempty"`
	TrendsStatus   string          `json:"trends_status,omitempty"`
	WordstatStatus string          `json:"wordstat_status,omitempty"`
	MarketStatus   string          `json:"market_status"`
	ContextSKU     string          `json:"market_sku,omitempty"`
	PriceFamilies  []PriceFamily   `json:"price_families,omitempty"`
	MissingPeriods []missingPeriod `json:"missing_periods,omitempty"`
}

type Outcome struct {
	Status        string `json:"status"`
	AnalysisRunID string `json:"analysis_run_id,omitempty"`
}

// Read performs one owner-scoped call to an approved bound app operation.
// The host adapter owns the personal grant; this package never sees credentials.
type Read func(ctx context.Context, binding string, input, output any) error

// Launch starts the review for a pinned signal and returns its run ID.
type Launch func(ctx context.Context, signal Signal) (string, error)

// Check is the offline-testable core of the weekly check. It reads exactly the
// configured operations, validates every row against the requested window and
// only then decides whether a review is warranted.
func Check(ctx context.Context, w Window, approved bool, read Read, launch Launch) (Outcome, error) {
	monday, err := w.Validate()
	if err != nil {
		return Outcome{}, err
	}
	date := func(offset int) string { return monday.AddDate(0, 0, offset).Format(time.DateOnly) }
	before, current := period{date(-14), date(-8)}, period{date(-7), date(-1)}
	var metrika, wordstat, trends batch
	var market weekDelta
	// Fixed source parameters keep filesystem selection and arbitrary connector calls out of this job.
	err = read(ctx, w.MetrikaBinding, map[string]any{"product_key": "store", "metric_keys": []string{w.Metric}, "baseline_from": before.From, "baseline_to": before.To, "current_from": current.From, "current_to": current.To}, &metrika)
	if err != nil {
		return Outcome{}, fmt.Errorf("read store comparison: %w", err)
	}
	if metrika.SchemaVersion != "aifence.v1" || metrika.Source != "metrika" {
		return Outcome{}, errors.New("Metrika binding returned an unexpected contract")
	}
	if w.MarketBinding != "" {
		err = read(ctx, w.MarketBinding, map[string]string{"product_key": w.SKU, "baseline_from": before.From, "baseline_to": before.To, "current_from": current.From, "current_to": current.To}, &market)
		if err != nil {
			return Outcome{}, fmt.Errorf("read price week delta: %w", err)
		}
		if market.SKU == "" || market.Status == "" {
			return Outcome{}, errors.New("market binding returned an unexpected contract")
		}
	}
	var demand *Demand
	demandUnavailable := false
	if w.WordstatBinding != "" {
		err = read(ctx, w.WordstatBinding, map[string]string{"phrase": w.Phrase, "monday": w.Monday}, &wordstat)
		if err != nil {
			return Outcome{}, fmt.Errorf("read demand comparison: %w", err)
		}
		if wordstat.SchemaVersion != "aifence.v1" || wordstat.Source != "wordstat" {
			return Outcome{}, errors.New("Wordstat binding returned an unexpected contract")
		}
		demand = extractDemand(wordstat, w.Phrase, before, current)
		if demand == nil {
			demandUnavailable = wordstat.unavailableFor(monday)
			if !demandUnavailable {
				return Outcome{Status: "incomplete"}, nil
			}
		}
	}
	var trendsObs *Trends
	trendsUnavailable := false
	if w.TrendsBinding != "" {
		err = read(ctx, w.TrendsBinding, map[string]string{"product_key": w.Phrase, "query": w.Phrase, "geo": w.TrendsGeo, "monday": w.Monday}, &trends)
		if err != nil {
			return Outcome{}, fmt.Errorf("read search interest: %w", err)
		}
		if trends.SchemaVersion != "aifence.v1" || trends.Source != "research" {
			return Outcome{}, errors.New("Google Trends binding returned an unexpected contract")
		}
		trendsObs = extractTrends(trends, w.Phrase, before, current)
		if trendsObs == nil {
			trendsUnavailable = trends.unavailableFor(monday)
			if !trendsUnavailable {
				return Outcome{Status: "incomplete"}, nil
			}
		}
	}
	if metrika.Status != "ok" || (w.MarketBinding != "" && market.Status != "ok" && market.Status != "partial") {
		return Outcome{Status: "incomplete"}, nil
	}
	delta := metrikaDelta(metrika, w.Metric, current)
	if delta == nil {
		return Outcome{Status: "incomplete"}, nil
	}
	trigger := ""
	if *delta.Value <= -w.DropPct {
		trigger = "metrika"
	}
	if demand != nil && w.DemandDropPct > 0 && demand.ChangePct != nil && *demand.ChangePct <= -w.DemandDropPct {
		if trigger != "" {
			trigger = "metrika_and_wordstat"
		} else {
			trigger = "wordstat"
		}
	}
	if trendsObs != nil && w.DemandDropPct > 0 && trendsObs.ChangePct != nil && *trendsObs.ChangePct <= -w.DemandDropPct {
		switch trigger {
		case "":
			trigger = "trends"
		case "metrika":
			trigger = "metrika_and_trends"
		case "wordstat":
			trigger = "wordstat_and_trends"
		case "metrika_and_wordstat":
			trigger = "metrika_wordstat_trends"
		}
	}
	if trigger == "" {
		if demandUnavailable || trendsUnavailable {
			return Outcome{Status: "incomplete"}, nil
		}
		return Outcome{Status: "quiet"}, nil
	}
	if !approved {
		return Outcome{Status: "model_route_unapproved"}, nil
	}
	sig := Signal{
		Trigger: trigger, Metric: w.Metric, ChangePct: *delta.Value, BaselineFrom: before.From, BaselineTo: before.To,
		CurrentFrom: current.From, CurrentTo: current.To, ObservationID: delta.ID, ObservationRef: delta.SourceRef.Locator,
		Demand: demand, Trends: trendsObs, TrendsStatus: trends.Status, WordstatStatus: wordstat.Status,
		ContextSKU: w.SKU, MarketStatus: market.Status,
		MissingPeriods: market.MissingPeriods,
	}
	if w.MarketBinding != "" {
		sig.PriceFamilies = priceFamilies(market, w.SKU)
	}
	id, err := launch(ctx, sig)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: "submitted", AnalysisRunID: id}, nil
}

func bounded(s string, n int) bool { return strings.TrimSpace(s) != "" && len(s) <= n }

// priceFamilies keeps only the series Market Watch itself marked comparable,
// and only those observed in both weeks. It recomputes nothing.
func priceFamilies(d weekDelta, sku string) []PriceFamily {
	var out []PriceFamily
	for _, s := range d.Series {
		if !s.Comparable || s.Baseline == nil || s.Current == nil || s.DeltaPct == nil || s.Currency != "RUB" {
			continue
		}
		if !bounded(s.Baseline.EvidenceID, 512) || !bounded(s.Current.EvidenceID, 512) || s.Baseline.Unit != "RUB" || s.Current.Unit != "RUB" {
			continue
		}
		pct := *s.DeltaPct
		out = append(out, PriceFamily{
			SKU: sku, Seller: s.Seller, PriceBasis: s.PriceBasis, Name: s.Name,
			BaselineID: s.Baseline.EvidenceID, CurrentID: s.Current.EvidenceID,
			BaselineAt: s.Baseline.CapturedAt, CurrentAt: s.Current.CapturedAt,
			BaselineRUB: s.Baseline.Value, CurrentRUB: s.Current.Value, ChangePct: &pct,
			Locator: s.URL, Quantity: s.Quantity, MaxQuantity: s.MaxQuantity,
		})
	}
	return out
}

// metrikaDelta returns the published relative change for the current week.
func metrikaDelta(b batch, metric string, current period) *observation {
	var delta *observation
	for i := range b.Observations {
		o := &b.Observations[i]
		if o.ProductKey != "store" || o.MetricKey != metric+".relative_change_pct" || o.Period != current {
			continue
		}
		n, e := strconv.ParseUint(strings.TrimPrefix(o.SourceRef.SourceID, "yandex-metrika:"), 10, 64)
		if !strings.HasPrefix(o.SourceRef.SourceID, "yandex-metrika:") || e != nil || n == 0 || o.Status != "observed" || o.Unit != "percent" || o.Value == nil || !bounded(o.ID, 256) || delta != nil {
			return nil
		}
		delta = o
	}
	return delta
}
