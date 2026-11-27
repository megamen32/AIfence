package softlinefamily

import (
	catalog "agent/collector"
	"bytes"
	"encoding/json"
	"github.com/PuerkitoBio/goquery"
	"net/url"
	"strings"
	"time"
)

// The publisher may redirect a standalone license URL to its family. Use the
// returned family model to collect exact child URLs, never transfer a price
// from a different canonical product to the requested URL.
func Direct(page string, b []byte, at time.Time) ([]Product, error) {
	source := catalog.Source{ID: "softline", Host: Host}
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	sku, _ := doc.Find(".js-sku-btn").First().Attr("data-code")
	name := strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	p := Product{URL: page, SKU: strings.ToUpper(strings.TrimSpace(sku)), Name: name, State: "unrecognized_product"}
	if canonical, ok := doc.Find("link[rel='canonical']").First().Attr("href"); ok && strings.TrimSpace(canonical) != "" {
		base, err := url.Parse(page)
		if err != nil {
			return nil, err
		}
		target, err := base.Parse(strings.TrimSpace(canonical))
		if err != nil || !catalog.Allowed(source, target.String()) {
			return []Product{{URL: page, State: "unrecognized_product"}}, nil
		}
		if target.String() != page {
			products, err := Extract(target.String(), b, at)
			if err == nil && len(products) > 0 {
				for _, v := range products {
					if v.URL == page {
						return products, nil
					}
				}
				return append(products, Product{URL: page, State: "unrecognized_product", StatusEvidence: "canonical_family_does_not_list_requested_url"}), nil
			}
			return []Product{{URL: page, State: "unrecognized_product"}}, nil
		}
	}
	offer, err := catalog.SoftlineDetail(doc, page, at)
	if err != nil {
		return nil, err
	}
	if offer == nil {
		offer = catalog.StructuredOffer(source, page, doc, at)
	}
	if offer != nil {
		p.State = "priced"
		p.Offer = offer
		return []Product{p}, nil
	}
	if name != "" {
		// Explicit null prices in the exact software product's own published
		// configuration are positive evidence of no public quote, not parse failure.
		// Absent keys, unrelated IDs and redirected pages stay unresolved.
		var cfg struct {
			Product struct {
				ID      json.RawMessage
				Type    string
				Price   json.RawMessage
				Current json.RawMessage `json:"priceWithDiscount"`
			}
		}
		if _, data, ok := strings.Cut(doc.Find("#js-front-config").Text(), "window.temporaryFrontConfig ="); ok && json.NewDecoder(strings.NewReader(data)).Decode(&cfg) == nil {
			id := strings.Trim(string(cfg.Product.ID), "\\\" ")
			suffix := strings.TrimSuffix(page, "/")
			i := strings.LastIndex(suffix, "-")
			if i >= 0 && id != "" && suffix[i+1:] == id && cfg.Product.Type == "software" && string(cfg.Product.Price) == "null" && string(cfg.Product.Current) == "null" {
				p.State = "no_public_price"
				return []Product{p}, nil
			}
		}
		main := strings.ToLower(strings.Join(strings.Fields(doc.Find("main,.b-product-card,.b-product-detail").Text()), " "))
		for _, marker := range []string{"цена по запросу", "цена предоставляется по запросу", "цена на данный продукт предоставляется по запросу", "узнать цену"} {
			if strings.Contains(main, marker) {
				p.State = "no_public_price"
				break
			}
		}
	}
	return []Product{p}, nil
}
