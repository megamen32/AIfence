package decision

import (
	"math"
	"testing"
)

func testNumber(x float64) *float64 { return &x }
func TestRankDraftsSeparatesComparableScenarios(t *testing.T) {
	record := func(id, proof string, rub, hours *float64) Record {
		return Record{Hypothesis: Hypothesis{ID: id, Status: "proposed", EvidenceLevel: proof}, Planning: &PlanningEstimate{WeeklyPotentialRUB: rub, CheckHours: hours, Basis: "synthetic test"}}
	}
	r := testNumber
	done := record("approved", "", r(100000), r(1))
	done.Task = &Task{ID: "test"}
	done.Hypothesis.Status = "in_backlog"
	out := rankDrafts([]Record{record("signal", "signal_only", r(1000), r(2)), record("unknown", "", nil, r(1)), done, record("path", "path_verified", r(1500), r(3)), record("fast", "correlation", r(800), r(1)), record("zero", "", r(0), r(2))})
	if len(out.Ranked) != 4 || len(out.NeedsEstimate) != 1 || out.NeedsEstimate[0].HypothesisID != "unknown" {
		t.Fatal(out)
	}
	for j, id := range []string{"fast", "path", "signal", "zero"} {
		if out.Ranked[j].HypothesisID != id {
			t.Fatal(out.Ranked)
		}
	}
	if *out.Ranked[0].WeeklyPotentialPerCheckHour != 800 || out.NeedsEstimate[0].WeeklyPotentialPerCheckHour != nil {
		t.Fatal(out)
	}
}
func TestPlanningInputs(t *testing.T) {
	for _, p := range []PlanningEstimate{{Basis: "source"}, {CheckHours: testNumber(0), Basis: "source"}, {CheckHours: testNumber(math.NaN()), Basis: "source"}, {WeeklyPotentialRUB: testNumber(-1), Basis: "source"}, {WeeklyPotentialRUB: testNumber(100)}} {
		if _, err := p.normalized(); err == nil {
			t.Fatal("invalid estimate accepted", p)
		}
	}
	for _, p := range []PlanningEstimate{{}, {WeeklyPotentialRUB: testNumber(0), Basis: " source "}, {CheckHours: testNumber(0.1), Basis: "source"}} {
		if _, err := p.normalized(); err != nil {
			t.Fatal(err)
		}
	}
}
