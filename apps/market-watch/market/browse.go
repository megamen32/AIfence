package market

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	catalog "agent/collector"
	"github.com/airlockrun/agentsdk"
)

const moreOffersWarning = "More offers match. Continue with offset and limit, or download the full CSV."

// Title filters describe words actually published by the seller. They can
// overlap (for example, a subscription renewal) and do not prove identity.
var licenseRules = []struct {
	id string
	re *regexp.Regexp
}{
	{"new", regexp.MustCompile("новая лицензия|новой лицензии|новую лицензию|(new|base) licen[cs]e|первичная лицензия|базовая лицензия")},
	{"renewal", regexp.MustCompile("продлен|продлён|renewal")},
	{"upgrade", regexp.MustCompile("переход с|апгрейд|upgrade|повышение версии")},
	{"subscription", regexp.MustCompile("подписк|subscription")},
	{"perpetual", regexp.MustCompile("бессрочн|perpetual")},
}

func licenseFilter(id string) (pattern string, unknown bool, err error) {
	if id == "" {
		return "", false, nil
	}
	var patterns []string
	for _, rule := range licenseRules {
		if id == rule.id {
			return rule.re.String(), false, nil
		}
		patterns = append(patterns, rule.re.String())
	}
	if id == "unknown" {
		return strings.Join(patterns, "|"), true, nil
	}
	return "", false, fmt.Errorf("unknown license type %q", id)
}

func pageSize(n int) int {
	if n <= 0 || n > 100 {
		return 100
	}
	return n
}

func (s *Service) Browse(ctx context.Context, in ListInput) (Evidence, bool, error) {
	in.ProductKey = strings.TrimSpace(in.ProductKey)
	if in.Offset < 0 {
		return Evidence{}, false, fmt.Errorf("offset must be nonnegative")
	}
	if in.ExactSKU && in.ProductKey == "" {
		return Evidence{}, false, fmt.Errorf("exact_sku requires a nonempty product_key")
	}
	in.Limit = pageSize(in.Limit)
	if s.queries != nil {
		return s.catalogPage(ctx, in)
	}
	pattern, _, err := licenseFilter(in.LicenseType)
	if err != nil {
		return Evidence{}, false, err
	}
	var license *regexp.Regexp
	if pattern != "" {
		license = regexp.MustCompile(pattern)
	}
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return Evidence{}, false, err
	}
	ev, more := snapshotPage(snap, in, license, s.now())
	return ev, more, nil
}

