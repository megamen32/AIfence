package sourcewatch

import (
	"context"
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
