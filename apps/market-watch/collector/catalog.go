// Package catalog is the functional core of Market Watch's site adapters.
// No model calls or platform dependencies: configuration, HTML and a capture
// timestamp in; normalized offers out. Fetch is injected by the host.
package catalog

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type Field struct {
	Selector string `json:"selector"`
	Attr     string `json:"attr,omitempty"`
}
type Rule struct {
	Row        string `json:"row"`
	SKU        Field  `json:"sku"`
	Name       Field  `json:"name"`
	Price      Field  `json:"price"`
	OldPrice   Field  `json:"old_price"`
	Link       Field  `json:"link"`
	Quantity   Field  `json:"quantity"`
	PriceBasis string `json:"price_basis"`
}
type Source struct {
	ID             string   `json:"id"`
	Host           string   `json:"host"`
	Sitemap        string   `json:"sitemap"`
	SitemapInclude string   `json:"sitemap_include,omitempty"`
	PageInclude    string   `json:"page_include"`
	Seeds          []string `json:"seeds"`
	Rule           Rule     `json:"rule"`
}
type Offer struct {
	Source        string `json:"source"`
	SKU           string `json:"sku"`
	Name          string `json:"name"`
	PriceMinor    int64  `json:"price_minor"`
	OldPriceMinor *int64 `json:"old_price_minor,omitempty"`
	Currency      string `json:"currency"`
	Quantity      *int64 `json:"quantity,omitempty"`
	PriceBasis    string `json:"price_basis"`
	URL           string `json:"url"`
	CapturedAt    string `json:"captured_at"`
}

func Allowed(s Source, raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host == s.Host && u.User == nil && u.Fragment == ""
}
func ReadSources(r io.Reader) ([]Source, error) {
	var s []Source
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, x := range s {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(x.ID) || seen[x.ID] || !Allowed(x, x.Sitemap) || x.Rule.Row == "" || x.Rule.SKU.Selector == "" || x.Rule.Price.Selector == "" || x.Rule.Name.Selector == "" {
			return nil, fmt.Errorf("invalid source %q", x.ID)
		}
		seen[x.ID] = true
		for _, p := range []string{x.PageInclude, x.SitemapInclude} {
			if _, e := regexp.Compile(p); e != nil {
				return nil, e
			}
		}
		for _, p := range x.Seeds {
			if !Allowed(x, p) {
				return nil, fmt.Errorf("invalid seed %s", p)
			}
		}
	}
	return s, nil
}
func field(row *goquery.Selection, f Field) string {
	if f.Selector == "" {
		return ""
	}
	n := row.Find(f.Selector).First()
	if row.Is(f.Selector) {
		n = row
	}
	if f.Attr != "" {
		v, _ := n.Attr(f.Attr)
		return strings.TrimSpace(v)
	}
	return strings.Join(strings.Fields(n.Text()), " ")
}

var moneyRE = regexp.MustCompile(`^[0-9]+(?:[ \x{00a0}\x{202f}][0-9]{3})*(?:[,.][0-9]{1,2})?`)

