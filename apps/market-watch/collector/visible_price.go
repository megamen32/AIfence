package catalog

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// A shop need not publish its price twice. For an exact standalone card,
// an explicit, unique current-price block is independent DOM evidence.
// This does not relax validation of family/listing rows.
func rubleAmount(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	upper := strings.ToUpper(s)
	for _, suffix := range []string{"₽", "РУБ.", "РУБ", "RUB"} {
		if strings.HasSuffix(upper, suffix) {
			// Remove from the original string by rune count, not byte length.
			rs := []rune(s)
			rs = rs[:len(rs)-len([]rune(suffix))]
			n, e := strictPrice(strings.TrimSpace(string(rs)))
			return n, e == nil
		}
	}
	return 0, false
}

func excludedPriceBranch(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.Data {
	case "s", "del", "strike", "script", "style", "template", "noscript":
		return true
	}
	for _, a := range n.Attr {
		v := strings.ToLower(a.Val)
		switch a.Key {
		case "hidden":
			return true
		case "aria-hidden":
			if v == "true" {
				return true
			}
		case "style":
			if strings.Contains(strings.ReplaceAll(v, " ", ""), "display:none") || strings.Contains(strings.ReplaceAll(v, " ", ""), "visibility:hidden") {
				return true
			}
		case "class", "id":
			for _, bad := range []string{"old-price", "old_price", "price-old", "price_old", "crossed", "installment", "monthly-payment", "shipping", "delivery", "discount-percent", "price-from", "min-price", "minimum-price", "per-month"} {
				if strings.Contains(v, bad) {
					return true
				}
			}
		}
	}
	return false
}
func insideNode(child, parent *html.Node) bool {
	for n := child; n != nil; n = n.Parent {
		if n == parent {
			return true
		}
	}
	return false
}
func semanticSKU(n *goquery.Selection, row *goquery.Selection) bool {
	if n == nil || row == nil {
		return false
	}
	for p := n.Get(0); p != nil; p = p.Parent {
		for _, a := range p.Attr {
			v := strings.ToLower(a.Val)
			if (a.Key == "itemprop" && v == "sku") || a.Key == "data-sku" || a.Key == "data-code" || ((a.Key == "class" || a.Key == "id") && (strings.Contains(v, "sku") || strings.Contains(v, "articul") || strings.Contains(v, "article-number"))) {
				return true
			}
		}
		if p == row.Get(0) {
			break
		}
	}
	return false
}
func uniqueVisibleCurrentPrice(row, scope, selected, nameNode, skuNode *goquery.Selection, doc *goquery.Document, price int64) bool {
	if row == nil || scope == nil || selected == nil || nameNode == nil || skuNode == nil || !priceNode(scope) || !semanticSKU(skuNode, row) {
		return false
	}
	if doc.Find("h1").Length() != 1 || nameNode.Get(0) != doc.Find("h1").Get(0) {
		return false
	}
	root, node := scope.Get(0), selected.Get(0)
	if !insideNode(node, root) || !insideNode(root, row.Get(0)) {
		return false
	}
	// Reject the whole selected branch if it is hidden or explicitly old.
	for p := node; p != nil; p = p.Parent {
		if excludedPriceBranch(p) {
			return false
		}
		if p == row.Get(0) {
			break
		}
	}
	// Each amount must be in a leaf element (or a self-contained currency span).
	// Do not mistake a parent containing an old and a current price for a quote.
	var found []*html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if excludedPriceBranch(n) {
			return
		}
		if n.Type != html.ElementNode {
			return
		}
		children := 0
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && !excludedPriceBranch(c) {
				children++
			}
		}
		if children == 0 {
			if value, ok := rubleAmount(goquery.NewDocumentFromNode(n).Text()); ok && value > 0 {
				found = append(found, n)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	if len(found) != 1 || found[0] != node {
		return false
	}
	actual, ok := rubleAmount(selected.Text())
	return ok && actual == price
}
