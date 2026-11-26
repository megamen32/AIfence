package decision

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const TaskTag = "сгенерирована ИИ-помощником"

var snapshotPattern = regexp.MustCompile("^ev-[0-9a-f]{64}$")
var rulePattern = regexp.MustCompile("^(relative_change_pct|difference_in_relative_changes_pp)\\s*(>=|<=)\\s*([+-]?[0-9]+(?:\\.[0-9]+)?)$")
var ErrVersion = errors.New("версия карточки изменилась, перечитайте её перед сохранением")
var ErrMissing = errors.New("карточка не найдена")

func checkDraft(in SaveInput) error {
	h, c := in.Hypothesis, in.Hypothesis.SuccessCriterion
	if !snapshotPattern.MatchString(in.EvidenceSnapshotID) {
		return errors.New("требуется идентификатор исходного снимка ev-<sha256>")
	}
	if h.SchemaVersion != "aifence.v1" || strings.TrimSpace(h.ID) == "" || len(h.ID) > 256 || strings.TrimSpace(h.Title) == "" || strings.TrimSpace(h.ProductKey) == "" || strings.TrimSpace(h.ProposedAction) == "" {
		return errors.New("заполните версию схемы, ID, продукт, заголовок и проверяемое действие")
	}
	if len(h.SignalIDs) == 0 || h.SupportIDs == nil || h.CounterIDs == nil || h.Unknowns == nil || h.EconomicEffect.Assumptions == nil {
		return errors.New("укажите источники сигнала, свидетельства, неизвестные и допущения; для пустого списка передайте []")
	}
	for _, group := range [][]string{h.SignalIDs, h.SupportIDs, h.CounterIDs, h.Unknowns, h.EconomicEffect.Assumptions, h.CompetingExplanations, h.BrowserEvidenceIDs, in.ContextIDs} {
		for _, v := range group {
			if strings.TrimSpace(v) == "" {
				return errors.New("пустая ссылка или формулировка в карточке")
			}
		}
	}
	if h.Status != "proposed" || h.ReleaseID != "" || h.BacklogRef != nil {
		return errors.New("сохранять можно черновик со статусом proposed; выпуск и задачу фиксируют отдельные действия")
	}
	if in.ExpectedVersion < 0 {
		return ErrVersion
	}
	if strings.TrimSpace(c.MetricKey) == "" || c.ObservationDays < 1 || c.ObservationDays > 3650 || c.ControlProductKey == h.ProductKey {
		return errors.New("проверьте метрику, отдельный контрольный продукт и длительность наблюдения")
	}
	from, e1 := time.Parse(time.DateOnly, c.BaselineFrom)
	to, e2 := time.Parse(time.DateOnly, c.BaselineTo)
	if e1 != nil || e2 != nil || from.After(to) {
		return errors.New("проверьте даты базового периода")
	}
	parts := rulePattern.FindStringSubmatch(c.Rule)
	if len(parts) == 0 || (parts[1] == "difference_in_relative_changes_pp") != (c.ControlProductKey != "") {
		return errors.New("правило: relative_change_pct >= число или difference_in_relative_changes_pp >= число с контрольным продуктом; поддерживается также <=")
	}
	v, err := strconv.ParseFloat(parts[3], 64)
	if err != nil || !finite(v) {
		return errors.New("порог должен быть конечным числом")
	}
	switch h.EconomicEffect.Status {
	case "unknown":
		if h.EconomicEffect.Amount != nil {
			return errors.New("при неизвестном эффекте денежная сумма должна быть пустой")
		}
	case "scenario":
		if h.EconomicEffect.Amount == nil || !finite(*h.EconomicEffect.Amount) || len(h.EconomicEffect.Assumptions) == 0 || len(h.EconomicEffect.Currency) != 3 {
			return errors.New("сценарная сумма требует валюты и допущений")
		}
	default:
		return errors.New("в черновике экономический эффект может быть unknown или scenario")
	}
	return nil
}
func draft(previous *Record, in SaveInput) (Record, error) {
	if err := checkDraft(in); err != nil {
		return Record{}, err
	}
	if previous != nil && previous.Task != nil {
		return Record{}, errors.New("критерий и данные уже зафиксированы в задаче, создайте отдельную гипотезу для новой проверки")
	}
	if (previous == nil && in.ExpectedVersion != 0) || (previous != nil && in.ExpectedVersion != previous.Version) {
		return Record{}, ErrVersion
	}
	return Record{Hypothesis: in.Hypothesis, ContextIDs: in.ContextIDs, Version: in.ExpectedVersion + 1, EvidenceSnapshotID: in.EvidenceSnapshotID}, nil
}
func newTask(owner string, r Record) Task {
	h := r.Hypothesis
	sum := sha256.Sum256([]byte(owner + "\x00" + h.ID))
	return Task{ID: "PM-" + hex.EncodeToString(sum[:8]), URL: "./?id=" + url.QueryEscape(h.ID), Tag: TaskTag, HypothesisID: h.ID, Title: h.Title, Description: h.ProposedAction, Tracker: "local_prototype_backlog", EvidenceSnapshotID: r.EvidenceSnapshotID, Criterion: h.SuccessCriterion}
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func assess(r Record, in EvaluateInput, now time.Time) (Assessment, error) {
	a := Assessment{Status: "awaiting_measurement", ReleaseID: in.ReleaseID, ReleasedOn: in.ReleasedOn, ReleaseEvidenceRef: in.ReleaseEvidenceRef, EvidenceRefs: []string{}, Measurement: in.Measurement}
	if r.Assessment != nil && in.ReleaseID == "" && in.ReleasedOn == "" && in.Measurement == nil {
		return *r.Assessment, nil
	}
	if r.Assessment != nil && r.Assessment.ReleaseID != "" {
		if a.ReleaseID == "" {
			a.ReleaseID = r.Assessment.ReleaseID
		}
		if a.ReleasedOn == "" {
			a.ReleasedOn = r.Assessment.ReleasedOn
		}
		if r.Assessment.ReleaseEvidenceRef == "" && (r.Assessment.Status == "supported" || r.Assessment.Status == "rejected") {
			a.ReleaseEvidenceRef = ""
		}
		if r.Assessment.ReleaseEvidenceRef != "" {
			a.ReleaseEvidenceRef = r.Assessment.ReleaseEvidenceRef
		}
		if a.ReleaseID != r.Assessment.ReleaseID || a.ReleasedOn != r.Assessment.ReleasedOn {
			return Assessment{}, errors.New("релиз уже зафиксирован для данной проверки")
		}
	}
	if a.ReleaseID == "" && a.ReleasedOn == "" && a.Measurement == nil {
		a.Reason = "Нет зафиксированного релиза и полного наблюдения после него."
		return a, nil
	}
	if r.Task == nil {
		return Assessment{}, errors.New("сначала подтвердите задачу и зафиксируйте критерий")
	}
	released, err := time.Parse(time.DateOnly, a.ReleasedOn)
	today := now.UTC().Truncate(24 * time.Hour)
	if err != nil || strings.TrimSpace(a.ReleaseID) == "" || released.After(today) {
		return Assessment{}, errors.New("укажите ID состоявшегося релиза и его дату YYYY-MM-DD")
	}
	c := r.Hypothesis.SuccessCriterion
	if a.ReleasedOn <= c.BaselineTo {
		return Assessment{}, errors.New("базовый период должен завершиться до релиза")
	}
	if a.Measurement == nil {
		a.Reason = "Релиз зафиксирован. Ожидаются полные сопоставимые наблюдения."
		return a, nil
	}
	m := a.Measurement
	if m.Kind != "product_outcome" {
		a.Reason = "Данные о качестве регистрации событий отдельно от продуктового результата. Нужны независимые продуктовые измерения."
		return a, nil
	}
	if m.Quality != "comparable" {
		a.Reason = "Сопоставимость периодов не подтверждена."
		return a, nil
	}
	start := released.AddDate(0, 0, 1)
	// Whole-week criteria start on the next complete Monday, other windows on the next day.
	if c.ObservationDays%7 == 0 {
		start = start.AddDate(0, 0, (8-int(start.Weekday()))%7)
	}
	end := start.AddDate(0, 0, c.ObservationDays-1)
	if !end.Before(today) {
		a.Reason = fmt.Sprintf("Наблюдение заканчивается %s включительно.", end.Format(time.DateOnly))
		return a, nil
	}
	if !validPeriod(m.Baseline, r.Hypothesis.ProductKey, c.MetricKey, c.BaselineFrom, c.BaselineTo) ||
		!validPeriod(m.Current, r.Hypothesis.ProductKey, c.MetricKey, start.Format(time.DateOnly), end.Format(time.DateOnly)) ||
		m.Current.Unit != m.Baseline.Unit || m.Current.Segment != m.Baseline.Segment || *m.Baseline.Value <= 0 {
		a.Reason = "Нужны сопоставимые полные периоды, положительная база, одинаковый сегмент и единицы, ссылки на оба значения."
		return a, nil
	}
	change := 100 * (*m.Current.Value - *m.Baseline.Value) / *m.Baseline.Value
	a.EvidenceRefs = []string{m.Baseline.SourceRef, m.Current.SourceRef}
	a.Unit = "%"
	if c.ControlProductKey != "" {
		b, n := m.ControlBaseline, m.ControlCurrent
		if b == nil || n == nil || !validPeriod(*b, c.ControlProductKey, c.MetricKey, c.BaselineFrom, c.BaselineTo) || !validPeriod(*n, c.ControlProductKey, c.MetricKey, m.Current.From, m.Current.To) || b.Unit != m.Baseline.Unit || n.Unit != b.Unit || b.Segment != m.Baseline.Segment || n.Segment != b.Segment || *b.Value <= 0 {
			a.Reason = "Нужен контроль за те же периоды с теми же метрикой, единицами и сегментом."
			return a, nil
		}
		change -= 100 * (*n.Value - *b.Value) / *b.Value
		a.EvidenceRefs = append(a.EvidenceRefs, b.SourceRef, n.SourceRef)
		a.Unit = "п.п."
	}
	if !finite(change) {
		return Assessment{}, errors.New("расчёт вне диапазона конечных чисел")
	}
	parts := rulePattern.FindStringSubmatch(c.Rule)
	if len(parts) == 0 {
		return Assessment{}, errors.New("неизвестное сохранённое правило")
	}
	target, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		return Assessment{}, err
	}
	a.Value = &change
	a.Status = "rejected"
	if (parts[2] == ">=" && change >= target) || (parts[2] == "<=" && change <= target) {
		a.Status = "supported"
	}
	a.Reason = fmt.Sprintf("Расчёт %.4g %s. Зарегистрированный критерий %s. Сравнение оценивает показатель и само по себе не устанавливает причину изменения.", change, a.Unit, c.Rule)
	return a, nil
}
func validPeriod(p PeriodValue, product, metric, from, to string) bool {
	return p.Complete && p.Value != nil && finite(*p.Value) && p.ProductKey == product && p.MetricKey == metric && p.From == from && p.To == to && strings.TrimSpace(p.Unit) != "" && strings.TrimSpace(p.SourceRef) != ""
}
