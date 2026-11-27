package catalog

import (
	"net/netip"
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
