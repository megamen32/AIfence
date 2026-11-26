package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type urlCounts struct {
	Total      int            `json:"total_urls"`
	States     map[string]int `json:"by_state"`
	Unresolved map[string]int `json:"unresolved_by_state,omitempty"`
}

type sourceSummary struct {
	InventoryAt                 string    `json:"inventory_at"`
	WorkerState                 string    `json:"worker_state"`
	Complete                    bool      `json:"complete"`
	PricedRows                  int       `json:"priced_offer_rows"`
	AdditionalPriceRowsRetained int       `json:"retained_rows_from_previous_observations,omitempty"`
	ExpectedFamilies            int       `json:"expected_family_urls"`
	ExpectedVariants            *int      `json:"expected_sitemap_variant_urls,omitempty"`
	Families                    urlCounts `json:"family_urls"`
	Variants                    urlCounts `json:"variant_urls"`
	CategoryEnumeration         *bool     `json:"category_discovery_complete,omitempty"`
	Blockers                    []string  `json:"blockers,omitempty"`
}
type coverageSummary struct {
	Schema   int                       `json:"schema_version"`
	AsOf     string                    `json:"as_of"`
	Complete bool                      `json:"complete"`
	Sources  map[string]*sourceSummary `json:"sources"`
}

// The tables count distinct (store, URL kind, URL) in the register. A product
// family and its license offers deliberately have no combined "product total".
func counts() urlCounts { return urlCounts{States: map[string]int{}, Unresolved: map[string]int{}} }
func (x *urlCounts) add(state string) {
	x.Total++
	x.States[state]++
	switch state {
	case "priced", "no_public_price", "unavailable", "out_of_scope", "not_product",
		"family_with_prices", "family_with_variants":
		// Known observations with an explicit interpretation.
	default:
		x.Unresolved[state]++
	}
}
func workerSummary(root, s string) (sourceSummary, error) {
	result := sourceSummary{Families: counts(), Variants: counts()}
	if s == "softline" {
		var m struct {
			InventoryAt string `json:"inventory_at"`
			Complete    bool   `json:"complete"`
			Families    int    `json:"families"`
			Variants    int    `json:"inventory_products"`
			Prices      int    `json:"priced_offers"`
			Retained    int    `json:"retained_from_previous_observations"`
		}
		if e := load(filepath.Join(root, s, "inventory.json"), &m); e != nil {
			return result, e
		}
		result.InventoryAt = m.InventoryAt
		result.ExpectedFamilies = m.Families
		result.ExpectedVariants = &m.Variants
		result.PricedRows = m.Prices
		result.AdditionalPriceRowsRetained = m.Retained
		result.WorkerState = "running"
		if m.Complete {
			result.WorkerState = "complete"
		}
	} else {
		var m struct {
			InventoryAt string `json:"inventory_at"`
			State       string `json:"state"`
			Families    int    `json:"candidate_pages"`
			Variants    int    `json:"expected_variant_urls"`
			Prices      int    `json:"priced_offers"`
			Categories  *bool  `json:"category_discovery_complete"`
		}
		if e := load(filepath.Join(root, s, "progress.json"), &m); e != nil {
			return result, e
		}
		result.InventoryAt = m.InventoryAt
		result.WorkerState = m.State
		result.ExpectedFamilies = m.Families
		result.PricedRows = m.Prices
		result.CategoryEnumeration = m.Categories
		if s == "softmap" {
			result.ExpectedVariants = &m.Variants
		}
	}
	return result, nil
}
func (r register) writeSummary(root string) error {
	summary := coverageSummary{Schema: 1, AsOf: time.Now().UTC().Format(time.RFC3339), Complete: true, Sources: map[string]*sourceSummary{}}
	for _, id := range []string{"softline", "softmap", "softmagazin"} {
		s, e := workerSummary(root, id)
		if e != nil {
			return e
		}
		summary.Sources[id] = &s
	}
	for _, entry := range r {
		s, ok := summary.Sources[entry.Source]
		if !ok {
			return fmt.Errorf("unknown source %q", entry.Source)
		}
		switch entry.Kind {
		case "семейство":
			s.Families.add(entry.State)
		case "вариант":
			s.Variants.add(entry.State)
		default:
			return fmt.Errorf("unknown URL kind %q", entry.Kind)
		}
	}
	for id, s := range summary.Sources {
		if s.InventoryAt == "" {
			s.Blockers = append(s.Blockers, "missing_inventory_timestamp")
		}
		if s.WorkerState != "complete" {
			s.Blockers = append(s.Blockers, "worker_"+s.WorkerState)
		}
		if id == "softmagazin" && (s.CategoryEnumeration == nil || !*s.CategoryEnumeration) {
			s.Blockers = append(s.Blockers, "software_categories_unverified")
		}
		if s.ExpectedFamilies == 0 || s.Families.Total != s.ExpectedFamilies {
			s.Blockers = append(s.Blockers, "family_inventory_mismatch")
		}
		if s.ExpectedVariants != nil && (*s.ExpectedVariants == 0 || s.Variants.Total < *s.ExpectedVariants) {
			s.Blockers = append(s.Blockers, "variant_inventory_mismatch")
		}
		if len(s.Families.Unresolved) > 0 {
			s.Blockers = append(s.Blockers, "unresolved_family_urls")
		}
		if len(s.Variants.Unresolved) > 0 {
			s.Blockers = append(s.Blockers, "unresolved_variant_urls")
		}
		if s.AdditionalPriceRowsRetained > 0 {
			s.Blockers = append(s.Blockers, "previous_observations_retained")
		}
		s.Complete = len(s.Blockers) == 0
		if !s.Complete {
			summary.Complete = false
		}
	}
	f, e := os.CreateTemp(root, ".coverage-summary-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if e = enc.Encode(summary); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), filepath.Join(root, "coverage-summary.json"))
}
