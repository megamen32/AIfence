package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

// TestBlind30Offline runs the production blind scan over generated case inputs
// without a model. BLIND30_DIR points at a folder of run-*/inputs/case-*;
// BLIND30_OUT receives one scan JSON per case.
func TestBlind30Offline(t *testing.T) {
	root, out := os.Getenv("BLIND30_DIR"), os.Getenv("BLIND30_OUT")
	if root == "" || out == "" {
		t.Skip("BLIND30_DIR and BLIND30_OUT not set")
	}
	if z, err := strconv.ParseFloat(os.Getenv("BLIND30_Z"), 64); err == nil {
		causeZ = z
	}
	cases, _ := filepath.Glob(filepath.Join(root, "run-*", "inputs", "case-*"))
	sort.Strings(cases)
	for _, dir := range cases {
		var series Batch
		raw, err := os.ReadFile(filepath.Join(dir, "metrika.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &series); err != nil {
			t.Fatal(dir, err)
		}
		var rels []map[string]any
		rraw, _ := os.ReadFile(filepath.Join(dir, "releases.json"))
		_ = json.Unmarshal(rraw, &rels)
		facts := []ContextFact{}
		for _, r := range rels {
			s := func(k string) string { v, _ := r[k].(string); return v }
			text := s("description")
			facts = append(facts, ContextFact{ID: s("release_id"), Kind: "release", ProductKey: s("family"), MetricKey: "release_log",
				Value: &text, Unit: "text", Period: Period{s("release_date"), s("release_date")}, Status: "observed",
				SourceRef: Ref{SourceID: "blind-releases", Locator: "release_id=" + s("release_id")},
				Details:   map[string]any{"release_type": s("release_type"), "affected_metrics": s("affected_metrics"), "synthetic": true}})
		}
		syn := true
		ctxw := WatchContext{SchemaVersion: "aifence.v1", Synthetic: &syn, Batches: map[string]ContextBatch{
			"release": {Source: "blind-releases", Status: "ok", Observations: facts},
			"metrika": {Source: "blind-metrika", Status: "ok", Observations: []ContextFact{}},
		}}
		d := blindDataset{id: uuid.New(), series: series, context: ctxw}
		scan, _, err := d.scan(t.Context())
		if err != nil {
			t.Fatal(dir, err)
		}
		rel, _ := filepath.Rel(root, dir)
		dst := filepath.Join(out, filepath.Dir(filepath.Dir(rel)), filepath.Base(rel)+".scan.json")
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		b, _ := json.MarshalIndent(scan, "", " ")
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("scanned %d cases", len(cases))
}
