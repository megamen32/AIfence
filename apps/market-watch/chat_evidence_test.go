package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
	"github.com/airlockrun/goai/tool"
)

func TestChatQuoteKeepsComparisonLimitsAndSource(t *testing.T) {
	const reply = `{"sku":"KL4210RAKDS","groups":[{"comparable":false,"reason":"unknown quantity","rows":[{"product_key":"KL4210RAKDS","source_ref":{"locator":"catalog/8"}}]}],"view_path":"/compare"}`
	source := tool.Tool{Name: "compare_offers", Execute: func(context.Context, json.RawMessage, tool.CallOptions) (tool.Result, error) {
		return tool.Result{Output: reply}, nil
	}}
	u := &wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	ctx := testutil.WithCaller(t.Context(), wire.Caller{Kind: "user", Access: wire.AccessUser, User: u, Initiator: u, Origin: wire.CallerOrigin{Interface: "chat", Execution: "request"}})
	result, err := withMarketEvidence(source).Execute(ctx, nil, tool.CallOptions{})
	if err != nil || result.Output != reply || len(result.Attachments) != 1 {
		t.Fatalf("unexpected comparison result: %v", err)
	}
	body, err := base64.StdEncoding.DecodeString(result.Attachments[0].Data)
	if err != nil || !strings.Contains(string(body), "unknown quantity") || !strings.Contains(string(body), "catalog/8") {
		t.Fatal("comparison evidence lost its limit or source")
	}
}
