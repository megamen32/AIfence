package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func quantityValue(s string) *int64 {
	n, err := strconv.ParseInt(strings.Trim(s, " \""), 10, 64)
	if err != nil || n < 1 {
		return nil
	}
	return &n
}

// SoftlineDetail reads the public per-license unit price used by the storefront.
// The visible counter total can be the unit price multiplied by the minimum order.
func SoftlineDetail(doc *goquery.Document, page string, at time.Time) (*Offer, error) {
	if !Allowed(Source{Host: "store.softline.ru"}, page) {
		return nil, fmt.Errorf("outside Softline store")
	}
	name := strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	if name == "" {
		return nil, nil
	}
	sku, _ := doc.Find(".js-sku-btn").First().Attr("data-code")
	counter := doc.Find("input.js-product-quantity").First()
	minimum, _ := counter.Attr("min")
	maximum, _ := counter.Attr("max")
	// min/max are the cart's overall input limits; data-minval/data-maxval
	// describe the selected license tier. Only compare the tier to product JSON.
	if tier, ok := counter.Attr("data-minval"); ok && quantityValue(tier) != nil {
		minimum = tier
	}
	if tier, ok := counter.Attr("data-maxval"); ok {
		maximum = tier
	}
	o := Offer{Source: "softline", SKU: strings.ToUpper(strings.TrimSpace(sku)), Name: name, URL: page, Currency: "RUB", CapturedAt: at.UTC().Format(time.RFC3339), Quantity: quantityValue(minimum)}
	var cfg struct {
		Product struct {
			Min     json.RawMessage `json:"minQuantity"`
			Current json.RawMessage `json:"priceWithDiscount"`
			List    json.RawMessage `json:"price"`
		}
	}
	script := doc.Find("#js-front-config").Text()
	if _, data, ok := strings.Cut(script, "window.temporaryFrontConfig ="); ok {
		if err := json.NewDecoder(strings.NewReader(data)).Decode(&cfg); err != nil {
			return nil, fmt.Errorf("Softline product configuration: %w", err)
		}
		if q := quantityValue(string(cfg.Product.Min)); q != nil {
			if o.Quantity != nil && *o.Quantity != *q {
				return nil, fmt.Errorf("inconsistent Softline minimum quantity")
			}
			o.Quantity = q
		}
	}
	o.MaxQuantity = PublicMaxQuantity(o.Quantity, quantityValue(maximum))
	if price, err := Money(strings.Trim(string(cfg.Product.Current), "\" ")); err == nil {
		o.PriceBasis = "per_unit"
		o.PriceMinor = price
		if old, err := Money(strings.Trim(string(cfg.Product.List), "\" ")); err == nil && old > price {
			o.OldPriceMinor = &old
		}
		return &o, nil
	}
	displayed := strings.TrimSpace(doc.Find(".js-price-with-discount").First().Text())
	if displayed == "" {
		return nil, nil
	}
	price, err := Money(displayed)
	if err != nil {
		return nil, err
	}
	o.PriceMinor = price
	o.PriceBasis = "displayed_total"
	return &o, nil
}
