package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"agent/internal/decision"
	"agent/internal/evidence"
)

type BriefQuery struct {
	Offset int `json:"offset,omitempty"`
}

type BriefFact struct {
	SKU     string `json:"sku,omitempty"`
	Product string `json:"product"`
	Fact    string `json:"observation"`
	Source  string `json:"source"`
	Locator string `json:"locator"`
}

type PriceComparison struct {
	Product          string   `json:"product"`
	SKU              string   `json:"sku"`
	Sellers          []string `json:"sellers"`
	ComparableGroups int      `json:"comparable_price_groups"`
	Finding          string   `json:"price_evidence"`
}

type SalesBrief struct {
	Comparisons []PriceComparison `json:"price_comparisons"`
	Facts       []BriefFact       `json:"observations"`
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
	return salesBrief(rows, records.Records, q.Offset), nil
}

func salesBrief(rows []monitorObservation, records []decision.Record, offset int) SalesBrief {
	rows = separateMonitorRows(rows, false)
	slices.SortStableFunc(rows, func(a, b monitorObservation) int { return strings.Compare(b.Period.To, a.Period.To) })
	_, active := watchdogState(rows)
	var facts []BriefFact
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
		weekly, basis, _ := signalPlanning(records, r)
		if weekly != "" {
			fact += " Оценка за неделю: " + weekly + " (" + basis + ")."
		} else {
			fact += " Оценки в ₽ за неделю нет: нужны потерянные за неделю заказы в штуках и вклад на заказ."
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
		facts = append(facts, BriefFact{Product: observedProductName(r.Product, r), Fact: fact, Source: source.SourceID, Locator: source.Locator})
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
				facts = append(facts, BriefFact{SKU: k.sku, Product: title.Name, Fact: fact, Source: offer.Seller, Locator: loc})
			}
		}
		if summary.ComparableGroups == 0 {
			summary.Finding = "По этому артикулу нет пары магазинов с подтверждённо одинаковыми условиями. Преимущество конкурента в цене не установлено."
		} else {
			summary.Finding = "Сравнение цен возможно внутри подтверждённых групп с одинаковыми условиями."
		}
		comparisons[k.sku] = summary
	}
	start := min(max(0, offset), len(facts))
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
