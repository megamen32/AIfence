package catalog

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestAdaptivePublicBoundary(t *testing.T) {
	for _, s := range []string{"http://shop.example.com/", "https://127.0.0.1/", "https://[::1]/", "https://name.local/", "https://user:pass@shop.example.com/", "https://shop.example.com:444/", "https://shop.example.com/?token=x"} {
		if PublicStoreURL(s) == nil {
			t.Fatal("accepted", s)
		}
	}
	if e := PublicStoreURL("https://shop.example.com/catalog/"); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.1.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(s)) {
			t.Fatal("non-public IP accepted", s)
		}
	}
}
func TestAdaptiveWrongIdentityAndPrice(t *testing.T) {
	u := "https://shop.example.com/item/"
	s := Source{ID: "shop", Host: "shop.example.com"}
	c := AdapterCase{ID: "p", Kind: "product", Match: ".product", Rows: ".product", Name: Field{Selector: "h1"}, SKU: Field{Selector: ".sku"}, Price: Field{Selector: ".price"}, Currency: Field{Selector: ".currency"}, Basis: "displayed_total"}
	spec := AdapterSpec{Cases: []AdapterCase{c}}
	if e := ValidateAdapter(spec); e != nil {
		t.Fatal(e)
	}
	raw := []byte(`<html><head><link rel="canonical" href="https://shop.example.com/other/"></head><body><div class="product"><h1>Item</h1><b class="sku">ABC</b><span class="price" itemprop="price">100</span><b class="currency">RUB</b></div></body></html>`)
	result, e := ParseAdapter(s, spec, u, raw, time.Now())
	if e != nil || len(result.Offers) > 0 {
		t.Fatal("wrong product price accepted", result, e)
	}
	raw = []byte(`<html><head><link rel="canonical" href="https://shop.example.com/item/"></head><body><div class="product"><h1>Item</h1><b class="sku">ABC</b><span class="price" itemprop="price">от 100</span><b class="currency">RUB</b></div></body></html>`)
	result, e = ParseAdapter(s, spec, u, raw, time.Now())
	if e != nil || len(result.Offers) > 0 {
		t.Fatal("from price accepted", result, e)
	}
}

func TestAdaptiveUniqueVisibleCurrentPrice(t *testing.T) {
	u := "https://shop.example.com/item/"
	s := Source{ID: "shop", Host: "shop.example.com"}
	field := func(q string) Field { return Field{Selector: q} }
	c := AdapterCase{ID: "visible", Kind: "product", Match: ".pd-price", Rows: "body", Name: field("h1"), SKU: field(".pd-sku code"), Price: field(".pd-price > b.pr"), Currency: field(".pd-price > b.pr"), PriceScope: ".pd-price", Status: field(".pd-price__ask"), Basis: "displayed_total"}
	spec := AdapterSpec{Cases: []AdapterCase{c}}
	if e := ValidateAdapter(spec); e != nil {
		t.Fatal(e)
	}
	body := `<html><head><link rel="canonical" href="/item/"></head><body><h1>License</h1><div class="pd-sku">Артикул <code>ABC</code></div><div class="pd-price"><s>341 600 ₽</s><b class="pr">170 800 ₽</b></div></body></html>`
	r, e := ParseAdapter(s, spec, u, []byte(body), time.Now())
	if e != nil || len(r.Offers) != 1 || r.Offers[0].PriceMinor != 17080000 || r.Offers[0].SKU != "ABC" {
		t.Fatal("actual published amount rejected", r, e)
	}
	for _, bad := range []string{
		strings.Replace(body, "<s>341 600 ₽</s>", "<span>341 600 ₽</span>", 1),
		strings.Replace(body, "<b class=\"pr\">", "<b class=\"pr\" hidden>", 1),
		strings.Replace(body, "170 800 ₽", "от 170 800 ₽", 1),
		strings.Replace(body, "170 800 ₽", "170 800", 1),
		strings.Replace(body, "href=\"/item/\"", "href=\"/other/\"", 1),
		strings.Replace(body, "<h1>License</h1>", "<h1>License</h1><h1>Other</h1>", 1),
	} {
		r, e = ParseAdapter(s, spec, u, []byte(bad), time.Now())
		if e != nil || len(r.Offers) > 0 {
			t.Fatal("ambiguous/incorrect amount accepted", bad, r, e)
		}
	}
}
