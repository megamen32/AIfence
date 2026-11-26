package product

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type InvestigateInput struct {
	SnapshotID string `json:"snapshot_id"`
	ProductKey string `json:"product_key,omitempty"`
	WeekFrom   string `json:"week_from,omitempty"`
}
type salesDetails struct {
	Variant      string  `json:"variant"`
	BaselineMean float64 `json:"baseline_mean"`
	CurrentMean  float64 `json:"current_mean"`
	CurrentWeeks int     `json:"current_weeks"`
	BaselineFrom string  `json:"baseline_from"`
	BaselineTo   string  `json:"baseline_to"`
	CurrentFrom  string  `json:"current_from"`
}
type signal struct {
	row    evidence.Row
	meta   salesDetails
	change float64
}

func salesSignals(rows []evidence.Row) []signal {
	out := []signal{}
	for _, r := range rows {
		if r.Source != "sales" || r.Metric != "family_orders_idx.relative_change_pct" || r.Status != "observed" {
			continue
		}
		var d salesDetails
		if json.Unmarshal([]byte(r.Details), &d) != nil || d.Variant != "full_weeks" || d.BaselineMean <= 0 || d.CurrentWeeks < 1 {
			continue
		}
		out = append(out, signal{r, d, 100 * (d.CurrentMean/d.BaselineMean - 1)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].change < out[j].change })
	return out
}
func (s Service) Investigate(ctx context.Context, in InvestigateInput) (decision.Record, error) {
	var rows []evidence.Row
	for offset := 0; ; offset += 100 {
		batch, err := s.Evidence.Read(ctx, evidence.Query{SnapshotID: in.SnapshotID, Offset: offset, Limit: 100})
		if err != nil {
			return decision.Record{}, err
		}
		rows = append(rows, batch.Rows...)
		if len(rows) >= batch.Total {
			break
		}
	}
	draft, err := investigationDraft(rows, in)
	if err != nil {
		return decision.Record{}, err
	}
	rec, err := s.Get(ctx, decision.GetInput{HypothesisID: draft.Hypothesis.ID})
	switch {
	case err == nil:
		rec, err = s.refreshBrowser(ctx, rec, draft)
	case errors.Is(err, decision.ErrMissing):
		rec, err = s.Save(ctx, draft)
		if errors.Is(err, decision.ErrVersion) {
			rec, err = s.Get(ctx, decision.GetInput{HypothesisID: draft.Hypothesis.ID})
		}
	}
	if err == nil {
		_, err = s.SetFocus(ctx, Focus{HypothesisID: rec.Hypothesis.ID})
	}
	if err == nil {
		s.Mail.publish(ctx, rec, draft, rows)
	}
	return rec, err
}

