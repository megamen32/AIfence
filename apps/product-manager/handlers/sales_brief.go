package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"agent/internal/decision"
	"agent/internal/evidence"
)

type BriefQuery struct {
	Offset  int  `json:"offset,omitempty"`
	Summary bool `json:"summary,omitempty"`
}

type BriefFact struct {
	observed time.Time
	SKU      string `json:"sku,omitempty"`
	Product  string `json:"product"`
	Fact     string `json:"observation"`
	Source   string `json:"source"`
	Locator  string `json:"locator"`
}

type PriceComparison struct {
	Product          string   `json:"product"`
	SKU              string   `json:"sku"`
	Sellers          []string `json:"sellers"`
	ComparableGroups int      `json:"comparable_price_groups"`
	Finding          string   `json:"price_evidence"`
}

type ProductReading struct {
	windowEnd           string
	Product             string      `json:"product"`
	PriceEvidenceStatus string      `json:"price_evidence_status"`
	Price               string      `json:"price_comparison"`
	PriceTiming         string      `json:"timing_of_price_evidence,omitempty"`
	OfferExample        []BriefFact `json:"example_offers_for_one_sku,omitempty"`
	Sales               string      `json:"measured_sales"`
	Weekly              string      `json:"weekly_financial_impact"`
	Source              string      `json:"sales_source"`
}

type SalesBrief struct {
	Readings    []ProductReading  `json:"product_readings,omitempty"`
	Comparisons []PriceComparison `json:"price_comparisons,omitempty"`
	Facts       []BriefFact       `json:"observations,omitempty"`
	PriceCount  int               `json:"imported_price_observations"`
	Total       int               `json:"total"`
	NextOffset  *int              `json:"next_offset,omitempty"`
}

func (h *Handler) SalesBrief(ctx context.Context, q BriefQuery) (SalesBrief, error) {
	index, err := h.svc.Evidence.List(ctx, evidence.Empty{})
	if err != nil {
		return SalesBrief{}, err
	}
	rows, err := h.monitorRows(ctx, index.Snapshots)
	if err != nil {
		return SalesBrief{}, err
	}
	records, err := h.svc.List(ctx, evidence.Empty{})
	if err != nil {
		return SalesBrief{}, err
	}
	return salesBrief(rows, records.Records, q), nil
}

