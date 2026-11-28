package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/uuid"
)

// TestBlindDirScan runs the production blind scan, without a model, over every
// <BLINDDIR>/<case>/{series.json,context.json} and writes <case>.scan.json to BLINDOUT.
func TestBlindDirScan(t *testing.T) {
	root, out := os.Getenv("BLINDDIR"), os.Getenv("BLINDOUT")
	if root == "" || out == "" {
		t.Skip("BLINDDIR and BLINDOUT not set")
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "*"))
	sort.Strings(dirs)
	_ = os.MkdirAll(out, 0o755)
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, "series.json"))
		if err != nil {
			continue
		}
		var series Batch
		if err := json.Unmarshal(raw, &series); err != nil {
			t.Fatal(dir, err)
		}
		var ctxw WatchContext
		craw, _ := os.ReadFile(filepath.Join(dir, "context.json"))
		_ = json.Unmarshal(craw, &ctxw)
		d := blindDataset{id: uuid.New(), series: series, context: ctxw}
		scan, _, err := d.scan(t.Context())
		if err != nil {
			t.Fatal(dir, err)
		}
		b, _ := json.MarshalIndent(scan, "", " ")
		if err := os.WriteFile(filepath.Join(out, filepath.Base(dir)+".scan.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBlindDirCollect writes <case>/watch-docs.json for every case under BLINDCOLLECT:
// the document of the first collectable scan candidate, as the agent would collect it.
func TestBlindDirCollect(t *testing.T) {
	root := os.Getenv("BLINDCOLLECT")
	if root == "" {
		t.Skip("BLINDCOLLECT not set")
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "*"))
	sort.Strings(dirs)
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, "series.json"))
		if err != nil {
			continue
		}
		var series Batch
		if err := json.Unmarshal(raw, &series); err != nil {
			t.Fatal(dir, err)
		}
		var ctxw WatchContext
		craw, _ := os.ReadFile(filepath.Join(dir, "context.json"))
		_ = json.Unmarshal(craw, &ctxw)
		d := blindDataset{id: uuid.New(), series: series, context: ctxw, raw: craw}
		scan, _, err := d.scan(t.Context())
		if err != nil {
			t.Fatal(dir, err)
		}
		docs := []map[string]any{}
		for _, c := range scan.Candidates {
			if c.Kind != causeKind && c.Kind != "controlled_decline" {
				continue
			}
			res, err := d.collect(t.Context(), BlindCaseInput{DatasetID: d.id.String(), CandidateID: c.ID})
			if err != nil {
				t.Fatal(dir, err)
			}
			docs = append(docs, map[string]any{"snapshot": "case-" + c.Kind + "-" + c.ID[:8], "document": res.Document})
			break
		}
		b, _ := json.Marshal(docs)
		if err := os.WriteFile(filepath.Join(dir, "watch-docs.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