func proposeLegacy(rows []evidence.Row, snapshot, product string) (decision.SaveInput, error) {
	ss := salesSignals(rows)
	candidate := slices.IndexFunc(ss, func(s signal) bool { return product == "" || s.row.Product == product })
	if candidate == -1 || ss[candidate].change >= 0 {
		return decision.SaveInput{}, errors.New("нет снижения индекса заказов по полным неделям")
	}
	p := ss[candidate]
	src := p.row
	h := decision.Hypothesis{SchemaVersion: "aifence.v1", ProductKey: src.Product, Title: "Проверить снижение самостоятельных заказов " + src.Product, SignalIDs: []string{src.ID}, SupportIDs: []string{}, CounterIDs: []string{}, Unknowns: []string{}, Status: "proposed", EconomicEffect: decision.EconomicEffect{Status: "unknown", Assumptions: []string{}}}
	sum := sha256.Sum256([]byte(src.Product + "|" + src.Metric + "|" + p.meta.BaselineFrom + "|" + p.meta.BaselineTo))
	h.ID = fmt.Sprintf("orders-%x", sum[:8])
	end, err := time.Parse(time.DateOnly, src.Period.To)
	if err != nil || p.meta.CurrentWeeks < 4 {
		return decision.SaveInput{}, errors.New("нужны четыре полные недели для базы следующей проверки")
	}
	c := decision.Criterion{MetricKey: "family_orders_idx", ObservationDays: 28, BaselineFrom: end.AddDate(0, 0, -27).Format(time.DateOnly), BaselineTo: end.Format(time.DateOnly), Rule: "relative_change_pct >= 10"}
	for _, o := range ss {
		if o.row.Product != src.Product && o.row.Period == src.Period && o.meta.BaselineFrom == p.meta.BaselineFrom && o.meta.BaselineTo == p.meta.BaselineTo && o.meta.CurrentFrom == p.meta.CurrentFrom {
			h.CounterIDs = append(h.CounterIDs, o.row.ID)
			c.ControlProductKey = o.row.Product
			c.Rule = "difference_in_relative_changes_pp >= 10"
			break
		}
	}
	h.SuccessCriterion = c
	h.EvidenceLevel = "signal_only"
	if c.ControlProductKey != "" {
		h.EvidenceLevel = "correlation"
	}
	h.CompetingExplanations = []string{
		"Изменился общий спрос на продукт.",
		"Часть покупателей перешла в другой канал оформления.",
	}
	h.Forecast = "Если исправление влияет на самостоятельное оформление, индекс после релиза вырастет."
	var release, task string
	var manager, cart, price bool
	for _, r := range rows {
		if r.Product != src.Product {
			continue
		}
		var d struct {
			AffectedMetrics string   `json:"affected_metrics"`
			State           string   `json:"state"`
			StorePrice      *float64 `json:"store_price"`
			Matched         string   `json:"matched_by_source"`
		}
		_ = json.Unmarshal([]byte(r.Details), &d)
		switch r.Source {
		case "release":
			if release == "" && strings.Contains(d.AffectedMetrics, c.MetricKey) {
				release = r.ID
				h.SupportIDs = append(h.SupportIDs, r.ID)
			}
		case "backlog":
			if task == "" && d.State == "New" && (strings.Contains(strings.ToLower(r.Value), "корзин") || strings.Contains(strings.ToLower(r.Value), "событи")) {
				task = strings.TrimPrefix(r.ID, "task/")
				h.SupportIDs = append(h.SupportIDs, r.ID)
			}
		case "metrika":
			if !cart && r.Status == "missing" {
				cart = true
				h.SupportIDs = append(h.SupportIDs, r.ID)
			}
		case "market":
			v, e := strconv.ParseFloat(r.Value, 64)
			if !price && e == nil && d.StorePrice != nil && d.Matched == "Да" && *d.StorePrice <= v {
				price = true
				h.CounterIDs = append(h.CounterIDs, r.ID)
			}
		case "research":
			if !manager && strings.Contains(strings.ToLower(r.Value), "через менеджер") {
				manager = true
				h.SupportIDs = append(h.SupportIDs, r.ID)
			}
		}
	}
	h.Unknowns = append(h.Unknowns, "Неизвестно, как изменилось число оплаченных заказов с учетом всех каналов.")
	h.ProposedAction = fmt.Sprintf("Сверить оплаченные заказы %s по неделям и каналам с реестром заказов. Раздельно посчитать самостоятельные покупки и заказы через менеджера.", src.Product)
	if release != "" {
		h.ProposedAction += " Проверить выбор варианта и переход к оформлению на компьютере и телефоне. Зафиксировать этап расхождения."
	}
	if task != "" {
		h.ProposedAction += " Связать проверку регистрации событий с существующей " + task + "."
	}
	if manager {
		h.Title = "Часть покупок " + src.Product + " могла перейти к менеджерам при выборе варианта"
		h.Unknowns = append(h.Unknowns, "Интервью допускает переход покупателей к менеджеру. Размер перехода неизвестен.")
	}
	if cart {
		h.Unknowns = append(h.Unknowns, "Полнота регистрации событий требует сверки с реестром заказов.")
	}
	if price {
		h.Unknowns = append(h.Unknowns, "Сопоставимая цена относится к конкретному предложению и дате.")
	}
	return decision.SaveInput{Hypothesis: h, EvidenceSnapshotID: snapshot}, nil
}
