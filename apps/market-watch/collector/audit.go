package catalog

import (
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"net/url"
	"regexp"
	"strconv"
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
	URL          string    `json:"url"`
	At           string    `json:"at"`
	State        string    `json:"state"`
	Title        string    `json:"title,omitempty"`
	EvidenceURL  string    `json:"evidence_url,omitempty"`
	Category     string    `json:"category,omitempty"`
	CategoryURL  string    `json:"category_url,omitempty"`
	ExcludedURLs []string  `json:"excluded_urls,omitempty"`
	Offers       []Offer   `json:"offers,omitempty"`
	Variants     []Variant `json:"variants,omitempty"`
	Error        string    `json:"error,omitempty"`
	Attempts     int       `json:"attempts,omitempty"`
}

// InspectPage uses the same producer of prices as the native app and records
// the evidence needed to distinguish a request-only product from a changed template.
func InspectPage(s Source, page string, body io.Reader, at time.Time) (PageResult, error) {
	p := PageResult{URL: page, At: at.UTC().Format(time.RFC3339), State: "unclassified"}
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return p, err
	}
	return inspectDocument(s, page, doc, at)
}
func inspectDocument(s Source, page string, doc *goquery.Document, at time.Time) (PageResult, error) {
	p := PageResult{URL: page, At: at.UTC().Format(time.RFC3339), State: "unclassified"}
	var err error
	p.Title = strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	if s.ID == "softmap" && doc.Find(".products-list .brand-name h1").Length() > 0 && doc.Find(".products-detail").Length() == 0 {
		p.State = "not_product"
		return p, nil
	}
	if s.ID == "softmagazin" {
		switch {
		case doc.Find(".hard-tovar-style").Length() > 0:
			p.Category = "hardware"
		case doc.Find(".soft-tovar-style").Length() > 0:
			p.Category = "software"
		case doc.Find(".info-category .base-links-tovar-item a[href^='/soft/']").Length() > 0:
			p.Category = "software"
		case doc.Find(".info-category .base-links-tovar-item a[href^='/hard/']").Length() > 0:
			p.Category = "hardware"
		default:
			p.Category = "unknown"
		}
		if doc.Find(".offer-tovar-style").Length() > 0 {
			s.Rule.Row = ".offer-tovar-style"
			s.Rule.SKU = Field{Selector: ".articul-manufacturer h2 span"}
			s.Rule.Name = Field{Selector: "h1"}
			s.Rule.Price = Field{Selector: ".price-block .price-field:not(.no-price)"}
			s.Rule.Link = Field{}
		}
	}
	p.Offers, err = extractDoc(s, page, doc, at)
	// Existing verified tier data wins over less-specific structured prices.
	if len(p.Offers) == 0 && err == nil && (s.ID != "softmagazin" || p.Category == "software") {
		if o := StructuredOffer(s, page, doc, at); o != nil {
			p.Offers = []Offer{*o}
		}
	}
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
	case p.Title != "" && ((s.ID == "softmagazin" && strings.Contains(strings.ToLower(doc.Find(".soft-tovar-style #placeforminprice .no-price").Text()), "запрос")) || (s.ID == "softmap" && strings.Contains(strings.ToLower(doc.Find("a[data-popup-target-ajax='ask-price']").Text()), "запрос"))):
		p.State = "no_public_price"
		p.Variants = []Variant{{URL: page, Name: p.Title, Status: "no_public_price"}}
	}
	if s.ID == "softmagazin" && p.Category == "hardware" {
		p.State = "out_of_scope"
		p.Offers = nil
		for _, v := range p.Variants {
			p.ExcludedURLs = append(p.ExcludedURLs, v.URL)
		}
	}
	return p, err
}

// VendorListing reads the product-owned license rows on an official SoftMagazin
// vendor page. Never treat a family's headline "from" price as a license price.
type SoftwareListing struct {
	Rows  []PageResult
	Next  string
	Total int
}

var countLabel = regexp.MustCompile(`^[\s\p{Z}]*([0-9][0-9\s\p{Z}]*)`)

func VendorListing(s Source, page string, html io.Reader, at time.Time) ([]PageResult, string, error) {
	result, err := ReadSoftwareListing(s, page, html, at)
	return result.Rows, result.Next, err
}
func ReadSoftwareListing(s Source, page string, html io.Reader, at time.Time) (SoftwareListing, error) {
	var result SoftwareListing
	doc, err := goquery.NewDocumentFromReader(html)
	if err != nil {
		return result, err
	}
	base, err := url.Parse(page)
	if err != nil {
		return result, err
	}
	// Vendor-listing SKU spans carry the code as visible text; on the family
	// template the same element also has a title attribute.
	listingSource := s
	listingSource.Rule.SKU.Attr = ""
	if strings.HasPrefix(base.Path, "/soft/") {
		match := countLabel.FindStringSubmatch(doc.Find(".productOutput").First().Text())
		if len(match) != 2 {
			return result, fmt.Errorf("software section did not publish product count")
		}
		total, e := strconv.Atoi(strings.Join(strings.Fields(match[1]), ""))
		if e != nil {
			return result, e
		}
		result.Total = total
	}
	var out []PageResult
	doc.Find(".wrapper-items-with-offers .wrapper-info-product").Each(func(_ int, block *goquery.Selection) {
		link := block.Find(".information-product a.title").First()
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		family, e := base.Parse(href)
		if e != nil || !Allowed(s, family.String()) {
			return
		}
		sub := goquery.NewDocumentFromNode(block.Nodes[0])
		record, e := inspectDocument(listingSource, family.String(), sub, at)
		if e != nil {
			return
		}
		record.Title = strings.Join(strings.Fields(link.Text()), " ")
		record.EvidenceURL = page
		if strings.HasPrefix(base.Path, "/soft/") {
			record.Category = "software"
		}
		out = append(out, record)
	})
	raw, _ := doc.Find(".navigation a#navigation_1_next_page").First().Attr("href")
	var next string
	if raw != "" {
		u, e := base.Parse(raw)
		if e != nil {
			return result, e
		}
		if Allowed(s, u.String()) && u.Path == base.Path && len(u.Query()) == 1 {
			n, e := strconv.Atoi(u.Query().Get("PAGEN_1"))
			if e == nil && n > 1 && n < 1000 {
				next = u.String()
			}
		}
	}
	if strings.HasPrefix(base.Path, "/soft/") && len(out) == 0 && doc.Find(".wrapper-items-with-offers").Length() == 0 {
		return result, fmt.Errorf("software section is missing its list container")
	}
	result.Rows = out
	result.Next = next
	return result, nil
}
