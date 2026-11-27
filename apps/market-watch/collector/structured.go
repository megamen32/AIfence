package catalog

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func exactURL(page, raw string) bool {
	if raw == "" {
		return false
	}
	base, e := url.Parse(page)
	if e != nil {
		return false
	}
	target, e := base.Parse(raw)
	if e != nil {
		return false
	}
	target.Fragment = ""
	return target.String() == page
}
func canonicalExact(doc *goquery.Document, page string) bool {
	raw, ok := doc.Find("link[rel='canonical']").First().Attr("href")
	return ok && exactURL(page, raw)
}
func schemaType(value any, want string) bool {
	switch x := value.(type) {
	case string:
		return x == want || strings.HasSuffix(x, "/"+want)
	case []any:
		for _, v := range x {
			if schemaType(v, want) {
				return true
			}
		}
	}
	return false
}
func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	}
	return ""
}
func objectList(v any) []map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return []map[string]any{x}
	case []any:
		var out []map[string]any
		for _, r := range x {
			out = append(out, objectList(r)...)
		}
		return out
	}
	return nil
}
func strictPrice(s string) (int64, error) {
	// Structured prices must be complete numeric values, not a heading, "from",
	// installment amount or an unrelated number inside the text.
	if m := moneyRE.FindString(strings.TrimSpace(s)); m != strings.TrimSpace(s) {
		return Money("")
	}
	return Money(s)
}
func structuredJSON(s Source, page string, doc *goquery.Document, at time.Time) []Offer {
	var out []Offer
	doc.Find("script[type='application/ld+json']").Each(func(_ int, n *goquery.Selection) {
		var v any
		d := json.NewDecoder(strings.NewReader(n.Text()))
		d.UseNumber()
		if d.Decode(&v) != nil {
			return
		}
		roots := objectList(v)
		for _, r := range roots {
			if graph := objectList(r["@graph"]); len(graph) > 0 {
				roots = append(roots, graph...)
			}
		}
		for _, p := range roots {
			if !schemaType(p["@type"], "Product") {
				continue
			}
			name, sku := scalar(p["name"]), scalar(p["sku"])
			if name == "" || sku == "" {
				continue
			}
			for _, o := range objectList(p["offers"]) {
				if !schemaType(o["@type"], "Offer") || scalar(o["priceCurrency"]) != "RUB" {
					continue
				}
				pu, ou := scalar(p["url"]), scalar(o["url"])
				if pu != "" && !exactURL(page, pu) || ou != "" && !exactURL(page, ou) {
					continue
				}
				if !exactURL(page, pu) && !exactURL(page, ou) {
					continue
				}
				// Do not turn AggregateOffer.lowPrice or tier minima into actual quotes.
				if o["priceSpecification"] != nil || o["eligibleQuantity"] != nil {
					continue
				}
				value, e := strictPrice(scalar(o["price"]))
				if e != nil {
					continue
				}
				out = append(out, Offer{Source: s.ID, SKU: strings.ToUpper(sku), Name: name, PriceMinor: value, Currency: "RUB", PriceBasis: "displayed_total", URL: page, CapturedAt: at.UTC().Format(time.RFC3339)})
			}
		}
	})
	return out
}
func itemValue(n *goquery.Selection, prop string) string {
	v := n.Find("[itemprop='" + prop + "']").First()
	for _, attr := range []string{"content", "href", "value"} {
		if x, ok := v.Attr(attr); ok && strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return strings.TrimSpace(v.Text())
}
func structuredMicro(s Source, page string, doc *goquery.Document, at time.Time) []Offer {
	var out []Offer
	doc.Find("[itemscope][itemtype$='/Product']").Each(func(_ int, p *goquery.Selection) {
		if !exactURL(page, itemValue(p, "url")) {
			return
		}
		sku, name := itemValue(p, "sku"), itemValue(p, "name")
		if sku == "" || name == "" {
			return
		}
		p.Find("[itemprop='offers'][itemscope][itemtype$='/Offer']").Each(func(_ int, o *goquery.Selection) {
			if raw := itemValue(o, "url"); raw != "" && !exactURL(page, raw) {
				return
			}
			if itemValue(o, "priceCurrency") != "RUB" || o.Find("[itemprop='priceSpecification'],[itemprop='eligibleQuantity']").Length() > 0 {
				return
			}
			price, e := strictPrice(itemValue(o, "price"))
			if e != nil {
				return
			}
			out = append(out, Offer{Source: s.ID, SKU: strings.ToUpper(sku), Name: name, PriceMinor: price, Currency: "RUB", PriceBasis: "displayed_total", URL: page, CapturedAt: at.UTC().Format(time.RFC3339)})
		})
	})
	return out
}

// StructuredOffer accepts one exact product/offer only. A price basis that
// isn't explicitly a verified unit tier remains "displayed_total".
func StructuredOffer(s Source, page string, doc *goquery.Document, at time.Time) *Offer {
	if !Allowed(s, page) || !canonicalExact(doc, page) {
		return nil
	}
	rows := append(structuredJSON(s, page, doc, at), structuredMicro(s, page, doc, at)...)
	if len(rows) == 0 {
		return nil
	}
	first := rows[0]
	for _, r := range rows[1:] {
		if r.SKU != first.SKU || r.PriceMinor != first.PriceMinor {
			return nil
		}
	}
	return &first
}
