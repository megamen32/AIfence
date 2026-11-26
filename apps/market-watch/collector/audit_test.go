package catalog

import (
	"strings"
	"testing"
	"time"
)

func TestRequestOnlyFamilyIsRepresented(t *testing.T) {
	s := sources(t)[0]
	p, e := InspectPage(s, s.Seeds[0], strings.NewReader(`<h1>Example</h1><div class="soft-tovar-style"><div id="placeforminprice"><span class="no-price">Запрос цены</span></div></div>`), time.Now())
	if e != nil || p.State != "no_public_price" || len(p.Offers) != 0 || len(p.Variants) != 1 {
		t.Fatal(p, e)
	}
}

func TestCSVLeadingControlsAndFormula(t *testing.T) {
	for _, s := range []string{"=1+2", "+cmd", "-3", "@cmd", " \t4", "\ufeff=4", "\u0007=4", "\r\nplain"} {
		if safe(s) != "'"+s {
			t.Fatalf("unsafe %q", s)
		}
	}
	if safe("KL-123") != "KL-123" || decimal(12345) != "123.45" {
		t.Fatal("ordinary values changed")
	}
}

func TestMissingManufacturerCodeStillHasPublicPrice(t *testing.T) {
	s := sources(t)[0]
	h := `<div class="offers"><a class="name" href="/v/license-a/">License A</a><span class="price" content="50 руб."></span></div><div class="offers"><a class="name" href="/v/license-b/">License B</a><span class="price" content="80 руб."></span></div>`
	p, e := InspectPage(s, s.Seeds[0], strings.NewReader(h), time.Now())
	if e != nil || p.State != "priced" || len(p.Offers) != 2 {
		t.Fatal(p, e)
	}
	got, e := Dedupe(p.Offers)
	if e != nil || len(got) != 2 || got[0].SKU != "" {
		t.Fatal(got, e)
	}
}
func TestVendorPublicListing(t *testing.T) {
	s := sources(t)[0]
	html := `<div class="wrapper-items-with-offers"><div class="wrapper-info-product"><div class="information-product"><a class="title" href="/vendor/family/">Family</a></div><div class="offers"><span class="articul"><span></span> CODE-1 </span><a class="name" href="/vendor/lic/">License</a><span class="price" content="99 руб.">99 руб.</span></div></div></div><div class="navigation"><a id="navigation_1_next_page" href="/vendor/?PAGEN_1=2">Next</a></div>`
	rows, next, err := VendorListing(s, "https://www.softmagazin.ru/vendor/", strings.NewReader(html), time.Now())
	if err != nil || len(rows) != 1 || len(rows[0].Offers) != 1 || rows[0].Offers[0].SKU != "CODE-1" || rows[0].Offers[0].PriceMinor != 9900 || rows[0].EvidenceURL == "" || !strings.HasSuffix(next, "/vendor/?PAGEN_1=2") {
		t.Fatal(rows, next, err)
	}
}

func TestMinimumDisplayedPriceKeepsItsBasis(t *testing.T) {
	s := sources(t)[0]
	h := `<div class="offers"><span class="articul" title="CODE">CODE</span><a class="name" href="/v/lic/">License</a><span class="price" content="от 50 руб.">от 50 руб.</span></div>`
	p, e := InspectPage(s, s.Seeds[0], strings.NewReader(h), time.Now())
	if e != nil || len(p.Offers) != 1 || p.Offers[0].PriceMinor != 5000 || p.Offers[0].PriceBasis != "minimum_displayed_price" {
		t.Fatal(p, e)
	}
}
