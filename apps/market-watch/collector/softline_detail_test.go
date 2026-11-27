package catalog

import (
	"strings"
	"testing"
	"time"
)

func TestSoftlineUnitAndCounterTotal(t *testing.T) {
	src := sources(t)[2]
	const html = `<script id="js-front-config">window.temporaryFrontConfig = {"product":{"minQuantity":5,"priceWithDiscount":"6589","price":"6589"}};</script><h1>Five license tier</h1><input class="js-product-quantity" min="5" max="9" value="5"><span class="js-price-with-discount">32 945</span>`
	for _, tc := range []struct {
		label, body, basis string
		amount             int64
	}{
		{"unit", html, "per_unit", 658900},
		{"only total", html[strings.Index(html, "<h1>"):], "displayed_total", 3294500},
	} {
		t.Run(tc.label, func(t *testing.T) {
			rows, err := Extract(src, src.Seeds[0], strings.NewReader(tc.body), time.Unix(0, 0))
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows %v err %v", rows, err)
			}
			o := rows[0]
			if o.PriceMinor != tc.amount || o.PriceBasis != tc.basis || o.Quantity == nil || *o.Quantity != 5 || o.MaxQuantity == nil || *o.MaxQuantity != 9 {
				t.Fatalf("incorrect price or quantity %+v", o)
			}
		})
	}
}

func TestSoftlineConflictingUpperBound(t *testing.T) {
	src := sources(t)[2]
	const html = `<script id="js-front-config">window.temporaryFrontConfig = {"product":{"minQuantity":10000,"priceWithDiscount":6352}};</script><h1>Кибер Файлы</h1><input class="js-product-quantity" min="10000" max="9999" data-maxval="9999"><span class="js-price-with-discount">63 520 000</span>`
	for _, body := range []string{html, html[strings.Index(html, "<h1>"):]} {
		rows, err := Extract(src, src.Seeds[0], strings.NewReader(body), time.Unix(0, 0))
		if err != nil || len(rows) != 1 {
			t.Fatalf("observations %v: %v", rows, err)
		}
		o := rows[0]
		if o.Quantity == nil || *o.Quantity != 10000 || o.MaxQuantity != nil {
			t.Fatalf("inconsistent public range %+v", o)
		}
	}
}

func TestSoftlineSelectedTierAndOverallCounterBounds(t *testing.T) {
	src := sources(t)[2]
	const html = `<script id="js-front-config">window.temporaryFrontConfig = {"product":{"minQuantity":200,"priceWithDiscount":"7125","price":"7125"}};</script><h1>License</h1><input class="js-product-quantity" min="50" max="9999" data-minval="200" data-maxval="499" value="200"><span class="js-price-with-discount">1 425 000</span>`
	rows, err := Extract(src, src.Seeds[0], strings.NewReader(html), time.Unix(0, 0))
	if err != nil || len(rows) != 1 {
		t.Fatalf("price: %v %v", rows, err)
	}
	o := rows[0]
	if o.PriceMinor != 712500 || o.PriceBasis != "per_unit" || o.Quantity == nil || *o.Quantity != 200 || o.MaxQuantity == nil || *o.MaxQuantity != 499 {
		t.Fatalf("tier %+v", o)
	}
	// An actual conflict with the tier, unlike the global control minimum,
	// remains unresolved rather than silently accepting incorrect terms.
	bad := strings.Replace(html, `data-minval="200"`, `data-minval="100"`, 1)
	if _, e := Extract(src, src.Seeds[0], strings.NewReader(bad), time.Unix(0, 0)); e == nil {
		t.Fatal("conflicting tier accepted")
	}
}
