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
	Product string `json:"product"`
	Fact    string `json:"observation"`
	Source  string `json:"source"`
	Locator string `json:"locator"`
}

type SalesBrief struct {
	Facts      []BriefFact `json:"observations"`
	PriceCount int         `json:"imported_price_observations"`
	Total      int         `json:"total"`
	NextOffset *int        `json:"next_offset,omitempty"`
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
		facts = append(facts, BriefFact{observedProductName(r.Product, r), fact, source.SourceID, source.Locator})
	}

	salesCount := len(facts)
	type key struct{ sku, snapshot string }
	groups := map[key][]monitorObservation{}
	var order []key
	for _, r := range rows {
		if r.Source != "market" || r.Status != "observed" || r.Kind != "offer" || r.Metric != "displayed_price" {
			continue
		}
		k := key{strings.ToUpper(r.Product), r.snapshot}
		if len(groups[k]) == 0 {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	seen := map[string]bool{}
	for _, k := range order {
		if seen[k.sku] {
			continue
		}
		quotes := groups[k]
		prices := currentPrices(k.sku, k.snapshot, quotes, nil)
		if prices == nil {
			continue
		}
		seen[k.sku] = true
		packages := append(prices.Comparable, prices.Separate...)
		var title struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(quotes[0].Details), &title)
		for i, group := range packages {
			for _, offer := range group.Offers {
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
				facts = append(facts, BriefFact{strings.TrimSpace(title.Name + " " + k.sku), fact, offer.Seller, loc})
			}
		}
	}
	start := min(max(0, offset), len(facts))
	end := min(start+12, len(facts))
	result := SalesBrief{Facts: append([]BriefFact{}, facts[start:end]...), Total: len(facts), PriceCount: len(facts) - salesCount}
	if end < len(facts) {
		result.NextOffset = &end
	}
	return result
}
