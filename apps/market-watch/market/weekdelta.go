package market

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	catalog "agent/collector"
	"agent/internal/db"
)

// WeekDeltaInput compares two complete Monday..Sunday UTC weeks. The current
// week starts after the baseline week ends.
type WeekDeltaInput struct {
	ProductKey   string `json:"product_key" jsonschema:"description=Exact manufacturer SKU or store article already observed by Market Watch."`
	BaselineFrom string `json:"baseline_from" jsonschema:"description=Baseline week start, YYYY-MM-DD, a Monday."`
	BaselineTo   string `json:"baseline_to" jsonschema:"description=Baseline week end, YYYY-MM-DD, the Sunday of the same week."`
	CurrentFrom  string `json:"current_from" jsonschema:"description=Current week start, YYYY-MM-DD, a Monday after the baseline week."`
	CurrentTo    string `json:"current_to" jsonschema:"description=Current week end, YYYY-MM-DD, the Sunday of the same week."`
}

// WeekRead is the latest stored observation of one series inside one week.
type WeekRead struct {
	CapturedAt string  `json:"captured_at"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	EvidenceID string  `json:"evidence_id"`
	URL        string  `json:"url"`
}

// WeekDeltaSeries is one offer identity followed across both weeks. A series
// observed in only one week keeps the other week absent and is never counted
// as a price change.
type WeekDeltaSeries struct {
	Seller      string    `json:"seller"`
	URL         string    `json:"url"`
	PriceBasis  string    `json:"price_basis"`
	Currency    string    `json:"currency"`
	Quantity    *int64    `json:"quantity,omitempty"`
	MaxQuantity *int64    `json:"max_quantity,omitempty"`
	Name        string    `json:"name,omitempty"`
	Baseline    *WeekRead `json:"baseline,omitempty"`
	Current     *WeekRead `json:"current,omitempty"`
	Delta       *float64  `json:"delta,omitempty"`
	DeltaPct    *float64  `json:"delta_pct,omitempty"`
	Comparable  bool      `json:"comparable"`
	Reason      string    `json:"reason"`
	EvidenceIDs []string  `json:"evidence_ids"`
}

// MissingPeriod names a seller and week for which no comparable read exists.
type MissingPeriod struct {
	SKU    string `json:"sku"`
	Seller string `json:"seller"`
	URL    string `json:"url"`
	Period string `json:"period"`
	Reason string `json:"reason"`
}

type WeekDeltaSource struct {
	Seller  string `json:"seller"`
	Reads   int    `json:"reads"`
	Series  int    `json:"series"`
	Missing int    `json:"missing"`
}

// WeekDelta is the typed result of price_week_delta. Evidence keeps the shared
// aifence.v1 batch so other apps can import it without a second contract.
type WeekDelta struct {
	SKU             string            `json:"sku"`
	BaselineWeek    Period            `json:"baseline_week"`
	CurrentWeek     Period            `json:"current_week"`
	ComparablePairs int               `json:"comparable_pairs"`
	MissingPeriods  []MissingPeriod   `json:"missing_periods"`
	Sources         []WeekDeltaSource `json:"sources"`
	Series          []WeekDeltaSeries `json:"series"`
	Status          string            `json:"status"`
	AsOf            string            `json:"as_of"`
	Truncated       bool              `json:"truncated"`
	Warnings        []string          `json:"warnings"`
	Evidence        Evidence          `json:"evidence"`
}

type deltaSeriesKey struct {
	seller, url, basis, currency string
	quantity, maximum            int64
	terms                        comparisonTerms
}

func deltaKeyOf(o Observation) deltaSeriesKey {
	return deltaSeriesKey{o.Details.Seller, o.SourceRef.URL, o.Details.PriceBasis, o.Currency,
		quantityValue(o.Details.Quantity), quantityValue(o.Details.MaxQuantity), termsOf(o)}
}

func deltaLabel(k deltaSeriesKey) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d/%d|%v", k.seller, k.url, k.basis, k.currency, k.quantity, k.maximum, k.terms)
}

func parseWeek(label, from, to string) (time.Time, time.Time, error) {
	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%s must be YYYY-MM-DD", label)
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%s must be YYYY-MM-DD", label)
	}
	if start.Weekday() != time.Monday {
		return time.Time{}, time.Time{}, fmt.Errorf("%s must start on a Monday", label)
	}
	if end.Sub(start) != 6*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("%s must end on the Sunday of the same week", label)
	}
	return start, end.AddDate(0, 0, 1), nil
}

// WeekDelta reads stored observations and compares the latest read of each
// series in two complete weeks. It performs no network requests.
func (s *Service) WeekDelta(ctx context.Context, in WeekDeltaInput) (WeekDelta, error) {
	out := WeekDelta{Series: []WeekDeltaSeries{}, MissingPeriods: []MissingPeriod{}, Sources: []WeekDeltaSource{}, Warnings: []string{}}
	sku := strings.ToUpper(strings.TrimSpace(in.ProductKey))
	if sku == "" || len(sku) > 300 {
		return out, fmt.Errorf("exact SKU required")
	}
	baselineFrom, baselineEnd, err := parseWeek("baseline", in.BaselineFrom, in.BaselineTo)
	if err != nil {
		return out, err
	}
	currentFrom, currentEnd, err := parseWeek("current", in.CurrentFrom, in.CurrentTo)
	if err != nil {
		return out, err
	}
	if currentFrom.Before(baselineEnd) {
		return out, fmt.Errorf("current week must start after the baseline week")
	}
	if currentEnd.After(s.now().UTC()) {
		return out, fmt.Errorf("current week is not complete")
	}
	rows, truncated, err := s.weekRows(ctx, sku, baselineFrom, currentEnd)
	if err != nil {
		return out, err
	}
	if truncated {
		out.SKU = sku
		out.BaselineWeek = Period{From: in.BaselineFrom, To: in.BaselineTo}
		out.CurrentWeek = Period{From: in.CurrentFrom, To: in.CurrentTo}
		out.AsOf = s.now().UTC().Format(time.RFC3339Nano)
		out.Status, out.Truncated = "partial", true
		out.Warnings = []string{"Observation history exceeded 4000 rows; no incomplete price delta was calculated."}
		out.Evidence = deltaEvidence(out, s.now())
		return out, nil
	}
	out = GroupWeekDelta(rows, sku, baselineFrom, currentFrom, s.now())
	return out, nil
}

// GroupWeekDelta is the pure grouping shared by the service and tests. It
// takes the stored rows of one SKU in chronological order and the two week
// starts, and returns the latest read per series per week plus the summary.
func GroupWeekDelta(rows []catalog.Offer, sku string, baselineFrom, currentFrom, now time.Time) WeekDelta {
	out := WeekDelta{Series: []WeekDeltaSeries{}, MissingPeriods: []MissingPeriod{}, Sources: []WeekDeltaSource{}, Warnings: []string{}}
	baselineEnd := baselineFrom.AddDate(0, 0, 7)
	currentEnd := currentFrom.AddDate(0, 0, 7)
	baselines, currents := map[deltaSeriesKey]catalog.Offer{}, map[deltaSeriesKey]catalog.Offer{}
	readCounts := map[deltaSeriesKey]int{}
	latest := time.Time{}
	for _, row := range rows {
		if !strings.EqualFold(row.SKU, sku) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, row.CapturedAt)
		if err != nil {
			continue
		}
		key := deltaKeyOf(obs(row))
		if at.Before(baselineFrom) || !at.Before(currentEnd) || !at.Before(baselineEnd) && at.Before(currentFrom) {
			continue
		}
		readCounts[key]++
		if at.After(latest) {
			latest = at
		}
		switch {
		case at.Before(baselineFrom) || !at.Before(baselineEnd):
			// outside the baseline week
		default:
			prev, ok := baselines[key]
			prevAt, _ := time.Parse(time.RFC3339Nano, prev.CapturedAt)
			if !ok || at.After(prevAt) {
				baselines[key] = row
			}
		}
		switch {
		case at.Before(currentFrom) || !at.Before(currentEnd):
			// outside the current week
		default:
			prev, ok := currents[key]
			prevAt, _ := time.Parse(time.RFC3339Nano, prev.CapturedAt)
			if !ok || at.After(prevAt) {
				currents[key] = row
			}
		}
	}
	keys := map[deltaSeriesKey]bool{}
	for key := range baselines {
		keys[key] = true
	}
	for key := range currents {
		keys[key] = true
	}
	ordered := make([]deltaSeriesKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool { return deltaLabel(ordered[i]) < deltaLabel(ordered[j]) })
	out.SKU = sku
	out.BaselineWeek = Period{From: baselineFrom.Format(time.DateOnly), To: baselineFrom.AddDate(0, 0, 6).Format(time.DateOnly)}
	out.CurrentWeek = Period{From: currentFrom.Format(time.DateOnly), To: currentFrom.AddDate(0, 0, 6).Format(time.DateOnly)}
	out.AsOf = now.UTC().Format(time.RFC3339Nano)
	if !latest.IsZero() {
		out.AsOf = latest.UTC().Format(time.RFC3339Nano)
	}
	type sourceTotals struct{ reads, series, missing int }
	totals := map[string]*sourceTotals{}
	for _, key := range ordered {
		a, aOK := baselines[key]
		b, bOK := currents[key]
		series := WeekDeltaSeries{Seller: key.seller, URL: key.url, PriceBasis: key.basis, Currency: key.currency, EvidenceIDs: []string{}, Reason: "observed in both weeks"}
		if aOK {
			series.Quantity, series.MaxQuantity, series.Name = a.Quantity, a.MaxQuantity, a.Name
		} else {
			series.Quantity, series.MaxQuantity, series.Name = b.Quantity, b.MaxQuantity, b.Name
		}
		if aOK {
			series.Baseline = weekRead(a)
			series.EvidenceIDs = append(series.EvidenceIDs, series.Baseline.EvidenceID)
		}
		if bOK {
			series.Current = weekRead(b)
			series.EvidenceIDs = append(series.EvidenceIDs, series.Current.EvidenceID)
		}
		if t := totals[key.seller]; t == nil {
			totals[key.seller] = &sourceTotals{}
		}
		switch {
		case aOK && bOK && key.quantity > 0 && (key.basis == "displayed_total" || key.basis == "per_unit" && key.maximum >= key.quantity) && a.PriceMinor > 0 && b.PriceMinor > 0:
			delta := price(b.PriceMinor - a.PriceMinor)
			pct := 0.0
			if a.PriceMinor > 0 {
				pct = math.Round(float64(b.PriceMinor-a.PriceMinor)/float64(a.PriceMinor)*10000) / 100
			}
			series.Delta, series.DeltaPct, series.Comparable = &delta, &pct, true
			out.ComparablePairs++
			totals[key.seller].series++
		case aOK && bOK:
			series.Reason = "quantity or price basis is not verified for a numeric comparison"
		case aOK:
			series.Reason = "no observation in the current week"
			series.Comparable = false
			out.MissingPeriods = append(out.MissingPeriods, MissingPeriod{SKU: sku, Seller: key.seller, URL: key.url, Period: "current", Reason: "no stored observation for this offer in the current week"})
			totals[key.seller].missing++
		default:
			series.Reason = "no observation in the baseline week"
			series.Comparable = false
			out.MissingPeriods = append(out.MissingPeriods, MissingPeriod{SKU: sku, Seller: key.seller, URL: key.url, Period: "baseline", Reason: "no stored observation for this offer in the baseline week"})
			totals[key.seller].missing++
		}
		totals[key.seller].reads += readCounts[key]
		out.Series = append(out.Series, series)
	}
	sellers := make([]string, 0, len(totals))
	for seller := range totals {
		sellers = append(sellers, seller)
	}
	sort.Strings(sellers)
	for _, seller := range sellers {
		out.Sources = append(out.Sources, WeekDeltaSource{Seller: seller, Reads: totals[seller].reads, Series: totals[seller].series, Missing: totals[seller].missing})
	}
	out.Status = "ok"
	switch {
	case len(out.Series) == 0:
		out.Status = "unavailable"
		out.MissingPeriods = append(out.MissingPeriods,
			MissingPeriod{SKU: sku, Period: "baseline", Reason: "no stored observation of this SKU in the baseline week"},
			MissingPeriod{SKU: sku, Period: "current", Reason: "no stored observation of this SKU in the current week"})
		out.Warnings = append(out.Warnings, "No stored observations of this SKU in the requested weeks.")
	case out.ComparablePairs == 0:
		out.Status = "partial"
		out.Warnings = append(out.Warnings, "No comparable numeric price pair in both weeks.")
	default:
		if len(out.MissingPeriods) > 0 {
			out.Status = "partial"
			out.Warnings = append(out.Warnings, "Some offers were observed in only one of the two weeks.")
		}
	}
	out.Evidence = deltaEvidence(out, now)
	return out
}

func weekRead(o catalog.Offer) *WeekRead {
	x := obs(o)
	return &WeekRead{CapturedAt: o.CapturedAt, Value: x.Value, Unit: x.Unit, EvidenceID: x.ID, URL: o.URL}
}

// deltaEvidence projects the comparable pairs into the shared batch shape.
func deltaEvidence(d WeekDelta, now time.Time) Evidence {
	ev := empty(now)
	ev.SchemaVersion, ev.Source, ev.AsOf, ev.Status = "aifence.v1", "market", d.AsOf, d.Status
	ev.Observations, ev.Warnings = []Observation{}, append([]string{}, d.Warnings...)
	for _, series := range d.Series {
		if !series.Comparable {
			continue
		}
		delta := *series.Delta
		x := Observation{ID: "week-delta/" + series.EvidenceIDs[0] + "/" + series.EvidenceIDs[1], Kind: "metric", ProductKey: d.SKU,
			MetricKey: "displayed_price_week_delta", Value: delta, Unit: "RUB", Period: Period{From: d.CurrentWeek.From, To: d.CurrentWeek.To},
			SourceRef: SourceRef{SourceID: series.Seller, Locator: series.URL, URL: series.Current.URL, Revision: series.Current.CapturedAt},
			Status:    "observed", Currency: "RUB", TaxBasis: "unknown",
			Caveats: []string{"Latest stored read of each complete week for one SKU, seller, URL, currency, price basis and quantity tier. Absence is not a change."},
			Details: Details{Seller: series.Seller, Name: series.Name, Quantity: series.Quantity, MaxQuantity: series.MaxQuantity, PriceBasis: series.PriceBasis,
				CapturedAt: series.Current.CapturedAt, PreviousPrice: &series.Baseline.Value, CurrentPrice: &series.Current.Value,
				PreviousURL: series.Baseline.URL, PreviousCapturedAt: series.Baseline.CapturedAt, MatchStatus: "exact_sku",
				EvidenceIDs: series.EvidenceIDs, BaselineWeek: &d.BaselineWeek, CurrentWeek: &d.CurrentWeek}}
		ev.Observations = append(ev.Observations, x)
	}
	return ev
}

func (s *Service) weekRows(ctx context.Context, sku string, from, to time.Time) ([]catalog.Offer, bool, error) {
	if s.queries == nil {
		return nil, false, fmt.Errorf("catalog database required")
	}
	raw, err := s.queries.SKUWindowObservations(ctx, db.SKUWindowObservationsParams{Sku: sku, FromTime: from, ToTime: to})
	if err != nil {
		return nil, false, err
	}
	truncated := len(raw) > 4000
	if truncated {
		raw = raw[:4000]
	}
	out := make([]catalog.Offer, 0, len(raw))
	for _, b := range raw {
		var offer catalog.Offer
		if err := json.Unmarshal(b, &offer); err != nil {
			return nil, false, err
		}
		out = append(out, offer)
	}
	return out, truncated, nil
}
