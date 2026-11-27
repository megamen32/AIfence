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
	MetrikaBinding  string `json:"metrika_binding"`
	MarketBinding   string `json:"market_binding,omitempty"`
	WordstatBinding string `json:"wordstat_binding,omitempty"`
	TrendsBinding   string `json:"trends_binding,omitempty"`
	TrendsGeo       string `json:"trends_geo,omitempty"`
	Phrase          string `json:"phrase,omitempty"`
	// ProductKey selects an explicitly labelled product family snapshot. Empty
	// keeps the live store-level path. A product is never relabelled as store.
	ProductKey    string  `json:"product_key,omitempty"`
	Segment       string  `json:"segment,omitempty"`
	DemandDropPct float64 `json:"demand_drop_pct,omitempty"`
	SKU           string  `json:"sku,omitempty"`
	// SKUs selects an explicit reviewed set for family_price_week_delta. Family
	// lets Market Watch discover candidates from indexed titles. Both exclude
	// the single exact SKU.
	SKUs    []string `json:"skus,omitempty"`
	Family  string   `json:"family,omitempty"`
	Metric  string   `json:"metric"`
	DropPct float64  `json:"drop_pct"`
	Monday  string   `json:"monday"`
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
	familySelected := len(w.SKUs) > 0 || w.Family != ""
	if len(w.SKUs) > 0 && w.Family != "" {
		return time.Time{}, errors.New("choose either an explicit SKU set or a family phrase")
	}
	if familySelected && w.SKU != "" {
		return time.Time{}, errors.New("a family selection cannot be combined with one exact SKU")
	}
	if familySelected && w.MarketBinding == "" {
		return time.Time{}, errors.New("a family selection requires the market binding")
	}
	seenSKU := map[string]bool{}
	for _, sku := range w.SKUs {
		if !bounded(sku, 300) || strings.TrimSpace(sku) != sku || seenSKU[sku] {
			return time.Time{}, errors.New("SKUs must be distinct exact keys without outer whitespace")
		}
		seenSKU[sku] = true
	}
	if len(w.SKUs) > 20 || (w.Family != "" && (!bounded(w.Family, 200) || strings.TrimSpace(w.Family) != w.Family || len(w.Family) < 4)) {
		return time.Time{}, errors.New("select at most 20 SKUs or a family phrase of 4 to 200 characters")
	}
	// The exact SKU is one way to select a price scope. A family selection is
	// the other, so the two cannot be required at the same time.
	if (w.MarketBinding != "" && !familySelected && (!bounded(w.SKU, 128) || strings.TrimSpace(w.SKU) != w.SKU)) || (w.MarketBinding == "" && w.SKU != "") {
		return time.Time{}, errors.New("set an exact SKU with the market binding")
	}
	usesPhrase := w.WordstatBinding != "" || w.TrendsBinding != ""
	if w.ProductKey != "" && (!bounded(w.ProductKey, 128) || strings.TrimSpace(w.ProductKey) != w.ProductKey) {
		return time.Time{}, errors.New("product_key must be an exact family key without outer whitespace")
	}
	if w.Segment != "" && (!bounded(w.Segment, 64) || strings.TrimSpace(w.Segment) != w.Segment) {
		return time.Time{}, errors.New("segment must not have outer whitespace")
	}
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
	Revision string `json:"revision,omitempty"`
}
type observation struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	ProductKey string         `json:"product_key"`
	MetricKey  string         `json:"metric_key"`
	Status     string         `json:"status"`
	Unit       string         `json:"unit"`
	Value      *float64       `json:"value"`
	Period     period         `json:"period"`
	Segment    string         `json:"segment,omitempty"`
	SourceRef  sourceRef      `json:"source_ref"`
	Details    map[string]any `json:"details,omitempty"`
	Caveats    []string       `json:"caveats,omitempty"`
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
	SKU    string `json:"sku,omitempty"`
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
	SKU          string   `json:"sku"`
	Seller       string   `json:"seller"`
	PriceBasis   string   `json:"price_basis"`
	Name         string   `json:"name,omitempty"`
	BaselineID   string   `json:"baseline_observation_id"`
	CurrentID    string   `json:"current_observation_id"`
	BaselineAt   string   `json:"baseline_captured_at"`
	CurrentAt    string   `json:"current_captured_at"`
	BaselineRUB  float64  `json:"baseline_rub"`
	CurrentRUB   float64  `json:"current_rub"`
	ChangePct    *float64 `json:"relative_change_pct,omitempty"`
	Locator      string   `json:"locator"`
	Quantity     *int64   `json:"quantity,omitempty"`
	MaxQuantity  *int64   `json:"max_quantity,omitempty"`
	BaselineWeek period   `json:"baseline_week,omitempty"`
	CurrentWeek  period   `json:"current_week,omitempty"`
}

