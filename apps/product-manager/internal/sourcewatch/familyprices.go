package sourcewatch

// This file consumes market-watch family_price_week_delta exactly as
// published. Market Watch owns the series identity and comparability rules;
// sourcewatch only keeps its verdict, evidence IDs and coverage.

type familyCoverage struct {
	SelectedSKUs    int `json:"selected_skus"`
	SKUsWithPairs   int `json:"skus_with_pairs"`
	ObservedSeries  int `json:"observed_series"`
	ComparablePairs int `json:"comparable_pairs"`
	MissingSeries   int `json:"missing_series"`
	MissingSKUWeeks int `json:"missing_sku_weeks"`
}

// familyDelta aggregates counts and evidence IDs only. Absolute prices stay
// inside each SKU series and are never averaged across SKUs or licences.
type familyDelta struct {
	Family             string         `json:"family,omitempty"`
	Discovery          string         `json:"discovery"`
	DiscoveryTruncated bool           `json:"discovery_truncated"`
	SKUs               []string       `json:"skus"`
	BaselineWeek       period         `json:"baseline_week"`
	CurrentWeek        period         `json:"current_week"`
	Coverage           familyCoverage `json:"coverage"`
	Results            []weekDelta    `json:"results"`
	EvidenceIDs        []string       `json:"evidence_ids"`
	Status             string         `json:"status"`
	Warnings           []string       `json:"warnings"`
}

// familyPriceFamilies keeps every series Market Watch marked comparable, per
// SKU and per seller. Each one carries its own baseline and current evidence
// ID, the two exact weeks, and its own ruble change. Nothing is averaged.
func familyPriceFamilies(d familyDelta) ([]PriceFamily, []missingPeriod) {
	var out []PriceFamily
	var missing []missingPeriod
	for _, result := range d.Results {
		missing = append(missing, result.MissingPeriods...)
		for _, s := range result.Series {
			if !s.Comparable || s.Baseline == nil || s.Current == nil || s.DeltaPct == nil {
				continue
			}
			if result.SKU == "" || s.Currency != "RUB" || s.Baseline.Unit != "RUB" || s.Current.Unit != "RUB" {
				continue
			}
			if !bounded(s.Baseline.EvidenceID, 512) || !bounded(s.Current.EvidenceID, 512) || s.Baseline.EvidenceID == s.Current.EvidenceID {
				continue
			}
			if result.BaselineWeek.From == "" || result.CurrentWeek.From == "" || !bounded(s.URL, 2048) {
				continue
			}
			pct := *s.DeltaPct
			out = append(out, PriceFamily{
				SKU: result.SKU, Seller: s.Seller, PriceBasis: s.PriceBasis, Name: s.Name,
				BaselineID: s.Baseline.EvidenceID, CurrentID: s.Current.EvidenceID,
				BaselineAt: s.Baseline.CapturedAt, CurrentAt: s.Current.CapturedAt,
				BaselineRUB: s.Baseline.Value, CurrentRUB: s.Current.Value, ChangePct: &pct,
				Locator: s.URL, Quantity: s.Quantity, MaxQuantity: s.MaxQuantity,
				BaselineWeek: result.BaselineWeek, CurrentWeek: result.CurrentWeek,
			})
		}
	}
	return out, missing
}
