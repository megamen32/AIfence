package market

import (
	catalog "agent/collector"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type SourceRef struct {
	SourceID string `json:"source_id"`
	Locator  string `json:"locator"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
}
type Details struct {
	Seller             string   `json:"seller"`
	Name               string   `json:"name"`
	Quantity           *int64   `json:"quantity,omitempty"`
	PriceBasis         string   `json:"price_basis"`
	CapturedAt         string   `json:"captured_at"`
	OldPrice           *float64 `json:"old_price,omitempty"`
	PreviousPrice      *float64 `json:"previous_price,omitempty"`
	CurrentPrice       *float64 `json:"current_price,omitempty"`
	PreviousURL        string   `json:"previous_url,omitempty"`
	PreviousCapturedAt string   `json:"previous_captured_at,omitempty"`
	MaxQuantity        *int64   `json:"max_quantity,omitempty"`
	MatchStatus        string   `json:"match_status"`
}
type Observation struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	ProductKey string    `json:"product_key"`
	MetricKey  string    `json:"metric_key"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	Period     Period    `json:"period"`
	SourceRef  SourceRef `json:"source_ref"`
	Status     string    `json:"status"`
	Currency   string    `json:"currency"`
	TaxBasis   string    `json:"tax_basis"`
	Caveats    []string  `json:"caveats"`
	Details    Details   `json:"details"`
}
type Evidence struct {
	SchemaVersion string        `json:"schema_version"`
	Source        string        `json:"source"`
	AsOf          string        `json:"as_of"`
	Status        string        `json:"status"`
	Observations  []Observation `json:"observations"`
	Warnings      []string      `json:"warnings"`
}

func empty(now time.Time) Evidence {
	return Evidence{SchemaVersion: "aifence.v1", Source: "market", AsOf: now.UTC().Format(time.RFC3339), Status: "ok", Observations: []Observation{}, Warnings: []string{}}
}
func matches(o catalog.Offer, q string) bool {
	q = strings.TrimSpace(q)
	return q == "" || strings.EqualFold(o.SKU, q) || strings.Contains(strings.ToLower(o.Name), strings.ToLower(q))
}
func price(v int64) float64 { return float64(v) / 100 }
func day(t string) string {
	if len(t) >= 10 {
		return t[:10]
	}
	return t
}
func obs(o catalog.Offer) Observation {
	var old *float64
	if o.OldPriceMinor != nil {
		p := price(*o.OldPriceMinor)
		old = &p
	}
	d := day(o.CapturedAt)
	match := "source_item_only"
	if strings.TrimSpace(o.SKU) != "" {
		match = "exact_sku"
	}
	qty, maxQty := int64(0), int64(0)
	if o.Quantity != nil {
		qty = *o.Quantity
	}
	if o.MaxQuantity != nil {
		maxQty = *o.MaxQuantity
	}
	identity := fmt.Sprintf("%s/%x/%s", o.Source, sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d/%d", o.SKU, o.URL, o.PriceBasis, qty, maxQty))), o.CapturedAt)
	return Observation{ID: identity, Kind: "offer", ProductKey: o.SKU, MetricKey: "displayed_price", Value: price(o.PriceMinor), Unit: "RUB", Currency: "RUB", TaxBasis: "unknown", Period: Period{From: d, To: d}, SourceRef: SourceRef{SourceID: o.Source, Locator: o.URL, URL: o.URL, Revision: o.CapturedAt}, Status: "observed", Caveats: []string{"Public displayed price; final terms and tax basis unknown. Check quantity and price basis."}, Details: Details{Seller: o.Source, Name: o.Name, Quantity: o.Quantity, MaxQuantity: o.MaxQuantity, PriceBasis: o.PriceBasis, CapturedAt: o.CapturedAt, OldPrice: old, MatchStatus: match}}
}
func FromSnapshot(s Snapshot, q string, limit int, now time.Time) Evidence {
	in := ListInput{ProductKey: q, Limit: pageSize(limit)}
	ev, _ := snapshotPage(s, in, nil, now)
	return ev
}
func snapshotPage(s Snapshot, in ListInput, license *regexp.Regexp, now time.Time) (Evidence, bool) {
	ev := empty(now)
	if s.CapturedAt != "" {
		ev.AsOf = s.CapturedAt
	}
	ev.Warnings = append(ev.Warnings, "Configured page sample only, not full catalog coverage.")
	ev.Warnings = append(ev.Warnings, s.Warnings...)
	if len(s.Runs) < 3 {
		ev.Status = "partial"
	}
	for _, r := range s.Runs {
		if r.Status != "ok" {
			ev.Status = "partial"
			ev.Warnings = append(ev.Warnings, r.Source+": "+r.Status+"; retained prices may be stale")
		}
		for _, o := range r.Offers {
			if !browseMatch(o, in, license) {
				continue
			}
			x := obs(o)
			if r.Status != "ok" {
				x.Caveats = append(x.Caveats, "Stale snapshot; latest fetch did not succeed.")
			}
			ev.Observations = append(ev.Observations, x)
		}
	}
	sort.SliceStable(ev.Observations, func(i, j int) bool {
		return offerOrder(ev.Observations[i], ev.Observations[j], in.ProductKey) < 0
	})
	ev.Observations = ev.Observations[min(in.Offset, len(ev.Observations)):]
	if len(ev.Observations) == 0 {
		ev.Status = "unavailable"
		ev.Warnings = append(ev.Warnings, "No observed matching price in configured pages.")
	}
	more := len(ev.Observations) > in.Limit
	if more {
		ev.Observations = ev.Observations[:in.Limit]
		ev.Status = "partial"
		ev.Warnings = append(ev.Warnings, moreOffersWarning)
	}
	return ev, more
}
func Compare(snaps []Snapshot, in ChangesInput, now time.Time) Evidence {
	ev := empty(now)
	ev.Warnings = append(ev.Warnings, "Configured sample only. Absence is not a price change or delisting.")
	type offerKey struct {
		source, sku, url, basis, currency string
		minQty, maxQty                    int64
	}
	first, last := map[offerKey]catalog.Offer{}, map[offerKey]catalog.Offer{}
	for _, s := range snaps {
		for _, r := range s.Runs {
			if r.Status != "ok" {
				continue
			}
			for _, o := range r.Offers {
				if !matches(o, in.ProductKey) {
					continue
				}
				k := offerKey{o.Source, o.SKU, o.URL, o.PriceBasis, o.Currency, quantityValue(o.Quantity), quantityValue(o.MaxQuantity)}
				if _, ok := first[k]; !ok {
					first[k] = o
				}
				last[k] = o
			}
		}
	}
	for k, a := range first {
		b := last[k]
		if a.CapturedAt == b.CapturedAt {
			continue
		}
		x := changeObs(a, b)
		ev.Observations = append(ev.Observations, x)
	}
	sort.Slice(ev.Observations, func(i, j int) bool { return ev.Observations[i].ID < ev.Observations[j].ID })
	if len(ev.Observations) == 0 {
		ev.Status = "unavailable"
		ev.Warnings = append(ev.Warnings, "Insufficient distinct comparable snapshots.")
	}
	return ev
}

func changeObs(a, b catalog.Offer) Observation {
	x := obs(b)
	x.ID += "/change"
	x.MetricKey = "displayed_price_change"
	x.Period = Period{From: day(a.CapturedAt), To: day(b.CapturedAt)}
	x.Value = price(b.PriceMinor - a.PriceMinor)
	before, after := price(a.PriceMinor), price(b.PriceMinor)
	x.Details.PreviousPrice = &before
	x.Details.CurrentPrice = &after
	x.Details.PreviousURL = a.URL
	x.Details.PreviousCapturedAt = a.CapturedAt
	return x
}
