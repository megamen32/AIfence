package market

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	catalog "agent/collector"
)

func browseService(t *testing.T, rows ...catalog.Offer) *Service {
	t.Helper()
	runs := []catalog.Run{{Source: "softline", Status: "ok"}, {Source: "softmap", Status: "ok"}, {Source: "softmagazin", Status: "ok"}}
	for _, row := range rows {
		for i := range runs {
			if row.Source == runs[i].Source {
				runs[i].Offers = append(runs[i].Offers, row)
			}
		}
	}
	b, err := json.Marshal(Snapshot{CapturedAt: "2026-11-26T08:00:00Z", Runs: runs})
	if err != nil {
		t.Fatal(err)
	}
	return New(memoryStore{current: b})
}

func TestBrowseFiltersBeforePagination(t *testing.T) {
	var rows []catalog.Offer
	for i := 105; i >= 0; i-- {
		rows = append(rows, catalog.Offer{Source: "softline", SKU: "AA", Name: "Продление подписки", URL: fmt.Sprintf("https://store.softline.ru/v/%03d/", i)})
	}
	rows = append(rows, catalog.Offer{Source: "softmap", SKU: "A1", Name: "AA", URL: "https://softmap.ru/v/aaa/"})
	s := browseService(t, rows...)
	in := ListInput{ProductKey: "aa", ExactSKU: true, Source: "softline", LicenseType: "renewal", Limit: 5, Offset: 100}
	ev, more, err := s.Browse(t.Context(), in)
	if err != nil || !more || len(ev.Observations) != 5 || ev.Observations[0].SourceRef.URL != "https://store.softline.ru/v/100/" {
		t.Fatalf("wrong page: %+v %v %v", ev, more, err)
	}
	in.Offset = 105
	ev, more, err = s.Browse(t.Context(), in)
	if err != nil || more || len(ev.Observations) != 1 {
		t.Fatalf("wrong last page: %+v %v %v", ev, more, err)
	}
	ev, _, err = s.Browse(t.Context(), ListInput{LicenseType: "unknown"})
	if err != nil || len(ev.Observations) != 1 || ev.Observations[0].ProductKey != "A1" {
		t.Fatalf("unknown filter: %+v %v", ev, err)
	}
	ev, _, err = s.Browse(t.Context(), ListInput{ProductKey: "AA", ExactSKU: true, Source: "softmap"})
	if err != nil || len(ev.Observations) != 0 {
		t.Fatalf("title matched exact SKU: %+v", ev)
	}
	for _, in := range []ListInput{{Offset: -1}, {ExactSKU: true}, {LicenseType: "arbitrary"}} {
		if _, _, err = s.Browse(t.Context(), in); err == nil {
			t.Fatal("accepted invalid filter", in)
		}
	}
}

func TestCompareSKURequiresMatchingConditions(t *testing.T) {
	minQty, maxQty := int64(5), int64(9)
	a := catalog.Offer{SKU: "K / 1&X", Source: "softline", Name: "Продление", PriceBasis: "per_unit", Currency: "RUB", Quantity: &minQty, MaxQuantity: &maxQty, PriceMinor: 12345, CapturedAt: "2026-11-26T08:00:00Z"}
	b := a
	b.Source = "softmap"
	b.PriceMinor = 11200
	for _, tc := range []struct {
		name       string
		change     func(*catalog.Offer)
		comparable bool
	}{
		{"same", func(*catalog.Offer) {}, true},
		{"different tier", func(o *catalog.Offer) { n := int64(10); o.MaxQuantity = &n }, false},
		{"same title different SKU", func(o *catalog.Offer) { o.SKU = "other"; o.Name = a.SKU }, false},
		{"different license wording", func(o *catalog.Offer) { o.Name = "новая лицензия" }, false},
		{"unknown quantity", func(o *catalog.Offer) { o.Quantity = nil }, false},
		{"same seller", func(o *catalog.Offer) { o.Source = "softline" }, false},
		{"advertised floor", func(o *catalog.Offer) { o.PriceBasis = "minimum_displayed_price" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := b
			tc.change(&row)
			out, err := browseService(t, a, row).CompareSKU(t.Context(), CompareInput{SKU: a.SKU})
			if err != nil || len(out.Groups) == 0 {
				t.Fatalf("%+v %v", out, err)
			}
			u, err := url.Parse(out.ViewPath)
			if err != nil || u.Query().Get("q") != a.SKU || u.Query().Get("tab") != "compare" {
				t.Fatal("bad deep link", out.ViewPath)
			}
			for _, g := range out.Groups {
				if g.Comparable != tc.comparable || (g.LowestPrice != nil) != tc.comparable {
					t.Fatalf("incorrect comparison: %+v", g)
				}
				if tc.comparable && (*g.LowestPrice != 112 || g.Rows[0].Details.Seller != "softmap") {
					t.Fatal(g)
				}
			}
		})
	}
}

