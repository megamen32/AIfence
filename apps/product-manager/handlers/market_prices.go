package handlers

import (
	"agent/internal/decision"
	"agent/views"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

type quoteDetails struct {
	Seller     string           `json:"seller"`
	Captured   string           `json:"captured_at"`
	Basis      string           `json:"price_basis"`
	Quantity   *int64           `json:"quantity"`
	Max        *int64           `json:"max_quantity"`
	Comparison *quoteComparison `json:"market_comparison"`
}

type quoteComparison struct {
	Group      string `json:"group"`
	Comparable bool   `json:"comparable"`
	Reason     string `json:"reason"`
	Hypothesis string `json:"hypothesis_id"`
}

type quoteKey struct {
	group, basis, currency string
	quantity, max          int64
}

func currentPrices(key, snapshot string, rows []monitorObservation, records []decision.Record) *views.MarketPrices {
	p := &views.MarketPrices{SKU: key, Name: catalogFamilyNames[catalogSKUFamily[strings.ToUpper(key)]], TaskStatus: "Задача для SKU не привязана"}
	positions := map[quoteKey]int{}
	var groups []views.PriceGroup
	var sellers []map[string]bool
	var taskRefs []string
	for _, o := range rows {
		if o.Source != "market" || o.Kind != "offer" || o.Metric != "displayed_price" ||
			o.Status != "observed" || !strings.EqualFold(o.Product, key) || (snapshot != "" && o.snapshot != snapshot) {
			continue
		}
		amount, parseErr := strconv.ParseFloat(o.Value, 64)
		var d quoteDetails
		if parseErr != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 ||
			json.Unmarshal([]byte(o.Details), &d) != nil || len(o.Unit) != 3 {
			continue
		}
		stamp := quoteDate(d.Captured, o.Ref.Revision, o.Period.To)
		if stamp == "" {
			continue
		}
		// Keep a single immutable quote set when a manual selection did not pin a snapshot.
		if snapshot == "" {
			snapshot = o.snapshot
		}
		group := quoteKey{basis: d.Basis, currency: o.Unit}
		if d.Quantity != nil && *d.Quantity > 0 {
			group.quantity = *d.Quantity
		}
		if d.Max != nil && *d.Max >= group.quantity {
			group.max = *d.Max
		}
		if d.Comparison != nil {
			group.group = d.Comparison.Group
			if related := d.Comparison.Hypothesis; related != "" && !slices.Contains(taskRefs, related) {
				taskRefs = append(taskRefs, related)
			}
		}
		pos, ok := positions[group]
		if !ok {
			pos = len(groups)
			positions[group] = pos
			match, reason := quoteComparable(group, d.Comparison)
			groups = append(groups, views.PriceGroup{Title: quoteGroupTitle(group), Comparable: match, Reason: reason})
			sellers = append(sellers, map[string]bool{})
		}
		if match, reason := quoteComparable(group, d.Comparison); !match && groups[pos].Comparable {
			groups[pos].Comparable, groups[pos].Reason = false, reason
		}
		seller := d.Seller
		if seller == "" {
			seller = o.Ref.SourceID
		}
		price := num(amount) + " " + o.Unit
		if o.Unit == "RUB" {
			price = rub(amount)
		}
		sellers[pos][seller] = true
		groups[pos].Offers = append(groups[pos].Offers, views.PriceOffer{
			Seller: label(marketSellers, seller), Price: price, Date: stamp,
			URL: rowView(snapshot, o.Row).SourceURL,
		})
	}
	if len(groups) == 0 {
		return nil
	}
	for i, g := range groups {
		if g.Comparable && len(sellers[i]) > 1 {
			g.Reason = fmt.Sprintf("%d предложений, %d магазинов", len(g.Offers), len(sellers[i]))
			p.Comparable = append(p.Comparable, g)
			continue
		}
		if g.Comparable {
			g.Reason = "Эти количество и базис найдены у одного магазина."
		}
		p.Separate = append(p.Separate, g)
	}
	if len(taskRefs) == 1 {
		for _, record := range records {
			if record.Hypothesis.ID != taskRefs[0] {
				continue
			}
			p.TaskURL = href("id", record.Hypothesis.ID)
			p.TaskStatus = "Задача ещё не создана"
			if record.Task != nil {
				p.TaskStatus = "Задача " + record.Task.ID + " · " + label(statusTitles, record.Hypothesis.Status)
			}
			break
		}
	}
	p.SnapshotURL = href("snapshot", snapshot, "source", "market")
	return p
}

var marketSellers = map[string]string{"softline": "Softline", "softmap": "SoftMap", "allsoft": "Allsoft", "1c_interes": "1С Интерес"}

func quoteComparable(key quoteKey, source *quoteComparison) (bool, string) {
	switch {
	case key.quantity == 0:
		return false, "Магазин не указал количество."
	case key.basis != "per_unit" && key.basis != "displayed_total":
		return false, "Базис цены неизвестен."
	case key.basis == "per_unit" && key.max == 0:
		return false, "Не указана верхняя граница количества."
	case source == nil || source.Group == "":
		return false, "Сопоставимость не подтверждена Market Watch."
	case !source.Comparable:
		return false, source.Reason
	default:
		return true, ""
	}
}

func quoteGroupTitle(k quoteKey) string {
	qty := "Количество не указано"
	if k.quantity > 0 {
		qty = fmt.Sprintf("%d шт.", k.quantity)
		if k.max > k.quantity {
			qty = fmt.Sprintf("%d–%d шт.", k.quantity, k.max)
		}
	}
	basis := label(map[string]string{"per_unit": "за единицу", "displayed_total": "за заказ"}, k.basis)
	if k.basis != "per_unit" && k.basis != "displayed_total" {
		basis = "базис неизвестен"
	}
	return strings.Join([]string{qty, basis, k.currency}, " / ")
}

func quoteDate(values ...string) string {
	for _, value := range values {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return t.Format("02.01.2006")
		}
		if t, err := time.Parse(time.DateOnly, value); err == nil {
			return t.Format("02.01.2006")
		}
	}
	return ""
}
