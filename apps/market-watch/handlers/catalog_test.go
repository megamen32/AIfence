package handlers

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"agent/market"
	"github.com/PuerkitoBio/goquery"
)

type fixture struct {
	input  market.ListInput
	crawls int
}

func (f *fixture) Browse(_ context.Context, in market.ListInput) (market.Evidence, bool, error) {
	f.input = in
	d := market.Details{Seller: "softline", Name: "Лицензия <и> поддержка", PriceBasis: "per_unit", CapturedAt: "2026-11-26T10:09:00Z"}
	o := market.Observation{ProductKey: "AA&1", Details: d, Value: 1234.51, SourceRef: market.SourceRef{URL: "https://store.softline.ru/vendor/license/"}}
	return market.Evidence{Observations: []market.Observation{o}}, true, nil
}
func (f *fixture) PriceHistory(context.Context, market.ChangesInput) (market.PriceHistory, error) {
	return market.PriceHistory{}, nil
}
func (f *fixture) Changes(context.Context, market.ChangesInput) (market.Evidence, error) {
	return market.Evidence{}, nil
}
func (f *fixture) CompareSKU(context.Context, market.CompareInput) (market.Comparison, error) {
	return market.Comparison{}, nil
}
func (f *fixture) Coverage(context.Context, struct{}) (market.CatalogStatus, error) {
	return market.CatalogStatus{Total: 12984, Sources: []market.SourceSummary{{Source: "softline", Offers: 12984}, {Source: "softmagazin"}, {Source: "softmap"}}}, nil
}
func (f *fixture) CrawlCoverage(context.Context, struct{}) (market.CrawlReport, error) {
	f.crawls++
	return market.CrawlReport{AsOf: "2026-11-26T10:09:00Z", Sources: []market.CrawlSource{{Source: "softline", Known: true, Total: 15, Priced: 8, Pending: 7}, {Source: "softmagazin"}}}, nil
}
func TestFullAndPartialCatalogRequests(t *testing.T) {
	f := new(fixture)
	h := Pages{Market: f}
	path := "/?q=AA%261&license=renewal&source=softline&page=2"
	for _, partial := range []bool{false, true} {
		r := httptest.NewRequest("GET", path, nil)
		if partial {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		if err := h.Home(w, r); err != nil {
			t.Fatal(err)
		}
		doc, err := goquery.NewDocumentFromReader(w.Result().Body)
		if err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || doc.Find("#results").Length() != 1 || f.input.ProductKey != "AA&1" || f.input.Offset != 25 || f.input.Source != "softline" || f.input.LicenseType != "renewal" {
			t.Fatalf("incorrect query or response: %v %+v", w.Code, f.input)
		}
		if doc.Find("form").Length() != map[bool]int{true: 0, false: 1}[partial] {
			t.Fatal("wrong response layout")
		}
		row := doc.Find("tbody tr").First()
		if !strings.Contains(row.Text(), "1\u202f234,51 ₽") || !strings.Contains(row.Text(), "Лицензия <и> поддержка") {
			t.Fatal(row.Text())
		}
		href, _ := row.Find(".row-links a").First().Attr("href")
		u, err := url.Parse(href)
		if err != nil || u.Query().Get("q") != "AA&1" || u.Query().Get("tab") != "compare" {
			t.Fatal(href)
		}
		next, _ := doc.Find(".pager a").Last().Attr("href")
		u, err = url.Parse(next)
		if err != nil || u.Query().Get("q") != "AA&1" || u.Query().Get("page") != "3" || u.Query().Get("source") != "softline" {
			t.Fatal(next)
		}
	}
	if f.crawls != 0 {
		t.Fatal("catalog requested crawl inventory unnecessarily")
	}
}
func TestSourceCountsAndValidation(t *testing.T) {
	h := Pages{Market: new(fixture)}
	for _, c := range []struct {
		path   string
		status int
		text   string
	}{
		{"/?tab=sources", 200, "12\u202f984"},
		{"/?tab=sources", 200, "Полный список URL лицензий"},
		{"/?tab=changes&from=2026-11-26&to=2026-11-25", 400, "Проверьте даты"},
		{"/?page=-1", 400, "номер страницы"},
		{"/?source=other", 400, "Выберите магазин"},
		{"/?tab=missing", 404, "404"},
	} {
		w := httptest.NewRecorder()
		if err := h.Home(w, httptest.NewRequest("GET", c.path, nil)); err != nil {
			t.Fatal(err)
		}
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.text) {
			t.Fatalf("path %s: status=%d, missing %q", c.path, w.Code, c.text)
		}
	}
}

func TestDeepLinkViewsSelectExistingScreens(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"prices", "/?view=prices&q=KES", "catalog"},
		{"offers", "/?view=offers&q=KL4141RCEFS&from=2026-11-01&to=2026-11-28", "compare"},
		{"invalid view", "/?view=unknown&q=KES", "catalog"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tabFromQuery(mustQuery(tc.raw)); got != tc.want {
				t.Fatalf("tabFromQuery(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func mustQuery(raw string) url.Values {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u.Query()
}
