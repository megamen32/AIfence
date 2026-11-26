package main

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

// The host forwards nested tool attachments even when a chat script awaits
// the call without returning its value. Keep the original paginated response.
func withWatchFindings(t tool.Tool) tool.Tool {
	read := t.Execute
	t.Execute = func(ctx context.Context, input json.RawMessage, opts tool.CallOptions) (tool.Result, error) {
		result, err := read(ctx, input, opts)
		if err != nil || agentsdk.CallerFromContext(ctx).Origin().Interface != agentsdk.InterfaceChat {
			return result, err
		}
		var summary struct {
			Status     string           `json:"status"`
			ScanStatus string           `json:"scan_status,omitempty"`
			Origin     string           `json:"origin,omitempty"`
			AsOf       string           `json:"as_of,omitempty"`
			DatasetID  string           `json:"dataset_id,omitempty"`
			Findings   []BlindCandidate `json:"findings,omitempty"`
			Candidates []BlindCandidate `json:"candidates,omitempty"`
			Warnings   []string         `json:"warnings,omitempty"`
			Partial    bool             `json:"partial,omitempty"`
		}
		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			return result, err
		}
		if len(summary.Candidates) > 4 {
			summary.Candidates = summary.Candidates[:4]
			summary.Partial = true
		}
		for _, rows := range [][]BlindCandidate{summary.Findings, summary.Candidates} {
			for i := range rows {
				rows[i].SourceRefs = rows[i].SourceRefs[:min(2, len(rows[i].SourceRefs))]
				rows[i].LastConfirmationRefs = rows[i].LastConfirmationRefs[:min(2, len(rows[i].LastConfirmationRefs))]
			}
		}
		data, err := json.Marshal(summary)
		if err != nil {
			return result, err
		}
		if len(data) <= 24*1024 {
			result.Attachments = append(result.Attachments, tool.Attachment{
				Filename: "sales-observations.txt", MimeType: "text/plain", Data: base64.StdEncoding.EncodeToString(data),
			})
		}
		return result, nil
	}
	return t
}
