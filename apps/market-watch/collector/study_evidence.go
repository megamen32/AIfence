package catalog

import (
	"bytes"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// This is an acquisition hint, NOT a verified price. It lets learning
// spend its requests on pages with actual product/offer markup.
type StudySignals struct {
	Title           string `json:"title"`
	Canonical       string `json:"canonical"`
	ProductObjects  int    `json:"product_objects"`
	StructuredPrice bool   `json:"structured_price"`
	PriceNodes      int    `json:"price_nodes"`
	SKUFields       int    `json:"sku_fields"`
	PricedLinks     int    `json:"priced_links"`
}

func InspectStudySignals(s Source, page string, b []byte) StudySignals {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(b))
	if e != nil {
		return StudySignals{}
	}
	sig := StudySignals{Title: strings.Join(strings.Fields(doc.Find("h1").First().Text()), " "), ProductObjects: len(schemaProducts(doc))}
	sig.Canonical, _ = doc.Find("link[rel=canonical]").First().Attr("href")
	sig.StructuredPrice = StructuredOffer(s, page, doc, time.Now()) != nil
	doc.Find("[itemprop=price],[class*=price],[data-price],[data-offer-price]").Each(func(_ int, n *goquery.Selection) {
		if !priceNode(n) {
			return
		}
		for _, a := range []string{"content", "data-price", "data-offer-price", "data-price-value"} {
			if v, ok := n.Attr(a); ok {
				if _, e := strictVisiblePrice(v); e == nil {
					sig.PriceNodes++
					return
				}
			}
		}
		if _, e := strictVisiblePrice(n.Text()); e == nil {
			sig.PriceNodes++
		}
	})
	sig.SKUFields = doc.Find("[itemprop=sku],[class*=sku],[class*=articul],[data-sku],[data-code]").Length()
	sig.PricedLinks = len(StudyLinkScores(s, page, b))
	return sig
}
func (sig StudySignals) Score() int {
	n := 0
	if sig.StructuredPrice {
		n += 1000
	}
	if sig.ProductObjects > 0 {
		n += 30
	}
	if sig.PriceNodes > 0 {
		n += 50
	}
	if sig.SKUFields > 0 {
		n += 15
	}
	return n
}
func (sig StudySignals) HasQuoteEvidence() bool {
	return sig.StructuredPrice || (sig.PriceNodes > 0 && (sig.ProductObjects > 0 || sig.SKUFields > 0))
}

// Rank links based on evidence in their surrounding product card, not
// guessed endpoints or a manufacturer name. Every returned URL occurred
// in actual archived HTML and remains inside the approved source.
func StudyLinkScores(s Source, page string, b []byte) map[string]int {
	out := map[string]int{}
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(b))
	if e != nil {
		return out
	}
	// Restrict to links already accepted by the public discovery filter.
	allowed := map[string]bool{}
	for _, u := range PublishedLinks(s, page, b) {
		allowed[u] = true
	}
	doc.Find("a[href]").Each(func(_ int, n *goquery.Selection) {
		raw, _ := n.Attr("href")
		u, ok := seedLink(s, page, raw)
		if !ok || !allowed[u] {
			return
		}
		node := n
		score := 0
		for depth := 0; depth < 3 && node.Length() > 0; depth++ {
			if node.Is("body,html,nav,header,footer") {
				break
			}
			if node.Find("[itemprop=price],[class*=price],[data-price],[data-offer-price]").Length() > 0 {
				// Don't treat an entire large category grid as an individual row.
				if node.Find("a[href]").Length() <= 12 {
					score = max(score, 100-depth*20)
				}
			}
			node = node.Parent()
		}
		if score > 0 {
			out[u] = max(out[u], score)
		}
	})
	return out
}
func StudyURLScore(raw string) int {
	u, e := url.Parse(raw)
	if e != nil {
		return -100
	}
	p := strings.ToLower(strings.Trim(u.Path, "/"))
	n := strings.Count(p, "/") * 4
	for _, x := range []string{"product", "catalog", "software", "tovar", "license", "licens", "soft"} {
		if strings.Contains(p, x) {
			n += 4
		}
	}
	for _, x := range []string{"news", "article", "brand", "vendor", "blog", "about", "service", "contact", "support"} {
		if strings.Contains(p, x) {
			n -= 12
		}
	}
	if u.RawQuery != "" {
		n -= 8
	}
	return n
}
func RankStudyURLs(urls []string, hints map[string]int) {
	sort.SliceStable(urls, func(i, j int) bool {
		a, b := StudyURLScore(urls[i])+hints[urls[i]], StudyURLScore(urls[j])+hints[urls[j]]
		if a != b {
			return a > b
		}
		return urls[i] < urls[j]
	})
}
func StudySummary(train, hold int) string {
	return fmt.Sprintf("product price evidence: training=%d holdout=%d", train, hold)
}
