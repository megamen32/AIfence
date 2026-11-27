package catalog

import (
	"strings"
	"testing"
	"time"
)

func TestSoftMagazinExactDisposition(t *testing.T) {
	s, _ := sourceByID("softmagazin")
	u := "https://www.softmagazin.ru/1c/example-license/"
	body := `<!doctype html><html><head><link rel="canonical" href="https://www.softmagazin.ru/1c/example-license/">
 <script type="application/ld+json">{"@graph":[{"@type":"Product","@id":"https://www.softmagazin.ru/1c/example-license/#product","mainEntityOfPage":{"@id":"https://www.softmagazin.ru/1c/example-license/"},"sku":"1C-EX","name":"1C license","offers":{"@type":"Offer","description":"Цена по запросу. Уточняйте стоимость у менеджера.","availability":"https://schema.org/InStock","priceCurrency":"RUB"}}]}</script></head>
 <body><div class="info-category"><div class="base-links-tovar-item"><a href="/soft/1c/">Software</a></div></div><div class="offer-tovar-style"><h1>1C license</h1><div class="articul-manufacturer"><h2>Артикул: <span>1C-EX</span></h2></div><span class="out-of-production-label">Снято с производства</span></div></body></html>`
	cases := []struct{ name, html, want string }{
		{"discontinued", body, "unavailable"},
		{"request only", strings.Replace(body, `<span class="out-of-production-label">Снято с производства</span>`, "", 1), "no_public_price"},
		{"other product", strings.ReplaceAll(body, "example-license/#product", "different/#product"), "unclassified"},
		{"other sku", strings.Replace(body, `"sku":"1C-EX"`, `"sku":"OTHER"`, 1), "unclassified"},
		{"explicit price conflict", strings.Replace(body, `"priceCurrency":"RUB"`, `"priceCurrency":"RUB","price":"1000"`, 1), "unclassified"},
		{"no proven status", strings.Replace(strings.Replace(body, "Цена по запросу. Уточняйте стоимость у менеджера.", "", 1), "Снято с производства", "", 1), "unclassified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, e := InspectPage(s, u, strings.NewReader(c.html), time.Now())
			if e != nil {
				t.Fatal(e)
			}
			if p.State != c.want || len(p.Offers) != 0 {
				t.Fatalf("state=%s offers=%v", p.State, p.Offers)
			}
			if c.want != "unclassified" && (len(p.Variants) != 1 || p.Variants[0].URL != u || p.Variants[0].SKU != "1C-EX" || p.StatusEvidence == "") {
				t.Fatal(p)
			}
		})
	}
}
