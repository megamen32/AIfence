package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

// The model describes site-specific behavior, not executable code. A profile
// may contain several independent templates and public discovery rules.
type AdapterSpec struct {
	Cases     []AdapterCase   `json:"cases"`
	Discovery []DiscoveryRule `json:"discovery"`
}
type AdapterCase struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // structured, product, family
	Match        string `json:"match"`
	Rows         string `json:"rows,omitempty"`
	Name         Field  `json:"name"`
	SKU          Field  `json:"sku"`
	Link         Field  `json:"link"`
	Price        Field  `json:"price"`
	ConfirmPrice Field  `json:"confirm_price"`
	PriceScope   string `json:"price_scope,omitempty"`
	Currency     Field  `json:"currency"`
	Quantity     Field  `json:"quantity"`
	MaxQuantity  Field  `json:"max_quantity"`
	UnitEvidence Field  `json:"unit_evidence"`
	Status       Field  `json:"status"`
	Basis        string `json:"basis,omitempty"`
}
type DiscoveryRule struct {
	Selector    string `json:"selector"`
	PathPattern string `json:"path_pattern"` // URL path only
}
type AdapterSample struct {
	URL        string    `json:"url"`
	CapturedAt time.Time `json:"captured_at"`
	Body       []byte    `json:"-"`
	Holdout    bool      `json:"holdout"`
}
type AdapterTrainingPage struct {
	URL  string `json:"url"`
	HTML string `json:"html"`
}
type AdapterTraining struct {
	Origin   string                `json:"origin"`
	Pages    []AdapterTrainingPage `json:"pages"`
	Current  *AdapterSpec          `json:"current,omitempty"`
	Feedback []string              `json:"feedback,omitempty"`
}
type AdapterDecision struct {
	Accepted            bool     `json:"accepted"`
	TrainingPrices      int      `json:"training_prices"`
	HoldoutPrices       int      `json:"holdout_prices"`
	HoldoutImprovements int      `json:"holdout_improvements"`
	Resolved            int      `json:"resolved"`
	BaselineResolved    int      `json:"baseline_resolved"`
	Regressions         int      `json:"regressions"`
	Feedback            []string `json:"feedback"`
}
type AdapterResult struct {
	Offers   []Offer
	Variants []Variant
	Unknown  int
	Matched  bool
}

