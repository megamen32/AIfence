package market

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	catalog "agent/collector"
	"agent/internal/db"
)

type PricePoint struct {
	CapturedAt string  `json:"captured_at"`
	Value      float64 `json:"value"`
}

type PriceSeries struct {
	Offer  Observation  `json:"offer"`
	Points []PricePoint `json:"points"`
}

type PriceHistory struct {
	Series    []PriceSeries `json:"series"`
	Truncated bool          `json:"truncated"`
}

type seriesIdentity struct {
	sku, source, url, basis string
	quantity, maximum       int64
	terms                   comparisonTerms
}

func seriesOf(o Observation) seriesIdentity {
	return seriesIdentity{strings.ToUpper(o.ProductKey), o.Details.Seller, o.SourceRef.URL, o.Details.PriceBasis,
		quantityValue(o.Details.Quantity), quantityValue(o.Details.MaxQuantity), termsOf(o)}
}

// PriceHistory returns sampled prices, including unchanged observations. A
// price printed as "before" on a seller's page is not an earlier observation.
func (s *Service) PriceHistory(ctx context.Context, in ChangesInput) (PriceHistory, error) {
	out := PriceHistory{Series: []PriceSeries{}}
	key := strings.TrimSpace(in.ProductKey)
	if key == "" || len(key) > 300 {
		return out, fmt.Errorf("exact SKU required")
	}
	from, e1 := time.Parse("2006-01-02", in.From)
	to, e2 := time.Parse("2006-01-02", in.To)
	if e1 != nil || e2 != nil || to.Before(from) {
		return out, fmt.Errorf("from and to must be an inclusive UTC date range")
	}
	end := to.AddDate(0, 0, 1)
	var rows []catalog.Offer
	if s.queries != nil {
		raw, err := s.queries.ObservedSKUHistory(ctx, db.ObservedSKUHistoryParams{Sku: key, FromTime: from, ToTime: end})
		if err != nil {
			return out, err
		}
		if len(raw) > 4000 {
			raw = raw[:4000]
			out.Truncated = true
		}
		for _, b := range raw {
			var offer catalog.Offer
			if err := json.Unmarshal(b, &offer); err != nil {
				return out, err
			}
			rows = append(rows, offer)
		}
	} else {
		snap, err := s.Snapshot(ctx)
		if err != nil {
			return out, err
		}
		for _, run := range snap.Runs {
			if run.Status != "ok" {
				continue
			}
			for _, offer := range run.Offers {
				at, err := time.Parse(time.RFC3339Nano, offer.CapturedAt)
				if err == nil && strings.EqualFold(offer.SKU, key) && !at.Before(from) && at.Before(end) {
					rows = append(rows, offer)
				}
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, rows[i].CapturedAt)
		b, _ := time.Parse(time.RFC3339Nano, rows[j].CapturedAt)
		return a.Before(b)
	})
	seen := map[seriesIdentity]int{}
	for _, r := range rows {
		o := obs(r)
		k := seriesOf(o)
		i, ok := seen[k]
		if !ok {
			i = len(out.Series)
			seen[k] = i
			out.Series = append(out.Series, PriceSeries{})
		}
		line := &out.Series[i]
		line.Offer = o
		line.Points = append(line.Points, PricePoint{r.CapturedAt, o.Value})
	}
	return out, nil
}