func quantityValue(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func browseMatch(row catalog.Offer, in ListInput, license *regexp.Regexp) bool {
	if in.ExactSKU && !strings.EqualFold(row.SKU, in.ProductKey) {
		return false
	}
	if !matches(row, in.ProductKey) && row.URL != in.ProductKey {
		return false
	}
	if in.Source != "" && row.Source != in.Source || in.PriceBasis != "" && row.PriceBasis != in.PriceBasis {
		return false
	}
	if in.Quantity != nil && *in.Quantity != quantityValue(row.Quantity) || in.MaxQuantity != nil && *in.MaxQuantity != quantityValue(row.MaxQuantity) {
		return false
	}
	return license == nil || license.MatchString(strings.ToLower(row.Name)) != (in.LicenseType == "unknown")
}

// Mirror the unique latest_offers ordering so page boundaries always fall
// between identities, even for quantity tiers and rows without an article.
func offerOrder(a, b Observation, query string) int {
	aExact, bExact := strings.EqualFold(a.ProductKey, query), strings.EqualFold(b.ProductKey, query)
	if aExact != bExact {
		if aExact {
			return -1
		}
		return 1
	}
	return cmp.Or(cmp.Compare(a.ProductKey, b.ProductKey), cmp.Compare(a.Details.Seller, b.Details.Seller),
		cmp.Compare(a.SourceRef.URL, b.SourceRef.URL), cmp.Compare(a.Details.PriceBasis, b.Details.PriceBasis),
		cmp.Compare(quantityValue(a.Details.Quantity), quantityValue(b.Details.Quantity)),
		cmp.Compare(quantityValue(a.Details.MaxQuantity), quantityValue(b.Details.MaxQuantity)))
}

type CompareInput struct {
	SKU string `json:"sku"`
}

type ComparisonGroup struct {
	Label       string        `json:"label,omitempty"`
	Rows        []Observation `json:"rows"`
	Comparable  bool          `json:"comparable"`
	LowestPrice *float64      `json:"lowest_displayed_price,omitempty"`
	Reason      string        `json:"reason"`
}

type Comparison struct {
	SKU       string            `json:"sku"`
	Groups    []ComparisonGroup `json:"groups"`
	Truncated bool              `json:"truncated"`
	Partial   bool              `json:"partial,omitempty"`
	ViewPath  string            `json:"view_path"`
	ViewURL   string            `json:"view_url,omitempty"`
}

type comparisonTerms struct {
	currency, taxBasis             string
	licenseTypes, months, packSize int
}

var termInTitle = regexp.MustCompile(`(?:^|[^0-9a-zа-яё])([1-9][0-9]{0,3})[\s-]*(year|month|год|лет|месяц)`)
var packSizeInTitle = regexp.MustCompile(`(?:^|[^0-9a-zа-яё])([1-9][0-9]{0,5})[\s-]*(mobile device|desktop|device|user|seat|workstation|computers?|pcs?|пк|устройств|пользовател|рабочих мест|рабочее место|компьютер)`)
var rangeInTitle = regexp.MustCompile(`[0-9]\s*[-\x{2013}\x{2014}]\s*[0-9]`)

// The numeric values belong to the license pack (term and seats), not the
// checkout quantity. Conflicting numbers never establish a one-pack match.
func statedCount(re *regexp.Regexp, title string) int {
	value := 0
	for _, match := range re.FindAllStringSubmatch(title, -1) {
		n, _ := strconv.Atoi(match[1])
		switch match[2] {
		case "год", "лет", "year":
			n *= 12
		}
		if value != 0 && n != value {
			return -1
		}
		value = n
	}
	return value
}

func termsOf(row Observation) comparisonTerms {
	title := strings.ToLower(row.Details.Name)
	terms := comparisonTerms{currency: row.Currency, taxBasis: row.TaxBasis, months: statedCount(termInTitle, title), packSize: statedCount(packSizeInTitle, title)}
	for i, rule := range licenseRules {
		if rule.re.MatchString(title) {
			terms.licenseTypes |= 1 << i
		}
	}
	if rangeInTitle.MatchString(title) {
		terms.packSize = -1
	}
	return terms
}

type packBases struct{ unit, total bool }

func (b packBases) mixed() bool { return b.unit && b.total }

func keysInvalidTerms(row Observation) bool {
	t := termsOf(row)
	return t.months < 0 || t.packSize < 0
}

func onePackBasis(row Observation, terms comparisonTerms) string {
	d := row.Details
	if terms.currency == "" || terms.licenseTypes == 0 || terms.months < 1 || terms.packSize < 1 || quantityValue(d.Quantity) != 1 {
		return ""
	}
	if d.PriceBasis == "per_unit" && quantityValue(d.MaxQuantity) >= 1 {
		return d.PriceBasis
	}
	if d.PriceBasis == "displayed_total" && (d.MaxQuantity == nil || *d.MaxQuantity == 1) {
		return d.PriceBasis
	}
	return ""
}

func (s *Service) CompareSKU(ctx context.Context, in CompareInput) (Comparison, error) {
	sku := strings.TrimSpace(in.SKU)
	if sku == "" || len(sku) > 300 {
		return Comparison{}, fmt.Errorf("enter a SKU of 1 to 300 bytes")
	}
	ev, more, err := s.Browse(ctx, ListInput{ProductKey: sku, ExactSKU: true, Limit: 100})
	if err != nil {
		return Comparison{}, err
	}
	result := Comparison{SKU: sku, Groups: []ComparisonGroup{}, Truncated: more, Partial: ev.Status == "partial",
		ViewPath: "/?" + url.Values{"tab": {"compare"}, "q": {sku}}.Encode()}
	if origin, err := agentsdk.AgentURLFromContext(ctx); err == nil {
		result.ViewURL = strings.TrimRight(origin, "/") + result.ViewPath
	}
	result.Groups = groupOffers(ev.Observations, more, ev.Status == "partial")
	return result, nil
}

func groupOffers(rows []Observation, more, partial bool) []ComparisonGroup {
	groups := []ComparisonGroup{}
	type conditions struct {
		comparisonTerms
		basis                 string
		quantity, maxQuantity int64
	}
	// Convert only when both actual quote bases occur under the same explicitly
	// documented pack terms. All stored rows keep their original units.
	bases := map[comparisonTerms]packBases{}
	keys := make([]conditions, len(rows))
	for i, row := range rows {
		d := row.Details
		terms := termsOf(row)
		keys[i] = conditions{terms, d.PriceBasis, quantityValue(d.Quantity), quantityValue(d.MaxQuantity)}
		b := bases[terms]
		switch onePackBasis(row, terms) {
		case "per_unit":
			b.unit = true
		case "displayed_total":
			b.total = true
		}
		bases[terms] = b
	}
	positions := map[conditions]int{}
	for i, row := range rows {
		key := keys[i]
		label := ""
		if bases[key.comparisonTerms].mixed() && onePackBasis(row, key.comparisonTerms) != "" {
			key.basis = "one_pack"
			key.maxQuantity = 1
			label = "Один лицензионный комплект"
		}
		position, ok := positions[key]
		if !ok {
			position = len(groups)
			positions[key] = position
			groups = append(groups, ComparisonGroup{Label: label})
		}
		g := &groups[position]
		g.Rows = append(g.Rows, row)
	}
	for i := range groups {
		g := &groups[i]
		first := g.Rows[0]
		d := first.Details
		sellers := map[string]bool{}
		for _, o := range g.Rows {
			sellers[o.Details.Seller] = true
		}
		switch {
		case more:
		g.Reason = "Показаны первые 100 вариантов артикула. Минимум относится к этой выборке."
		case partial:
			g.Reason = "Снимок охватывает часть магазинов или содержит ранее сохранённые цены."
		case keysInvalidTerms(first):
			g.Reason = "В описании указаны несколько сроков или диапазон количества. Проверьте условия у продавца."
		case d.Quantity == nil:
			g.Reason = "Магазин не указал количество для этой цены."
		case d.PriceBasis != "per_unit" && d.PriceBasis != "displayed_total":
			g.Reason = "Основание цены не позволяет сопоставить стоимость единицы или заказа."
		case d.PriceBasis == "per_unit" && d.MaxQuantity == nil:
			g.Reason = "Неизвестна верхняя граница ценовой ступени."
		case len(sellers) < 2:
			g.Reason = "Такие условия найдены только у одного магазина."
		default:
			g.Comparable = true
			sort.SliceStable(g.Rows, func(i, j int) bool { return g.Rows[i].Value < g.Rows[j].Value })
			g.LowestPrice = &g.Rows[0].Value
			g.Reason = "Минимум опубликованных цен при этих условиях. Даты наблюдений указаны у предложений. Налоги и итоговую сумму уточните у продавца."
		}
	}
	return groups
}
