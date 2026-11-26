package product

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"agent/internal/decision"
	"agent/internal/evidence"
)

const WatchdogMetric = "watchdog.alert"

type watchdogDetails struct {
	MetricKey         string          `json:"metric_key"`
	ControlProductKey string          `json:"control_product_key"`
	Baseline          evidence.Period `json:"baseline_period"`
	Current           evidence.Period `json:"current_period"`
	BaselineMean      *float64        `json:"baseline_mean"`
	CurrentMean       *float64        `json:"current_mean"`
	ControlBaseline   *float64        `json:"control_baseline_mean"`
	ControlCurrent    *float64        `json:"control_current_mean"`
	Change            *float64        `json:"relative_change_pct"`
	Adjusted          *float64        `json:"control_adjusted_change_pct"`
	EvidenceIDs       []string        `json:"evidence_ids"`
	SourceRefs        []struct {
		ID  string             `json:"id"`
		Ref evidence.SourceRef `json:"source_ref"`
	} `json:"source_refs"`
}

func investigationDraft(rows []evidence.Row, in InvestigateInput) (decision.SaveInput, error) {
	if in.WeekFrom != "" {
		monday, err := time.Parse(time.DateOnly, in.WeekFrom)
		if err != nil || monday.Weekday() != time.Monday {
			return decision.SaveInput{}, errors.New("укажите понедельник в week_from в формате YYYY-MM-DD")
		}
	}
	var selected evidence.Row
	var hasWatchdog bool
	for _, r := range rows {
		if r.Source == "sales" && r.Metric == WatchdogMetric {
			hasWatchdog = true
			if (in.ProductKey != "" && r.Product != in.ProductKey) ||
				(in.WeekFrom != "" && r.Period.From != in.WeekFrom) ||
				r.Status != "observed" || r.Value != "true" {
				continue
			}
			if selected.ID == "" || r.Period.From > selected.Period.From ||
				(r.Period.From == selected.Period.From && r.ID < selected.ID) {
				selected = r
			}
		}
	}
	if !hasWatchdog && in.WeekFrom == "" {
		return proposeLegacy(rows, in.SnapshotID, in.ProductKey)
	}
	if selected.ID == "" {
		return decision.SaveInput{}, errors.New("для выбранного продукта и недели нет наблюдаемой тревоги сторожа")
	}
	return proposeWatchdog(rows, selected, in.SnapshotID)
}

func proposeWatchdog(rows []evidence.Row, r evidence.Row, snapshot string) (decision.SaveInput, error) {
	var d watchdogDetails
	if json.Unmarshal([]byte(r.Details), &d) != nil || !d.usable(r) {
		return decision.SaveInput{}, errors.New("тревога должна иметь полные недели, положительную базу, сравнение с контролем и ссылки на исходные ряды")
	}
	key := sha256.Sum256([]byte(r.ID))
	h := decision.Hypothesis{
		SchemaVersion: "aifence.v1", ID: fmt.Sprintf("watch-%x", key[:16]),
		Title: "Проверить снижение самостоятельных заказов " + r.Product, ProductKey: r.Product,
		SignalIDs: []string{r.ID}, SupportIDs: []string{}, CounterIDs: []string{},
		Unknowns:      []string{"Изменение индекса не устанавливает причину падения или изменение прибыли."},
		EvidenceLevel: "correlation",
		CompetingExplanations: []string{
			"Часть покупателей перешла к оформлению через менеджера.",
			"Изменилось поведение покупателей или полнота регистрации событий.",
		},
		ProposedAction: "Сверить самостоятельные и сопровождаемые покупки в реестре заказов и локализовать расхождение на пути до корзины.",
		Forecast:       "Если устранить препятствие для самостоятельного оформления, после релиза показатель улучшится относительно контроля.",
		SuccessCriterion: decision.Criterion{
			MetricKey: d.MetricKey, BaselineFrom: d.Current.From, BaselineTo: d.Current.To,
			ObservationDays: 28, Rule: "difference_in_relative_changes_pp >= 10", ControlProductKey: d.ControlProductKey,
		},
		EconomicEffect: decision.EconomicEffect{Status: "unknown", Assumptions: []string{}},
		Status:         "proposed",
	}
	contextIDs := addWatchdogContext(&h, rows, d)
	return decision.SaveInput{Hypothesis: h, EvidenceSnapshotID: snapshot, ContextIDs: contextIDs}, nil
}

func (d watchdogDetails) usable(r evidence.Row) bool {
	start, e1 := time.Parse(time.DateOnly, d.Current.From)
	end, e2 := time.Parse(time.DateOnly, d.Current.To)
	baselineFrom, e3 := time.Parse(time.DateOnly, d.Baseline.From)
	baselineTo, e4 := time.Parse(time.DateOnly, d.Baseline.To)
	return d.MetricKey != "" && d.ControlProductKey != "" && d.ControlProductKey != r.Product &&
		e1 == nil && e2 == nil && e3 == nil && e4 == nil &&
		start.Weekday() == time.Monday && end.Equal(start.AddDate(0, 0, 27)) &&
		baselineFrom.Equal(start.AddDate(0, 0, -28)) && baselineTo.Equal(start.AddDate(0, 0, -1)) &&
		d.Current.To == r.Period.To && r.Period.From == end.AddDate(0, 0, -6).Format(time.DateOnly) &&
		d.BaselineMean != nil && *d.BaselineMean > 0 && d.CurrentMean != nil && *d.CurrentMean >= 0 &&
		d.ControlBaseline != nil && *d.ControlBaseline > 0 && d.ControlCurrent != nil && *d.ControlCurrent > 0 &&
		d.Change != nil && *d.Change < 0 && d.Adjusted != nil && *d.Adjusted < 0 && d.hasSourceRefs()
}

