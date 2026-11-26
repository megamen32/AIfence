package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalog "agent/collector"
)

func TestSoftwareCategoryResumeAndAdditionalProduct(t *testing.T) {
	sources, e := catalog.Sources()
	if e != nil {
		t.Fatal(e)
	}
	s := sources[0]
	const family = "https://www.softmagazin.ru/test-vendor/family/"
	const section = "https://www.softmagazin.ru/soft/software-type/"
	dir := t.TempDir()
	inv := inventory{Source: s.ID, DiscoveredAt: "2025-01-01T00:00:00Z", URLs: []string{"https://www.softmagazin.ru/other/family/"}}
	pages := map[string]catalog.PageResult{}
	fetchCount := 0
	fetch := func(_ context.Context, _ catalog.Source, url string) ([]byte, error) {
		if strings.HasSuffix(url, "/soft/") {
			return []byte(`<a class="catalog-name" href="/soft/software-type/">Software</a>`), nil
		}
		fetchCount++
		if url != section {
			t.Fatalf("unexpected path: %s", url)
		}
		return []byte(`<div class="wrapper-items-with-offers"><div class="wrapper-info-product"><div class="information-product"><a class="title" href="/test-vendor/family/">Family</a></div><div class="offers"><span class="articul">SKU</span><a class="name" href="/test-vendor/license/">License</a><span class="price" content="789 руб."></span></div></div></div>`), nil
	}
	save := func(p catalog.PageResult) error { pages[p.URL] = p; return nil }
	for range 2 {
		if e = collectListings(context.Background(), s, dir, &inv, pages, fetch, save); e != nil {
			t.Fatal(e)
		}
	}
	st, e := snapshot(dir, inv, pages)
	if e != nil || len(inv.URLs) != 2 || pages[family].Category != "software" || fetchCount != 1 || st.PricedOffers != 1 || st.ProcessedPages != 0 || st.ListedPages != 1 || st.PendingPages != 2 {
		t.Fatal(st, fetchCount, pages, e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "offers.jsonl"))
	if e != nil || !strings.Contains(string(b), `"price_minor":78900`) {
		t.Fatal(string(b), e)
	}
}
