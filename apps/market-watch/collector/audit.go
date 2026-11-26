package catalog

import (
	"github.com/PuerkitoBio/goquery"
	"io"
	"net/url"
	"strings"
	"time"
)

// Variant captures visible license rows even when their prices are by request.
type Variant struct {
	URL    string `json:"url"`
	SKU    string `json:"sku,omitempty"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type PageResult struct {
	URL      string    `json:"url"`
	At       string    `json:"at"`
	State    string    `json:"state"`
	Title    string    `json:"title,omitempty"`
	Offers   []Offer   `json:"offers,omitempty"`
	Variants []Variant `json:"variants,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// InspectPage uses the same producer of prices as the native app and records
// the evidence needed to distinguish a request-only product from a changed template.
func InspectPage(s Source, page string, body io.Reader, at time.Time) (PageResult, error) {
	p := PageResult{URL: page, At: at.UTC().Format(time.RFC3339), State: "unclassified"}
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return p, err
	}
	p.Title = strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	p.Offers, err = extractDoc(s, page, doc, at)
	priced := map[string]bool{}
	for _, o := range p.Offers {
		priced[o.URL+"\x00"+o.SKU] = true
	}
	base, _ := url.Parse(page)
	missing := 0
	doc.Find(s.Rule.Row).Each(func(_ int, r *goquery.Selection) {
		name := field(r, s.Rule.Name)
		if name == "" {
			return
		}
		sku := strings.ToUpper(field(r, s.Rule.SKU))
		href := page
		if u, e := base.Parse(field(r, s.Rule.Link)); e == nil && Allowed(s, u.String()) {
			href = u.String()
		}
		state := "unclassified"
		if priced[href+"\x00"+sku] {
			state = "priced"
		} else {
			words := strings.ToLower(strings.Join(strings.Fields(r.Text()), " "))
			switch {
			case strings.Contains(words, "запрос"), strings.Contains(words, "уточн"), strings.Contains(words, "закрыта продажа"):
				state = "no_public_price"
			case sku == "":
				state = "missing_sku"
				missing++
			default:
				missing++
			}
		}
		p.Variants = append(p.Variants, Variant{URL: href, Name: name, SKU: sku, Status: state})
	})
	switch {
	case err != nil || missing > 0:
		p.State = "unclassified"
	case len(p.Offers) > 0:
		p.State = "priced"
	case len(p.Variants) > 0:
		p.State = "no_public_price"
	case s.ID == "softmagazin" && p.Title != "" && strings.Contains(strings.ToLower(doc.Find(".soft-tovar-style #placeforminprice .no-price").Text()), "запрос"):
		p.State = "no_public_price"
		p.Variants = []Variant{{URL: page, Name: p.Title, Status: "no_public_price"}}
	}
	return p, err
}
