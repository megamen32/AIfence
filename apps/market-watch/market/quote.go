package market

import (
	"context"
	"fmt"
	"math"
	"sort"
)

type QuoteInput struct {
	SKU      string `json:"sku"`
	Quantity int64  `json:"quantity"`
}
type OrderRow struct {
	Offer Observation `json:"offer"`
	Total float64     `json:"total"`
}
type OrderGroup struct {
	Rows       []OrderRow `json:"rows"`
	Comparable bool       `json:"comparable"`
}
type OrderQuote struct {
	Quantity  int64        `json:"quantity"`
	Groups    []OrderGroup `json:"groups"`
	Truncated bool         `json:"truncated"`
}

func (s *Service) OrderQuote(ctx context.Context, in QuoteInput) (OrderQuote, error) {
	return s.QuoteSKU(ctx, in.SKU, in.Quantity)
}

func (s *Service) QuoteSKU(ctx context.Context, sku string, quantity int64) (OrderQuote, error) {
	current, err := s.CompareSKU(ctx, CompareInput{SKU: sku})
	if err != nil {
		return OrderQuote{}, err
	}
	return QuoteOffers(current, quantity)
}
func QuoteOffers(current Comparison, quantity int64) (OrderQuote, error) {
	out := OrderQuote{Quantity: quantity, Groups: []OrderGroup{}}
	if quantity < 1 {
		return out, fmt.Errorf("positive order quantity required")
	}
	out.Truncated = current.Truncated
	type quoteKey struct {
		comparisonTerms
		basis string
	}
	positions := map[quoteKey]int{}
	for _, g := range current.Groups {
		for _, o := range g.Rows {
			t := termsOf(o)
			d := o.Details
			if keysInvalidTerms(o) || d.Quantity == nil {
				continue
			}
			cents := int64(math.Round(o.Value * 100))
			switch d.PriceBasis {
			case "per_unit":
				if d.MaxQuantity == nil || quantity < *d.Quantity || quantity > *d.MaxQuantity {
					continue
				}
				if cents <= 0 || quantity > (1<<53-1)/cents {
					return out, fmt.Errorf("order exceeds supported monetary precision")
				}
				cents *= quantity
			case "displayed_total":
				if quantity != *d.Quantity || (d.MaxQuantity != nil && *d.MaxQuantity != quantity) {
					continue
				}
			default:
				continue
			}
			key := quoteKey{t, d.PriceBasis}
			if t.months > 0 && t.packSize > 0 && t.licenseTypes != 0 && t.currency != "" {
				key.basis = "order_total"
			}
			idx, ok := positions[key]
			if !ok {
				idx = len(out.Groups)
				positions[key] = idx
				out.Groups = append(out.Groups, OrderGroup{})
			}
			out.Groups[idx].Rows = append(out.Groups[idx].Rows, OrderRow{o, price(cents)})
		}
	}
	for i := range out.Groups {
		g := &out.Groups[i]
		sellers := map[string]bool{}
		for _, r := range g.Rows {
			sellers[r.Offer.Details.Seller] = true
		}
		g.Comparable = !out.Truncated && !current.Partial && len(sellers) > 1
		sort.SliceStable(g.Rows, func(i, j int) bool { return g.Rows[i].Total < g.Rows[j].Total })
	}
	return out, nil
}
