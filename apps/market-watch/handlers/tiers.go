package handlers

import (
	"agent/market"
	"agent/views"
	"fmt"
	"math"
	"sort"
	"strings"
)

// A selectable band represents exactly one published, bounded unit-price range.
// Differently named licenses or overlapping ranges are never joined into a stair.
func quantityTiers(current market.Comparison, p *views.Page, selected int64) []views.TierSchedule {
	type tierKey struct{ seller, title, currency, tax string }
	buckets := map[tierKey][]market.Observation{}
	for _, g := range current.Groups {
		for _, o := range g.Rows {
			d := o.Details
			if d.PriceBasis != "per_unit" || d.Quantity == nil || d.MaxQuantity == nil || *d.Quantity < 1 || *d.MaxQuantity < *d.Quantity || *d.Quantity > 1000000 {
				continue
			}
			key := tierKey{d.Seller, d.Name, o.Currency, o.TaxBasis}
			buckets[key] = append(buckets[key], o)
		}
	}
	var schedules []views.TierSchedule
	for key, offers := range buckets {
		if len(offers) < 2 {
			continue
		}
		sort.SliceStable(offers, func(i, j int) bool { return *offers[i].Details.Quantity < *offers[j].Details.Quantity })
		low, high := offers[0].Value, offers[0].Value
		valid := true
		for i, o := range offers {
			low = math.Min(low, o.Value)
			high = math.Max(high, o.Value)
			if i > 0 && *offers[i-1].Details.MaxQuantity >= *o.Details.Quantity {
				valid = false
			}
		}
		if !valid || low == high {
			continue
		}
		band := 300 / float64(len(offers))
		schedule := views.TierSchedule{Seller: storeName(key.seller), Title: key.title}
		var path strings.Builder
		for i, o := range offers {
			from, to := *o.Details.Quantity, *o.Details.MaxQuantity
			x := float64(i) * band
			y := 4 + 32*(high-o.Value)/(high-low)
			if i == 0 || *offers[i-1].Details.MaxQuantity+1 != from {
				fmt.Fprintf(&path, "M%.2f %.2f ", x, y)
			} else {
				fmt.Fprintf(&path, "V%.2f ", y)
			}
			fmt.Fprintf(&path, "H%.2f ", x+band)
			label := grouped(from)
			if to != from {
				label += "–" + grouped(to)
			}
			schedule.Steps = append(schedule.Steps, views.TierStep{Range: label + " шт.", Amount: rubles(o.Value), URL: cmpURL(p, p.From, p.To, from), Active: selected >= from && selected <= to})
		}
		schedule.Path = path.String()
		schedules = append(schedules, schedule)
	}
	sort.Slice(schedules, func(i, j int) bool {
		if schedules[i].Seller != schedules[j].Seller {
			return schedules[i].Seller < schedules[j].Seller
		}
		return schedules[i].Title < schedules[j].Title
	})
	return schedules
}
