package sourcewatch

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/airlockrun/agentsdk"
)

func TestResumePinnedOccurrenceNeverRecollectsChangedSources(t *testing.T) {
	original := Signal{SignalID: occurrenceSignalID("owner", JobInput{WatchID: "watch", Monday: "2026-11-23"}), ObservationID: "original-observation", Trigger: "store", ChangePct: -25, SourceWarnings: []string{"Wordstat unavailable"}}
	pinned := &ReviewInput{Signal: original, EvidenceIDs: []string{original.ObservationID}}
	calls := 0
	launch := func(_ context.Context, signal Signal) (string, error) {
		calls++
		if !reflect.DeepEqual(signal, original) {
			t.Fatalf("recovery changed pinned evidence: %+v", signal)
		}
		return "original-task", nil
	}
	read := func(context.Context, string, any, any) error {
		t.Fatal("recovery must not read a changed or newly quiet source")
		return nil
	}
	for range 2 {
		out, err := checkOrResume(context.Background(), Window{}, true, read, launch, pinned)
		if err != nil || out.Status != "submitted" || out.AnalysisRunID != "original-task" || !reflect.DeepEqual(out.Warnings, original.SourceWarnings) {
			t.Fatalf("recovery = %+v, %v", out, err)
		}
	}
	if calls != 2 {
		t.Fatalf("expected two idempotent resume requests, got %d", calls)
	}
}

func TestResumePinnedOccurrenceHonorsDisabledRoute(t *testing.T) {
	pinned := &ReviewInput{Signal: Signal{ObservationID: "original"}}
	out, err := checkOrResume(context.Background(), Window{}, false, nil, func(context.Context, Signal) (string, error) {
		t.Fatal("disabled model route must not start a task")
		return "", nil
	}, pinned)
	if err != nil || out.Status != "model_route_unapproved" {
		t.Fatalf("disabled route = %+v, %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkOrResume(ctx, Window{}, true, nil, nil, pinned); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recovery: %v", err)
	}
}

func TestReviewSubmissionRequiresEvidenceFromOriginalSignal(t *testing.T) {
	in := ReviewInput{Signal: Signal{ObservationID: "observed"}, EvidenceIDs: []string{"observed", "not-in-signal"}}
	valid := ReviewOutput{Hypothesis: "Check the checkout path", EvidenceIDs: []string{"observed"}, Test: ReviewTest{Metric: "purchase completion", ThresholdPct: 5, Days: 7}}
	decision, err := reviewSubmission(context.Background(), agentsdk.AgentReviewContext{}, agentsdk.AgentSubmission[ReviewInput, ReviewOutput]{Input: in, Output: valid})
	if err != nil || decision.Kind != "accept" {
		t.Fatalf("valid review = %+v, %v", decision, err)
	}
	valid.EvidenceIDs = []string{"not-in-signal"}
	decision, err = reviewSubmission(context.Background(), agentsdk.AgentReviewContext{}, agentsdk.AgentSubmission[ReviewInput, ReviewOutput]{Input: in, Output: valid})
	if err != nil || decision.Kind != "continue" || decision.Feedback == "" {
		t.Fatalf("foreign evidence = %+v, %v", decision, err)
	}
}

func TestMarshalReviewCarriesStableInputHash(t *testing.T) {
	in := ReviewInput{Signal: Signal{ObservationID: "observed", ChangePct: -25}, EvidenceIDs: []string{"observed"}}
	first, err := MarshalReview(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalReview(in)
	if err != nil || string(first) != string(second) {
		t.Fatalf("same review input must serialize identically: %s / %s", first, second)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(document["input_hash"], &got); err != nil || got != ReviewInputHash(in) {
		t.Fatalf("wrong input hash: %q", got)
	}
	changed := in
	changed.Signal.ChangePct = -26
	if ReviewInputHash(in) == ReviewInputHash(changed) {
		t.Fatal("changed review input reused the same hash")
	}
}

func TestOccurrenceIdentitySurvivesRebindingAndSeparatesOwnersWeeks(t *testing.T) {
	in := JobInput{WatchID: "watch", Monday: "2026-11-23"}
	id := occurrenceSignalID("owner", in)
	for _, other := range []string{
		occurrenceSignalID("other", in),
		occurrenceSignalID("owner", JobInput{WatchID: "different", Monday: in.Monday}),
		occurrenceSignalID("owner", JobInput{WatchID: in.WatchID, Monday: "2026-11-30"}),
	} {
		if id == other {
			t.Fatal("different owner or occurrence shares a signal")
		}
	}
	if id != occurrenceSignalID("owner", in) {
		t.Fatal("retry changed occurrence identity")
	}
}

func TestNativeReviewAcceptsBothPinnedFamilyWeeks(t *testing.T) {
	in := ReviewInput{Signal: Signal{ObservationID: "current", FamilyChange: &FamilyChange{BaselineID: "baseline", CurrentID: "current"}}}
	out := ReviewOutput{Hypothesis: "Check the selected family", EvidenceIDs: []string{"baseline", "current"}, Test: ReviewTest{Metric: "purchase completion", ThresholdPct: 5, Days: 7}}
	decision, err := reviewSubmission(t.Context(), agentsdk.AgentReviewContext{}, agentsdk.AgentSubmission[ReviewInput, ReviewOutput]{Input: in, Output: out})
	if err != nil || decision.Kind != "accept" {
		t.Fatalf("native review lost family evidence: %+v, %v", decision, err)
	}
}
