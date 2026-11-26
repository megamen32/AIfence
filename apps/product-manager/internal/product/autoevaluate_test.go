package product

import (
	"testing"
	"time"

	"agent/internal/decision"
	"agent/internal/evidence"
)

func measurementFixture() (decision.Record, []followupRow) {
	r := decision.Record{
		Task: &decision.Task{ID: "T"}, EvidenceSnapshotID: "original",
		Hypothesis: decision.Hypothesis{ID: "H", ProductKey: "target", Status: "in_backlog", SuccessCriterion: decision.Criterion{
			MetricKey: "orders", BaselineFrom: "2025-01-06", BaselineTo: "2025-02-02", ObservationDays: 28, ControlProductKey: "control",
		}},
	}
	before := evidence.Period{From: "2025-01-06", To: "2025-02-02"}
	after := evidence.Period{From: "2025-02-10", To: "2025-03-09"}
	outcome := func(snapshot, id, product, v string, p evidence.Period) followupRow {
		row := followupRow{snapshot: snapshot, Row: evidence.Row{ID: id, Source: "sales", Kind: "metric", Product: product, Metric: "orders", Status: "observed", Period: p, Value: v, Unit: "orders", Segment: "all", Ref: evidence.SourceRef{SourceID: "orders-system", Locator: id}}}
		row.meta.measurementMeta = measurementMeta{Complete: true, Quality: "comparable", Kind: "product_outcome"}
		return row
	}
	release := followupRow{snapshot: "release", Row: evidence.Row{ID: "fix", Kind: "release", Source: "release", Product: "target", Status: "observed", Period: evidence.Period{From: "2025-02-05", To: "2025-02-05"}}}
	release.meta.HypothesisID = "H"
	return r, []followupRow{release, outcome("original", "b", "target", "80", before), outcome("original", "cb", "control", "100", before), outcome("target", "n", "target", "100", after), outcome("control", "cn", "control", "105", after)}
}

func TestPlanFollowupAcrossSnapshots(t *testing.T) {
	r, rows := measurementFixture()
	now := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, incoming string
		limit          int
		today          time.Time
	}{
		{"release only", "release", 1, now},
		{"missing control", "target", 4, now},
		{"current window still open", "control", 5, now.AddDate(0, 0, -1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, why := planFollowup(r, tc.incoming, rows[:tc.limit], tc.today)
			if in == nil || in.Measurement != nil || in.ReleaseEvidenceRef != "release/fix" || why == "" {
				t.Fatalf("pending release = %+v, reason %q", in, why)
			}
		})
	}
	r.Assessment = &decision.Assessment{ReleaseID: "fix", ReleasedOn: "2025-02-05", ReleaseEvidenceRef: "release/fix"}
	if in, why := planFollowup(r, "target", rows[:4], now); in != nil || why == "" {
		t.Fatal("persisting an incomplete retry would add a revision")
	}
	in, why := planFollowup(r, "control", rows, now)
	if in == nil || in.Measurement == nil || why != "" {
		t.Fatalf("complete control = %+v / %q", in, why)
	}
	m := in.Measurement
	if m.Baseline.SourceRef != "original/b" || m.Current.SourceRef != "target/n" || m.ControlBaseline.SourceRef != "original/cb" || m.ControlCurrent.SourceRef != "control/cn" {
		t.Fatalf("unexpected provenance: %+v", m)
	}
}

func TestIgnoreUnusableEvidence(t *testing.T) {
	cases := map[string]func(*decision.Record, []followupRow){
		"no approved task":            func(r *decision.Record, _ []followupRow) { r.Task = nil },
		"terminal":                    func(r *decision.Record, _ []followupRow) { r.Hypothesis.Status = "rejected" },
		"release without link":        func(_ *decision.Record, x []followupRow) { x[0].meta.HypothesisID = "" },
		"release for another product": func(_ *decision.Record, x []followupRow) { x[0].Product = "other" },
		"wrong release tag":           func(_ *decision.Record, x []followupRow) { x[3].meta.ReleaseID = "other" },
		"wrong hypothesis":            func(_ *decision.Record, x []followupRow) { x[3].meta.HypothesisID = "other" },
		"repair to tracking":          func(_ *decision.Record, x []followupRow) { x[3].meta.Kind = "measurement_repair" },
		"incomplete":                  func(_ *decision.Record, x []followupRow) { x[3].meta.Complete = false },
		"missing quality":             func(_ *decision.Record, x []followupRow) { x[3].meta.Quality = "" },
		"unknown status":              func(_ *decision.Record, x []followupRow) { x[3].Status = "unknown" },
		"alien origin":                func(_ *decision.Record, x []followupRow) { x[3].Ref.SourceID = "other" },
		"wrong window":                func(_ *decision.Record, x []followupRow) { x[3].Period.To = "2025-03-08" },
		"nonfinite":                   func(_ *decision.Record, x []followupRow) { x[3].Value = "NaN" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r, rows := measurementFixture()
			change(&r, rows)
			in, _ := planFollowup(r, "control", rows, time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC))
			if in != nil && in.Measurement != nil {
				t.Fatalf("evaluated invalid evidence: %+v", in)
			}
		})
	}
}

func TestFollowupAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*followupRow)
		which   int
		measure bool
	}{
		{"duplicate source row", func(x *followupRow) { x.snapshot = "copy" }, 3, true},
		{"different values", func(x *followupRow) { x.Value = "101" }, 3, false},
		{"different locator", func(x *followupRow) { x.Ref.Locator = "other" }, 3, false},
		{"second release", func(x *followupRow) { x.ID = "another" }, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, rows := measurementFixture()
			dup := rows[tc.which]
			tc.change(&dup)
			rows = append(rows, dup)
			in, _ := planFollowup(r, "control", rows, time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC))
			if (in != nil && in.Measurement != nil) != tc.measure {
				t.Fatalf("plan: %+v", in)
			}
		})
	}
}

func TestObservedByRequiresBothSourceAndDocumentDates(t *testing.T) {
	for _, tc := range []struct {
		status, source, document string
		want                     bool
	}{
		{"ok", "2025-03-09T20:00:00Z", "2025-03-10", true},
		{"partial", "2025-03-09", "2025-03-09", true},
		{"unavailable", "2025-03-10", "2025-03-10", false},
		{"ok", "2025-03-08", "2025-03-10", false},
		{"ok", "2025-03-10", "2025-03-08", false},
		{"ok", "invalid", "2025-03-10", false},
	} {
		row := evidence.Row{BatchStatus: tc.status, BatchAsOf: tc.source, Period: evidence.Period{To: "2025-03-09"}}
		if observedBy(row, tc.document) != tc.want {
			t.Fatalf("unexpected relevance for %+v", tc)
		}
	}
}