func AdapterVersion(spec AdapterSpec) string {
	raw, _ := json.Marshal(spec)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func validateSelector(s string) error {
	if len(s) > 300 || s == "" {
		return fmt.Errorf("missing or oversized selector")
	}
	_, e := cascadia.Compile(s)
	return e
}
func validateField(f Field, required bool) error {
	if f.Selector == "" {
		if required {
			return fmt.Errorf("required field missing")
		}
		if f.Attr != "" {
			return fmt.Errorf("attribute without selector")
		}
		return nil
	}
	if e := validateSelector(f.Selector); e != nil {
		return e
	}
	switch f.Attr {
	case "", "content", "href", "value", "title", "aria-label":
		return nil
	}
	if strings.HasPrefix(f.Attr, "data-") && len(f.Attr) < 100 && regexp.MustCompile(`^data-[a-zA-Z0-9_-]+$`).MatchString(f.Attr) {
		return nil
	}
	return fmt.Errorf("unsupported attribute")
}
func ValidateAdapter(spec AdapterSpec) error {
	if len(spec.Cases) < 1 || len(spec.Cases) > 16 || len(spec.Discovery) > 8 {
		return fmt.Errorf("need 1-16 cases and at most 8 discovery rules")
	}
	ids := map[string]bool{}
	for _, c := range spec.Cases {
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,60}$`).MatchString(c.ID) || ids[c.ID] {
			return fmt.Errorf("invalid or duplicate case ID")
		}
		ids[c.ID] = true
		if e := validateSelector(c.Match); e != nil {
			return fmt.Errorf("%s match: %w", c.ID, e)
		}
		switch c.Kind {
		case "structured":
			continue
		case "product", "family":
		default:
			return fmt.Errorf("%s unknown case kind", c.ID)
		}
		if e := validateSelector(c.Rows); e != nil {
			return fmt.Errorf("%s rows: %w", c.ID, e)
		}
		for _, f := range []Field{c.Name, c.SKU, c.Price, c.Currency} {
			if e := validateField(f, true); e != nil {
				return fmt.Errorf("%s field: %w", c.ID, e)
			}
		}
		for _, f := range []Field{c.ConfirmPrice, c.Quantity, c.MaxQuantity, c.UnitEvidence, c.Status} {
			if e := validateField(f, false); e != nil {
				return e
			}
		}
		if e := validateField(c.Link, c.Kind == "family"); e != nil {
			return e
		}
		if c.Link.Selector != "" && c.Link.Attr != "href" {
			return fmt.Errorf("link must reference href")
		}
		switch c.Basis {
		case "displayed_total":
		case "per_unit":
			if c.Quantity.Selector == "" || c.UnitEvidence.Selector == "" {
				return fmt.Errorf("unit price requires quantity and unit evidence")
			}
		default:
			return fmt.Errorf("unknown price basis")
		}
		if c.ConfirmPrice.Selector != "" && c.ConfirmPrice == c.Price {
			return fmt.Errorf("confirmation must be independent")
		}
		if c.PriceScope != "" {
			if c.Kind != "product" || c.Basis != "displayed_total" || c.Price.Attr != "" {
				return fmt.Errorf("visible price scope requires standalone displayed-total text")
			}
			if e := validateSelector(c.PriceScope); e != nil {
				return e
			}
		}

	}
	for _, r := range spec.Discovery {
		if e := validateSelector(r.Selector); e != nil {
			return e
		}
		if len(r.PathPattern) < 1 || len(r.PathPattern) > 300 {
			return fmt.Errorf("invalid path pattern")
		}
		if _, e := regexp.Compile(r.PathPattern); e != nil {
			return e
		}
	}
	return nil
}
func fieldOne(row *goquery.Selection, f Field) (string, *goquery.Selection) {
	if f.Selector == "" {
		return "", nil
	}
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
func priceNode(n *goquery.Selection) bool {
	if n == nil {
		return false
	}
	class, _ := n.Attr("class")
	id, _ := n.Attr("id")
	prop, _ := n.Attr("itemprop")
	sig := strings.ToLower(class + " " + id + " " + prop)
	for _, bad := range []string{"old-price", "old_price", "price-old", "price_old", "crossed", "installment", "monthly-payment", "shipping", "delivery", "discount-percent"} {
		if strings.Contains(sig, bad) {
			return false
		}
	}
	if prop == "price" {
		return true
	}
	for _, a := range []string{"data-price", "data-offer-price", "data-price-value"} {
		if _, ok := n.Attr(a); ok {
			return true
		}
	}
	return strings.Contains(sig, "price") || strings.Contains(sig, "cost")
}
func strictVisiblePrice(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	for _, suffix := range []string{"₽", "руб.", "руб", "RUB"} {
		if strings.HasSuffix(raw, suffix) {
			raw = strings.TrimSpace(strings.TrimSuffix(raw, suffix))
			break
		}
	}
	return strictPrice(raw)
}
func moneyRUB(raw string) bool {
	v := strings.ToUpper(strings.TrimSpace(raw))
	return v == "RUB" || v == "RUR" || v == "₽" || v == "РУБ" || v == "РУБ." || v == "РУБЛЕЙ"
}
func explicitUnit(s string) bool {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	for _, v := range []string{"за штуку", "за копию", "за лицензию", "цена/шт.", "за единицу", "per unit", "per license"} {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}
func explicitDisposition(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	switch {
	case s == "снято с производства" || s == "нет в наличии" || s == "товар недоступен" || s == "discontinued" || s == "out of stock":
		return "unavailable"
	case explicitRequest(s) || s == "по запросу" || s == "request a quote":
		return "no_public_price"
	}
	return ""
}
func sameQuote(a, b Offer) bool {
	ptr := func(x *int64) int64 {
		if x == nil {
			return -1
		}
		return *x
	}
	return a.Source == b.Source && a.URL == b.URL && a.SKU == b.SKU && a.PriceMinor == b.PriceMinor && a.Currency == b.Currency && a.PriceBasis == b.PriceBasis && ptr(a.Quantity) == ptr(b.Quantity) && ptr(a.MaxQuantity) == ptr(b.MaxQuantity) && ptr(a.OldPriceMinor) == ptr(b.OldPriceMinor)
}
func adaptiveRow(s Source, page string, doc *goquery.Document, row *goquery.Selection, c AdapterCase, at time.Time) (*Offer, Variant, bool) {
	name, nameNode := fieldOne(row, c.Name)
	sku, skuNode := fieldOne(row, c.SKU)
	sku = strings.ToUpper(strings.TrimSpace(sku))
	if name == "" || sku == "" || len(name) > 10000 || len(sku) > 300 {
		return nil, Variant{}, false
	}
	target := page
	if c.Link.Selector != "" {
		raw, _ := fieldOne(row, c.Link)
		u, ok := seedLink(s, page, raw)
		if !ok {
			return nil, Variant{}, false
		}
		target = u
	}
	if c.Kind == "product" {
		// A canonical redirect to a different item cannot supply this quote.
		if target != page || !canonicalExact(doc, page) {
			return nil, Variant{}, false
		}
	} else if target == page {
		return nil, Variant{}, false
	}
	v := Variant{URL: target, Name: name, SKU: sku, Status: "unclassified"}
	raw, node := fieldOne(row, c.Price)
	status, _ := fieldOne(row, c.Status)
	if raw == "" {
		if state := explicitDisposition(status); state != "" {
			v.Status = state
			return nil, v, true
		}
		return nil, v, false
	}
	if explicitDisposition(status) != "" {
		return nil, v, false
	}
	if node == nil {
		return nil, v, false
	}
	if !priceNode(node) && c.PriceScope == "" {
		return nil, v, false
	}
	price, e := strictVisiblePrice(raw)
	if e != nil {
		return nil, v, false
	}
	currency, _ := fieldOne(row, c.Currency)
	if !moneyRUB(currency) && !(currency == raw && func() bool { _, ok := rubleAmount(raw); return ok }()) {
		return nil, v, false
	}
	var qty, maxQty *int64
	if c.Quantity.Selector != "" {
		a, _ := fieldOne(row, c.Quantity)
		qty = positiveInt(a)
		if qty == nil {
			return nil, v, false
		}
	}
	if c.MaxQuantity.Selector != "" {
		a, _ := fieldOne(row, c.MaxQuantity)
		maxQty = positiveInt(a)
		if maxQty == nil || qty == nil || *maxQty < *qty {
			return nil, v, false
		}
	}
	if c.Basis == "per_unit" {
		a, _ := fieldOne(row, c.UnitEvidence)
		if qty == nil || !explicitUnit(a) {
			return nil, v, false
		}
	}
	// Corroborate with an independent published price node or exact JSON-LD
	// product/offer evidence. A single itemprop price is independently typed.
	prop, _ := node.Attr("itemprop")
	corroborated := prop == "price"
	if c.ConfirmPrice.Selector != "" {
		raw2, n2 := fieldOne(row, c.ConfirmPrice)
		p2, err := strictVisiblePrice(raw2)
		corroborated = n2 != nil && n2.Get(0) != node.Get(0) && priceNode(n2) && err == nil && p2 == price
	} else if c.Kind == "product" {
		if o := StructuredOffer(s, page, doc, at); o != nil && o.SKU == sku && o.PriceMinor == price {
			corroborated = true
		}
	}
	if c.PriceScope != "" {
		_, scope := fieldOne(row, Field{Selector: c.PriceScope})
		// If a scope was declared, its proof is mandatory, even if a different
		// extraction method happens to return a coincidentally matching value.
		corroborated = uniqueVisibleCurrentPrice(row, scope, node, nameNode, skuNode, doc, price)
	}
	if !corroborated {
		return nil, v, false
	}
	v.Status = "priced"
	return &Offer{Source: s.ID, SKU: sku, Name: name, PriceMinor: price, Currency: "RUB", URL: target, PriceBasis: c.Basis, Quantity: qty, MaxQuantity: maxQty, CapturedAt: at.UTC().Format(time.RFC3339)}, v, true
}

func ParseAdapter(s Source, spec AdapterSpec, page string, body []byte, at time.Time) (AdapterResult, error) {
	if !Allowed(s, page) {
		return AdapterResult{}, fmt.Errorf("page outside source")
	}
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if e != nil {
		return AdapterResult{}, e
	}
	var result AdapterResult
	seen := map[string]Variant{}
	offers := map[string]Offer{}
	for _, c := range spec.Cases {
		if doc.Find(c.Match).Length() == 0 {
			continue
		}
		result.Matched = true
		if c.Kind == "structured" {
			if o := StructuredOffer(s, page, doc, at); o != nil {
				v := Variant{URL: o.URL, SKU: o.SKU, Name: o.Name, Status: "priced"}
				key := v.URL + "\x00" + v.SKU
				if old, ok := offers[key]; ok && !sameQuote(old, *o) {
					return result, fmt.Errorf("conflicting case results for %s", v.URL)
				}
				seen[key] = v
				offers[key] = *o
			}
			continue
		}
		rows := doc.Find(c.Rows)
		if c.Kind == "product" && rows.Length() != 1 {
			result.Unknown++
			continue
		}
		if rows.Length() > 2000 {
			return result, fmt.Errorf("template row limit exceeded")
		}
		var conflict error
		rows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
			o, v, ok := adaptiveRow(s, page, doc, row, c, at)
			if !ok {
				result.Unknown++
				return true
			}
			key := v.URL + "\x00" + v.SKU
			if o != nil && o.Quantity != nil {
				key += fmt.Sprintf("\x00%d", *o.Quantity)
			}
			if old, exists := seen[key]; exists && old.Status != v.Status {
				conflict = fmt.Errorf("conflicting statuses for %s", v.URL)
				return false
			}
			if old, exists := offers[key]; exists && o != nil && !sameQuote(old, *o) {
				conflict = fmt.Errorf("conflicting prices for %s", v.URL)
				return false
			}
			seen[key] = v
			if o != nil {
				offers[key] = *o
			}
			return true
		})
		if conflict != nil {
			return result, conflict
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		result.Variants = append(result.Variants, seen[k])
		if o, ok := offers[k]; ok {
			result.Offers = append(result.Offers, o)
		}
	}
	return result, nil
}
func AdapterLinks(s Source, spec AdapterSpec, page string, body []byte) []string {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if e != nil {
		return nil
	}
	seen := map[string]bool{}
	permitted := map[string]bool{}
	for _, u := range PublishedLinks(s, page, body) {
		permitted[u] = true
	}
	var out []string
	for _, r := range spec.Discovery {
		re, e := regexp.Compile(r.PathPattern)
		if e != nil {
			continue
		}
		doc.Find(r.Selector).EachWithBreak(func(_ int, n *goquery.Selection) bool {
			raw, _ := n.Attr("href")
			u, ok := seedLink(s, page, raw)
			if ok && u != page && !seen[u] && permitted[u] {
				p, e := url.Parse(u)
				if e == nil && re.MatchString(p.Path) && PublicStoreURL(u) == nil {
					seen[u] = true
					out = append(out, u)
				}
			}
			return len(out) < 2000
		})
	}
	return out
}

// Deterministic validation. Holdout bodies never enter AdapterTraining.
// Regressions compare full price identity and terms, not just a row count.
func EvaluateAdapter(s Source, spec AdapterSpec, previous *AdapterSpec, samples []AdapterSample) AdapterDecision {
	d, _ := EvaluateAdapterStream(s, spec, previous, func(yield func(AdapterSample) error) error {
		for _, sample := range samples {
			if e := yield(sample); e != nil {
				return e
			}
		}
		return nil
	})
	return d
}
func EvaluateAdapterStream(s Source, spec AdapterSpec, previous *AdapterSpec, visit func(func(AdapterSample) error) error) (AdapterDecision, error) {
	d := AdapterDecision{}
	if e := ValidateAdapter(spec); e != nil {
		d.Feedback = []string{e.Error()}
		return d, nil
	}
	train, hold := map[string]bool{}, map[string]bool{}
	err := visit(func(sample AdapterSample) error {
		next, e := ParseAdapter(s, spec, sample.URL, sample.Body, sample.CapturedAt)
		if e != nil {
			d.Regressions++
			d.Feedback = append(d.Feedback, sample.URL+": "+e.Error())
			return nil
		}
		if len(next.Offers) > 0 {
			if sample.Holdout {
				hold[sample.URL] = true
			} else {
				train[sample.URL] = true
			}
		}
		d.Resolved += len(next.Variants)
		var old AdapterResult
		if previous != nil {
			old, _ = ParseAdapter(s, *previous, sample.URL, sample.Body, sample.CapturedAt)
		}
		// The standard structured-data extractor is an independent oracle where
		// its exact identity is available, even for the initial candidate.
		if doc, err := goquery.NewDocumentFromReader(bytes.NewReader(sample.Body)); err == nil {
			if expected := StructuredOffer(s, sample.URL, doc, sample.CapturedAt); expected != nil {
				found := false
				for _, o := range next.Offers {
					if o.URL == expected.URL && o.SKU == expected.SKU {
						if o.PriceMinor != expected.PriceMinor {
							d.Regressions++
							d.Feedback = append(d.Feedback, sample.URL+": disagrees with published structured quote")
						}
						found = true
					}
				}
				if !found {
					d.Feedback = append(d.Feedback, sample.URL+": exact structured quote not extracted")
					d.Regressions++
				}
			}
		}
		d.BaselineResolved += len(old.Variants)
		if sample.Holdout && len(next.Variants) > len(old.Variants) {
			d.HoldoutImprovements++
		}
		for _, o := range old.Offers {
			found := false
			for _, n := range next.Offers {
				if sameQuote(o, n) {
					found = true
					break
				}
			}
			if !found {
				d.Regressions++
				d.Feedback = append(d.Feedback, sample.URL+": prior verified offer or terms changed")
			}
		}
		for _, v := range old.Variants {
			found := false
			for _, n := range next.Variants {
				if v.URL == n.URL && v.SKU == n.SKU && v.Status == n.Status {
					found = true
					break
				}
			}
			if !found {
				d.Regressions++
				d.Feedback = append(d.Feedback, sample.URL+": prior status changed")
			}
		}
		return nil
	})
	if err != nil {
		return d, err
	}
	d.TrainingPrices = len(train)
	d.HoldoutPrices = len(hold)
	if d.TrainingPrices < 1 || d.HoldoutPrices < 1 {
		d.Feedback = append(d.Feedback, "need independently priced training and holdout pages")
	}
	if previous != nil && (d.Resolved <= d.BaselineResolved || d.HoldoutImprovements == 0) {
		d.Feedback = append(d.Feedback, "need additional resolved rows on independent holdout pages")
	}
	if d.Regressions == 0 && d.TrainingPrices > 0 && d.HoldoutPrices > 0 && (previous == nil || (d.Resolved > d.BaselineResolved && d.HoldoutImprovements > 0)) {
		d.Accepted = true
	}
	if len(d.Feedback) > 20 {
		d.Feedback = d.Feedback[:20]
	}
	return d, nil
}

// Model input contains product-relevant DOM evidence, not entire scripts.
func TrainingPage(sample AdapterSample) AdapterTrainingPage {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(sample.Body))
	if e != nil {
		return AdapterTrainingPage{URL: sample.URL}
	}
	var parts []string
	for _, p := range schemaProducts(doc) {
		if b, e := json.Marshal(p); e == nil && len(b) < 2800 {
			parts = append(parts, string(b))
		}
		if len(parts) >= 3 {
			break
		}
	}
	doc.Find("script,style,iframe,form,noscript").Remove()
	doc.Find("h1,link[rel=canonical],[class*=price],[class*=sku],[class*=articul],[class*=currency],[itemprop=price],[itemprop=sku],[itemprop=priceCurrency]").EachWithBreak(func(_ int, n *goquery.Selection) bool {
		if len(parts) >= 35 {
			return false
		}
		h, e := goquery.OuterHtml(n)
		if e == nil && len(h) < 1600 {
			parts = append(parts, h)
		}
		return true
	})
	// A small observed link sample enables a candidate discovery rule.
	doc.Find("a[href]").EachWithBreak(func(_ int, n *goquery.Selection) bool {
		if len(parts) >= 50 {
			return false
		}
		h, e := goquery.OuterHtml(n)
		if e == nil && len(h) < 500 {
			parts = append(parts, h)
		}
		return true
	})
	out := strings.Join(parts, "\n")
	if len(out) > 10000 {
		out = out[:10000]
	}
	return AdapterTrainingPage{sample.URL, strings.ToValidUTF8(out, "")}
}

func AdapterFingerprint(page string, body []byte) string {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if e != nil {
		return "invalid"
	}
	fields := map[string]bool{}
	doc.Find("[itemprop],[class*=price],[class*=sku],[class*=articul],[class*=currency],[class*=product],[class*=license]").EachWithBreak(func(_ int, n *goquery.Selection) bool {
		prop, _ := n.Attr("itemprop")
		if prop != "" {
			fields["prop:"+prop] = true
		}
		classes, _ := n.Attr("class")
		for _, c := range strings.Fields(classes) {
			if len(c) < 64 && strings.IndexAny(c, "0123456789") == -1 {
				fields[c] = true
			}
		}
		return len(fields) < 120
	})
	var keys []string
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if canonicalExact(doc, page) {
		keys = append(keys, "canonical:exact")
	} else {
		keys = append(keys, "canonical:other")
	}
	h := sha256.Sum256([]byte(strings.Join(keys, "|")))
	return hex.EncodeToString(h[:12])
}
