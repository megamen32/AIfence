package main

import (
	catalog "agent/collector"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSoftMagazinMixedVariantInventory(t *testing.T) {
	s := catalog.Source{ID: "softmagazin", Host: "www.softmagazin.ru", Sitemap: "https://www.softmagazin.ru/sitemap.xml", PageInclude: ".*"}
	root := "https://www.softmagazin.ru/"
	dir := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339)
	inv := inventory{Source: s.ID, DiscoveredAt: now, URLs: []string{root + "vendor/family/"}}
	if err := saveJSON(filepath.Join(dir, "inventory.json"), inv); err != nil {
		t.Fatal(err)
	}
	fetched := []string{}
	fetch := func(_ context.Context, _ catalog.Source, u string) ([]byte, error) {
		fetched = append(fetched, u)
		if u == s.Sitemap {
			return []byte("<sitemapindex><sitemap><loc>" + root + "sitemap-3.xml</loc></sitemap><sitemap><loc>" + root + "sitemap-4-0.xml</loc></sitemap><sitemap><loc>" + root + "sitemap-4-1.xml</loc></sitemap></sitemapindex>"), nil
		}
		if u == root+"sitemap-4-0.xml" {
			return []byte("<urlset><url><loc>" + root + "vendor/software/</loc></url><url><loc>" + root + "vendor/hardware/</loc></url></urlset>"), nil
		}
		if u == root+"sitemap-4-1.xml" {
			return []byte("<urlset><url><loc>" + root + "vendor/software/</loc></url><url><loc>" + root + "vendor/unseen/</loc></url></urlset>"), nil
		}
		t.Fatalf("unexpected request: %s", u)
		return nil, nil
	}
	upgraded, err := readInventory(context.Background(), dir, s, fetch, time.Hour)
	if err != nil || len(upgraded.VariantURLs) != 3 || len(fetched) != 3 || upgraded.DiscoveredAt != now || upgraded.VariantSitemapAt == "" {
		t.Fatal(upgraded, fetched, err)
	}
	pages := map[string]catalog.PageResult{
		inv.URLs[0]:               {At: now, Category: "software", State: "priced", Variants: []catalog.Variant{{URL: root + "vendor/software/", Status: "priced"}}},
		root + "vendor/hardware/": {At: now, Category: "hardware", State: "out_of_scope"},
	}
	rows := reconcileSoftMagazin(upgraded, pages)
	states := map[string]variantReceipt{}
	for _, r := range rows {
		states[r.URL] = r
	}
	if states[root+"vendor/software/"].Scope != "software" || states[root+"vendor/software/"].State != "priced" || states[root+"vendor/hardware/"].State != "out_of_scope" || states[root+"vendor/unseen/"].State != "pending" {
		t.Fatal(rows)
	}
	st, err := snapshot(dir, upgraded, pages)
	if err != nil || st.ExpectedVariantURLs != 3 || st.SoftwareVariantURLs != 1 || st.HardwareVariantURLs != 1 || st.UnknownScopeVariantURLs != 1 || st.PendingVariantURLs != 1 {
		t.Fatal(st, err)
	}
}
