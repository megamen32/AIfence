package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointUsesOnlySDKResponseBody(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "air")
	// The official CLI writes target and HTTP status to stderr for successful requests.
	if err := os.WriteFile(cli, []byte("#!/bin/sh\necho 'Market Watch · production · default' >&2\necho 'HTTP 200' >&2\necho '{\"accepted\":1}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "offers.jsonl")
	if err := os.WriteFile(input, []byte("{\"sku\":\"test\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(dir, "receipts")
	state, err := os.OpenFile(checkpoint, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	u := uploader{air: cli, dir: dir, uploaded: map[string]bool{}, state: state}
	for _, want := range []int{1, 0} {
		n, e := u.importFile(input)
		if e != nil || n != want {
			t.Fatalf("uploaded %d, want %d: %v", n, want, e)
		}
	}
	b, err := os.ReadFile(checkpoint)
	if err != nil || len(strings.Fields(string(b))) != 1 {
		t.Fatalf("checkpoint: %q %v", b, err)
	}
}

func TestQuarantinePreservesReversedTierAndContinues(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "air")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nfor arg do last=$arg; done\nprintf '%s\\n' \"$last\" >> \"$0.batches\"\necho '{\"accepted\":1}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := "  " + `{"sku":"TIER-A","quantity":10000,"max_quantity":9999,"price_minor":635200,"name":"Кибер Файлы <sample>"}` + "   "
	valid := `{"sku":"GOOD","quantity":1,"max_quantity":1}`
	path := filepath.Join(dir, "offers.jsonl")
	writeInput := func(rows ...string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint := filepath.Join(dir, "receipts")
	// Reopen the checkpoint on each pass as scheduled invocations do.
	run := func(want int) {
		t.Helper()
		state, err := os.OpenFile(checkpoint, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		received, err := os.ReadFile(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		u := uploader{air: cli, dir: dir, state: state, uploaded: map[string]bool{}}
		for _, key := range strings.Fields(string(received)) {
			u.uploaded[key] = true
		}
		n, err := u.importFile(path)
		if err != nil || n != want {
			t.Fatalf("accepted %d, want %d: %v", n, want, err)
		}
	}
	writeInput(raw, valid)
	run(1)
	run(0)
	corrected := strings.Replace(raw, `"max_quantity":9999`, `"max_quantity":10000`, 1)
	writeInput(raw, valid, corrected)
	run(1)
	wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(raw))))
	audit := checkpoint + ".quarantine"
	files, err := os.ReadDir(audit)
	if err != nil || len(files) != 1 || files[0].Name() != wantDigest+".json" {
		t.Fatalf("audit %v: %v", files, err)
	}
	b, err := os.ReadFile(filepath.Join(audit, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var item struct {
		Raw    string `json:"raw_json"`
		Reason string `json:"reason"`
		Line   int    `json:"line"`
		Path   string `json:"source_file"`
		SHA    string `json:"record_sha256"`
	}
	if err = json.Unmarshal(b, &item); err != nil || item.Raw != raw || item.Reason != "max_quantity_less_than_quantity" || item.Line != 1 || item.Path != path || item.SHA != wantDigest {
		t.Fatalf("audit item: %+v %v", item, err)
	}
	b, err = os.ReadFile(checkpoint)
	if err != nil || strings.Contains(string(b), wantDigest) || len(strings.Fields(string(b))) != 2 {
		t.Fatalf("incorrect receipts %s: %v", b, err)
	}
	b, err = os.ReadFile(cli + ".batches")
	if err != nil || strings.Contains(string(b), `"max_quantity":9999`) || !strings.Contains(string(b), `"max_quantity":10000`) {
		t.Fatalf("wrong posted records %s: %v", b, err)
	}
}
