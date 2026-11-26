package catalog

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// These are vetted, seed-only profiles. No site-wide inventory or sitemap
// coverage is asserted. Keep the existing external full-catalog workers separate.
func CompetitorProfiles() []Source {
	return []Source{
		{ID: "allsoft", Host: "allsoft.ru", Adapter: "allsoft_family", Seeds: []string{"https://allsoft.ru/software/vendors/kaspersky-lab/-kaspersky-small-office-security-/"}},
		{ID: "syssoft", Host: "www.syssoft.ru", Adapter: "syssoft_family", Seeds: []string{"https://www.syssoft.ru/kaspersky/kaspersky-small-office-security/"}},
	}
}

var tierRE = regexp.MustCompile(`(?i)от\s+([0-9]+)\s+(?:до\s+([0-9]+)|и\s+более)`)

func positiveInt(v string) *int64 {
	n, e := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if e != nil || n <= 0 {
		return nil
	}
	return &n
}
func seedLink(s Source, page, href string) (string, bool) {
	base, e := url.Parse(page)
	if e != nil {
		return "", false
	}
	u, e := base.Parse(href)
	if e != nil || href == "" || !Allowed(s, u.String()) {
		return "", false
	}
	return u.String(), true
}
func textOf(r *goquery.Selection, sel string) string {
	return strings.Join(strings.Fields(r.Find(sel).First().Text()), " ")
}
func seedOffer(s Source, page, sku, name, price, href, basis string, min, max *int64, at time.Time) (Offer, bool) {
	p, e := Money(price)
	link, ok := seedLink(s, page, href)
	if e != nil || !ok || sku == "" || name == "" {
		return Offer{}, false
	}
	return Offer{Source: s.ID, SKU: strings.ToUpper(strings.TrimSpace(sku)), Name: name, PriceMinor: p, Currency: "RUB", URL: link, PriceBasis: basis, Quantity: min, MaxQuantity: PublicMaxQuantity(min, max), CapturedAt: at.UTC().Format(time.RFC3339)}, true
}
func allsoftFamily(s Source, page string, doc *goquery.Document, at time.Time) ([]Offer, error) {
	out := []Offer{}
	missing := 0
	family := textOf(doc.Selection, "h1")
	doc.Find("table.js-price_group").Each(func(_ int, t *goquery.Selection) {
		name := textOf(t, ".b-text_product-version")
		if family != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(family)) {
			name = family + " — " + name
		}
		t.Find("tr").Each(func(_ int, r *goquery.Selection) {
			sku := textOf(r, ".b-col_description-sku a")
			if sku == "" {
				return
			}
			label := strings.ToLower(textOf(r, ".b-col_description-text"))
			price := textOf(r, "[data-offer-price]")
			href, _ := r.Find(".b-col_description-sku a").First().Attr("href")
			// A selected, visibly published tier is not every hidden tier.
			// Do not infer the upper tier from the quantity input's global max.
			m := tierRE.FindStringSubmatch(label)
			if len(m) != 3 || !strings.Contains(label, "цена за копию") {
				missing++
				return
			}
			min, max := positiveInt(m[1]), positiveInt(m[2])
			if min == nil || max != nil && *max < *min {
				missing++
				return
			}
			o, ok := seedOffer(s, page, sku, name, price, href, "per_unit", min, max, at)
			if !ok {
				missing++
				return
			}
			out = append(out, o)
		})
	})
	if missing > 0 {
		return out, fmt.Errorf("%d rows without a verified public price or tier", missing)
	}
	return out, nil
}
func syssoftFamily(s Source, page string, doc *goquery.Document, at time.Time) ([]Offer, error) {
	out := []Offer{}
	missing := 0
	doc.Find(".product-edition").Each(func(_ int, r *goquery.Selection) {
		sku := ""
		r.Find(".product-edition_specs li").Each(func(_ int, li *goquery.Selection) {
			if strings.Contains(textOf(li, "span"), "Артикул:") {
				sku = textOf(li, "b")
			}
		})
		name := textOf(r, "h3.title a")
		href, _ := r.Find("h3.title a").First().Attr("href")
		price := textOf(r, ".price_text_rub")
		label := strings.ToLower(strings.Join(strings.Fields(r.Find(".price_text_rub").First().Parent().Text()), " "))
		minRaw, _ := r.Find("input[name=item_value_min]").First().Attr("value")
		min := positiveInt(minRaw)
		if sku == "" && name == "" {
			return
		}
		basis := "displayed_total"
		if strings.Contains(label, "цена/шт.") {
			basis = "per_unit"
		} else if !strings.Contains(label, "цена:") {
			missing++
			return
		}
		// The displayed total applies to the actual selected quantity, not an
		// assumed minimum. Unit prices carry the explicit minimum and no guessed cap.
		if basis == "displayed_total" {
			raw, _ := r.Find("input[name=item_value]").First().Attr("value")
			min = positiveInt(raw)
		}
		if min == nil {
			missing++
			return
		}
		o, ok := seedOffer(s, page, sku, name, price, href, basis, min, nil, at)
		if !ok {
			missing++
			return
		}
		out = append(out, o)
	})
	if missing > 0 {
		return out, fmt.Errorf("%d rows without a verified public price or quantity", missing)
	}
	return out, nil
}
