package catalog

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

type RepairReport struct {
	Source              string   `json:"source"`
	Archived            int      `json:"archived"`
	Evaluated           int      `json:"evaluated"`
	BaselinePriced      int      `json:"baseline_priced"`
	CandidatePriced     int      `json:"candidate_priced"`
	Improvements        int      `json:"improvements"`
	HeldOutImprovements int      `json:"held_out_improvements"`
	Regressions         int      `json:"regressions"`
	Accepted            bool     `json:"accepted"`
	Reason              string   `json:"reason"`
	ImprovedURLs        []string `json:"improved_urls,omitempty"`
}

func sourceByID(id string) (Source, error) {
	sources, e := Sources()
	if e != nil {
		return Source{}, e
	}
	for _, s := range sources {
		if s.ID == id {
			return s, nil
		}
	}
	return Source{}, fmt.Errorf("unknown source")
}
func (j *Journal) ActiveRules(source string) ([]RuleProposal, string, error) {
	var raw, version string
	err := j.db.QueryRow("SELECT rule_json,version FROM active_rules WHERE source=?", source).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var rules []RuleProposal
	err = json.Unmarshal([]byte(raw), &rules)
	return rules, version, err
}
func validateRule(s Source, r RuleProposal) error {
	if r.NoChange {
		return fmt.Errorf("model proposed no change")
	}
	if r.Source != s.ID || len(r.Reason) > 2000 || r.Price == r.ConfirmPrice {
		return fmt.Errorf("invalid rule scope or independent price evidence")
	}
	fields := []Field{r.Name, r.SKU, r.Price, r.ConfirmPrice, r.Currency}
	for _, f := range append(fields, Field{Selector: r.Container}) {
		if f.Selector == "" || len(f.Selector) > 300 {
			return fmt.Errorf("selector is missing or too long")
		}
		if _, e := cascadia.Compile(f.Selector); e != nil {
			return e
		}
		switch f.Attr {
		case "", "content", "value", "data-price", "data-value", "data-code", "data-sku", "data-currency", "data-price-value":
		default:
			return fmt.Errorf("unsupported attribute %q", f.Attr)
		}
	}
	// Both numeric fields must carry semantic price evidence, not a counter,
	// delivery time, SKU, generic body text or arbitrary matching numbers.
	for _, f := range []Field{r.Price, r.ConfirmPrice} {
		key := strings.ToLower(f.Selector + " " + f.Attr)
		if !strings.Contains(key, "price") && !strings.Contains(key, "cost") && !strings.Contains(key, "цена") {
			return fmt.Errorf("price selector lacks price semantics")
		}
	}
	return nil
}
func oneField(row *goquery.Selection, f Field) (string, *goquery.Selection) {
	n := row.Find(f.Selector)
	if row.Is(f.Selector) {
		n = n.AddSelection(row)
	}
	if n.Length() != 1 {
		return "", nil
	}
	if f.Attr != "" {
		v, _ := n.Attr(f.Attr)
		return strings.TrimSpace(v), n
	}
	return strings.Join(strings.Fields(n.Text()), " "), n
}
func directIdentity(s Source, page string, doc *goquery.Document) bool {
	if !canonicalExact(doc, page) {
		return false
	}
	switch s.ID {
	case "softline":
		var cfg struct {
			Product struct {
				ID      json.RawMessage
				Type    string
				Price   json.RawMessage
				Current json.RawMessage `json:"priceWithDiscount"`
			}
		}
		_, data, ok := strings.Cut(doc.Find("#js-front-config").Text(), "window.temporaryFrontConfig =")
		if !ok || json.NewDecoder(strings.NewReader(data)).Decode(&cfg) != nil {
			return false
		}
		id := strings.Trim(string(cfg.Product.ID), " \"")
		suffix := strings.TrimSuffix(page, "/")
		i := strings.LastIndex(suffix, "-")
		return i >= 0 && id != "" && suffix[i+1:] == id && cfg.Product.Type == "software" && !(string(cfg.Product.Price) == "null" && string(cfg.Product.Current) == "null")
	case "softmagazin":
		return doc.Find(".soft-tovar-style").Length() > 0 && doc.Find(".offer-tovar-style").Length() == 1 && doc.Find(".hard-tovar-style").Length() == 0
	case "softmap":
		return strings.Contains(page, "/element-") && doc.Find(".products-detail").Length() > 0
	}
	return false
}

