package catalog

import (
	"strings"
	"testing"
	"time"
)

func TestVettedCompetitorParsers(t *testing.T) {
	for _, p := range CompetitorProfiles() {
		var fixture, sku string
		var amount, min int64
		switch p.ID {
		case "allsoft":
			fixture = `<h1>Kaspersky Small Office Security</h1><table class="js-price_group"><tr><th><span class="b-text_product-version">License 1 year</span></th></tr><tr><td><span class="b-col_description-text">цена за копию (от 5 до 9)</span><span class="b-col_description-sku"><a href="/product/123/">KL4141RDEFS</a></span></td><td><input class="b-product-quantity__value" min="5" max="14"/><span data-offer-price>7 488 ₽</span></td></tr></table>`
			sku = "KL4141RDEFS"
			amount = 748800
			min = 5
		case "syssoft":
			fixture = `<div class="product-edition"><h3 class="title"><a href="/product/123/">License 1 year</a></h3><div class="price"><span>Цена/шт.: <span class="price_text_rub">2 242,80 ₽</span></span></div><input name="item_value_min" value="5"/><input name="item_value" value="5"/><ul class="product-edition_specs"><li><span>Артикул:</span><b>KL4546RAEFS</b></li></ul></div>`
			sku = "KL4546RAEFS"
			amount = 224280
			min = 5
		}
		t.Run(p.ID, func(t *testing.T) {
			rows, e := Extract(p, p.Seeds[0], strings.NewReader(fixture), time.Now())
			if e != nil || len(rows) != 1 {
				t.Fatalf("%v %v", rows, e)
			}
			o := rows[0]
			if o.SKU != sku || o.PriceMinor != amount || o.Quantity == nil || *o.Quantity != min || o.PriceBasis != "per_unit" || !Allowed(p, o.URL) {
				t.Fatalf("%+v", o)
			}
			if p.ID == "allsoft" && (o.MaxQuantity == nil || *o.MaxQuantity != 9) {
				t.Fatal("must not use input's global maximum")
			}
			if p.ID == "syssoft" && o.MaxQuantity != nil {
				t.Fatal("must not invent a cap")
			}
		})
	}
}
