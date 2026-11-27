// Package sourcewatch checks completed store-level periods against an owner-selected threshold.
package sourcewatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"math"
	"strconv"
	"strings"
	"time"
)

type Window struct {
	MetrikaBinding  string  `json:"metrika_binding"`
	MarketBinding   string  `json:"market_binding,omitempty"`
	WordstatBinding string  `json:"wordstat_binding,omitempty"`
	Phrase          string  `json:"phrase,omitempty"`
	DemandDropPct   float64 `json:"demand_drop_pct,omitempty"`
	SKU             string  `json:"sku,omitempty"`
	Metric          string  `json:"metric"`
	DropPct         float64 `json:"drop_pct"`
	Monday          string  `json:"monday"`
}

func (w Window) Validate() (time.Time, error) {
	if w.MarketBinding == "" && w.WordstatBinding == "" {
		return time.Time{}, errors.New("configure Wordstat or Market Watch as a second source")
	}
	bindings := []string{w.MetrikaBinding}
	if w.MarketBinding != "" {
		bindings = append(bindings, w.MarketBinding)
	}
	if w.WordstatBinding != "" {
		bindings = append(bindings, w.WordstatBinding)
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
	if (w.WordstatBinding != "" && (!bounded(w.Phrase, 256) || strings.TrimSpace(w.Phrase) != w.Phrase)) || (w.WordstatBinding == "" && (w.Phrase != "" || w.DemandDropPct != 0)) {
		return time.Time{}, errors.New("set a search phrase with the Wordstat binding")
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
}
type offerDetails struct {
	MatchStatus string `json:"match_status"`
	CapturedAt  string `json:"captured_at"`
	PriceBasis  string `json:"price_basis"`
	Quantity    *int64 `json:"quantity"`
	MaxQuantity *int64 `json:"max_quantity"`
}
type observation struct {
	ID         string       `json:"id"`
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
type Offer struct {
	ID          string  `json:"id"`
	Source      string  `json:"source"`
	PriceRUB    float64 `json:"price_rub"`
	CapturedAt  string  `json:"captured_at"`
	PriceBasis  string  `json:"price_basis"`
	Quantity    *int64  `json:"quantity,omitempty"`
	MaxQuantity *int64  `json:"max_quantity,omitempty"`
}
type Signal struct {
	WordstatStatus string  `json:"wordstat_status,omitempty"`
	Trigger        string  `json:"trigger"`
	Demand         *Demand `json:"demand,omitempty"`
	Metric         string  `json:"store_metric"`
	ChangePct      float64 `json:"change_pct"`
	BaselineFrom   string  `json:"baseline_from"`
	BaselineTo     string  `json:"baseline_to"`
	CurrentFrom    string  `json:"current_from"`
	CurrentTo      string  `json:"current_to"`
	ObservationID  string  `json:"observation_id"`
	ContextSKU     string  `json:"market_sku"`
	MarketStatus   string  `json:"market_status"`
	Offers         []Offer `json:"sample_offers"`
}
type Outcome struct {
	Status        string `json:"status"`
	AnalysisRunID string `json:"analysis_run_id,omitempty"`
}
type Read func(context.Context, string, any, any) error
type Launch func(context.Context, Signal) (string, error)

func Check(ctx context.Context, w Window, approved bool, read Read, launch Launch) (Outcome, error) {
	monday, err := w.Validate()
	if err != nil {
		return Outcome{}, err
	}
	date := func(offset int) string { return monday.AddDate(0, 0, offset).Format(time.DateOnly) }
	before, current := period{date(-14), date(-8)}, period{date(-7), date(-1)}
	var metrika, market, wordstat batch
	// Fixed source parameters keep filesystem selection and arbitrary connector calls out of this job.
	err = read(ctx, w.MetrikaBinding, map[string]any{"product_key": "store", "metric_keys": []string{w.Metric}, "baseline_from": before.From, "baseline_to": before.To, "current_from": current.From, "current_to": current.To}, &metrika)
	if err != nil {
		return Outcome{}, fmt.Errorf("read store comparison: %w", err)
	}
	if metrika.SchemaVersion != "aifence.v1" || metrika.Source != "metrika" {
		return Outcome{}, errors.New("Metrika binding returned an unexpected contract")
	}
	if w.MarketBinding != "" {
		err = read(ctx, w.MarketBinding, map[string]any{"product_key": w.SKU, "exact_sku": true, "limit": 5}, &market)
		if err != nil {
			return Outcome{}, fmt.Errorf("read market sample: %w", err)
		}
		if market.SchemaVersion != "aifence.v1" || market.Source != "market" {
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
	if metrika.Status != "ok" || (w.MarketBinding != "" && market.Status != "ok" && market.Status != "partial") {
		return Outcome{Status: "incomplete"}, nil
	}
	var delta *observation
	for i := range metrika.Observations {
		o := &metrika.Observations[i]
		if o.ProductKey != "store" || o.MetricKey != w.Metric+".relative_change_pct" || o.Period != current {
			continue
		}
		n, e := strconv.ParseUint(strings.TrimPrefix(o.SourceRef.SourceID, "yandex-metrika:"), 10, 64)
		if !strings.HasPrefix(o.SourceRef.SourceID, "yandex-metrika:") || e != nil || n == 0 || o.Status != "observed" || o.Unit != "percent" || o.Value == nil || !bounded(o.ID, 256) || delta != nil {
			return Outcome{Status: "incomplete"}, nil
		}
		delta = o
	}
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
	if trigger == "" {
		if demandUnavailable {
			return Outcome{Status: "incomplete"}, nil
		}
		return Outcome{Status: "quiet"}, nil
	}
	if !approved {
		return Outcome{Status: "model_route_unapproved"}, nil
	}
	sig := Signal{WordstatStatus: wordstat.Status, Trigger: trigger, Demand: demand, Metric: w.Metric, ChangePct: *delta.Value, BaselineFrom: before.From, BaselineTo: before.To, CurrentFrom: current.From, CurrentTo: current.To, ObservationID: delta.ID, ContextSKU: w.SKU, MarketStatus: market.Status, Offers: []Offer{}}
	if len(market.Observations) > 5 {
		return Outcome{}, errors.New("market sample exceeded requested bound")
	}
	for _, o := range market.Observations {
		stamp, e := time.Parse(time.RFC3339Nano, o.Details.CapturedAt)
		if o.Status != "observed" || !strings.EqualFold(o.ProductKey, w.SKU) || o.MetricKey != "displayed_price" || o.Unit != "RUB" || o.Value == nil || *o.Value <= 0 || o.Details.MatchStatus != "exact_sku" || !bounded(o.ID, 512) || !bounded(o.SourceRef.SourceID, 128) || !bounded(o.Details.PriceBasis, 128) || e != nil {
			continue
		}
		sig.Offers = append(sig.Offers, Offer{o.ID, o.SourceRef.SourceID, *o.Value, stamp.UTC().Format(time.RFC3339Nano), o.Details.PriceBasis, o.Details.Quantity, o.Details.MaxQuantity})
	}
	id, err := launch(ctx, sig)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: "submitted", AnalysisRunID: id}, nil
}
func bounded(s string, n int) bool { return strings.TrimSpace(s) != "" && len(s) <= n }