func (d watchdogDetails) hasSourceRefs() bool {
	for _, source := range d.SourceRefs {
		if slices.Contains(d.EvidenceIDs, source.ID) && source.Ref.SourceID != "" && source.Ref.Locator != "" {
			return true
		}
	}
	return false
}

func addWatchdogContext(h *decision.Hypothesis, rows []evidence.Row, d watchdogDetails) []string {
	var contextIDs []string
	latestBrowser := map[string]evidence.Row{}
	contextRows := append([]evidence.Row(nil), rows...)
	slices.SortFunc(contextRows, func(a, b evidence.Row) int {
		if a.Period.To != b.Period.To {
			return strings.Compare(b.Period.To, a.Period.To)
		}
		return strings.Compare(a.ID, b.ID)
	})
	contextSources := map[string]int{}
	for _, row := range contextRows {
		if row.Source == "browser" {
			if row.Kind == "path_check" && row.Product == h.ProductKey && latestBrowser[row.Segment].ID == "" {
				latestBrowser[row.Segment] = row
			}
			continue
		}
		if contextSources[row.Source] == 4 || !watchdogRelated(row, h.ProductKey, d) {
			continue
		}
		contextSources[row.Source]++
		var meta struct {
			Direction string `json:"evidence_direction"`
		}
		_ = json.Unmarshal([]byte(row.Details), &meta)
		switch {
		case row.Status == "observed" && (meta.Direction == "against" || meta.Direction == "counter"):
			h.CounterIDs = append(h.CounterIDs, row.ID)
		case row.Status == "observed" && (meta.Direction == "for" || meta.Direction == "support"):
			h.SupportIDs = append(h.SupportIDs, row.ID)
		default:
			contextIDs = append(contextIDs, row.ID)
		}
	}
	for _, row := range contextRows {
		if row.Source == "browser" && latestBrowser[row.Segment].ID == row.ID {
			h.BrowserEvidenceIDs = append(h.BrowserEvidenceIDs, row.ID)
			if verifiedPath(row) {
				h.EvidenceLevel = "path_verified"
			}
		}
	}
	if h.EvidenceLevel == "path_verified" {
		h.Unknowns = append(h.Unknowns, browserCaveat)
	} else {
		h.Unknowns = append(h.Unknowns, "Исход проверки пути в браузере пока неизвестен.")
	}
	return contextIDs
}

const browserCaveat = "Браузерная проверка относится к дате проверки. Связь её результата с историческим падением требует измерения."

func watchdogRelated(row evidence.Row, product string, d watchdogDetails) bool {
	switch row.Source {
	case "release", "backlog", "metrika", "research", "market":
	default:
		return false
	}
	if row.Product != product && row.Product != "all" {
		return false
	}
	var meta struct {
		KnownFrom string `json:"known_from"`
	}
	_ = json.Unmarshal([]byte(row.Details), &meta)
	return row.Period.From <= d.Current.To && (meta.KnownFrom == "" || meta.KnownFrom <= d.Current.To) &&
		(row.Period.To >= d.Baseline.From || row.Source == "research" || row.Source == "market")
}

func (s Service) refreshBrowser(ctx context.Context, prior decision.Record, next decision.SaveInput) (decision.Record, error) {
	if prior.Task != nil || prior.Hypothesis.EvidenceLevel == "path_verified" ||
		len(next.Hypothesis.BrowserEvidenceIDs) == 0 || prior.EvidenceSnapshotID == next.EvidenceSnapshotID {
		return prior, nil
	}
	h := prior.Hypothesis
	added := slices.ContainsFunc(next.Hypothesis.BrowserEvidenceIDs, func(id string) bool {
		return !slices.Contains(h.BrowserEvidenceIDs, id)
	})
	if !added {
		return prior, nil
	}
	h.BrowserEvidenceIDs = next.Hypothesis.BrowserEvidenceIDs
	h.EvidenceLevel = next.Hypothesis.EvidenceLevel
	if h.EvidenceLevel == "path_verified" {
		h.Unknowns = slices.DeleteFunc(h.Unknowns, func(v string) bool {
			return v == "Исход проверки пути в браузере пока неизвестен."
		})
		if !slices.Contains(h.Unknowns, browserCaveat) {
			h.Unknowns = append(h.Unknowns, browserCaveat)
		}
	}
	next.Hypothesis, next.ExpectedVersion, next.ContextIDs = h, prior.Version, prior.ContextIDs
	r, err := s.Save(ctx, next)
	if errors.Is(err, decision.ErrVersion) {
		return s.Get(ctx, decision.GetInput{HypothesisID: h.ID})
	}
	return r, err
}
