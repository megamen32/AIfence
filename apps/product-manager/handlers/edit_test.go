package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"errors"
	"net/url"
	"testing"
)

func TestEditOnlyDraftFields(t *testing.T) {
	h := decision.Hypothesis{ID: "fixture", ProductKey: "product", SignalIDs: []string{"s1"}, SuccessCriterion: decision.Criterion{ControlProductKey: "control", ObservationDays: 28}}
	rec := decision.Record{Version: 2, EvidenceSnapshotID: "snapshot", Hypothesis: h}
	vals := url.Values{"title": {"Проверка"}, "action": {"Сверить данные"}, "version": {"2"}, "operator": {"<="}, "threshold": {"-3,5"}, "evidence_snapshot_id": {"other"}, "product_key": {"other"}}
	in, err := editInput(rec, vals)
	if err != nil || in.Hypothesis.SuccessCriterion.Rule != "difference_in_relative_changes_pp <= -3.5" || in.Hypothesis.SuccessCriterion.ObservationDays != 28 || in.Hypothesis.ProductKey != "product" || in.EvidenceSnapshotID != "snapshot" || in.ExpectedVersion != 2 || in.Hypothesis.SignalIDs[0] != "s1" {
		t.Fatal(in, err)
	}
	for _, n := range []string{"NaN", "Inf", "5 e", "1e309", "", "1+3"} {
		vals.Set("threshold", n)
		if _, err := editInput(rec, vals); err == nil {
			t.Fatal(n)
		}
	}
	vals.Set("threshold", "10")
	vals.Set("version", "1")
	if _, err := editInput(rec, vals); !errors.Is(err, decision.ErrVersion) {
		t.Fatal(err)
	}
	vals.Set("version", "2")
	rec.Task = &decision.Task{ID: "already-approved"}
	if _, err := editInput(rec, vals); !errors.Is(err, decision.ErrVersion) {
		t.Fatal(err)
	}
}
func TestHistoricalPriceShowsPairAndDate(t *testing.T) {
	r := evidence.Row{Metric: "historical_competitor_price", Value: "1265.2", Unit: "RUB", Status: "observed", Period: evidence.Period{From: "2025-11-25", To: "2025-11-25"}, Details: `{"store_price":1251.15,"seller":"Продавец"}`}
	got := rowView("s", r)
	if got.Metric != "Сравнение цен" || got.Value != "Продавец 1 265,20 ₽" || got.Summary != "Softline Store 1 251,15 ₽ · проверка 25.11.2025" {
		t.Fatal(got)
	}
}
