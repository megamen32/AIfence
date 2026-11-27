package market

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FamilyDeltaInput accepts either an explicit, reviewed SKU set or a search
// phrase found independently in indexed public offer titles.
type FamilyDeltaInput struct {
	Family       string   `json:"family,omitempty"`
	SKUs         []string `json:"skus,omitempty"`
	BaselineFrom string   `json:"baseline_from"`
	BaselineTo   string   `json:"baseline_to"`
	CurrentFrom  string   `json:"current_from"`
	CurrentTo    string   `json:"current_to"`
}

type FamilyCoverage struct {
	SelectedSKUs    int `json:"selected_skus"`
	SKUsWithPairs   int `json:"skus_with_pairs"`
	ObservedSeries  int `json:"observed_series"`
	ComparablePairs int `json:"comparable_pairs"`
	MissingSeries   int `json:"missing_series"`
	MissingSKUWeeks int `json:"missing_sku_weeks"`
}

// FamilyDelta aggregates counts and evidence IDs only. Absolute prices are
// retained within each SKU series and are never averaged across SKUs.
type FamilyDelta struct {
	Family             string         `json:"family,omitempty"`
	Discovery          string         `json:"discovery"`
	DiscoveryTruncated bool           `json:"discovery_truncated"`
	SKUs               []string       `json:"skus"`
	BaselineWeek       Period         `json:"baseline_week"`
	CurrentWeek        Period         `json:"current_week"`
	Coverage           FamilyCoverage `json:"coverage"`
	Results            []WeekDelta    `json:"results"`
	EvidenceIDs        []string       `json:"evidence_ids"`
	Evidence           Evidence       `json:"evidence"`
	Status             string         `json:"status"`
	Warnings           []string       `json:"warnings"`
}

func normalizeFamilySKUs(values []string) ([]string, error) {
	seen := map[string]bool{}
	var skus []string
	for _, value := range values {
		sku := strings.ToUpper(strings.TrimSpace(value))
		if sku == "" || len(sku) > 300 {
			return nil, fmt.Errorf("each SKU must contain 1 to 300 characters")
		}
		if !seen[sku] {
			skus = append(skus, sku)
			seen[sku] = true
		}
	}
	if len(skus) == 0 || len(skus) > 20 {
		return nil, fmt.Errorf("select 1 to 20 distinct SKUs")
	}
	sort.Strings(skus)
	return skus, nil
}

func (s *Service) FamilyWeekDelta(ctx context.Context, in FamilyDeltaInput) (FamilyDelta, error) {
	out := FamilyDelta{Results: []WeekDelta{}, EvidenceIDs: []string{}, Warnings: []string{}}
	if _, _, err := parseWeek("baseline", in.BaselineFrom, in.BaselineTo); err != nil {
		return out, err
	}
	if _, _, err := parseWeek("current", in.CurrentFrom, in.CurrentTo); err != nil {
		return out, err
	}
	if strings.TrimSpace(in.Family) != "" && len(in.SKUs) > 0 {
		return out, fmt.Errorf("choose either family discovery or an explicit SKU set")
	}
	if s.queries == nil {
		return out, fmt.Errorf("catalog database required")
	}
	var candidates []string
	if len(in.SKUs) > 0 {
		candidates = in.SKUs
		out.Discovery = "explicit_skus"
	} else {
		family := strings.TrimSpace(in.Family)
		if len(family) < 4 || len(family) > 200 {
			return out, fmt.Errorf("family phrase must contain 4 to 200 characters")
		}
		found, err := s.queries.DiscoverFamilySKUs(ctx, family)
		if err != nil {
			return out, err
		}
		out.DiscoveryTruncated = len(found) > 20
		candidates = found[:min(len(found), 20)]
		out.Family = family
		out.Discovery = "indexed_offer_titles"
		out.Warnings = append(out.Warnings, "Family discovery matched indexed offer titles; review the returned SKU list before treating it as a complete manufacturer family.")
		if out.DiscoveryTruncated {
			out.Warnings = append(out.Warnings, "More than 20 SKUs match; coverage describes only the first 20 candidates. Narrow the phrase or pass an explicit SKU set.")
		}
	}
	if len(candidates) == 0 {
		out.Status = "unavailable"
		out.Warnings = append(out.Warnings, "No indexed SKU matched this family phrase.")
		out.Evidence = empty(s.now())
		out.Evidence.Status = out.Status
		out.Evidence.Warnings = append(out.Evidence.Warnings, out.Warnings...)
		return out, nil
	}
	skus, err := normalizeFamilySKUs(candidates)
	if err != nil {
		return out, err
	}
	out.SKUs = skus
	out.Coverage.SelectedSKUs = len(skus)
	out.BaselineWeek = Period{From: in.BaselineFrom, To: in.BaselineTo}
	out.CurrentWeek = Period{From: in.CurrentFrom, To: in.CurrentTo}
	for _, sku := range skus {
		one, err := s.WeekDelta(ctx, WeekDeltaInput{ProductKey: sku, BaselineFrom: in.BaselineFrom, BaselineTo: in.BaselineTo, CurrentFrom: in.CurrentFrom, CurrentTo: in.CurrentTo})
		if err != nil {
			return FamilyDelta{}, err
		}
		out.Results = append(out.Results, one)
	}
	return aggregateFamilyDelta(out, s.now()), nil
}

func aggregateFamilyDelta(out FamilyDelta, now time.Time) FamilyDelta {
	out.Evidence = empty(now)
	out.Status = "ok"
	latest := time.Time{}
	for _, one := range out.Results {
		out.Coverage.ObservedSeries += len(one.Series)
		out.Coverage.ComparablePairs += one.ComparablePairs
		for _, missing := range one.MissingPeriods {
			if missing.Seller == "" {
				out.Coverage.MissingSKUWeeks++
			} else {
				out.Coverage.MissingSeries++
			}
		}
		if one.ComparablePairs > 0 {
			out.Coverage.SKUsWithPairs++
		}
		if one.Status != "ok" {
			out.Status = "partial"
		}
		if at, err := time.Parse(time.RFC3339Nano, one.AsOf); err == nil && at.After(latest) {
			latest = at
		}
		out.Evidence.Observations = append(out.Evidence.Observations, one.Evidence.Observations...)
		for _, row := range one.Evidence.Observations {
			out.EvidenceIDs = append(out.EvidenceIDs, row.ID)
		}
	}
	if out.Coverage.ComparablePairs == 0 {
		out.Status = "unavailable"
		out.Warnings = append(out.Warnings, "No comparable numeric price pairs for the selected SKUs and weeks.")
	} else if out.DiscoveryTruncated {
		out.Status = "partial"
	} else if out.Coverage.SKUsWithPairs < out.Coverage.SelectedSKUs || out.Coverage.MissingSeries > 0 {
		out.Status = "partial"
	}
	if !latest.IsZero() {
		out.Evidence.AsOf = latest.UTC().Format(time.RFC3339Nano)
	}
	out.Evidence.Status = out.Status
	out.Evidence.Warnings = append(out.Evidence.Warnings, out.Warnings...)
	return out
}