func Money(s string) (int64, error) {
	s = strings.TrimSpace(s)
	m := moneyRE.FindString(s)
	if m == "" {
		return 0, fmt.Errorf("invalid price %q", s)
	}
	// Reject truncated numeric fields (e.g. malformed thousands/decimal).
	tail := strings.TrimSpace(s[len(m):])
	if len(tail) > 0 && strings.ContainsRune("0123456789,.", rune(tail[0])) {
		return 0, fmt.Errorf("invalid price %q", s)
	}
	m = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "", ",", ".").Replace(m)
	p := strings.Split(m, ".")
	a, e := strconv.ParseInt(p[0], 10, 64)
	if e != nil || a > 10000000000000 {
		return 0, fmt.Errorf("invalid price %q", s)
	}
	var cents int64
	if len(p) == 2 {
		v := p[1]
		if len(v) == 1 {
			v += "0"
		}
		cents, e = strconv.ParseInt(v, 10, 64)
		if e != nil {
			return 0, e
		}
	}
	if a*100+cents <= 0 {
		return 0, fmt.Errorf("non-positive price")
	}
	return a*100 + cents, nil
}
func Extract(s Source, page string, html io.Reader, at time.Time) ([]Offer, error) {
	if !Allowed(s, page) {
		return nil, fmt.Errorf("page outside source")
	}
	doc, e := goquery.NewDocumentFromReader(html)
	if e != nil {
		return nil, e
	}
	base, _ := url.Parse(page)
	var out []Offer
	var invalid int
	doc.Find(s.Rule.Row).Each(func(_ int, r *goquery.Selection) {
		sku := field(r, s.Rule.SKU)
		name := field(r, s.Rule.Name)
		p := field(r, s.Rule.Price)
		if sku == "" || name == "" || p == "" {
			return
		}
		price, e := Money(p)
		if e != nil {
			invalid++
			return
		}
		raw := field(r, s.Rule.Link)
		link := page
		if raw != "" {
			if u, e := base.Parse(raw); e == nil && Allowed(s, u.String()) {
				link = u.String()
			}
		}
		var old *int64
		if v, e := Money(field(r, s.Rule.OldPrice)); e == nil && v >= price {
			old = &v
		}
		var quantity *int64
		if v, e := strconv.ParseInt(field(r, s.Rule.Quantity), 10, 64); e == nil && v > 0 {
			quantity = &v
		}
		out = append(out, Offer{Quantity: quantity, PriceBasis: s.Rule.PriceBasis, Source: s.ID, SKU: strings.ToUpper(sku), Name: name, PriceMinor: price, OldPriceMinor: old, Currency: "RUB", URL: link, CapturedAt: at.UTC().Format(time.RFC3339)})
	})
	if invalid > 0 {
		return out, fmt.Errorf("%d invalid price rows", invalid)
	}
	return out, nil
}

type Sitemap struct {
	XMLName xml.Name
	Entries []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
	Children []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

func ParseSitemap(s Source, r io.Reader) (pages, children []string, err error) {
	var sm Sitemap
	if err = xml.NewDecoder(r).Decode(&sm); err != nil {
		return
	}
	if sm.XMLName.Local != "urlset" && sm.XMLName.Local != "sitemapindex" {
		err = fmt.Errorf("unexpected sitemap root %s", sm.XMLName.Local)
		return
	}
	p, e := regexp.Compile(s.PageInclude)
	if e != nil {
		err = e
		return
	}
	c, e := regexp.Compile(s.SitemapInclude)
	if e != nil {
		err = e
		return
	}
	for _, x := range sm.Entries {
		if Allowed(s, x.Loc) && p.MatchString(x.Loc) {
			pages = append(pages, x.Loc)
		}
	}
	for _, x := range sm.Children {
		if Allowed(s, x.Loc) && c.MatchString(x.Loc) {
			children = append(children, x.Loc)
		}
	}
	return
}

// Dedupe refuses conflicting prices for the same source and exact manufacturer SKU.
func Dedupe(in []Offer) ([]Offer, error) {
	seen := map[string]Offer{}
	for _, o := range in {
		k := o.Source + "\x00" + o.SKU
		if prev, ok := seen[k]; ok && prev.PriceMinor != o.PriceMinor {
			return nil, fmt.Errorf("conflicting prices for %s/%s", o.Source, o.SKU)
		}
		seen[k] = o
	}
	out := make([]Offer, 0, len(seen))
	for _, o := range seen {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].SKU < out[j].SKU
	})
	return out, nil
}
func decimal(n int64) string { return fmt.Sprintf("%d.%02d", n/100, n%100) }

// Excel-safe: untrusted website text must not turn into formulas.
func safe(s string) string {
	v := strings.TrimLeft(s, " \t\r\n")
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + s
	}
	return s
}
func CSV(w io.Writer, offers []Offer) error {
	c := csv.NewWriter(w)
	c.Comma = ';'
	if e := c.Write([]string{"source", "sku", "name", "price", "old_price", "currency", "quantity", "price_basis", "url", "captured_at"}); e != nil {
		return e
	}
	for _, o := range offers {
		old := ""
		if o.OldPriceMinor != nil {
			old = decimal(*o.OldPriceMinor)
		}
		qty := ""
		if o.Quantity != nil {
			qty = strconv.FormatInt(*o.Quantity, 10)
		}
		if e := c.Write([]string{safe(o.Source), safe(o.SKU), safe(o.Name), decimal(o.PriceMinor), old, o.Currency, qty, o.PriceBasis, safe(o.URL), o.CapturedAt}); e != nil {
			return e
		}
	}
	c.Flush()
	return c.Error()
}
