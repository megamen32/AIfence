package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Segment is either all or an explicit native Metrika filter. A device label is
// not assumed to be its API identifier: discover it with run_report/drilldown.
func segmentFilter(segment string) (string, error) {
	if segment == "all" {
		return "", nil
	}
	if strings.HasPrefix(segment, "filter:") && strings.TrimSpace(strings.TrimPrefix(segment, "filter:")) != "" && len(segment) <= 1024 {
		return strings.TrimSpace(strings.TrimPrefix(segment, "filter:")), nil
	}
	return "", fmt.Errorf("use all or filter:<verified Metrika filter from report>")
}

// The external MCP computes live comparison values. We only normalize its
// keyed output into the shared evidence contract and convert percent to ratio.
func (s Service) compareLive(ctx context.Context, a, b Query, c Settings) (Batch, error) {
	if a.ProductKey != "store" {
		return Batch{}, fmt.Errorf("live MVP supports store-level metrics; SKU needs explicit mapping")
	}
	if b.To >= time.Now().UTC().Format(time.DateOnly) {
		return Batch{}, fmt.Errorf("choose completed days; verify counter timezone")
	}
	filter, e := segmentFilter(a.Segment)
	if e != nil {
		return Batch{}, e
	}
	out := batch()
	out.Warnings = append(out.Warnings, "Live comparison computed by BoxLab; confirm collection, weekdays, timezone and release date. Association is not causation.")
	type pair struct {
		A        *float64 `json:"a"`
		B        *float64 `json:"b"`
		Delta    *float64 `json:"delta"`
		DeltaPct *float64 `json:"delta_pct"`
	}
	type result struct {
		Totals map[string]pair `json:"totals"`
		Meta   reportMeta      `json:"meta"`
	}
	for _, k := range a.MetricKeys {
		d, ok := metrics[k]
		args := map[string]any{"counterId": c.Counter, "metrics": []string{d.api}, "date1A": a.From, "date2A": a.To, "date1B": b.From, "date2B": b.To, "limit": 1, "accuracy": "full"}
		if filter != "" {
			args["filtersA"] = filter
			args["filtersB"] = filter
		}
		rawArgs, _ := json.Marshal(args)
		ref := Ref{SourceID: "yandex-metrika:" + strconv.FormatInt(c.Counter, 10), Locator: "run_comparison " + string(rawArgs)}
		old := missing(a, k, ref, "Unsupported metric or e-commerce instrumentation not confirmed.")
		cur := missing(b, k, ref, "Unsupported metric or e-commerce instrumentation not confirmed.")
		var r result
		if ok && (!d.ec || c.Ecommerce) {
			old.Unit, cur.Unit = d.unit, d.unit
			old.Currency, cur.Currency = d.currency, d.currency
			raw, err := s.call(ctx, "run_comparison", args)
			if err != nil || json.Unmarshal(unwrap(raw), &r) != nil {
				old.Caveats = []string{"MCP comparison failed or invalid response."}
				cur.Caveats = old.Caveats
			} else {
				old = observed(old, r.Totals[d.api].A, d.scale, r.Meta)
				cur = observed(cur, r.Totals[d.api].B, d.scale, r.Meta)
			}
		}
		// Construct contract records without recomputing the live numeric delta.
		// The historical-only path retains deltas() for imported aggregates.
		abs := Observation{ID: hash([]string{old.ID, cur.ID, "absolute"}), Kind: "metric", ProductKey: cur.ProductKey, MetricKey: k + ".absolute_change", Unit: cur.Unit, Currency: cur.Currency, Period: cur.Period, Segment: cur.Segment, SourceRef: ref, Status: "missing", ComparisonKey: k, Details: map[string]any{"baseline_id": old.ID, "current_id": cur.ID, "baseline_period": old.Period}}
		pct := abs
		pct.ID = hash([]string{old.ID, cur.ID, "relative"})
		pct.MetricKey = k + ".relative_change_pct"
		pct.Unit = "percent"
		pct.Currency = ""
		if d.unit == "ratio" {
			abs.MetricKey = k + ".change_pp"
			abs.Unit = "pp"
		}
		if old.Status == "observed" && cur.Status == "observed" {
			v := r.Totals[d.api]
			if v.Delta != nil {
				abs.Value = num(*v.Delta)
				abs.Status = "observed"
			}
			if old.Value != nil && *old.Value != 0 && v.DeltaPct != nil {
				pct.Value = num(*v.DeltaPct)
				pct.Status = "observed"
			}
		}
		if abs.Status == "missing" {
			abs.Caveats = []string{"Comparison unavailable or sampled."}
		}
		if pct.Status == "missing" {
			pct.Caveats = []string{"Comparison unavailable, sampled, or zero baseline."}
		}
		for _, o := range []Observation{old, cur, abs, pct} {
			out.Observations = append(out.Observations, o)
			if o.Status != "observed" {
				out.Status = "partial"
			}
		}
	}
	return out, nil
}
