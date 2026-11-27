package catalog

import (
	"bytes"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

var stableClass = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,80}$`)

func exactSelector(root, n *goquery.Selection) string {
	if n == nil || n.Length() != 1 {
		return ""
	}
	match := func(sel string) bool {
		q := root.Find(sel)
		if root.Is(sel) {
			q = q.AddSelection(root)
		}
		return q.Length() == 1 && q.Get(0) == n.Get(0)
	}
	if id, ok := n.Attr("id"); ok && stableClass.MatchString(id) && match("#"+id) {
		return "#" + id
	}
	class, _ := n.Attr("class")
	for _, token := range strings.Fields(class) {
		if stableClass.MatchString(token) && match("."+token) {
			return "." + token
		}
	}
	tag := goquery.NodeName(n)
	if stableClass.MatchString(tag) && match(tag) {
		return tag
	}
	return ""
}
func relativeSelector(root, n *goquery.Selection) string {
	if sel := exactSelector(root, n); sel != "" {
		return sel
	}
	for p := n.Parent(); p.Length() == 1 && p.Get(0) != root.Get(0); p = p.Parent() {
		if sel := exactSelector(root, p); sel != "" {
			rel := exactSelector(p, n)
			if rel != "" {
				q := root.Find(sel + " " + rel)
				if q.Length() == 1 && q.Get(0) == n.Get(0) {
					return sel + " " + rel
				}
			}
		}
	}
	return ""
}

type seedSKU struct {
	node  *goquery.Selection
	field Field
}

func chooseSKU(body *goquery.Selection) []seedSKU {
	var out []seedSKU
	seen := map[*html.Node]bool{}
	body.Find("[itemprop=sku],[data-sku],[data-code],[class*=sku] code,[class*=articul] code,[class*=sku] b,[class*=articul] b").Each(func(_ int, n *goquery.Selection) {
		if seen[n.Get(0)] || !semanticSKU(n, body) || excludedPriceBranch(n.Get(0)) || n.Is("button,script,style,input[type=hidden]") {
			return
		}
		v := strings.TrimSpace(n.Text())
		attr := ""
		for _, a := range []string{"data-sku", "data-code", "content"} {
			if x, ok := n.Attr(a); ok && strings.TrimSpace(x) != "" {
				v = strings.TrimSpace(x)
				attr = a
				break
			}
		}
		if v == "" || len(v) > 150 || strings.Contains(strings.ToLower(v), "артикул") || strings.Contains(strings.ToLower(v), "sku:") {
			return
		}
		selector := relativeSelector(body, n)
		if selector == "" {
			return
		}
		out = append(out, seedSKU{n, Field{Selector: selector, Attr: attr}})
		seen[n.Get(0)] = true
	})
	return out
}
func seedCommonRow(body, h1, sku, scope *goquery.Selection) *goquery.Selection {
	for p := scope; p.Length() == 1; p = p.Parent() {
		if insideNode(h1.Get(0), p.Get(0)) && insideNode(sku.Get(0), p.Get(0)) {
			if p.Get(0) == body.Get(0) || exactSelector(body, p) != "" {
				return p
			}
		}
		if p.Get(0) == body.Get(0) {
			break
		}
	}
	return nil
}

// Derive a candidate only from a training page. This is a hint for the
// model, never a quote or an activation. The unchanged holdout validator
// must independently prove it before accepting a version.
func VisibleAdapterSeed(s Source, sample AdapterSample) (AdapterCase, bool) {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(sample.Body))
	if e != nil || !canonicalExact(doc, sample.URL) || doc.Find("h1").Length() != 1 {
		return AdapterCase{}, false
	}
	body := doc.Find("body")
	h1 := doc.Find("h1")
	skus := chooseSKU(body)
	if len(skus) != 1 {
		return AdapterCase{}, false
	}
	sku := skus[0]
	var candidates []AdapterCase
	body.Find("[class*=price],[id*=price],[class*=cost],[id*=cost]").Each(func(_ int, scope *goquery.Selection) {
		if !priceNode(scope) {
			return
		}
		var leaves []*html.Node
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if excludedPriceBranch(n) || n.Type != html.ElementNode {
				return
			}
			children := 0
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && !excludedPriceBranch(c) {
					children++
				}
			}
			if children == 0 {
				if _, ok := rubleAmount(goquery.NewDocumentFromNode(n).Text()); ok {
					leaves = append(leaves, n)
				}
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(scope.Get(0))
		if len(leaves) != 1 {
			return
		}
		// Use the original DOM element, not a copied node.
		leaf := scope.Find("*").FilterFunction(func(_ int, n *goquery.Selection) bool { return n.Get(0) == leaves[0] })
		if scope.Get(0) == leaves[0] {
			leaf = scope
		}
		if leaf.Length() != 1 {
			return
		}
		row := seedCommonRow(body, h1, sku.node, scope)
		if row == nil {
			return
		}
		rowSel := exactSelector(body, row)
		if row.Get(0) == body.Get(0) {
			rowSel = "body"
		}
		scopeSel := relativeSelector(row, scope)
		priceSel := relativeSelector(row, leaf)
		skuSel := relativeSelector(row, sku.node)
		if rowSel == "" || scopeSel == "" || priceSel == "" || skuSel == "" {
			return
		}
		// The actual leaf selector, not the outer price block with crossed-out
		// text, must identify exactly one value.
		c := AdapterCase{ID: "visible", Kind: "product", Match: rowSel, Rows: rowSel, Name: Field{Selector: "h1"}, SKU: Field{Selector: skuSel, Attr: sku.field.Attr}, Price: Field{Selector: priceSel}, Currency: Field{Selector: priceSel}, PriceScope: scopeSel, Basis: "displayed_total"}
		if err := ValidateAdapter(AdapterSpec{Cases: []AdapterCase{c}}); err != nil {
			return
		}
		r, err := ParseAdapter(s, AdapterSpec{Cases: []AdapterCase{c}}, sample.URL, sample.Body, sample.CapturedAt)
		if err != nil || len(r.Offers) != 1 {
			return
		}
		candidates = append(candidates, c)
	})
	if len(candidates) == 0 {
		return AdapterCase{}, false
	}
	// If the proposed selectors capture distinct prices, leave this template
	// to the LLM instead of guessing which one is the actual offer.
	var chosen AdapterCase
	var value int64
	for _, c := range candidates {
		r, e := ParseAdapter(s, AdapterSpec{Cases: []AdapterCase{c}}, sample.URL, sample.Body, time.Now())
		if e != nil || len(r.Offers) != 1 {
			continue
		}
		if value != 0 && value != r.Offers[0].PriceMinor {
			return AdapterCase{}, false
		}
		value = r.Offers[0].PriceMinor
		if chosen.ID == "" || len(c.PriceScope)+len(c.Price.Selector) < len(chosen.PriceScope)+len(chosen.Price.Selector) {
			chosen = c
		}
	}
	return chosen, chosen.ID != ""
}
