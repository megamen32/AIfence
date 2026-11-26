package softlinefamily

import (
	"bytes"
	"github.com/PuerkitoBio/goquery"
	"strings"
)

// Describe preserves public product families that have no row for an individual license.
func Describe(body []byte, products []Product) (string, string) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return "", "family_without_listed_variants"
	}
	title := strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	for _, p := range products {
		if p.Offer != nil {
			return title, "family_with_prices"
		}
	}
	if len(products) > 0 {
		return title, "family_with_variants"
	}
	text := strings.ToLower(strings.Join(strings.Fields(doc.Find("body").Text()), " "))
	if strings.Contains(text, "цена на данный продукт предоставляется по запросу") {
		return title, "no_public_price"
	}
	return title, "family_without_listed_variants"
}