func TestCompareSKUTruncatedHasNoMinimum(t *testing.T) {
	quantity := int64(1)
	rows := make([]catalog.Offer, 101)
	for i := range rows {
		rows[i] = catalog.Offer{Source: []string{"softline", "softmap"}[i%2], SKU: "test", Name: "test license", Currency: "RUB", PriceBasis: "displayed_total", Quantity: &quantity, PriceMinor: 100, URL: fmt.Sprintf("https://example.test/%d", i)}
	}
	out, err := browseService(t, rows...).CompareSKU(t.Context(), CompareInput{SKU: "test"})
	if err != nil || !out.Truncated || len(out.Groups) != 1 || out.Groups[0].Comparable || out.Groups[0].LowestPrice != nil {
		t.Fatalf("truncated: %+v %v", out, err)
	}
}

func TestHistoryDoesNotJoinDifferentURLsOrPriceTiers(t *testing.T) {
	q, maxQty := int64(5), int64(9)
	a := catalog.Offer{Source: "softline", SKU: "", URL: "https://example.test/a", PriceBasis: "per_unit", Currency: "RUB", Quantity: &q, MaxQuantity: &maxQty, PriceMinor: 200, CapturedAt: "2026-11-25T00:00:00Z"}
	for _, change := range []func(*catalog.Offer){
		func(o *catalog.Offer) { o.URL = "https://example.test/b" },
		func(o *catalog.Offer) { v := int64(10); o.MaxQuantity = &v },
	} {
		b := a
		b.CapturedAt = "2026-11-26T00:00:00Z"
		b.PriceMinor = 300
		change(&b)
		snaps := []Snapshot{}
		for _, row := range []catalog.Offer{a, b} {
			snaps = append(snaps, Snapshot{Runs: []catalog.Run{{Source: row.Source, Status: "ok", Offers: []catalog.Offer{row}}}})
		}
		if got := Compare(snaps, ChangesInput{}, time.Now()); len(got.Observations) != 0 {
			t.Fatal("compared unrelated offers", got)
		}
	}
}

func TestCompareOnePackAcrossBases(t *testing.T) {
	one, five, maxQty := int64(1), int64(5), int64(9999)
	unit := catalog.Offer{Source: "softline", SKU: "A", Name: "Новая лицензия на 5 ПК на 1 год", Quantity: &one, MaxQuantity: &maxQty, PriceBasis: "per_unit", Currency: "RUB", PriceMinor: 673920}
	total := catalog.Offer{Source: "softmap", SKU: "A", Name: "5-Mobile devices, 5-Desktop, 5-User 1 year Base License Pack", Quantity: &one, PriceBasis: "displayed_total", Currency: "RUB", PriceMinor: 673900}
	unknown := total
	unknown.Source = "softmagazin"
	unknown.PriceBasis = "displayed_offer_price"
	unknown.Quantity = nil
	for _, tc := range []struct {
		name    string
		change  func(*catalog.Offer)
		allowed bool
	}{
		{"same pack", func(*catalog.Offer) {}, true},
		{"total for five", func(r *catalog.Offer) { r.Quantity = &five }, false},
		{"unknown total quantity", func(r *catalog.Offer) { r.Quantity = nil }, false},
		{"conflicting total bound", func(r *catalog.Offer) { r.MaxQuantity = &five }, false},
		{"different term", func(r *catalog.Offer) { r.Name = "5 seats 2 years Base License" }, false},
		{"different pack size", func(r *catalog.Offer) { r.Name = "3 seats 1 year Base License" }, false},
		{"different type", func(r *catalog.Offer) { r.Name = "5 seats 1 year Renewal" }, false},
		{"unknown terms", func(r *catalog.Offer) { r.Name = "Base License" }, false},
		{"mixed seat range", func(r *catalog.Offer) { r.Name = "1-5 seats 1 year Base License" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := total
			tc.change(&b)
			out, err := browseService(t, unit, b, unknown).CompareSKU(t.Context(), CompareInput{SKU: "A"})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, g := range out.Groups {
				if g.Comparable {
					if len(g.Rows) != 2 || g.Label != "Один лицензионный комплект" || g.LowestPrice == nil || *g.LowestPrice != 6739 || g.Rows[0].Details.PriceBasis != "displayed_total" || g.Rows[1].Details.PriceBasis != "per_unit" {
						t.Fatalf("bad normalized group: %+v", g)
					}
					found = true
				}
			}
			if found != tc.allowed {
				t.Fatalf("one-pack comparison expected %v: %+v", tc.allowed, out)
			}
		})
	}
}
