package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agent/market"
	"agent/views"
)

const pageSize = 25

type Catalog interface {
	Browse(context.Context, market.ListInput) (market.Evidence, bool, error)
	Changes(context.Context, market.ChangesInput) (market.Evidence, error)
	CompareSKU(context.Context, market.CompareInput) (market.Comparison, error)
	Coverage(context.Context, struct{}) (market.CatalogStatus, error)
	CrawlCoverage(context.Context, struct{}) (market.CrawlReport, error)
	PriceHistory(context.Context, market.ChangesInput) (market.PriceHistory, error)
}

type Pages struct {
	Market      Catalog
	CatalogLink func(context.Context) string
}

var licenses = []views.Option{
	{Value: "new", Label: "Новая лицензия"},
	{Value: "renewal", Label: "Продление"},
	{Value: "upgrade", Label: "Обновление версии"},
	{Value: "subscription", Label: "Подписка"},
	{Value: "perpetual", Label: "Бессрочная"},
	{Value: "unknown", Label: "Вид не указан"},
}

func storeName(id string) string {
	switch id {
	case "softline":
		return "Softline"
	case "softmagazin":
		return "SoftMagazin"
	case "softmap":
		return "SoftMap"
	case "allsoft":
		return "Allsoft"
	case "syssoft":
		return "Syssoft"
	default:
		return id
	}
}

func grouped(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "\u202f" + s[i:]
	}
	return s
}

func rubles(v float64) string {
	k := int64(math.Round(math.Abs(v) * 100))
	s := grouped(k / 100)
	if k%100 != 0 {
		s += fmt.Sprintf(",%02d", k%100)
	}
	if v < 0 {
		s = "−" + s
	}
	return s + " ₽"
}

func dateTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return t.UTC().Format("02.01.2006 15:04 UTC")
}

func basisName(b string) string {
	switch b {
	case "per_unit":
		return "За единицу"
	case "displayed_total":
		return "За указанное количество"
	case "minimum_displayed_price":
		return "Минимальная опубликованная цена"
	case "displayed_offer_price":
		return "Цена предложения"
	case "":
		return "База цены неизвестна"
	default:
		return b
	}
}

func quantity(d market.Details) string {
	if d.Quantity == nil {
		if d.MaxQuantity != nil {
			return fmt.Sprintf("До %d шт.", *d.MaxQuantity)
		}
		return "Количество не указано"
	}
	if d.MaxQuantity != nil {
		if *d.MaxQuantity != *d.Quantity {
			return fmt.Sprintf("От %d до %d шт.", *d.Quantity, *d.MaxQuantity)
		}
	}
	if d.PriceBasis == "per_unit" && d.MaxQuantity == nil {
		return fmt.Sprintf("От %d шт.", *d.Quantity)
	}
	return fmt.Sprintf("%d шт.", *d.Quantity)
}

func link(tab, q string) string {
	p := url.Values{"tab": {tab}}
	if q != "" {
		p.Set("q", q)
	}
	return "./?" + p.Encode()
}

func tableRow(o market.Observation) views.Row {
	d := o.Details
	sku := strings.TrimSpace(o.ProductKey)
	r := views.Row{SKU: sku, Name: d.Name, Store: storeName(d.Seller), Price: rubles(o.Value), At: dateTime(d.CapturedAt), URL: o.SourceRef.URL, Basis: basisName(d.PriceBasis), Quantity: quantity(d)}
	r.SourceID = d.Seller
	if sku != "" {
		r.CompareURL = link("compare", sku)
	}
	q := sku
	if q == "" {
		q = o.SourceRef.URL
	}
	if q != "" {
		r.HistoryURL = link("changes", q)
	}
	if d.CurrentPrice != nil && d.PreviousPrice != nil {
		r.Previous = rubles(*d.PreviousPrice)
		r.PreviousAt = dateTime(d.PreviousCapturedAt)
		r.Price = rubles(*d.CurrentPrice)
		r.Delta = rubles(o.Value)
		if o.Value > 0 {
			r.Delta = "+" + r.Delta
		}
	}
	return r
}

func pageLink(p views.Page, n int) string {
	q := url.Values{"tab": {"catalog"}}
	if n > 1 {
		q.Set("page", strconv.Itoa(n))
	}
	for k, v := range map[string]string{"q": p.Query, "source": p.Source, "license": p.License} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return "./?" + q.Encode()
}