func proposalOffer(s Source, page string, doc *goquery.Document, at time.Time, r RuleProposal) *Offer {
	if r.Source != s.ID || !directIdentity(s, page, doc) {
		return nil
	}
	row := doc.Find(r.Container)
	if row.Length() != 1 {
		return nil
	}
	name, _ := oneField(row, r.Name)
	sku, _ := oneField(row, r.SKU)
	p, pNode := oneField(row, r.Price)
	confirm, cNode := oneField(row, r.ConfirmPrice)
	cur, _ := oneField(row, r.Currency)
	if name == "" || sku == "" || pNode == nil || cNode == nil || pNode.Get(0) == cNode.Get(0) {
		return nil
	}
	switch strings.ToUpper(cur) {
	case "RUB", "RUR", "₽", "РУБ", "РУБ.":
	default:
		return nil
	}
	a, e := strictVisiblePrice(p)
	if e != nil {
		return nil
	}
	b, e := strictVisiblePrice(confirm)
	if e != nil || a != b {
		return nil
	}
	return &Offer{Source: s.ID, Name: name, SKU: strings.ToUpper(sku), PriceMinor: a, Currency: "RUB", PriceBasis: "displayed_total", URL: page, CapturedAt: at.UTC().Format(time.RFC3339)}
}
func (j *Journal) RepairOffer(s Source, page string, body []byte, at time.Time) (*Offer, error) {
	rules, _, e := j.ActiveRules(s.ID)
	if e != nil || len(rules) == 0 {
		return nil, e
	}
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	return firstRuleOffer(s, page, doc, at, rules), nil
}
func firstRuleOffer(s Source, page string, doc *goquery.Document, at time.Time, rules []RuleProposal) *Offer {
	var first *Offer
	for _, r := range rules {
		o := proposalOffer(s, page, doc, at, r)
		if o == nil {
			continue
		}
		if first == nil {
			first = o
			continue
		}
		if first.PriceMinor != o.PriceMinor || first.SKU != o.SKU {
			return nil
		}
	}
	return first
}
func htmlExcerpt(raw []byte) string {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if e != nil {
		return ""
	}
	var parts []string
	// Visible product title, SKU, status and price before boilerplate.
	doc.Find("h1,.articul-manufacturer,.out-of-production-label,.no-price,[itemprop='price'],[itemprop='priceCurrency'],[itemprop='sku'],.price-field,.js-price-with-discount,input.js-product-quantity").EachWithBreak(func(_ int, n *goquery.Selection) bool {
		if len(parts) >= 25 {
			return false
		}
		if h, e := goquery.OuterHtml(n); e == nil && len(h) <= 700 {
			parts = append(parts, h)
		}
		return true
	})
	// Send product/offer metadata, not irrelevant Organization/WebSite data.
	for _, p := range schemaProducts(doc) {
		fields := map[string]any{}
		for _, k := range []string{"@type", "@id", "url", "mainEntityOfPage", "sku", "name"} {
			if v, ok := p[k]; ok {
				fields[k] = v
			}
		}
		var offers []map[string]any
		for _, o := range objectList(p["offers"]) {
			f := map[string]any{}
			for _, k := range []string{"@type", "url", "description", "price", "priceCurrency", "availability", "lowPrice", "highPrice", "eligibleQuantity", "priceSpecification"} {
				if v, ok := o[k]; ok {
					f[k] = v
				}
			}
			offers = append(offers, f)
			if len(offers) >= 5 {
				break
			}
		}
		fields["offers"] = offers
		if b, e := json.Marshal(fields); e == nil && len(b) <= 2000 {
			parts = append(parts, string(b))
		}
		if len(parts) >= 32 {
			break
		}
	}
	if _, data, ok := strings.Cut(doc.Find("#js-front-config").Text(), "window.temporaryFrontConfig ="); ok {
		var cfg map[string]json.RawMessage
		if json.NewDecoder(strings.NewReader(data)).Decode(&cfg) == nil {
			for _, k := range []string{"product", "familyId", "models"} {
				if v := cfg[k]; len(v) > 0 {
					parts = append(parts, k+": "+string(v[:min(len(v), 1300)]))
				}
			}
		}
	}
	doc.Find("script,style,iframe,noscript,input[type='hidden'],form").Remove()
	doc.Find("[class*='price'],[class*='articul'],[class*='quantity'],[class*='sku']").EachWithBreak(func(_ int, n *goquery.Selection) bool {
		if len(parts) >= 60 {
			return false
		}
		if h, e := goquery.OuterHtml(n); e == nil && len(h) <= 700 {
			parts = append(parts, h)
		}
		return true
	})
	out := strings.Join(parts, "\n")
	if len(out) > 6000 {
		out = out[:6000]
	}
	return strings.ToValidUTF8(out, "")
}

