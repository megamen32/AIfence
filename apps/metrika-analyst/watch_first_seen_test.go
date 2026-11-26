package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testFirstSeen(t *testing.T) watchRecorder {
	t.Helper()
	records := map[[2]string]watchDetection{}
	now := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	return func(_ context.Context, dataset string, ids []string) (map[string]watchDetection, error) {
		if dataset == "" || len(ids) == 0 {
			t.Fatal("missing dataset or signal identity")
		}
		out := map[string]watchDetection{}
		for _, id := range ids {
			key := [2]string{dataset, id}
			seen, ok := records[key]
			if !ok {
				seen = watchDetection{now.Format(time.RFC3339Nano), "agent_observed"}
				now = now.Add(time.Second)
				records[key] = seen
			}
			out[id] = seen
		}
		return out, nil
	}
}

func TestWatchFirstSeenAcrossSelectedTools(t *testing.T) {
	steady := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}
	fall := []float64{100, 100, 100, 100, 100, 55, 55, 55, 55, 55}
	d := blindTestDataset(t, fall, steady)
	d.record = testFirstSeen(t)
	discovery, err := d.discover(t.Context(), BlindInput{})
	if err != nil || len(discovery.Findings) == 0 {
		t.Fatalf("discovery: %v", err)
	}
	first := discovery.Findings[0]
	scan, _, err := d.scan(t.Context())
	if err != nil || len(scan.Candidates) == 0 || scan.Candidates[0].watchDetection != first.watchDetection {
		t.Fatalf("scan changed first detection: %v", err)
	}
	input := BlindCaseInput{DatasetID: d.id.String(), CandidateID: first.ID}
	result, err := d.collect(t.Context(), input)
	if err != nil || result.Document == nil || result.FirstAlertSignalID != first.SignalID ||
		result.watchDetection != first.watchDetection || result.DetectedAt != "2040-01-02T03:04:05Z" {
		t.Fatalf("collect changed first detection: %v", err)
	}
	for _, raw := range result.Document.Batches[0].Observations {
		if row := raw.(Signal); row.ID != first.SignalID || signalDetection(row) != first.watchDetection {
			t.Fatal("document dropped the first detection")
		}
	}
	wire, err := json.Marshal(result)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(wire, &fields) != nil ||
		string(fields["detected_at"]) != `"2040-01-02T03:04:05Z"` ||
		string(fields["detected_at_source"]) != `"agent_observed"` {
		t.Fatal("detection fields missing from typed result")
	}

	d.id = uuid.New()
	distinct, _, err := d.scan(t.Context())
	if err != nil || len(distinct.Candidates) == 0 || distinct.Candidates[0].SignalID != first.SignalID ||
		distinct.Candidates[0].DetectedAt == first.DetectedAt {
		t.Fatalf("dataset did not isolate first detection: %v", err)
	}
}
