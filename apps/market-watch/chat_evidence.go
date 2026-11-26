package main

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"agent/market"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

// Preserve the ordinary tool output. The short evidence attachment gets to
// the hosted model independently of how its calling JavaScript renders values.
func withMarketEvidence(t tool.Tool) tool.Tool {
	read := t.Execute
	t.Execute = func(ctx context.Context, input json.RawMessage, opts tool.CallOptions) (tool.Result, error) {
		result, err := read(ctx, input, opts)
		if err != nil || agentsdk.CallerFromContext(ctx).Origin().Interface != agentsdk.InterfaceChat {
			return result, err
		}
		var data []byte
		switch t.Name {
		case "list_offers":
			var page market.Evidence
			if err = json.Unmarshal([]byte(result.Output), &page); err != nil {
				return result, err
			}
			if len(page.Observations) > 6 {
				page.Observations = page.Observations[:6]
				page.Warnings = append(page.Warnings, "Attachment shows the first six observations on this page.")
			}
			data, err = json.Marshal(page)
		case "compare_offers":
			var comparison market.Comparison
			if err = json.Unmarshal([]byte(result.Output), &comparison); err != nil {
				return result, err
			}
			if len(comparison.Groups) > 6 {
				comparison.Groups = comparison.Groups[:6]
				comparison.Truncated = true
			}
			for i := range comparison.Groups {
				rows := comparison.Groups[i].Rows
				if len(rows) > 4 {
					comparison.Groups[i].Rows = rows[:4]
					comparison.Truncated = true
				}
			}
			data, err = json.Marshal(comparison)
		}
		if err != nil {
			return result, err
		}
		if len(data) > 0 && len(data) <= 24*1024 {
			result.Attachments = append(result.Attachments, tool.Attachment{
				Filename: "market-observations.txt", MimeType: "text/plain", Data: base64.StdEncoding.EncodeToString(data),
			})
		}
		return result, nil
	}
	return t
}