func (j *Journal) NextRepairBatch(now time.Time) (*RepairBatch, error) {
	if e := j.indexRepairTemplates(); e != nil {
		return nil, e
	}
	rows, e := j.db.Query(`SELECT f.source,f.error_type,t.template FROM failed_tasks f JOIN repair_templates t ON t.evidence_id=f.evidence_id
 WHERE f.state IN ('repair','exhausted') AND f.error_type LIKE 'parse_%'
 GROUP BY f.source,f.error_type,t.template HAVING COUNT(DISTINCT f.url)>=? ORDER BY MIN(f.updated_at)`, RepairMinExamples)
	if e != nil {
		return nil, e
	}
	var groups [][3]string
	for rows.Next() {
		var g [3]string
		if e = rows.Scan(&g[0], &g[1], &g[2]); e != nil {
			rows.Close()
			return nil, e
		}
		groups = append(groups, g)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return nil, e
	}
	rows.Close()
	for _, g := range groups {
		// Oldest unresolved group first. Keep at least one independent validation
		// page out of the model prompt when more than ten examples exist.
		ids, e := j.db.Query(`SELECT MAX(f.evidence_id) FROM failed_tasks f JOIN repair_templates t ON t.evidence_id=f.evidence_id
 WHERE f.source=? AND f.error_type=? AND t.template=? AND f.state IN ('repair','exhausted')
 AND NOT EXISTS(SELECT 1 FROM repair_batch_examples x JOIN repair_batches b ON b.id=x.batch_id WHERE x.evidence_id=f.evidence_id AND b.state IN ('accepted','rejected','no_candidate'))
 GROUP BY f.url ORDER BY MIN(f.updated_at) LIMIT 21`, g[0], g[1], g[2])
		if e != nil {
			return nil, e
		}
		var list []int64
		for ids.Next() {
			var id int64
			if e = ids.Scan(&id); e != nil {
				ids.Close()
				return nil, e
			}
			list = append(list, id)
		}
		if e = ids.Err(); e != nil {
			ids.Close()
			return nil, e
		}
		ids.Close()
		if len(list) > RepairMinExamples {
			list = list[:min(RepairMaxExamples, len(list)-1)]
		}
		if len(list) < RepairMinExamples {
			continue
		}
		rules, version, e := j.ActiveRules(g[0])
		if e != nil {
			return nil, e
		}
		h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%v", g[0], g[1], g[2], version, list)))
		id := hex.EncodeToString(h[:])
		var state string
		var created int64
		e = j.db.QueryRow("SELECT state,created_at FROM repair_batches WHERE id=?", id).Scan(&state, &created)
		if e == nil && (state != "waiting_model" || now.Sub(time.Unix(0, created)) < 5*time.Minute) {
			continue
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		src, e := sourceByID(g[0])
		if e != nil {
			return nil, e
		}
		b := &RepairBatch{ID: id, Source: g[0], ErrorType: g[1], CurrentRule: src.Rule, ActiveRules: rules}
		for _, eid := range list {
			ev, e := j.Evidence(eid)
			if e != nil {
				return nil, e
			}
			raw, e := j.Raw(ev)
			if e != nil {
				return nil, e
			}
			b.Examples = append(b.Examples, RepairExample{ev.ID, ev.URL, ev.At, htmlExcerpt(raw)})
		}
		return b, nil
	}
	return nil, nil
}
func (j *Journal) MarkBatch(b RepairBatch, state, report string) error {
	tx, e := j.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO repair_batches(id,source,error_type,state,created_at,report) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,created_at=excluded.created_at,report=excluded.report`, b.ID, b.Source, b.ErrorType, state, time.Now().UnixNano(), report)
	if e != nil {
		return e
	}
	for _, ex := range b.Examples {
		if _, e = tx.Exec("INSERT OR IGNORE INTO repair_batch_examples(batch_id,evidence_id) VALUES(?,?)", b.ID, ex.EvidenceID); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (j *Journal) corpus(source string) ([]Evidence, error) {
	rows, e := j.db.Query("SELECT "+evidenceCols+" FROM http_responses WHERE source=? AND status=200 GROUP BY url,sha256 ORDER BY id", source)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		ev, e := readEvidenceRow(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
func isHTML(body []byte) bool {
	x := strings.ToLower(string(body[:min(len(body), 2048)]))
	return strings.Contains(x, "<html") || strings.Contains(x, "<!doctype html") || strings.Contains(x, "<h1") || strings.Contains(x, "<div")
}
func eligibleRepair(p PageResult) bool {
	return len(p.Offers) == 0 && (p.State == "unclassified" || p.State == "error") && p.Category != "hardware"
}
func (j *Journal) ValidateRepair(b RepairBatch, r RuleProposal) (RepairReport, error) {
	report := RepairReport{Source: b.Source}
	s, e := sourceByID(b.Source)
	if e != nil {
		return report, e
	}
	if e = validateRule(s, r); e != nil {
		report.Reason = e.Error()
		return report, nil
	}
	old, _, e := j.ActiveRules(s.ID)
	if e != nil {
		return report, e
	}
	candidate := append(append([]RuleProposal{}, old...), r)
	rows, e := j.corpus(s.ID)
	if e != nil {
		return report, e
	}
	report.Archived = len(rows)
	shown := map[string]bool{}
	for _, ex := range b.Examples {
		shown[ex.URL] = true
	}
	improvements := map[string]bool{}
	held := map[string]bool{}
	for _, ev := range rows {
		raw, e := j.Raw(ev)
		if e != nil {
			return report, e
		}
		if !isHTML(raw) {
			continue
		}
		doc, e := goquery.NewDocumentFromReader(bytes.NewReader(raw))
		if e != nil {
			return report, e
		}
		report.Evaluated++
		p, pe := inspectDocument(s, ev.URL, doc, ev.At)
		oldCount := len(p.Offers)
		newCount := oldCount
		// A proposed selector that disagrees with a known exact quote is
		// suspicious even though the additive fallback would not overwrite it.
		proposed := proposalOffer(s, ev.URL, doc, ev.At, r)
		if proposed != nil {
			for _, o := range p.Offers {
				if o.URL == ev.URL && (o.SKU != proposed.SKU || o.PriceMinor != proposed.PriceMinor) {
					report.Regressions++
					break
				}
			}
		}
		if pe == nil && eligibleRepair(p) {
			before := firstRuleOffer(s, ev.URL, doc, ev.At, old)
			after := firstRuleOffer(s, ev.URL, doc, ev.At, candidate)
			if before != nil {
				oldCount++
				if after == nil || before.SKU != after.SKU || before.PriceMinor != after.PriceMinor {
					report.Regressions++
				}
			}
			if after != nil {
				newCount++
			}
			if before == nil && after != nil {
				improvements[ev.URL] = true
				if !shown[ev.URL] {
					held[ev.URL] = true
				}
			}
		}
		report.BaselinePriced += oldCount
		report.CandidatePriced += newCount
	}
	report.Improvements = len(improvements)
	report.HeldOutImprovements = len(held)
	for u := range improvements {
		report.ImprovedURLs = append(report.ImprovedURLs, u)
	}
	report.Accepted = report.Regressions == 0 && report.CandidatePriced > report.BaselinePriced && report.Improvements > 0 && report.HeldOutImprovements > 0
	if report.Accepted {
		report.Reason = "full archived corpus improved; held-out improvement; no price regression"
	} else {
		report.Reason = "no independent improvement or price regression; keep current rules"
	}
	return report, nil
}
func (j *Journal) ActivateRepair(b RepairBatch, r RuleProposal, report RepairReport) error {
	if !report.Accepted {
		return fmt.Errorf("rule not accepted")
	}
	rules, _, e := j.ActiveRules(b.Source)
	if e != nil {
		return e
	}
	rules = append(rules, r)
	raw, e := json.Marshal(rules)
	if e != nil {
		return e
	}
	h := sha256.Sum256(raw)
	version := hex.EncodeToString(h[:])
	receipt, e := json.Marshal(report)
	if e != nil {
		return e
	}
	tx, e := j.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UnixNano()
	if _, e = tx.Exec(`INSERT INTO active_rules(source,rule_json,version,activated_at) VALUES(?,?,?,?) ON CONFLICT(source) DO UPDATE SET rule_json=excluded.rule_json,version=excluded.version,activated_at=excluded.activated_at`, b.Source, raw, version, now); e != nil {
		return e
	}
	for _, u := range report.ImprovedURLs {
		if _, e = tx.Exec("UPDATE failed_tasks SET state='retry',attempts=0,next_attempt=?,updated_at=? WHERE source=? AND url=? AND evidence_id>0 AND error_type LIKE 'parse_%'", now, now, b.Source, u); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE crawl_tasks SET state='retry',updated_at=? WHERE source=? AND url=? AND EXISTS(SELECT 1 FROM failed_tasks f WHERE f.source=crawl_tasks.source AND f.url=crawl_tasks.url AND f.kind=crawl_tasks.kind AND f.generation=crawl_tasks.generation AND f.state='retry' AND f.attempts=0)", now, b.Source, u); e != nil {
			return e
		}

	}
	if _, e = tx.Exec(`INSERT INTO repair_batches(id,source,error_type,state,created_at,rule_version,report) VALUES(?,?,?,'accepted',?,?,?) ON CONFLICT(id) DO UPDATE SET state='accepted',rule_version=excluded.rule_version,report=excluded.report`, b.ID, b.Source, b.ErrorType, now, version, receipt); e != nil {
		return e
	}
	for _, ex := range b.Examples {
		if _, e = tx.Exec("INSERT OR IGNORE INTO repair_batch_examples(batch_id,evidence_id) VALUES(?,?)", b.ID, ex.EvidenceID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
