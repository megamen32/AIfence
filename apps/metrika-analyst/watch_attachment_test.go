package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/airlockrun/goai/tool"
)

func TestWatchEvidenceAccompaniesOnlySuccessfulChatReads(t *testing.T) {
	const body = `{"status":"ok","as_of":"2024-01-01","findings":[{"product_key":"p","kind":"controlled_decline","relative_change_pct":-24,"source_refs":[{"source_id":"orders","locator":"sheet!B4"}]}]}`
	for _, tc := range []struct {
		surface string
		err     error
		want    bool
	}{{"chat", nil, true}, {"http", nil, false}, {"chat", errors.New("access denied"), false}} {
		source := tool.Tool{Execute: func(context.Context, json.RawMessage, tool.CallOptions) (tool.Result, error) {
			return tool.Result{Output: body}, tc.err
		}}
		out, err := withWatchFindings(source).Execute(watchdogCaller(t.Context(), true, tc.surface, "request"), nil, tool.CallOptions{})
		if !errors.Is(err, tc.err) || out.Output != body || (len(out.Attachments) == 1) != tc.want {
			t.Fatalf("unexpected result for %s: %v", tc.surface, err)
		}
		if tc.want {
			note, err := base64.StdEncoding.DecodeString(out.Attachments[0].Data)
			if err != nil || !strings.Contains(string(note), "sheet!B4") || !strings.Contains(string(note), "-24") || out.Attachments[0].MimeType != "text/plain" {
				t.Fatal("missing measured value or source in chat evidence")
			}
		}
	}
}
