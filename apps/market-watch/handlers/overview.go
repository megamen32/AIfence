package handlers

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"agent/market"
	"agent/views"
)

const (
	overviewBars    = 6
	overviewChanges = 5
	barRowHeight    = 32
	barMaxWidth     = 215.0
	barLabelWidth   = 104.0
)

// percent formats a signed ratio in Russian typography: «+3,2 %», «−12,5 %».
func percent(ratio float64) string {
	v := math.Round(math.Abs(ratio)*1000) / 10
	s := strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
	switch {
	case v == 0:
		return s + " %"
	case ratio < 0:
		return "−" + s + " %"
	}
	return "+" + s + " %"
}

type weekChange struct {
	row views.ChangeRow
	abs float64
}

type storeCount struct {
	name string
	n    int64
}

// buildOverview turns stored coverage and the last week's recorded price changes into the catalog overview.
// A tile whose value cannot be computed from the data is left out. It returns nil when nothing is stored yet.
func buildOverview(status market.CatalogStatus, changes *market.Evidence) *views.Overview {
	if status.Total <= 0 {
		return nil
	}
	o := &views.Overview{}
	stores := 0
	var latest time.Time
	var list []storeCount
	for _, s := range status.Sources {
		if s.Offers <= 0 {
			continue
		}
		stores++
		list = append(list, storeCount{storeName(s.Source), s.Offers})
		if t, err := time.Parse(time.RFC3339Nano, s.LastObservation); err == nil && t.After(latest) {
			latest = t
		}
	}
	o.Tiles = append(o.Tiles, views.Tile{Label: "Предложений с ценой", Value: grouped(status.Total)})
	o.Tiles = append(o.Tiles, views.Tile{Label: "Магазинов в охвате", Value: grouped(int64(stores))})
	if status.SKUs > 0 {
		o.Tiles = append(o.Tiles, views.Tile{Label: "Артикулов лицензий", Value: grouped(status.SKUs)})
	}

	if changes != nil {
		o.ShowChanges = true
		var all []weekChange
		up, down := 0, 0
		for _, ob := range changes.Observations {
			d := ob.Details
			if d.PreviousPrice == nil || d.CurrentPrice == nil || *d.PreviousPrice <= 0 || *d.CurrentPrice == *d.PreviousPrice {
				continue
			}
			ratio := (*d.CurrentPrice - *d.PreviousPrice) / *d.PreviousPrice
			tone := "down"
			if ratio > 0 {
				up++
				tone = "up"
			} else {
				down++
			}
			r := tableRow(ob)
			all = append(all, weekChange{views.ChangeRow{Name: d.Name, SKU: r.SKU, Store: r.Store, Old: rubles(*d.PreviousPrice), New: rubles(*d.CurrentPrice), Percent: percent(ratio), Tone: tone, URL: r.HistoryURL}, math.Abs(ratio)})
		}
		sort.SliceStable(all, func(i, j int) bool {
			if all[i].abs != all[j].abs {
				return all[i].abs > all[j].abs
			}
			return all[i].row.Name < all[j].row.Name
		})
		for i := 0; i < len(all) && i < overviewChanges; i++ {
			o.Changes = append(o.Changes, all[i].row)
		}
		tile := views.Tile{Label: "Изменений цен за 7 дней", Value: grouped(int64(up + down)), Note: "с прошлого наблюдения"}
		for _, w := range changes.Warnings {
			if strings.HasPrefix(w, "Showing the 100") {
				tile.Value += "+"
			}
		}
		if up+down > 0 {
			tile.Note = fmt.Sprintf("↑ %s выросли · ↓ %s снизились", grouped(int64(up)), grouped(int64(down)))
			tile.Tone = "accent"
		}
		o.Tiles = append(o.Tiles, tile)
	}

	sort.SliceStable(list, func(i, j int) bool { return list[i].n > list[j].n })
	if len(list) > overviewBars {
		var rest int64
		for _, s := range list[overviewBars-1:] {
			rest += s.n
		}
		list = append(list[:overviewBars-1], storeCount{"Другие", rest})
	}
	var max int64
	for _, s := range list {
		if s.n > max {
			max = s.n
		}
	}
	for i, s := range list {
		w := barMaxWidth * float64(s.n) / float64(max)
		if w < 2 {
			w = 2
		}
		y := i * barRowHeight
		o.Bars = append(o.Bars, views.Bar{Label: s.name, Count: grouped(s.n), Y: fmt.Sprintf("%d", y+5), TextY: fmt.Sprintf("%d", y+21), Width: fmt.Sprintf("%.1f", w), CountX: fmt.Sprintf("%.1f", barLabelWidth+w+8), Delay: fmt.Sprintf("%dms", 250+i*90)})
	}
	o.BarsHeight = len(o.Bars) * barRowHeight
	o.Source = "Источник: публичные цены магазинов"
	if !latest.IsZero() {
		o.Source += ", последнее наблюдение " + dateTime(latest.Format(time.RFC3339))
	}
	return o
}
