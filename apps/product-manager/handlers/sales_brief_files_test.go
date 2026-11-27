package handlers

// This file runs the real salesBrief against the synthetic owner snapshots as
// delivered, without any oracle. It reproduces the hosted defect where the
// owner had imported rows but the chat summary returned zero readings.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/evidence"
)

type snapshotEnvelope struct {
	Source  string          `json:"source"`
	Status  string          `json:"status"`
	Batches []snapshotBatch `json:"batches"`
}
type snapshotBatch struct {
	Source       string        `json:"source"`
	Status       string        `json:"status"`
	Observations []snapshotRow `json:"observations"`
}
type snapshotRow struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	ProductKey string          `json:"product_key"`
	MetricKey  string          `json:"metric_key"`
	Value      json.RawMessage `json:"value"`
	Unit       string          `json:"unit"`
	Period     evidence.Period `json:"period"`
	Segment    string          `json:"segment"`
	SourceRef  struct {
		SourceID string `json:"source_id"`
		Locator  string `json:"locator"`
	} `json:"source_ref"`
	Status  string          `json:"status"`
	Details json.RawMessage `json:"details"`
}

func loadSnapshotRows(t *testing.T, path, snapshotID string) []monitorObservation {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc snapshotEnvelope
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []monitorObservation
	for _, b := range doc.Batches {
		for _, r := range b.Observations {
			row := evidence.Row{
				Kind: r.Kind, Source: b.Source, ID: r.ID, Product: r.ProductKey, Metric: r.MetricKey,
				Value: string(r.Value), Unit: r.Unit, Period: r.Period, Segment: r.Segment,
				Ref:    evidence.SourceRef{SourceID: r.SourceRef.SourceID, Locator: r.SourceRef.Locator},
				Status: r.Status, Details: string(r.Details),
			}
			o := monitorObservation{Row: row, snapshot: snapshotID}
			_ = json.Unmarshal(r.Details, &o.details)
			out = append(out, o)
		}
	}
	return out
}

// The owner imported real rows. The summary must not report an empty dataset.
func TestSalesBriefSeesLabelledSyntheticOwnerRows(t *testing.T) {
	dir := os.Getenv("SOURCEWATCH_PM_CASE_DIR")
	if dir == "" {
		t.Skip("SOURCEWATCH_PM_CASE_DIR not set; this is the explicit file-based run")
	}
	rows := loadSnapshotRows(t, filepath.Join(dir, "pm-00.json"), "ev-owner-00")
	if len(rows) != 560 {
		t.Fatalf("fixture changed: %d rows", len(rows))
	}
	// Before the fix this returned zero readings and zero facts with no explanation.
	got := salesBrief(rows, nil, BriefQuery{Summary: true, Director: true})
	raw, _ := json.Marshal(got)
	t.Logf("summary: %s", raw)
	if got.DataState == nil || got.DataState.ObservedRows != 560 || got.DataState.Snapshots != 1 {
		t.Fatalf("data state must count the owner's rows: %s", raw)
	}
	if !got.DataState.LabelledSynthetic {
		t.Fatalf("synthetic origin must stay visible: %s", raw)
	}
	if got.DataState.WatchdogAlertRows != 0 || got.DataState.NextStep == "" {
		t.Fatalf("missing watchdog alert must be explained: %s", raw)
	}
}

// The alert Metrika already computed must become a reading with its measured
// pair, control and source locators. This is the positive path of the defect.
func TestSalesBriefTurnsImportedWatchdogAlertIntoReading(t *testing.T) {
	dir := os.Getenv("SOURCEWATCH_PM_CASE_DIR")
	if dir == "" {
		t.Skip("SOURCEWATCH_PM_CASE_DIR not set; this is the explicit file-based run")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "..", "case-01", "receipts-qa-42082", "metrika-qa-case-read.json"))
	if err != nil {
		t.Skipf("watchdog case receipt not available: %v", err)
	}
	var receipt struct {
		Receipt struct {
			Transport struct {
				ResultJSON string `json:"resultJson"`
			} `json:"transport"`
		} `json:"receipt"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	var outer struct {
		Output string `json:"output"`
	}
	if err = json.Unmarshal([]byte(receipt.Receipt.Transport.ResultJSON), &outer); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Document struct {
			Batches []snapshotBatch `json:"batches"`
		} `json:"document"`
	}
	if err = json.Unmarshal([]byte(outer.Output), &doc); err != nil {
		t.Fatal(err)
	}
	var rows []monitorObservation
	for _, b := range doc.Document.Batches {
		for _, r := range b.Observations {
			row := evidence.Row{
				Kind: r.Kind, Source: b.Source, ID: r.ID, Product: r.ProductKey, Metric: r.MetricKey,
				Value: string(r.Value), Unit: r.Unit, Period: r.Period, Segment: r.Segment,
				Ref:    evidence.SourceRef{SourceID: r.SourceRef.SourceID, Locator: r.SourceRef.Locator},
				Status: r.Status, Details: string(r.Details),
			}
			o := monitorObservation{Row: row, snapshot: "ev-watchdog-case"}
			_ = json.Unmarshal(r.Details, &o.details)
			rows = append(rows, o)
		}
	}
	if len(rows) == 0 {
		t.Fatal("the watchdog case document carried no observations")
	}
	// The pure summary returns readings; directorReply is applied by the
	// handler on top of them, so assert the reading and its measured pair.
	got := salesBrief(rows, nil, BriefQuery{Summary: true})
	if len(got.Readings) != 1 || got.Total != 1 {
		b, _ := json.Marshal(got)
		t.Fatalf("imported watchdog alert did not become one reading: %s", b)
	}
	reading := got.Readings[0]
	reply := directorReply(reading)
	b, _ := json.Marshal(reply)
	// Values come from the producer's own document, never from this test.
	if reply.Change == nil || reply.Control == nil || *reply.Change >= 0 {
		t.Fatalf("measured pair lost: %s", b)
	}
	if !strings.Contains(reply.Control.Product, "Astra") || reading.Product != "Kaspersky Endpoint Security" {
		t.Fatalf("control product lost: %s", b)
	}
	if len(reply.Evidence) == 0 || !strings.Contains(reply.Evidence[0].Ref, "family=") {
		t.Fatalf("source locators lost: %s", b)
	}
	t.Logf("reading: change=%.3f control=%s %.3f evidence=%d", *reply.Change, reply.Control.Product, reply.Control.Change, len(reply.Evidence))
}
