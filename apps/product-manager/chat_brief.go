package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"agent/handlers"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

const chatBriefDirectory = "tmp/sales-review"

func salesReviewTool(a *agentsdk.Agent, h *handlers.Handler) tool.Tool {
	a.RegisterDirectory(chatBriefDirectory, agentsdk.DirectoryOpts{
		Read: agentsdk.AccessUser, Write: agentsdk.AccessUser, List: agentsdk.AccessUser,
		Scope: agentsdk.ScopeConversation, RetentionHours: 1,
		Description: "Temporary review evidence for the conversation that requested it.",
	})
	t := tool.Typed[handlers.BriefQuery, handlers.SalesBrief]("review_sales_and_prices").
		Description("Read the signed-in user’s active sales alerts and imported SKU quotes from the panel. Includes periods, control, source references and license terms. Read-only. Hosted run_js code: return await apps.product_manager.tools.review_sales_and_prices({summary:true});").
		Execute(h.SalesBrief).Build()
	read := t.Execute
	t.Execute = func(ctx context.Context, args json.RawMessage, opts tool.CallOptions) (tool.Result, error) {
		result, err := read(ctx, args, opts)
		caller := agentsdk.CallerFromContext(ctx)
		if _, ok := caller.User(); err != nil || !ok || caller.Origin().Interface != agentsdk.InterfaceChat || result.Output == "" || len(result.Output) > 24*1024 {
			return result, err
		}
		logical := fmt.Sprintf("%s/%x.txt", chatBriefDirectory, sha256.Sum256([]byte(result.Output)))
		path, err := a.ResolveFilePath(ctx, logical, agentsdk.FileOperationWrite)
		// A trusted write must use the conversation-scoped path from the SDK.
		if err != nil || !strings.HasPrefix(string(path), chatBriefDirectory+"/conv-") {
			slog.WarnContext(ctx, "Sales review attachment has no conversation scope")
			return result, nil
		}
		info, err := a.WriteFile(ctx, string(path), strings.NewReader(result.Output), "text/plain")
		if err != nil {
			slog.WarnContext(ctx, "Could not store the conversation's sales review attachment")
			return result, nil
		}
		result.Attachments = append(result.Attachments, tool.Attachment{Data: "s3ref:" + string(info.Path), Filename: "sales-review.txt", MimeType: "text/plain"})
		return result, nil
	}
	return t
}