type Signal struct {
	SignalID               string          `json:"signal_id"`
	Trigger                string          `json:"trigger"`
	Metric                 string          `json:"store_metric"`
	ChangePct              float64         `json:"change_pct"`
	StoreDropThresholdPct  float64         `json:"store_drop_threshold_pct"`
	DemandDropThresholdPct float64         `json:"demand_drop_threshold_pct"`
	SourceWarnings         []string        `json:"source_warnings,omitempty"`
	BaselineFrom           string          `json:"baseline_from"`
	BaselineTo             string          `json:"baseline_to"`
	CurrentFrom            string          `json:"current_from"`
	CurrentTo              string          `json:"current_to"`
	ObservationID          string          `json:"observation_id"`
	ObservationRef         string          `json:"observation_ref,omitempty"`
	Demand                 *Demand         `json:"demand,omitempty"`
	Trends                 *Trends         `json:"trends,omitempty"`
	TrendsStatus           string          `json:"trends_status,omitempty"`
	WordstatStatus         string          `json:"wordstat_status,omitempty"`
	MarketStatus           string          `json:"market_status"`
	ContextSKU             string          `json:"market_sku,omitempty"`
	PriceFamilies          []PriceFamily   `json:"price_families,omitempty"`
	MissingPeriods         []missingPeriod `json:"missing_periods,omitempty"`
	ProductKey             string          `json:"product_key,omitempty"`
	Segment                string          `json:"segment,omitempty"`
	FamilyChange           *FamilyChange   `json:"family_change,omitempty"`
	PriceCoverage          *familyCoverage `json:"price_coverage,omitempty"`
}

