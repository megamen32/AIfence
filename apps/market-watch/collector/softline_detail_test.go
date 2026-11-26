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
