package softlinefamily

import (
	"testing"
	"time"
)

func TestFamilyLicenses(t *testing.T) {
	b := []byte(`<html><h1>Suite</h1><script id="js-front-config">window.temporaryFrontConfig = {"familyId":11,"models":[{"name":"Basic","licenses":{"2":{"sku":"","name":"Configurable","link":"/v/suite-2/","price":{"with_discount":"0"}},"3":{"sku":"abc","name":"One year","link":"/v/suite-3/","min_quantity":5,"max_quantity":9,"price":{"with_discount":"1234.50","without_discount":"1450.00"}}}}]}</script><script type="application/ld+json">{"@type":"Product","name":"Suite five to nine devices","offers":{"url":"https://store.softline.ru/v/suite-3/"}}</script></html>`)
	a, e := Extract("https://store.softline.ru/v/suite/", b, time.Unix(0, 0))
	if e != nil || len(a) != 2 {
		t.Fatalf("products: %#v %v", a, e)
	}
	if a[0].SKU != "" || a[0].Offer != nil || a[0].State != "no_public_price" {
		t.Fatalf("unpriced license: %#v", a[0])
	}
	o := a[1].Offer
	if o == nil || o.Name != "Suite five to nine devices" || o.PriceMinor != 123450 || o.SKU != "ABC" || *o.Quantity != 5 || *o.MaxQuantity != 9 || *o.OldPriceMinor != 145000 || o.PriceBasis != "per_unit" {
		t.Fatalf("priced tier: %#v", o)
	}
}
func TestMissingFamilyData(t *testing.T) {
	if _, e := Extract("https://store.softline.ru/vendor/suite/", []byte("<h1>Try again later</h1>"), time.Now()); e == nil {
		t.Fatal("accepted page without family model")
	}
}
