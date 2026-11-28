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