type Outcome struct {
	Status        string   `json:"status"`
	AnalysisRunID string   `json:"analysis_run_id,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
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
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	monday, err := w.Validate()
	if err != nil {
		return Outcome{}, err
	}
	date := func(offset int) string { return monday.AddDate(0, 0, offset).Format(time.DateOnly) }
	before, current := period{date(-14), date(-8)}, period{date(-7), date(-1)}
	var metrika, wordstat, trends batch
	var market weekDelta
	var family familyDelta
	var warnings []string
	familySelected := len(w.SKUs) > 0 || w.Family != ""
	marketReadFailed := false
	// Fixed source parameters keep filesystem selection and arbitrary connector calls out of this job.
	target := w.ProductKey
	if target == "" {
		target = "store"
	}
	query := map[string]any{"product_key": target, "metric_keys": []string{w.Metric}, "baseline_from": before.From, "baseline_to": before.To, "current_from": current.From, "current_to": current.To}
	if w.ProductKey == "" {
		query["source_mode"] = "live"
	}
	if w.Segment != "" {
		query["segment"] = w.Segment
	}
	err = read(ctx, w.MetrikaBinding, query, &metrika)
	if err != nil {
		return Outcome{}, fmt.Errorf("read %s comparison: %w", target, err)
	}
	if metrika.SchemaVersion != "aifence.v1" || metrika.Source != "metrika" {
		return Outcome{}, errors.New("Metrika binding returned an unexpected contract")
	}
	warnings = append(warnings, comparisonWarnings(metrika)...)
	if w.MarketBinding != "" {
		if familySelected {
			err = read(ctx, w.MarketBinding, map[string]any{"skus": w.SKUs, "family": w.Family, "baseline_from": before.From, "baseline_to": before.To, "current_from": current.From, "current_to": current.To}, &family)
		} else {
			err = read(ctx, w.MarketBinding, map[string]string{"product_key": w.SKU, "baseline_from": before.From, "baseline_to": before.To, "current_from": current.From, "current_to": current.To}, &market)
		}
		if err != nil {
			if stop := interruptedRead(ctx, err); stop != nil {
				return Outcome{}, fmt.Errorf("read price week delta: %w", stop)
			}
			// A failed read may have partially decoded output. None of it is evidence.
			market = weekDelta{Status: "unavailable"}
			family = familyDelta{Status: "unavailable"}
			marketReadFailed = true
			warnings = append(warnings, "market_watch: source read failed; price evidence is unavailable")
		} else if familySelected {
			if family.Coverage.SelectedSKUs == 0 || family.Status == "" {
				return Outcome{}, errors.New("market binding returned an unexpected family contract")
			}
			if family.BaselineWeek != before || family.CurrentWeek != current {
				return Outcome{}, errors.New("family price delta returned different weeks")
			}
			if family.Status == "partial" {
				warnings = append(warnings, "market_watch: price history is incomplete for some seller series")
			}
		} else {
			if market.SKU == "" || market.Status == "" {
				return Outcome{}, errors.New("market binding returned an unexpected contract")
			}
			if market.SKU != w.SKU || market.BaselineWeek != before || market.CurrentWeek != current {
				return Outcome{}, errors.New("market binding returned a different SKU or comparison window")
			}
			if market.Status == "partial" {
				warnings = append(warnings, "market_watch: price history is incomplete for some seller series")
			}
		}
	}
	var demand *Demand
	demandUnavailable := false
	if w.WordstatBinding != "" {
		err = read(ctx, w.WordstatBinding, map[string]string{"phrase": w.Phrase, "monday": w.Monday}, &wordstat)
		if err != nil {
			if stop := interruptedRead(ctx, err); stop != nil {
				return Outcome{}, fmt.Errorf("read demand comparison: %w", stop)
			}
			wordstat = batch{Status: "unavailable"}
			demandUnavailable = true
			warnings = append(warnings, "wordstat: source read failed; query counts are unavailable")
		} else {
			if wordstat.SchemaVersion != "aifence.v1" || wordstat.Source != "wordstat" {
				return Outcome{}, errors.New("Wordstat binding returned an unexpected contract")
			}
			demand = extractDemand(wordstat, w.Phrase, before, current)
			if demand == nil {
				demandUnavailable = wordstat.unavailableFor(monday)
				if !demandUnavailable {
					return Outcome{Status: "incomplete", Warnings: append(warnings, "wordstat: the requested weekly comparison is incomplete")}, nil
				}
				warnings = append(warnings, "wordstat: query counts for the requested weeks are unavailable")
			}
		}
	}
	var trendsObs *Trends
	trendsUnavailable := false
	if w.TrendsBinding != "" {
		geo := w.TrendsGeo
		if geo == "" {
			geo = "RU"
		}
		err = read(ctx, w.TrendsBinding, map[string]string{"product_key": w.Phrase, "query": w.Phrase, "geo": geo, "monday": w.Monday}, &trends)
		if err != nil {
			if stop := interruptedRead(ctx, err); stop != nil {
				return Outcome{}, fmt.Errorf("read search interest: %w", stop)
			}
			trends = batch{Status: "unavailable"}
			trendsUnavailable = true
			warnings = append(warnings, "google_trends: source read failed; search interest is unavailable")
		} else {
			if trends.SchemaVersion != "aifence.v1" || trends.Source != "research" {
				return Outcome{}, errors.New("Google Trends binding returned an unexpected contract")
			}
			trendsObs = extractTrends(trends, w.Phrase, before, current)
			if trendsObs == nil {
				trendsUnavailable = trends.unavailableFor(monday)
				if !trendsUnavailable {
					return Outcome{Status: "incomplete", Warnings: append(warnings, "google_trends: the requested weekly comparison is incomplete")}, nil
				}
				warnings = append(warnings, "google_trends: search interest for the requested weeks is unavailable")
			} else {
				trendsObs.Geo = geo
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	marketStatus := market.Status
	if familySelected {
		marketStatus = family.Status
	}
	// A market answer that names the weeks it lacks is a valid observation of
	// absence. It must not silence a store or demand signal that the other
	// sources did prove, and it must not become a price cause on its own.
	// Only a malformed envelope, or no evidence from any source, is incomplete.
	marketUsable := marketStatus == "ok" || marketStatus == "partial" || marketStatus == "unavailable"
	if metrika.Status != "ok" {
		return Outcome{Status: "incomplete", Warnings: append(warnings, fmt.Sprintf("metrika: no complete %s comparison for %s..%s and %s..%s", w.Metric, before.From, before.To, current.From, current.To))}, nil
	}
	if w.MarketBinding != "" && !marketUsable {
		return Outcome{Status: "incomplete", Warnings: append(warnings, "market_watch: the requested price comparison has no usable status")}, nil
	}
	var delta *observation
	var computed *FamilyChange
	if w.ProductKey == "" {
		delta = metrikaDelta(metrika, w.Metric, current)
	} else {
		// Prefer a change Metrika already computed for the exact product and
		// window. Only a snapshot without that row falls back to the two raw
		// weeks, and then the signal records both source observations.
		delta = productDelta(metrika, w.ProductKey, w.Segment, w.Metric, current)
		if delta == nil {
			computed = familyChange(metrika, w.ProductKey, w.Segment, w.Metric, before, current)
		}
	}
	if delta == nil && computed == nil {
		return Outcome{Status: "incomplete", Warnings: append(warnings, "metrika: no unique observed relative change with the requested source, metric and periods")}, nil
	}
	changePct, observationID, observationRef := 0.0, "", ""
	if delta != nil {
		changePct, observationID, observationRef = *delta.Value, delta.ID, delta.SourceRef.Locator
	} else if computed.ChangePct == nil {
		return Outcome{Status: "incomplete", Warnings: append(warnings, "metrika: zero baseline; relative change is undefined")}, nil
	} else {
		changePct, observationID, observationRef = *computed.ChangePct, computed.CurrentID, computed.CurrentRef
	}
	trigger := ""
	if changePct <= -w.DropPct {
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
		if marketReadFailed || demandUnavailable || trendsUnavailable {
			return Outcome{Status: "incomplete", Warnings: warnings}, nil
		}
		return Outcome{Status: "quiet", Warnings: warnings}, nil
	}
	if !approved {
		return Outcome{Status: "model_route_unapproved", Warnings: warnings}, nil
	}
	sig := Signal{
		Trigger: trigger, Metric: w.Metric, ChangePct: changePct, BaselineFrom: before.From, BaselineTo: before.To,
		StoreDropThresholdPct: w.DropPct, DemandDropThresholdPct: w.DemandDropPct, SourceWarnings: warnings,
		CurrentFrom: current.From, CurrentTo: current.To, ObservationID: observationID, ObservationRef: observationRef,
		ProductKey: w.ProductKey, Segment: w.Segment, FamilyChange: computed,
		Demand: demand, Trends: trendsObs, TrendsStatus: trends.Status, WordstatStatus: wordstat.Status,
		ContextSKU: w.SKU, MarketStatus: marketStatus,
		MissingPeriods: market.MissingPeriods,
	}
	if familySelected && !marketReadFailed {
		sig.PriceFamilies, sig.MissingPeriods = familyPriceFamilies(family)
		sig.PriceCoverage = &family.Coverage
	} else if w.MarketBinding != "" && !marketReadFailed {
		sig.PriceFamilies = priceFamilies(market, w.SKU)
	}
	id, err := launch(ctx, sig)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: "submitted", AnalysisRunID: id, Warnings: warnings}, nil
}

// interruptedRead separates an interrupted check from one unavailable optional source.
// Provider errors are never copied into public warnings: they can contain credentials.
func interruptedRead(ctx context.Context, err error) error {
	if stopped := ctx.Err(); stopped != nil {
		return stopped
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
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

// comparisonWarnings keeps bounded source diagnostics, including a missing
// relative change when the counter reports a zero baseline.
func comparisonWarnings(b batch) []string {
	var out []string
	seen := map[string]bool{}
	add := func(text string) {
		text = strings.TrimSpace(text)
		if text != "" && len(text) <= 1024 && len(out) < 16 && !seen[text] {
			seen[text] = true
			out = append(out, "metrika: "+text)
		}
	}
	for _, warning := range b.Warnings {
		add(warning)
	}
	for _, observation := range b.Observations {
		if observation.Status == "observed" && observation.Value != nil {
			continue
		}
		for _, caveat := range observation.Caveats {
			add(fmt.Sprintf("%s %s..%s: %s", observation.MetricKey, observation.Period.From, observation.Period.To, caveat))
		}
	}
	return out
}
