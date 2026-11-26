// Package softlinefamily extracts the public license model that Softline renders on family pages.
package softlinefamily

import (
	"agent/collector"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const Host = "store.softline.ru"

type Product struct {
	URL   string         `json:"url"`
	Name  string         `json:"name"`
	SKU   string         `json:"sku,omitempty"`
	State string         `json:"state"`
	Offer *catalog.Offer `json:"offer,omitempty"`
}

func objects[T any](raw json.RawMessage) ([]T, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '[' {
		var a []T
		err := json.Unmarshal(raw, &a)
		return a, err
	}
	var indexed map[string]T
	if err := json.Unmarshal(raw, &indexed); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(indexed))
	for key := range indexed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var a []T
	for _, key := range keys {
		a = append(a, indexed[key])
	}
	return a, nil
}

type model struct {
	Name     string
	Licenses json.RawMessage
}
type license struct {
	Name    string
	SKU     string
	Link    string
	Minimum json.RawMessage `json:"min_quantity"`
	Maximum json.RawMessage `json:"max_quantity"`
	Price   json.RawMessage
}

func number(raw json.RawMessage) string { return strings.Trim(string(raw), "\" ") }
func positive(raw json.RawMessage) *int64 {
	n, err := strconv.ParseInt(number(raw), 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

func Extract(page string, html []byte, at time.Time) ([]Product, error) {
	base, err := url.Parse(page)
	source := catalog.Source{Host: Host}
	if err != nil || !catalog.Allowed(source, page) {
		return nil, fmt.Errorf("unexpected family host")
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	var config struct {
		Models   json.RawMessage
		FamilyID json.RawMessage
	}
	_, data, ok := strings.Cut(doc.Find("#js-front-config").Text(), "window.temporaryFrontConfig =")
	if !ok {
		return nil, fmt.Errorf("family data missing")
	}
	if err := json.NewDecoder(strings.NewReader(data)).Decode(&config); err != nil {
		return nil, err
	}
	if positive(config.FamilyID) == nil {
		return nil, fmt.Errorf("unrecognized family page")
	}
	models, err := objects[model](config.Models)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	doc.Find("script[type='application/ld+json']").Each(func(_ int, s *goquery.Selection) {
		var product struct {
			Type   string `json:"@type"`
			Name   string
			Offers struct{ URL string }
		}
		if json.Unmarshal([]byte(s.Text()), &product) == nil && product.Type == "Product" {
			names[product.Offers.URL] = product.Name
		}
	})
	title := strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	var products []Product
	for _, m := range models {
		items, err := objects[license](m.Licenses)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			u, err := base.Parse(item.Link)
			if err != nil || item.Link == "" || !catalog.Allowed(source, u.String()) {
				continue
			}
			name := strings.TrimSpace(names[u.String()])
			if name == "" {
				parts := []string{}
				for _, v := range []string{title, m.Name, item.Name} {
					v = strings.TrimSpace(v)
					if v != "" && (len(parts) == 0 || parts[len(parts)-1] != v) {
						parts = append(parts, v)
					}
				}
				name = strings.Join(parts, ", ")
			}
			p := Product{URL: u.String(), SKU: strings.ToUpper(strings.TrimSpace(item.SKU)), Name: name, State: "no_public_price"}
			var price struct {
				Current json.RawMessage `json:"with_discount"`
				List    json.RawMessage `json:"without_discount"`
			}
			if raw := bytes.TrimSpace(item.Price); len(raw) > 0 && raw[0] == '{' {
				if err := json.Unmarshal(raw, &price); err != nil {
					return nil, err
				}
			}
			v := number(price.Current)
			if v != "" && v != "null" {
				minor, err := catalog.Money(v)
				switch {
				case err == nil:
					p.State = "priced"
					o := catalog.Offer{Source: "softline", SKU: p.SKU, Name: name, URL: p.URL, Currency: "RUB", PriceMinor: minor, Quantity: positive(item.Minimum), PriceBasis: "per_unit", CapturedAt: at.UTC().Format(time.RFC3339), MaxQuantity: catalog.PublicMaxQuantity(positive(item.Minimum), positive(item.Maximum))}
					if old, err := catalog.Money(number(price.List)); err == nil && old > minor {
						o.OldPriceMinor = &old
					}
					p.Offer = &o
				default:
					if f, err := strconv.ParseFloat(v, 64); err != nil || f != 0 {
						p.State = "unrecognized_price"
					}
				}
			}
			products = append(products, p)
		}
	}
	return products, nil
}