func salesBrief(rows []monitorObservation, records []decision.Record, q BriefQuery) SalesBrief {
	rows = separateMonitorRows(rows, false)
	slices.SortStableFunc(rows, func(a, b monitorObservation) int { return strings.Compare(b.Period.To, a.Period.To) })
	_, active := watchdogState(rows)
	var facts []BriefFact
	var readings []ProductReading
	for _, r := range active {
		w := evidence.WatchdogWindow(r.Row)
		if w == nil {
			continue
		}
		m := signalMagnitude(w)
		control := signalPercent((w.ControlCurrentMean/w.ControlBaselineMean - 1) * 100)
		metric := label(metricTitles, r.details.Metric)
		if r.details.Metric == "orders_index" {
			metric = "Индекс заказов"
			if r.Segment == "self_service" {
				metric = "Индекс самостоятельных заказов"
			}
		}
		fact := fmt.Sprintf("%s, два окна по %d недели. С %s по сравнению с %s изменение составило %s. Изменение контроля (%s) за те же окна %s.", metric, w.WindowWeeks, m.Current, m.Baseline, m.Change, productName(r.details.Control), control)
		reading := ProductReading{Product: observedProductName(r.Product, r), windowEnd: w.Current.To}
		reading.Sales = fmt.Sprintf("%s, %s–%s к %s–%s (по %d недели): %s %s, %s %s.",
			metric, date(w.Current.From), date(w.Current.To), date(w.Baseline.From), date(w.Baseline.To),
			w.WindowWeeks, reading.Product, m.Change, productName(r.details.Control), control)
		weekly, basis, _ := signalPlanning(records, r)
		if weekly != "" {
			reading.Weekly = weekly + " (" + basis + ")."
		} else {
			reading.Weekly = "Оценки в ₽ за неделю нет: нужны потерянные за неделю заказы в штуках и вклад на заказ."
		}
		source := r.Ref
		var refs struct {
			Refs []struct {
				Ref evidence.SourceRef `json:"source_ref"`
			} `json:"source_refs"`
		}
		if json.Unmarshal([]byte(r.Details), &refs) == nil && len(refs.Refs) > 0 && refs.Refs[0].Ref.Locator != "" && refs.Refs[0].Ref.SourceID != "" {
			source = refs.Refs[0].Ref
		}
		reading.Source = source.SourceID + ", " + source.Locator
		readings = append(readings, reading)
		facts = append(facts, BriefFact{Product: reading.Product, Fact: fact + " " + reading.Weekly, Source: source.SourceID, Locator: source.Locator})
	}

	salesCount := len(facts)
	type key struct{ sku, snapshot string }
	groups := map[key][]monitorObservation{}
	var order []key
	for _, r := range rows {
		if r.Source != "market" || (r.Status != "observed" && r.Status != "incomparable") || r.Kind != "offer" || r.Metric != "displayed_price" {
			continue
		}
		k := key{strings.ToUpper(r.Product), r.snapshot}
		if len(groups[k]) == 0 {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	comparisons := map[string]PriceComparison{}
	for _, k := range order {
		if _, found := comparisons[k.sku]; found {
			continue
		}
		quotes := groups[k]
		prices := currentPrices(k.sku, k.snapshot, quotes, nil)
		if prices == nil {
			continue
		}
		packages := append(prices.Comparable, prices.Separate...)
		var title struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(quotes[0].Details), &title)
		summary := PriceComparison{Product: title.Name, SKU: k.sku, ComparableGroups: len(prices.Comparable)}
		for i, group := range packages {
			for _, offer := range group.Offers {
				if !slices.Contains(summary.Sellers, offer.Seller) {
					summary.Sellers = append(summary.Sellers, offer.Seller)
				}
				// Only groups admitted by the panel have matching terms from distinct sellers.
				match := "Нет подтверждённой пары магазинов"
				if i < len(prices.Comparable) {
					match = "Сопоставимые предложения"
				}
				fact := fmt.Sprintf("%s · %s · %s · %s. %s: %s.", offer.Date, offer.Seller, offer.Price, group.Title, match, group.Reason)
				loc := offer.URL
				if loc == "" {
					loc = prices.SnapshotURL
				}
				stamp, _ := time.Parse("02.01.2006", offer.Date) // validated by currentPrices
				facts = append(facts, BriefFact{observed: stamp, SKU: k.sku, Product: title.Name, Fact: fact, Source: offer.Seller, Locator: loc})
			}
		}
		if summary.ComparableGroups == 0 {
			summary.Finding = "По этому артикулу нет пары магазинов с подтверждённо одинаковыми условиями. Преимущество конкурента в цене не установлено."
		} else {
			summary.Finding = "Сравнение цен возможно внутри подтверждённых групп с одинаковыми условиями."
		}
		comparisons[k.sku] = summary
	}
	if q.Summary && len(readings) > 0 {
		start := min(max(0, q.Offset), len(readings))
		end := min(start+12, len(readings))
		result := SalesBrief{Readings: readings[start:end], Total: len(readings), PriceCount: len(facts) - salesCount}
		for i := range result.Readings {
			addPriceContext(&result.Readings[i], comparisons, facts[salesCount:])
		}
		if end < len(readings) {
			result.NextOffset = &end
		}
		return result
	}
	start := min(max(0, q.Offset), len(facts))
	end := min(start+12, len(facts))
	result := SalesBrief{Facts: append([]BriefFact{}, facts[start:end]...), Total: len(facts), PriceCount: len(facts) - salesCount}
	for _, fact := range result.Facts {
		if value, found := comparisons[fact.SKU]; found {
			result.Comparisons = append(result.Comparisons, value)
			delete(comparisons, fact.SKU)
		}
	}
	if end < len(facts) {
		result.NextOffset = &end
	}
	return result
}

// The sales family must start the catalog title on a word boundary. Individual
// SKUs keep their own license terms and are never compared with each other.
func addPriceContext(r *ProductReading, comparisons map[string]PriceComparison, quotes []BriefFact) {
	family := strings.ToLower(strings.Join(strings.Fields(r.Product), " "))
	var skus []string
	for sku, c := range comparisons {
		title := strings.ToLower(strings.Join(strings.Fields(c.Product), " "))
		if family != "" && (title == family || strings.HasPrefix(title, family+" ")) {
			skus = append(skus, sku)
		}
	}
	if len(skus) == 0 {
		r.PriceEvidenceStatus = "no_matching_offers"
		r.Price = "В импортированных предложениях этого семейства нет данных для сравнения."
		return
	}
	slices.Sort(skus)
	n := 0
	example := skus[0]
	for _, sku := range skus {
		if comparisons[sku].ComparableGroups > 0 {
			if n == 0 {
				example = sku
			}
			n++
		}
	}
	r.Price = fmt.Sprintf("В снимках проверено артикулов: %d. Одинаковые условия лицензии у двух или более магазинов подтверждены для %d из %d.", len(skus), n, len(skus))
	r.PriceEvidenceStatus = "comparable_quotes_available"
	if n == 0 {
		r.PriceEvidenceStatus = "insufficient_comparable_quotes"
		r.Price += " Оснований менять цену по этим предложениям нет. Для решения нужны цены на один артикул при одинаковом количестве и базисе."
	}
	var earliest time.Time
	for _, q := range quotes {
		if _, matches := slices.BinarySearch(skus, q.SKU); !matches {
			continue
		}
		if !q.observed.IsZero() && (earliest.IsZero() || q.observed.Before(earliest)) {
			earliest = q.observed
		}
		if q.SKU == example && len(r.OfferExample) < 4 {
			r.OfferExample = append(r.OfferExample, q)
		}
	}
	if !earliest.IsZero() && r.windowEnd != "" && earliest.Format(time.DateOnly) > r.windowEnd {
		r.PriceTiming = fmt.Sprintf("Даже самая ранняя из проверенных цен датирована %s, после окончания периода продаж %s. Эти цены не объясняют историческое падение.", earliest.Format("02.01.2006"), date(r.windowEnd))
	}
}
