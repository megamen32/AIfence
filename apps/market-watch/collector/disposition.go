package catalog

import (
	"encoding/json"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// A missing public quote is an observation, not a parser failure. Use the
// publisher's exact product identity, its own offer metadata and visible
// status label. Never infer a price or lack of price from an empty selector.
func exactSchemaProduct(page string, p map[string]any) bool {
	if !schemaType(p["@type"], "Product") {
		return false
	}
	main := scalar(p["mainEntityOfPage"])
	if m, ok := p["mainEntityOfPage"].(map[string]any); ok {
		main = scalar(m["@id"])
	}
	found := false
	for _, ref := range []string{scalar(p["url"]), scalar(p["@id"]), main} {
		if ref == "" {
			continue
		}
		if !exactURL(page, ref) {
			return false
		}
		found = true
	}
	return found
}
func schemaProducts(doc *goquery.Document) []map[string]any {
	var products []map[string]any
	doc.Find("script[type='application/ld+json']").Each(func(_ int, n *goquery.Selection) {
		var v any
		d := json.NewDecoder(strings.NewReader(n.Text()))
		d.UseNumber()
		if d.Decode(&v) != nil {
			return
		}
		roots := objectList(v)
		for _, r := range roots {
			roots = append(roots, objectList(r["@graph"])...)
		}
		for _, r := range roots {
			if schemaType(r["@type"], "Product") {
				products = append(products, r)
			}
		}
	})
	return products
}
func explicitRequest(s string) bool {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return strings.Contains(s, "цена по запросу") || strings.Contains(s, "цена предоставляется по запросу") || strings.Contains(s, "уточняйте стоимость у менеджера")
}
func classifySoftMagazinDisposition(page string, doc *goquery.Document, p *PageResult) bool {
	if p.Category != "software" || p.Title == "" || len(p.Offers) != 0 || p.State != "unclassified" || !canonicalExact(doc, page) {
		return false
	}
	// The exact standalone card has a visible, independently verified SKU.
	sku := strings.ToUpper(strings.TrimSpace(doc.Find(".articul-manufacturer h2 span").First().Text()))
	if sku == "" || doc.Find(".offer-tovar-style").Length() == 0 {
		return false
	}
	var request, discontinued, conflict bool
	matched := false
	for _, product := range schemaProducts(doc) {
		if !exactSchemaProduct(page, product) || !strings.EqualFold(scalar(product["sku"]), sku) {
			continue
		}
		matched = true
		for _, o := range objectList(product["offers"]) {
			if !schemaType(o["@type"], "Offer") {
				conflict = true
				continue
			}
			if u := scalar(o["url"]); u != "" && !exactURL(page, u) {
				conflict = true
				continue
			}
			// Explicit numeric data (including zero) or price specifications are
			// not equivalent to absence of a quote.
			if scalar(o["price"]) != "" || o["priceSpecification"] != nil {
				conflict = true
				continue
			}
			if explicitRequest(scalar(o["description"])) {
				request = true
			}
			if strings.HasSuffix(scalar(o["availability"]), "/Discontinued") {
				discontinued = true
			}
		}
	}
	if !matched || conflict {
		return false
	}
	// Restrict this visible label to the exact product card. A generic
	// schema.org InStock does not supply a price or undo the explicit
	// discontinued status when the same product has no public quote.
	if request {
		doc.Find(".offer-tovar-style .out-of-production-label").Each(func(_ int, n *goquery.Selection) {
			if strings.EqualFold(strings.Join(strings.Fields(n.Text()), " "), "Снято с производства") {
				discontinued = true
			}
		})
	}
	if !request && !discontinued {
		return false
	}
	status, reason := "no_public_price", "exact_product_jsonld_offer_request_only"
	if discontinued {
		status, reason = "unavailable", "exact_product_discontinued_without_public_quote"
	}
	p.State = status
	p.StatusEvidence = reason
	p.Variants = []Variant{{URL: page, SKU: sku, Name: p.Title, Status: status}}
	return true
}
