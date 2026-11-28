package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent/market"
	"agent/views"
)

type chartPoint struct {
	At    string  `json:"at"`
	Price float64 `json:"price"`
}
type chartSeries struct {
	Store  string       `json:"store"`
	Label  string       `json:"label"`
	URL    string       `json:"url"`
	Points []chartPoint `json:"points"`
}

// Match a historical series to the offer and terms shown in the comparison.
// A different title may describe a different term, license or pack size.
type chartKey struct {
	seller, url, title, basis, currency, tax string
	min, max                                 string
}

func bound(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}
func keyOf(o market.Observation) chartKey {
	d := o.Details
	return chartKey{d.Seller, o.SourceRef.URL, d.Name, d.PriceBasis, o.Currency, o.TaxBasis, bound(d.Quantity), bound(d.MaxQuantity)}
}
func cmpURL(p *views.Page, start, end string, q int64) string {
	params := url.Values{"tab": {"compare"}, "q": {p.Query}, "from": {start}, "to": {end}}
	if q > 1 {
		params.Set("qty", strconv.FormatInt(q, 10))
	}
	return "./?" + params.Encode()
}
func shortCondition(o market.Observation, reason string) string {
	switch {
	case o.Details.Quantity == nil:
		return "Количество отсутствует"
	case strings.Contains(reason, "одного магазина"):
		return "Один магазин"
	case strings.Contains(reason, "верхняя граница"):
		return "Диапазон не уточнён"
	default:
		return "Особые условия"
	}
}
func addDifference(row *views.Row, v, minimum float64) {
	delta := math.Round(v*100) - math.Round(minimum*100)
	row.Best = delta == 0
	if delta > 0 {
		row.Difference = "+" + rubles(delta/100)
	}
}
func (h Pages) compare(ctx context.Context, p *views.Page, requested string) error {
	if p.Query == "" {
		return nil
	}
	if len(p.Query) > 300 {
		p.Error = "Артикул слишком длинный."
		return nil
	}
	end, e2 := time.Parse(time.DateOnly, p.To)
	start, e1 := time.Parse(time.DateOnly, p.From)
	if e1 != nil || e2 != nil || end.Before(start) {
		p.Error = "Проверьте даты выбранного периода."
		return nil
	}
	qty := int64(1)
	if requested != "" {
		var err error
		qty, err = strconv.ParseInt(requested, 10, 64)
		if err != nil || qty < 1 || qty > 1000000 {
			p.Error = "Количество должно быть целым числом от 1 до 1 000 000."
			return nil
		}
	}
	p.Quantity = strconv.FormatInt(qty, 10)
	for _, n := range []int{7, 30, 90} {
		from := end.AddDate(0, 0, 1-n).Format(time.DateOnly)
		p.Periods = append(p.Periods, views.Option{Value: cmpURL(p, from, p.To, qty), Label: fmt.Sprintf("%d дн.", n), Active: from == p.From})
	}
	current, err := h.Market.CompareSKU(ctx, market.CompareInput{SKU: p.Query})
	if err != nil {
		return err
	}
	if len(current.Groups) == 0 {
		return nil
	}
	for _, g := range current.Groups {
		for _, o := range g.Rows {
			name := o.Details.Name
			if name != "" && (p.ProductName == "" || len(name) < len(p.ProductName)) {
				p.ProductName = name
			}
			if o.Details.PriceBasis == "per_unit" && o.Details.Quantity != nil && o.Details.MaxQuantity != nil && *o.Details.MaxQuantity > 1 {
				p.HasQuantity = true
			}
			if o.Details.PriceBasis == "displayed_total" && o.Details.Quantity != nil && *o.Details.Quantity > 1 {
				p.HasQuantity = true
			}
		}
	}
	history, err := h.Market.PriceHistory(ctx, market.ChangesInput{ProductKey: p.Query, From: p.From, To: p.To})
	if err != nil {
		return err
	}
	series := map[chartKey]market.PriceSeries{}
	for _, s := range history.Series {
		series[keyOf(s.Offer)] = s
	}
	p.Tiers = quantityTiers(current, p, qty)
	if p.HasQuantity {
		res, err := market.QuoteOffers(current, qty)
		if err != nil {
			return err
		}
		shown := map[chartKey]bool{}
		for _, group := range res.Groups {
			block := views.Group{Comparable: group.Comparable, Label: "За заказ · " + grouped(qty) + " шт."}
			if qty == 1 {
				block.Label = "Один лицензионный комплект"
			}
			for _, o := range group.Rows {
				shown[keyOf(o.Offer)] = true
				row := tableRow(o.Offer)
				row.Price = rubles(o.Total)
				row.ShortCondition = "Один магазин"
				if block.Comparable {
					addDifference(&row, o.Total, group.Rows[0].Total)
				}
				block.Rows = append(block.Rows, row)
			}
			if len(block.Rows) > 0 {
				p.Groups = append(p.Groups, block)
			}
		}
		if qty == 1 {
			// Surface raw, non-quotable offers separately. Never label their sticker price an order total.
			for _, group := range current.Groups {
				block := views.Group{Note: group.Reason}
				for _, o := range group.Rows {
					if shown[keyOf(o)] {
						continue
					}
					d := o.Details
					if d.Quantity != nil && *d.Quantity > qty {
						continue
					}
					row := tableRow(o)
					row.ShortCondition = shortCondition(o, group.Reason)
					block.Rows = append(block.Rows, row)
				}
				if len(block.Rows) > 0 {
					p.Groups = append(p.Groups, block)
				}
			}
		}
	} else {
		for _, group := range current.Groups {
			block := views.Group{Comparable: group.Comparable, Label: group.Label, Note: group.Reason}
			for _, o := range group.Rows {
				row := tableRow(o)
				row.ShortCondition = shortCondition(o, group.Reason)
				if group.Comparable && group.LowestPrice != nil {
					addDifference(&row, o.Value, *group.LowestPrice)
				}
				block.Rows = append(block.Rows, row)
			}
			if len(block.Rows) > 0 {
				p.Groups = append(p.Groups, block)
			}
		}
	}
	// Keep all strictly comparable rows in view before unmatched seller quotes.
	sort.SliceStable(p.Groups, func(i, j int) bool { return p.Groups[i].Comparable && !p.Groups[j].Comparable })
	if current.Truncated {
		p.Notice = "Показаны первые 100 вариантов. Общий минимум неизвестен."
	}
	if history.Truncated {
		p.HistoryNotice = "Показана часть накопленных наблюдений."
	}
	for _, g := range current.Groups {
		var lines []chartSeries
		for _, o := range g.Rows {
			s, ok := series[keyOf(o)]
			if !ok || len(s.Points) == 0 {
				continue
			}
			line := chartSeries{Label: storeName(o.Details.Seller), Store: o.Details.Seller, URL: o.SourceRef.URL}
			for _, pt := range s.Points {
				line.Points = append(line.Points, chartPoint{pt.CapturedAt, pt.Value})
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			continue
		}
		label := g.Label
		if label == "" || !g.Comparable {
			label = lines[0].Label + " · " + quantity(g.Rows[0].Details)
		}
		if g.Comparable && len(current.Groups) == 1 {
			label = ""
		}
		if !g.Comparable && len(lines) > 1 {
			for _, line := range lines {
				if err := addChart(p, []chartSeries{line}, line.Label+" · "+quantity(g.Rows[0].Details), true); err != nil {
					return err
				}
			}
		} else if err := addChart(p, lines, label, !g.Comparable); err != nil {
			return err
		}
	}
	sort.SliceStable(p.Charts, func(i, j int) bool { return !p.Charts[i].Separate && p.Charts[j].Separate })
	if len(p.Charts) > 0 {
		p.Charts[0].Separate = false
	} else if p.HistoryNotice == "" {
		p.HistoryNotice = "За выбранный период замеров нет."
	}
	return nil
}
func addChart(p *views.Page, lines []chartSeries, label string, separate bool) error {
	b, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	p.Charts = append(p.Charts, views.Chart{Label: label, Series: string(b), Separate: separate})
	return nil
}
