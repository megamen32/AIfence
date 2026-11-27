package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Cluster by markup shape rather than product names, prices, IDs or a vague
// parser error alone. Each raw response is fingerprinted at most once.
func templateFingerprint(ev Evidence, raw []byte) string {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if e != nil {
		return "invalid-html"
	}
	var features []string
	if canonicalExact(doc, ev.URL) {
		features = append(features, "canonical:exact")
	} else {
		features = append(features, "canonical:other")
	}
	for _, selector := range []string{"#js-front-config", ".products-detail", ".soft-tovar-style", ".hard-tovar-style", ".offer-tovar-style", "[itemprop='price']", "[itemprop='priceCurrency']", "[itemprop='sku']", ".js-price-with-discount", "input.js-product-quantity", ".js-add-to-request", ".no-price", "script[type='application/ld+json']"} {
		if doc.Find(selector).Length() > 0 {
			features = append(features, selector)
		}
	}
	if _, data, ok := strings.Cut(doc.Find("#js-front-config").Text(), "window.temporaryFrontConfig ="); ok {
		var cfg map[string]json.RawMessage
		if json.NewDecoder(strings.NewReader(data)).Decode(&cfg) == nil {
			for _, key := range []string{"product", "models", "familyId"} {
				if len(cfg[key]) > 0 {
					features = append(features, "config:"+key)
				}
			}
		}
	}
	classes := map[string]bool{}
	doc.Find("[class*='price'],[class*='sku'],[class*='articul'],[class*='currency']").Each(func(_ int, n *goquery.Selection) {
		cl, _ := n.Attr("class")
		for _, token := range strings.Fields(cl) {
			if strings.Contains(token, "price") || strings.Contains(token, "sku") || strings.Contains(token, "articul") || strings.Contains(token, "currency") {
				if len(token) <= 64 && strings.IndexAny(token, "0123456789") == -1 {
					classes[token] = true
				}
			}
		}
	})
	var keys []string
	for k := range classes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	features = append(features, keys[:min(len(keys), 12)]...)
	h := sha256.Sum256([]byte(strings.Join(features, "|")))
	return hex.EncodeToString(h[:12])
}

func (j *Journal) indexRepairTemplates() error {
	if _, e := j.db.Exec("CREATE TABLE IF NOT EXISTS repair_templates(evidence_id INTEGER PRIMARY KEY,template TEXT NOT NULL)"); e != nil {
		return e
	}
	rows, e := j.db.Query(`SELECT DISTINCT f.evidence_id FROM failed_tasks f
 JOIN http_responses r ON r.id=f.evidence_id AND r.status=200 AND r.source=f.source AND r.url=f.url
 LEFT JOIN repair_templates t ON t.evidence_id=f.evidence_id
 WHERE f.state IN ('repair','exhausted') AND f.error_type LIKE 'parse_%' AND t.evidence_id IS NULL ORDER BY f.evidence_id LIMIT 256`)
	if e != nil {
		return e
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	for _, id := range ids {
		ev, e := j.Evidence(id)
		if e != nil {
			return e
		}
		raw, e := j.Raw(ev)
		if e != nil {
			return e
		}
		if _, e = j.db.Exec("INSERT OR IGNORE INTO repair_templates(evidence_id,template) VALUES(?,?)", id, templateFingerprint(ev, raw)); e != nil {
			return e
		}
	}
	return nil
}
