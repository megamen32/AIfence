package decision

import (
	"errors"
	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"
)

func number(v float64) *float64 { return &v }
func fixture() SaveInput {
	return SaveInput{EvidenceSnapshotID: "ev-" + strings.Repeat("a", 64), Hypothesis: Hypothesis{SchemaVersion: "aifence.v1", ID: "test/a+b", Title: "Synthetic renewal experiment", ProductKey: "fixture-product", SignalIDs: []string{"fixture:signal"}, SupportIDs: []string{}, CounterIDs: []string{}, Unknowns: []string{"Channel shift"}, ProposedAction: "Check the alternative channel", Status: "proposed", EconomicEffect: EconomicEffect{Status: "unknown", Assumptions: []string{}}, SuccessCriterion: Criterion{MetricKey: "weekly_index", BaselineFrom: "2025-01-06", BaselineTo: "2025-02-02", ObservationDays: 28, Rule: "difference_in_relative_changes_pp >= 10", ControlProductKey: "fixture-control"}}}
}
func measuredFixture() (Record, EvaluateInput, time.Time) {
	in := fixture()
	r := Record{Hypothesis: in.Hypothesis, Version: 2, EvidenceSnapshotID: in.EvidenceSnapshotID, Task: &Task{ID: "test"}}
	base := PeriodValue{ProductKey: "fixture-product", MetricKey: "weekly_index", Unit: "index", From: "2025-01-06", To: "2025-02-02", SourceRef: "synthetic:baseline", Value: number(100), Complete: true}
	// Synthetic release on a Wednesday. The next four whole weeks start Monday.
	current := base
	current.From = "2025-02-10"
	current.To = "2025-03-09"
	current.SourceRef = "synthetic:current"
	current.Value = number(125)
	cb, cc := base, current
	cb.ProductKey, cc.ProductKey = "fixture-control", "fixture-control"
	cb.SourceRef, cc.SourceRef = "synthetic:control-baseline", "synthetic:control-current"
	cb.Value, cc.Value = number(50), number(55)
	measure := &Measurement{Quality: "comparable", Kind: "product_outcome", Baseline: base, Current: current, ControlBaseline: &cb, ControlCurrent: &cc}
	return r, EvaluateInput{HypothesisID: r.Hypothesis.ID, ReleaseID: "synthetic-fix", ReleasedOn: "2025-02-05", Measurement: measure}, time.Date(2025, 3, 10, 4, 0, 0, 0, time.UTC)
}
func TestDraftVersionAndFrozenCriterion(t *testing.T) {
	in := fixture()
	r, err := draft(nil, in)
	if err != nil || r.Version != 1 {
		t.Fatal(r, err)
	}
	if _, err = draft(&r, in); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
	}
	in.ExpectedVersion = 1
	updated, err := draft(&r, in)
	if err != nil || updated.Version != 2 {
		t.Fatal(updated, err)
	}
	r.Task = &Task{ID: "persisted-task"}
	if _, err = draft(&r, in); err == nil {
		t.Fatal("changed a committed criterion")
	}
}
func TestDraftInvalidInputs(t *testing.T) {
	cases := map[string]func(*SaveInput){
		"foreign schema":     func(in *SaveInput) { in.Hypothesis.SchemaVersion = "2" },
		"missing snapshot":   func(in *SaveInput) { in.EvidenceSnapshotID = "from-user" },
		"missing evidence":   func(in *SaveInput) { in.Hypothesis.SignalIDs = nil },
		"invalid cash claim": func(in *SaveInput) { in.Hypothesis.EconomicEffect.Amount = number(10) },
		"state bypass":       func(in *SaveInput) { in.Hypothesis.Status = "supported" },
		"forged task":        func(in *SaveInput) { in.Hypothesis.BacklogRef = &BacklogRef{ID: "other"} },
		"forged release":     func(in *SaveInput) { in.Hypothesis.ReleaseID = "unverified" },
		"reversed baseline":  func(in *SaveInput) { in.Hypothesis.SuccessCriterion.BaselineFrom = "2026-01-01" },
		"freeform rule":      func(in *SaveInput) { in.Hypothesis.SuccessCriterion.Rule = "improve" },
		"control discarded":  func(in *SaveInput) { in.Hypothesis.SuccessCriterion.Rule = "relative_change_pct >= 1" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			in := fixture()
			edit(&in)
			if err := checkDraft(in); err == nil {
				t.Fatal("invalid draft accepted")
			}
		})
	}
}
func TestTaskStableScopedAndLinkable(t *testing.T) {
	in := fixture()
	r, _ := draft(nil, in)
	task := newTask("alice", r)
	retry := newTask("alice", r)
	link, err := url.Parse(task.URL)
	if err != nil || link.Query().Get("id") != r.Hypothesis.ID || task.ID != retry.ID || task.ID == newTask("bob", r).ID || task.Tag != TaskTag || task.Criterion != r.Hypothesis.SuccessCriterion || task.EvidenceSnapshotID != r.EvidenceSnapshotID {
		t.Fatal(task, err)
	}
}
func TestProductEffectUsesPreRegisteredControlAndNeverMoney(t *testing.T) {
	r, in, now := measuredFixture()
	got, err := assess(r, in, now)
	if err != nil || got.Status != "supported" || got.Value == nil || *got.Value != 15 || got.Unit != "п.п." || len(got.EvidenceRefs) != 4 {
		t.Fatal(got, err)
	}
	in.Measurement.Current.Value = number(110)
	got, err = assess(r, in, now)
	if err != nil || got.Status != "rejected" || *got.Value != 0 {
		t.Fatal(got, err)
	}
	r.Hypothesis.SuccessCriterion.ControlProductKey = ""
	r.Hypothesis.SuccessCriterion.Rule = "relative_change_pct <= 10"
	got, err = assess(r, in, now)
	if err != nil || got.Status != "supported" || *got.Value != 10 || got.Unit != "%" {
		t.Fatal(got, err)
	}
}
func TestAwaitComparableObservedWindows(t *testing.T) {
	cases := map[string]func(*EvaluateInput){
		"missing readout":      func(in *EvaluateInput) { in.Measurement = nil },
		"measurement repair":   func(in *EvaluateInput) { in.Measurement.Kind = "measurement_repair" },
		"tracking break":       func(in *EvaluateInput) { in.Measurement.Quality = "tracking_break" },
		"last week incomplete": func(in *EvaluateInput) { in.Measurement.Current.Complete = false },
		"metric mismatch":      func(in *EvaluateInput) { in.Measurement.Current.MetricKey = "cart_clicks" },
		"different product":    func(in *EvaluateInput) { in.Measurement.Current.ProductKey = "another" },
		"mixed units":          func(in *EvaluateInput) { in.Measurement.Current.Unit = "count" },
		"mixed segment":        func(in *EvaluateInput) { in.Measurement.Current.Segment = "enterprise" },
		"missing source":       func(in *EvaluateInput) { in.Measurement.Current.SourceRef = "" },
		"zero baseline":        func(in *EvaluateInput) { in.Measurement.Baseline.Value = number(0) },
		"missing baseline":     func(in *EvaluateInput) { in.Measurement.Baseline.Value = nil },
		"nonfinite":            func(in *EvaluateInput) { in.Measurement.Current.Value = number(math.Inf(1)) },
		"cherry picked window": func(in *EvaluateInput) {
			in.Measurement.Current.From = "2025-02-17"
			in.Measurement.Current.To = "2025-03-16"
		},
		"overlap release":       func(in *EvaluateInput) { in.Measurement.Current.From = in.ReleasedOn },
		"missing control":       func(in *EvaluateInput) { in.Measurement.ControlCurrent = nil },
		"wrong control period":  func(in *EvaluateInput) { in.Measurement.ControlCurrent.From = "2025-01-01" },
		"wrong control product": func(in *EvaluateInput) { in.Measurement.ControlBaseline.ProductKey = "fixture-product" },
		"zero control baseline": func(in *EvaluateInput) { in.Measurement.ControlBaseline.Value = number(0) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r, in, now := measuredFixture()
			change(&in)
			a, err := assess(r, in, now)
			if err != nil || a.Status != "awaiting_measurement" || a.Value != nil || a.Reason == "" {
				t.Fatal(a, err)
			}
		})
	}
	r, in, now := measuredFixture()
	a, err := assess(r, in, now.Add(-24*time.Hour))
	if err != nil || a.Status != "awaiting_measurement" || a.Value != nil {
		t.Fatal("last day still open", a, err)
	}
}
func TestReleaseConstraints(t *testing.T) {
	r, in, now := measuredFixture()
	a, err := assess(r, EvaluateInput{}, now)
	if err != nil || a.Status != "awaiting_measurement" || a.Value != nil {
		t.Fatal(a, err)
	}
	r.Assessment = &Assessment{ReleaseID: in.ReleaseID, ReleasedOn: in.ReleasedOn, Status: "awaiting_measurement"}
	in.ReleasedOn = "2025-02-06"
	if _, err := assess(r, in, now); err == nil {
		t.Fatal("moved fixed release")
	}
	r.Assessment = nil
	in.ReleasedOn = "2025-02-02"
	if _, err := assess(r, in, now); err == nil {
		t.Fatal("release inside baseline accepted")
	}
	in.ReleasedOn = "2025-04-01"
	if _, err := assess(r, in, now); err == nil {
		t.Fatal("future release accepted")
	}
	r.Task = nil
	in.ReleasedOn = "2025-02-05"
	if _, err := assess(r, in, now); err == nil {
		t.Fatal("unapproved task used")
	}
}
func TestCallerScopeFailsClosed(t *testing.T) {
	ctx := testutil.WithCaller(t.Context(), wire.Caller{Kind: "application", Access: wire.AccessAdmin, Origin: wire.CallerOrigin{Interface: "application", Execution: "startup"}})
	if _, err := owner(ctx); err == nil {
		t.Fatal("anonymous or missing identity accepted")
	}
}
