package main

import (
	catalog "agent/collector"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTornCheckpointAndRefresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pages.jsonl")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	p := catalog.PageResult{URL: "https://x/p", At: "2025-01-01T00:00:00Z", State: "priced", Offers: []catalog.Offer{{SKU: "SKU", PriceMinor: 100}}}
	if e = json.NewEncoder(f).Encode(p); e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteString(`{"url":`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Seek(0, 0); e != nil {
		t.Fatal(e)
	}
	pages, e := replay(f)
	if e != nil || len(pages) != 1 {
		t.Fatal(pages, e)
	}
	got, e := os.ReadFile(path)
	if e != nil || !strings.HasSuffix(string(got), "}\n") {
		t.Fatal(string(got), e)
	}
	st, e := snapshot(dir, inventory{Source: "example", URLs: []string{p.URL}, DiscoveredAt: "2026-01-01T00:00:00Z"}, pages)
	if e != nil || st.PendingPages != 1 || st.PricedOffers != 1 || st.State != "running" {
		t.Fatal(st, e)
	}
}
func TestMissingVariantKeepsSnapshotProvisional(t *testing.T) {
	dir := t.TempDir()
	inv := inventory{Source: "s", URLs: []string{"parent"}, VariantURLs: []string{"variant"}}
	st, e := snapshot(dir, inv, map[string]catalog.PageResult{"parent": {State: "unavailable"}})
	if e != nil || st.State != "running" || st.PendingVariantURLs != 1 {
		t.Fatal(st, e)
	}
}
func TestOnlyRefreshClosedInventory(t *testing.T) {
	dir := t.TempDir()
	s := catalog.Source{ID: "demo", Host: "example.org", Sitemap: "https://example.org/sitemap.xml", PageInclude: ".*"}
	old := inventory{Source: s.ID, DiscoveredAt: "2024-01-01T00:00:00Z", URLs: []string{"old"}}
	if e := saveJSON(filepath.Join(dir, "inventory.json"), old); e != nil {
		t.Fatal(e)
	}
	called := 0
	fetch := func(context.Context, catalog.Source, string) ([]byte, error) {
		called++
		return []byte("<urlset><url><loc>https://example.org/next</loc></url></urlset>"), nil
	}
	for _, state := range []string{"running", "complete"} {
		if e := saveJSON(filepath.Join(dir, "progress.json"), progress{State: state, InventoryAt: old.DiscoveredAt}); e != nil {
			t.Fatal(e)
		}
		got, e := readInventory(context.Background(), dir, s, fetch, time.Hour)
		if e != nil {
			t.Fatal(e)
		}
		if (state == "running") != (got.URLs[0] == "old") {
			t.Fatal(state, got)
		}
	}
	if called != 1 {
		t.Fatal(called)
	}
}

func TestCoverageDistinguishesDeniedAndRemovedVariants(t *testing.T) {
	p := catalog.PageResult{State: "priced"}
	inv := inventory{URLs: []string{"family"}, VariantURLs: []string{"denied", "gone"}}
	pages := map[string]catalog.PageResult{"family": p, "denied": {State: "access_denied"}, "gone": {State: "unavailable"}}
	st, err := snapshot(t.TempDir(), inv, pages)
	if err != nil || st.State != "needs_review" || st.ExcludedVariantURLs != 1 || st.UnavailableVariantURLs != 1 || st.PendingVariantURLs != 0 {
		t.Fatal(st, err)
	}
}