func (h Pages) Home(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	now := time.Now().UTC()
	p := views.Page{Tab: q.Get("tab"), Query: strings.TrimSpace(q.Get("q")), Source: q.Get("source"), License: q.Get("license"), From: q.Get("from"), To: q.Get("to"), Licenses: licenses, CatalogURL: link("catalog", ""), CompareURL: link("compare", ""), ChangesURL: link("changes", ""), SourcesURL: link("sources", "")}
	if h.CatalogLink != nil {
		p.BaserowURL = h.CatalogLink(r.Context())
	}
	if p.Tab == "" {
		p.Tab = "catalog"
	}
	if p.From == "" {
		p.From = now.AddDate(0, 0, -6).Format(time.DateOnly)
	}
	if p.To == "" {
		p.To = now.Format(time.DateOnly)
	}
	partial := strings.EqualFold(r.Header.Get("HX-Request"), "true") && r.Header.Get("HX-History-Restore-Request") != "true"
	if !partial || p.Tab == "sources" {
		status, err := h.Market.Coverage(r.Context(), struct{}{})
		if err != nil {
			return err
		}
		p.Total = grouped(status.Total)
		var latest time.Time
		for _, s := range status.Sources {
			t, _ := time.Parse(time.RFC3339Nano, s.LastObservation)
			if t.After(latest) {
				latest = t
			}
			p.Sources = append(p.Sources, views.SourceRow{ID: s.Source, Name: storeName(s.Source), Count: grouped(s.Offers), Last: dateTime(s.LastObservation)})
		}
		if !latest.IsZero() {
			p.Freshness = dateTime(latest.Format(time.RFC3339))
		}
	}
	var err error
	switch p.Tab {
	case "catalog":
		err = h.catalog(r.Context(), &p, q.Get("page"))
	case "compare":
		err = h.compare(r.Context(), &p, q.Get("qty"))
	case "changes":
		err = h.changes(r.Context(), &p)
	case "sources":
		err = h.sources(r.Context(), &p)
	default:
		http.NotFound(w, r)
		return nil
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Add("Vary", "HX-Request, HX-History-Restore-Request")
	if p.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	if partial {
		return views.Results(p).Render(r.Context(), w)
	}
	return views.Screen(p).Render(r.Context(), w)
}

func (h Pages) catalog(ctx context.Context, p *views.Page, page string) error {
	n := 1
	var err error
	if page != "" {
		n, err = strconv.Atoi(page)
	}
	if err != nil || n < 1 || n > (math.MaxInt32/pageSize) {
		p.Error = "Укажите корректный номер страницы."
		return nil
	}
	if p.Source != "" && storeName(p.Source) == p.Source {
		p.Error = "Выберите магазин из списка."
		return nil
	}
	if p.License != "" {
		found := false
		for _, l := range licenses {
			if l.Value == p.License {
				found = true
				break
			}
		}
		if !found {
			p.Error = "Выберите вид лицензии из списка."
			return nil
		}
	}
	offset := (n - 1) * pageSize
	ev, more, err := h.Market.Browse(ctx, market.ListInput{ProductKey: p.Query, Source: p.Source, LicenseType: p.License, Limit: pageSize, Offset: offset})
	if err != nil {
		return err
	}
	for _, o := range ev.Observations {
		p.Rows = append(p.Rows, tableRow(o))
	}
	if len(p.Rows) > 0 {
		p.Range = fmt.Sprintf("С %s по %s", grouped(int64(offset+1)), grouped(int64(offset+len(p.Rows))))
	}
	if more {
		p.NextURL = pageLink(*p, n+1)
	}
	if n > 1 {
		p.PrevURL = pageLink(*p, n-1)
	}
	return nil
}

func (h Pages) changes(ctx context.Context, p *views.Page) error {
	start, e1 := time.Parse(time.DateOnly, p.From)
	end, e2 := time.Parse(time.DateOnly, p.To)
	if e1 != nil || e2 != nil || end.Before(start) {
		p.Error = "Проверьте даты. Конец периода должен совпадать с началом или быть позже."
		return nil
	}
	ev, err := h.Market.Changes(ctx, market.ChangesInput{ProductKey: p.Query, From: p.From, To: p.To})
	if err != nil {
		return err
	}
	for _, o := range ev.Observations {
		if o.Value != 0 && o.Details.PreviousPrice != nil && o.Details.CurrentPrice != nil {
			p.Rows = append(p.Rows, tableRow(o))
		}
	}
	for _, warning := range ev.Warnings {
		if strings.HasPrefix(warning, "Showing the 100") {
			p.Notice = "Показаны 100 последних изменений за выбранный период."
		}
	}
	return nil
}

func (h Pages) sources(ctx context.Context, p *views.Page) error {
	report, err := h.Market.CrawlCoverage(ctx, struct{}{})
	if err != nil {
		return err
	}
	for i := range p.Sources {
		s := &p.Sources[i]
		s.CoverageNote = "Сверка URL лицензий ещё не загружена."
		for _, c := range report.Sources {
			if c.Source != s.ID {
				continue
			}
			s.CoverageAt = dateTime(c.AsOf)
			if s.CoverageAt == "" {
				s.CoverageAt = dateTime(report.AsOf)
			}
			if !c.Known {
				s.CoverageNote = "Полный список URL лицензий ещё не подтверждён."
				continue
			}
			s.Expected = grouped(int64(c.Total))
			s.Priced = grouped(int64(c.Priced))
			s.NoPrice = grouped(int64(c.NoPublicPrice))
			s.Unavailable = grouped(int64(c.Unavailable))
			s.Unresolved = grouped(int64(c.Unresolved))
			s.Excluded = grouped(int64(c.Excluded))
			s.Pending = grouped(int64(c.Pending))
			s.CoverageNote = ""
			if c.InventoryAt != "" {
				s.CoverageNote = "Инвентаризация от " + dateTime(c.InventoryAt)
			}
		}
	}
	return nil
}
