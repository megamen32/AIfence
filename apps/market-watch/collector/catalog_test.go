package catalog

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func sources(t *testing.T) []Source {
	t.Helper()
	f, e := os.Open("sources.json")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	s, e := ReadSources(f)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestAdapters(t *testing.T) {
	html := []string{
		`<div class="offers"><span class="articul" title="KL4141RCEFS">KL4141RCEFS</span><a class="name" href="/vendor/item/">Kaspersky Base</a><span class="price" content="7 488 руб.">7 488 руб.</span></div>`,
		`<div class="table-tr"><span class="art">KL4141RCEFS</span><div class="div-h3"><a href="/vendor/item/">Kaspersky Base</a></div><span data-old="7488"></span><div class="item-count"><input value="1"></div><span data-price="6739" data-currency="RUB"></span><div class="new-price"><span>6 739 руб</span></div></div>`,
		`<html><body><button class="js-sku-btn" data-code="KL4141RCEFS">Артикул</button><h1>Kaspersky Base</h1><span class="js-price-with-discount">6 739,20</span></body></html>`}
	want := []int64{748800, 673900, 673920}
	for i, s := range sources(t) {
		o, e := Extract(s, s.Seeds[0], strings.NewReader(html[i]), time.Unix(0, 0))
		if e != nil || len(o) != 1 || o[0].SKU != "KL4141RCEFS" || o[0].PriceMinor != want[i] {
			t.Fatalf("%s: %+v %v", s.ID, o, e)
		}
	}
}
func TestMoney(t *testing.T) {
	for s, w := range map[string]int64{"7 488 руб.": 748800, "6\u00a0739,20 ₽": 673920, "6739.2": 673920} {
		v, e := Money(s)
		if e != nil || v != w {
			t.Fatalf("%q: %d %v", s, v, e)
		}
	}
	for _, s := range []string{"По запросу", "0", "12.123", "1 23"} {
		if _, e := Money(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestSitemap(t *testing.T) {
	s := sources(t)[2]
	p, c, e := ParseSitemap(s, strings.NewReader(`<sitemapindex><sitemap><loc>https://store.softline.ru/sitemap/softwareProducts-1.xml</loc></sitemap><sitemap><loc>https://store.softline.ru/sitemap/hardwareProducts.xml</loc></sitemap><sitemap><loc>https://evil.test/sitemap/softwareProducts-1.xml</loc></sitemap></sitemapindex>`))
	if e != nil || len(p) != 0 || len(c) != 1 {
		t.Fatal(p, c, e)
	}
}
func TestNoOffersIsFailure(t *testing.T) {
	s := sources(t)[0]
	r := Collect(context.Background(), s, func(context.Context, Source, string) ([]byte, error) { return []byte("<html>empty</html>"), nil }, false, 1, time.Now())
	if r.Status != "failed" {
		t.Fatal(r)
	}
}
func TestCSV(t *testing.T) {
	var b bytes.Buffer
	e := CSV(&b, []Offer{{Source: "x", SKU: "ABC", Name: "=1+1", PriceMinor: 12345, Currency: "RUB"}})
	if e != nil || !strings.Contains(b.String(), "'=1+1;123.45") {
		t.Fatal(b.String(), e)
	}
}

func TestSoftMapTotalNotUnit(t *testing.T) {
	s := sources(t)[1]
	h := `<div class="table-tr"><span class="art">KL4546RAEFR</span><div class="div-h3"><a href="/vendor/item/">Kaspersky Renewal</a></div><div class="item-count"><input value="5"></div><span data-old="8720"></span><span data-price="1570" data-currency="RUB"></span><div class="new-price"><span>7 850 руб</span></div></div>`
	o, e := Extract(s, s.Seeds[0], strings.NewReader(h), time.Now())
	if e != nil || len(o) != 1 || o[0].PriceMinor != 785000 || o[0].Quantity == nil || *o[0].Quantity != 5 {
		t.Fatalf("%+v %v", o, e)
	}
}
