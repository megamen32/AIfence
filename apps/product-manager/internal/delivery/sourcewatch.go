package delivery

import (
	"fmt"
	"math"
	"strings"

	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/internal/sourcewatch"
)

// BacklogItem is one code-owned signal ready for Redmine delivery. ReviewText
// is optional because a source-watch issue must be filed before its review run
// is complete.
type BacklogItem struct {
	Record           decision.Record
	ReviewText       string
	ReviewHypothesis string
	Actions          []string
	Synthetic        bool
}

// SourceWatchItem converts a pinned source-watch occurrence into the same
// delivery record used by the existing backlog path. The occurrence ID is the
// idempotency key. Before/after values are computed here, never copied from
// review prose.
func SourceWatchItem(in sourcewatch.DeliverySignal) (BacklogItem, error) {
	signalID := strings.TrimSpace(in.SignalID)
	if signalID == "" {
		signalID = strings.TrimSpace(in.Signal.SignalID)
	}
	if signalID == "" || in.Signal.SignalID != signalID {
		return BacklogItem{}, fmt.Errorf("source-watch signal has no matching occurrence ID")
	}
	contrast, err := sourceWatchContrast(in.Signal)
	if err != nil {
		return BacklogItem{}, err
	}

	productKey := strings.TrimSpace(in.Signal.ProductKey)
	if productKey == "" {
		productKey = "store"
	}
	supportIDs := sourcewatch.ReviewSignalIDs(in.Signal)
	h := decision.Hypothesis{
		SchemaVersion: "aifence.v1",
		ID:            "source-watch-" + signalID,
		Title:         fmt.Sprintf("Проверить изменение %s для %s", in.Signal.Metric, productKey),
		ProductKey:    productKey,
		SignalIDs:     []string{signalID},
		SupportIDs:    supportIDs,
		Unknowns: []string{
			"Наблюдаемое изменение само по себе не устанавливает причину.",
		},
		ProposedAction: fmt.Sprintf("Проверить источник изменения метрики %s в следующем окне.", in.Signal.Metric),
		SuccessCriterion: decision.Criterion{
			MetricKey:       in.Signal.Metric,
			BaselineFrom:    in.Signal.BaselineFrom,
			BaselineTo:      in.Signal.BaselineTo,
			ObservationDays: 7,
			Rule:            "source_watch:" + in.Signal.Trigger,
		},
		EconomicEffect: decision.EconomicEffect{Status: "unknown", Assumptions: []string{}},
		Status:         "proposed",
	}
	for _, warning := range in.Signal.SourceWarnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			h.Unknowns = append(h.Unknowns, warning)
		}
	}

	reviewText := formatReview(in.Review)
	actions := []string{"Проверить путь от выбора лицензии до корзины."}
	if len(in.Signal.PriceFamilies) > 0 || in.Signal.ContextSKU != "" {
		actions = append(actions, "Сравнить цены на ту же редакцию и срок лицензии.")
	}
	actions = append(actions, "Проверить релизы и изменения за неделю "+humanPeriod(in.Signal.CurrentFrom, in.Signal.CurrentTo)+".")
	if len(actions) > 3 {
		actions = actions[:3]
	}
	return BacklogItem{
		Record: decision.Record{
			SignalContrast:     contrast,
			Hypothesis:         h,
			EvidenceSnapshotID: "source-watch:" + signalID,
		},
		ReviewText:       reviewText,
		ReviewHypothesis: reviewHypothesis(in.Review),
		Actions:          actions,
		Synthetic:        in.Signal.FamilyChange != nil && in.Signal.FamilyChange.Synthetic,
	}, nil
}

func sourceWatchContrast(signal sourcewatch.Signal) (*evidence.WindowContrast, error) {
	if signal.BaselineFrom == "" || signal.BaselineTo == "" || signal.CurrentFrom == "" || signal.CurrentTo == "" {
		return nil, fmt.Errorf("source-watch signal %s has incomplete comparison periods", signal.SignalID)
	}
	if math.IsNaN(signal.ChangePct) || math.IsInf(signal.ChangePct, 0) || signal.ChangePct < -100 {
		return nil, fmt.Errorf("source-watch signal %s has an invalid relative change", signal.SignalID)
	}

	baseline, current := 100.0, 100.0+signal.ChangePct
	basis := "source_watch_normalized_index"
	if family := signal.FamilyChange; family != nil {
		if family.Baseline <= 0 || family.Current < 0 || math.IsNaN(family.Baseline) || math.IsNaN(family.Current) || math.IsInf(family.Baseline, 0) || math.IsInf(family.Current, 0) {
			return nil, fmt.Errorf("source-watch signal %s has invalid before/after values", signal.SignalID)
		}
		baseline, current = family.Baseline, family.Current
		basis = "source_watch_family_change"
	}
	return &evidence.WindowContrast{
		Basis:             basis,
		Baseline:          evidence.Period{From: signal.BaselineFrom, To: signal.BaselineTo},
		Current:           evidence.Period{From: signal.CurrentFrom, To: signal.CurrentTo},
		WindowWeeks:       1,
		BaselineMean:      baseline,
		CurrentMean:       current,
		RelativeChangePct: signal.ChangePct,
	}, nil
}

func formatReview(review *sourcewatch.ReviewOutput) string {
	if review == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Гипотеза: %s\n", strings.TrimSpace(review.Hypothesis))
	if len(review.EvidenceIDs) > 0 {
		fmt.Fprintf(&b, "Evidence: %s\n", strings.Join(review.EvidenceIDs, ", "))
	}
	if len(review.Missing) > 0 {
		fmt.Fprintf(&b, "Неизвестно: %s\n", strings.Join(review.Missing, "; "))
	}
	fmt.Fprintf(&b, "Следующая проверка: %s, порог %.1f%%, %d дн.", review.Test.Metric, review.Test.ThresholdPct, review.Test.Days)
	return strings.TrimSpace(b.String())
}

func reviewHypothesis(review *sourcewatch.ReviewOutput) string {
	if review == nil {
		return ""
	}
	return strings.TrimSpace(review.Hypothesis)
}

func humanPeriod(from, to string) string {
	if from == "" || to == "" {
		return "выбранную неделю"
	}
	return from + " – " + to
}
