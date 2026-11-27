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
		var query handlers.BriefQuery
		if json.Unmarshal(args, &query) != nil {
			return result, err
		}
		attachment := reviewAttachment(result.Output)
		if query.Director {
			attachment = directorReview(result.Output)
			result.Output = attachment
		}
		logical := fmt.Sprintf("%s/%x.txt", chatBriefDirectory, sha256.Sum256([]byte(attachment)))
		path, err := a.ResolveFilePath(ctx, logical, agentsdk.FileOperationWrite)
		// A trusted write must use the conversation-scoped path from the SDK.
		if err != nil || !strings.HasPrefix(string(path), chatBriefDirectory+"/conv-") {
			slog.WarnContext(ctx, "Sales review attachment has no conversation scope")
			return result, nil
		}
		info, err := a.WriteFile(ctx, string(path), strings.NewReader(attachment), "text/plain")
		if err != nil {
			slog.WarnContext(ctx, "Could not store the conversation's sales review attachment")
			return result, nil
		}
		result.Attachments = append(result.Attachments, tool.Attachment{Data: "s3ref:" + string(info.Path), Filename: "sales-review.txt", MimeType: "text/plain"})
		return result, nil
	}
	return t
}

// The attachment can be the only evidence a hosted model receives if its JS
// expression has no return. Keep the source locators with the measured facts.
func reviewAttachment(output string) string {
	var review handlers.SalesBrief
	if err := json.Unmarshal([]byte(output), &review); err != nil || len(review.Readings) == 0 {
		return output
	}
	var b strings.Builder
	for _, r := range review.Readings {
		c := r.Comparison
		if c.Control.Name != "" || c.Target.Name != "" {
			fmt.Fprintf(&b, "Окна %s–%s к %s–%s (%d недели): %s %.1f%%; %s %.1f%%.\n",
				c.Current.From, c.Current.To, c.Baseline.From, c.Baseline.To, c.WeeksPerWindow, c.Control.Name, c.Control.Percent, c.Target.Name, c.Target.Percent)
		} else {
			fmt.Fprintf(&b, "%s\n", r.Sales)
		}
		fmt.Fprintf(&b, "Сопоставимость цен: %s (%d из %d артикулов).\n", priceStatus(r), r.ComparableSKUs, r.CheckedSKUs)
		fmt.Fprintf(&b, "%s %s\n", r.Price, r.PriceTiming)
		for _, offer := range r.OfferExample {
			fmt.Fprintf(&b, "%s · %s · %s\n", offer.SKU, offer.Fact, offer.Locator)
		}
		fmt.Fprintf(&b, "Недельный ущерб: %s. Источник: %s.\n", r.Weekly, r.Source)
	}
	if review.NextOffset != nil {
		fmt.Fprintf(&b, "Следующая страница: %d.\n", *review.NextOffset)
	}
	return b.String()
}

func directorReview(output string) string {
	var review handlers.SalesBrief
	if err := json.Unmarshal([]byte(output), &review); err != nil || len(review.Readings) == 0 {
		return output
	}
	var b strings.Builder
	for _, r := range review.Readings {
		c := r.Comparison
		fmt.Fprintf(&b, "Окна %s–%s к %s–%s (%d недели): %s %.1f%%; %s %.1f%%.\n",
			c.Current.From, c.Current.To, c.Baseline.From, c.Baseline.To, c.WeeksPerWindow, c.Control.Name, c.Control.Percent, c.Target.Name, c.Target.Percent)
		fmt.Fprintf(&b, "Сопоставимость цен: %s (%d из %d артикулов).\n", priceStatus(r), r.ComparableSKUs, r.CheckedSKUs)
		fmt.Fprintf(&b, "Недельный ущерб: %s. Источник: %s.\n", r.Weekly, r.Source)
	}
	return b.String()
}

func priceStatus(r handlers.ProductReading) string {
	switch r.PriceEvidenceStatus {
	case "insufficient_comparable_quotes":
		return "сопоставимая пара не подтверждена"
	case "comparable_quotes_available":
		return "сопоставимые предложения подтверждены"
	case "no_matching_offers":
		return "предложения для сравнения не найдены"
	default:
		return r.PriceEvidenceStatus
	}
}
