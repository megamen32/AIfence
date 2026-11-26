package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"agent/market"
	"agent/views"
	"github.com/PuerkitoBio/goquery"
)

type comparisonFixture struct {
	fixture
	current market.Comparison
	history market.PriceHistory
}

func (f *comparisonFixture) CompareSKU(context.Context, market.CompareInput) (market.Comparison, error) {
	return f.current, nil
}
func (f *comparisonFixture) PriceHistory(context.Context, market.ChangesInput) (market.PriceHistory, error) {
	return f.history, nil
}

func TestComparisonPanelShowsDeltasAndOnlyMeasuredHistory(t *testing.T) {
	one, ten := int64(1), int64(10)
	offer := func(seller string, price float64, basis string, min, max *int64) market.Observation {
		return market.Observation{ProductKey: "TEST", Value: price, Currency: "RUB", TaxBasis: "unknown", SourceRef: market.SourceRef{URL: "https://" + seller + ".example/test"}, Details: market.Details{Seller: seller, Name: "Demo base license 1 year 5 users", PriceBasis: basis, Quantity: min, MaxQuantity: max, CapturedAt: "2026-11-26T10:00:00Z"}}
	}
	a := offer("softmap", 6739, "displayed_total", &one, &one)
	b := offer("softline", 6739.20, "per_unit", &one, &ten)
	u := offer("softmagazin", 7488, "displayed_offer_price", nil, nil)
	data := &comparisonFixture{current: market.Comparison{Groups: []market.ComparisonGroup{
		{Rows: []market.Observation{u}, Reason: "Магазин не указал количество."},
		{Rows: []market.Observation{a, b}, Label: "Один лицензионный комплект", Comparable: true, LowestPrice: &a.Value},
	}}}
	for _, o := range []market.Observation{a, b, u} {
		data.history.Series = append(data.history.Series, market.PriceSeries{Offer: o, Points: []market.PricePoint{{CapturedAt: o.Details.CapturedAt, Value: o.Value}}})
	}
	h := Pages{Market: data}
	check := func(path string) *goquery.Document {
		t.Helper()
		w := httptest.NewRecorder()
		err := h.Home(w, httptest.NewRequest("GET", path, nil))
		if err != nil || w.Code != 200 {
			t.Fatalf("home %s %v: %s", path, err, w.Body.String())
		}
		doc, err := goquery.NewDocumentFromReader(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	doc := check("/?tab=compare&q=TEST&from=2026-11-20&to=2026-11-26")
	rows := doc.Find(".price-matrix .cmp-row")
	if rows.Length() != 3 || !strings.Contains(rows.Eq(0).Text(), "Минимум") || !strings.Contains(rows.Eq(1).Text(), "+0,20 ₽") || !strings.Contains(rows.Eq(2).Text(), "Количество не указано") {
		t.Fatal(rows.Text())
	}
	primary := doc.Find(".cmp-chart market-price-history")
	separate := doc.Find(".cmp-secondary market-price-history")
	if primary.Length() != 1 || separate.Length() != 1 {
		t.Fatalf("expected separate unmatched history: %d %d", primary.Length(), separate.Length())
	}
	raw, _ := primary.Attr("data-series")
	var lines []chartSeries
	if err := json.Unmarshal([]byte(raw), &lines); err != nil || len(lines) != 2 || len(lines[0].Points) != 1 || lines[0].Points[0].At != "2026-11-26T10:00:00Z" {
		t.Fatalf("%s %v", raw, err)
	}
	r := check("/?tab=compare&q=TEST&qty=2")
	rr := r.Find(".price-matrix .cmp-row")
	if rr.Length() != 1 || !strings.Contains(rr.Text(), "13\u202f478,40 ₽") || strings.Contains(rr.Text(), "Минимум") {
		t.Fatal(rr.Text())
	}
}
func TestPublishedTierIntervals(t *testing.T) {
	one, two, three, ten := int64(1), int64(2), int64(3), int64(10)
	offer := func(min, max *int64, price float64) market.Observation {
		return market.Observation{Value: price, Details: market.Details{Seller: "softline", Name: "A", PriceBasis: "per_unit", Quantity: min, MaxQuantity: max}}
	}
	base := market.Comparison{Groups: []market.ComparisonGroup{{Rows: []market.Observation{offer(&one, &two, 20), offer(&three, &ten, 18)}}}}
	t1 := quantityTiers(base, &views.Page{Query: "test", From: "2026-11-20", To: "2026-11-26"}, 3)
	if len(t1) != 1 || !t1[0].Steps[1].Active || !strings.Contains(t1[0].Steps[1].URL, "qty=3") || !strings.Contains(t1[0].Path, "V") {
		t.Fatal(t1)
	}
	base.Groups[0].Rows[1].Details.Quantity = &two
	if len(quantityTiers(base, &views.Page{}, 3)) != 0 {
		t.Fatal("overlapping independent tiers joined")
	}
}
