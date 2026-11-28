package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"agent/internal/answer"
	"agent/internal/decision"
	"agent/internal/evidence"
)

type BriefQuery struct {
	Offset   int  `json:"offset,omitempty"`
	Summary  bool `json:"summary,omitempty"`
	Director bool `json:"director,omitempty"`
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

type SeriesChange struct {
	Name    string  `json:"name"`
	Percent float64 `json:"change_pct"`
}

type SalesComparison struct {
	Control        SeriesChange    `json:"control_product"`
	Target         SeriesChange    `json:"measured_product"`
	Baseline       evidence.Period `json:"baseline"`
	Current        evidence.Period `json:"current"`
	WeeksPerWindow int             `json:"weeks_in_each_window"`
}

type ProductReading struct {
	Comparison          SalesComparison `json:"paired_sales_comparison"`
	CheckedSKUs         int             `json:"checked_sku_count"`
	ComparableSKUs      int             `json:"comparable_sku_count"`
	EstimatedWeeklyRUB  *float64        `json:"estimated_weekly_potential_rub"`
	salesRef            evidence.SourceRef
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
	Replies     []answer.Input    `json:"director_replies,omitempty"`
	Readings    []ProductReading  `json:"product_readings,omitempty"`
	Causes      []CauseReading    `json:"metrika_causes,omitempty"`
	Comparisons []PriceComparison `json:"price_comparisons,omitempty"`
	Facts       []BriefFact       `json:"observations,omitempty"`
	PriceCount  int               `json:"imported_price_observations"`
	Total       int               `json:"total"`
	NextOffset  *int              `json:"next_offset,omitempty"`
	// DataState explains an empty reading with counts instead of silence, so a
	// chat can tell "no signal imported" from "no observations at all".
	DataState  *BriefDataState `json:"data_state,omitempty"`
	AnswerMode string          `json:"answer_mode,omitempty"`
}

type BriefDataState struct {
	Snapshots         int      `json:"snapshots"`
	ObservedRows      int      `json:"observed_rows"`
	Products          []string `json:"products,omitempty"`
	WatchdogAlertRows int      `json:"watchdog_alert_rows"`
	LabelledSynthetic bool     `json:"labelled_synthetic"`
	NextStep          string   `json:"next_step,omitempty"`
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
	if q.Director {
		q.Summary = true
	}
	result := salesBrief(rows, records.Records, q)
	if q.Director && len(result.Readings) > 0 {
		for _, r := range result.Readings {
			result.Replies = append(result.Replies, directorReply(r))
		}
		result.Readings = nil
	}
	return result, nil
}

func salesBrief(rows []monitorObservation, records []decision.Record, q BriefQuery) SalesBrief {
	// Real rows win when the owner has them. Only when every row is explicitly
	// labelled synthetic does the summary read those rows, so a demo duplicate
	// never mixes with a real series and a purely synthetic owner is not hidden.
	state := briefDataState(rows)
	causes := causeReadings(rows)
	if len(causes) > 0 && state.WatchdogAlertRows == 0 {
		state.NextStep = "Есть отдельные измеренные признаки Метрики; сверьте исходные ряды, периоды и релизы перед выводом."
	}
	rows = summaryRows(rows)
	slices.SortStableFunc(rows, func(a, b monitorObservation) int { return strings.Compare(b.Period.To, a.Period.To) })
	_, active := watchdogState(rows)
	var facts []BriefFact
	var readings []ProductReading
	seenReadings := map[string]bool{}
	newest := ""
	for _, r := range active {
		w := evidence.WatchdogWindow(r.Row)
		if w == nil {
			continue
		}
		// Rows are newest first. An alert whose window closed long before the
		// newest one is leftover history, not what the director asks about now.
		if newest == "" {
			newest = w.Current.To
		} else if staleWindow(newest, w.Current.To) {
			continue
		}
		m := signalMagnitude(w)
		controlPct := (w.ControlCurrentMean/w.ControlBaselineMean - 1) * 100
		control := signalPercent(controlPct)
		metric := label(metricTitles, r.details.Metric)
		if r.details.Metric == "orders_index" {
			metric = "Индекс заказов"
			if r.Segment == "self_service" {
				metric = "Индекс самостоятельных заказов"
			}
		}
		fact := fmt.Sprintf("%s, два окна по %d недели. С %s по сравнению с %s изменение составило %s. Изменение контроля (%s) за те же окна %s.", metric, w.WindowWeeks, m.Current, m.Baseline, m.Change, productName(r.details.Control), control)
		reading := ProductReading{Product: observedProductName(r.Product, r), windowEnd: w.Current.To}
		// The same alert imported twice must reach the director once.
		if id := fmt.Sprintf("%s|%s|%s|%.1f", reading.Product, w.Current.From, w.Current.To, w.RelativeChangePct); seenReadings[id] {
			continue
		} else {
			seenReadings[id] = true
		}
		reading.Sales = fmt.Sprintf("%s, %s–%s к %s–%s (по %d недели): %s %s, %s %s.",
			metric, date(w.Current.From), date(w.Current.To), date(w.Baseline.From), date(w.Baseline.To),
			w.WindowWeeks, reading.Product, m.Change, productName(r.details.Control), control)
		reading.Comparison = SalesComparison{
			Control:  SeriesChange{productName(r.details.Control), math.RoundToEven(controlPct*10) / 10},
			Target:   SeriesChange{reading.Product, math.RoundToEven(w.RelativeChangePct*10) / 10},
			Baseline: w.Baseline, Current: w.Current, WeeksPerWindow: w.WindowWeeks,
		}
		weekly, basis, amount := signalPlanning(records, r)
		reading.EstimatedWeeklyRUB = amount
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
		reading.salesRef = source
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
		result := SalesBrief{Readings: readings[start:end], Causes: causes, Total: len(readings), PriceCount: len(facts) - salesCount, DataState: state}
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
	result := SalesBrief{Facts: append([]BriefFact{}, facts[start:end]...), Causes: causes, Total: len(facts), PriceCount: len(facts) - salesCount, DataState: state}
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

func staleWindow(newest, other string) bool {
	a, errA := time.Parse("2006-01-02", newest)
	b, errB := time.Parse("2006-01-02", other)
	return errA == nil && errB == nil && a.Sub(b) > 26*7*24*time.Hour
}

// summaryRows keeps the panel's separation without hiding a synthetic owner.
// Real rows win while they carry an alert as fresh as any other. When the only
// real alerts are old leftovers and a labelled demo series holds the current
// alert, the director sees what the panel shows, never the leftover.
func summaryRows(rows []monitorObservation) []monitorObservation {
	allSynthetic := true
	for _, row := range rows {
		if !educationalObservation(row.Row, row.details) {
			allSynthetic = false
			break
		}
	}
	if allSynthetic {
		return rows
	}
	real := separateMonitorRows(rows, false)
	all, mine := newestActiveWindow(rows), newestActiveWindow(real)
	if mine != "" && !staleWindow(all, mine) {
		return real
	}
	if demo := separateMonitorRows(rows, true); newestActiveWindow(demo) != "" {
		return demo
	}
	return real
}

// newestActiveWindow is the closing date of the newest open alert, or "".
func newestActiveWindow(rows []monitorObservation) string {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b monitorObservation) int { return strings.Compare(b.Period.To, a.Period.To) })
	_, active := watchdogState(sorted)
	newest := ""
	for _, r := range active {
		if w := evidence.WatchdogWindow(r.Row); w != nil && w.Current.To > newest {
			newest = w.Current.To
		}
	}
	return newest
}

// briefDataState counts what the owner actually has, before any alert filter.
// It names the products and the origin so an empty reading is explainable.
func briefDataState(rows []monitorObservation) *BriefDataState {
	snapshots := map[string]bool{}
	products := map[string]bool{}
	synthetic := false
	alerts := 0
	observed := 0
	for _, row := range rows {
		snapshots[row.snapshot] = true
		if row.Product != "" {
			products[row.Product] = true
		}
		if row.Status == "observed" {
			observed++
		}
		if educationalObservation(row.Row, row.details) {
			synthetic = true
		}
		if row.Metric == watchdogMetric && row.Status == "observed" {
			alerts++
		}
	}
	state := &BriefDataState{Snapshots: len(snapshots), ObservedRows: observed, WatchdogAlertRows: alerts, LabelledSynthetic: synthetic}
	for p := range products {
		state.Products = append(state.Products, productName(p))
	}
	slices.Sort(state.Products)
	if observed > 0 && alerts == 0 {
		state.NextStep = "Наблюдения импортированы, но готового сигнала сторожа нет. Импортируйте документ из metrika-analyst collect_selected_watchdog_case, затем повторите сводку."
	}
	return state
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
		r.Price = "В импортированных предложениях этого семейства отсутствуют предложения для сравнения."
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
	r.CheckedSKUs, r.ComparableSKUs = len(skus), n
	r.PriceEvidenceStatus = "comparable_quotes_available"
	if n == 0 {
		r.PriceEvidenceStatus = "insufficient_comparable_quotes"
		r.Price += " Эти предложения не дают оснований снижать цену. Проверьте цены за одинаковое количество лицензий одного артикула на один срок."
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

func directorReply(r ProductReading) answer.Input {
	price := "Для решения о цене нужны сопоставимые предложения того же артикула."
	switch r.PriceEvidenceStatus {
	case "insufficient_comparable_quotes":
		price = fmt.Sprintf("Снижать цену пока не стоит. Одинаковые условия отсутствуют у %d проверенных артикулов.", r.CheckedSKUs)
	case "comparable_quotes_available":
		price = fmt.Sprintf("До решения о цене сравните нашу цену с предложениями на одинаковых условиях: такие предложения есть для %d из %d артикулов.", r.ComparableSKUs, r.CheckedSKUs)
	}
	out := answer.Input{Decision: price, Evidence: []answer.Evidence{}}
	c := r.Comparison
	if c.Target.Name != "" && c.Control.Name != "" && r.salesRef.Locator != "" {
		out.Change = &c.Target.Percent
		out.Control = &answer.Control{Product: c.Control.Name, Change: c.Control.Percent}
		out.Evidence = append(out.Evidence, answer.Evidence{Source: r.salesRef.SourceID, Ref: r.salesRef.Locator, Fact: fmt.Sprintf("%s: %s–%s к %s–%s, по %d недели.", c.Target.Name, c.Current.From, c.Current.To, c.Baseline.From, c.Baseline.To, c.WeeksPerWindow)})
	}
	for _, q := range r.OfferExample {
		fact, _, _ := strings.Cut(q.Fact, ". ")
		if len(out.Evidence) < 3 && utf8.RuneCountInString(fact) <= 130 && utf8.RuneCountInString(q.Locator) <= 240 && q.Locator != "" {
			out.Evidence = append(out.Evidence, answer.Evidence{Source: q.Source, Ref: q.Locator, Fact: fact})
		}
	}
	return out
}
