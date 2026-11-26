package main

import (
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
