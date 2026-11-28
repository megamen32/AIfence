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

const chatBriefDirectory = "sales-review"

func salesReviewTool(a *agentsdk.Agent, h *handlers.Handler, strictReply *agentsdk.EnvVarHandle) tool.Tool {
	a.RegisterDirectory(chatBriefDirectory, agentsdk.DirectoryOpts{
		Read: agentsdk.AccessUser, Write: agentsdk.AccessUser, List: agentsdk.AccessUser,
		Scope: agentsdk.ScopeConversation, RetentionHours: 1,
		Description: "Temporary review evidence for the conversation that requested it.",
	})
	t := tool.Typed[handlers.BriefQuery, handlers.SalesBrief]("review_sales_and_prices").
		Description("Read the signed-in user’s active sales alerts and imported SKU quotes from the panel. Includes periods, control, source references and license terms. Read-only. Hosted run_js code: return await apps.product_manager.tools.review_sales_and_prices({summary:true,director:true});").
		Execute(func(ctx context.Context, q handlers.BriefQuery) (handlers.SalesBrief, error) {
			mode, err := strictReply.Get(ctx)
			if err != nil {
				return handlers.SalesBrief{}, err
			}
			q, answerMode := salesReviewMode(q, mode)
			brief, err := h.SalesBrief(ctx, q)
			if err != nil {
				return brief, err
			}
			brief.AnswerMode = answerMode
			return brief, nil
		}).Build()
	read := t.Execute
	t.Execute = func(ctx context.Context, args json.RawMessage, opts tool.CallOptions) (tool.Result, error) {
		result, err := read(ctx, args, opts)
		caller := agentsdk.CallerFromContext(ctx)
		if _, ok := caller.User(); err != nil || !ok || caller.Origin().Interface != agentsdk.InterfaceChat || result.Output == "" {
			return result, err
		}
		attachment := reviewAttachment(result.Output)
		if len(attachment) > 24*1024 {
			return result, nil
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

func salesReviewMode(q handlers.BriefQuery, mode string) (handlers.BriefQuery, string) {
	if mode != "on" {
		q.Summary = q.Summary || q.Director
		q.Director = false
		return q, "free_form"
	}
	return q, "submit_answer_required"
}

// The attachment can be the only evidence a hosted model receives if its JS
// expression has no return. Keep the source locators with the measured facts.
func reviewAttachment(output string) string {
	var review handlers.SalesBrief
	if err := json.Unmarshal([]byte(output), &review); err != nil {
		return output
	}
	var b strings.Builder
	if len(review.Replies) > 0 {
		for _, r := range review.Replies {
			if validated, err := renderAnswer(context.Background(), r); err == nil {
				b.WriteString(validated.Text)
				b.WriteByte('\n')
			} else {
				return output
			}
		}
		appendCauseReview(&b, review.Causes)
		return b.String()
	}
	if len(review.Readings) == 0 {
		if len(review.Causes) == 0 {
			return output
		}
		appendCauseReview(&b, review.Causes)
		for _, comparison := range review.Comparisons {
			fmt.Fprintf(&b, "Проверка цены: %s, артикул %s. %s\n", comparison.Product, comparison.SKU, comparison.Finding)
		}
		for _, fact := range review.Facts {
			fmt.Fprintf(&b, "Цена: %s; источник %s, %s.\n", fact.Fact, fact.Source, fact.Locator)
		}
		return b.String()
	}
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
	appendCauseReview(&b, review.Causes)
	return b.String()
}

func appendCauseReview(b *strings.Builder, causes []handlers.CauseReading) {
	for _, c := range causes {
		kind := map[string]string{
			"taxonomy_migration":            "перенос ряда на другой ключ каталога",
			"masked_multi_cause_regression": "снижение оформлений при росте трафика",
			"late_event_delivery":           "запаздывание событий покупки",
			"traffic_quality":               "изменение качества трафика",
		}[c.CauseType]
		if kind == "" {
			kind = "причинный признак"
		}
		confirmation := "кандидат причины: величины измерены, причинность не проверена"
		if c.Confirmation == "confirmed" {
			confirmation = "подтверждено"
		}
		fmt.Fprintf(b, "Метрика: %s, %s (%s), неделя %s–%s; %s, %s. Локатор сигнала: %s, %s.\n",
			kind, c.Product, c.Segment, c.Period.From, c.Period.To, causeStatus(c.Status), confirmation,
			c.SourceRef.SourceID, c.SourceRef.Locator)
		if c.Origin == "synthetic_fixture" {
			b.WriteString("Происхождение: синтетический срез.\n")
		}
		for _, m := range c.MeasurementProvenance {
			if m.Baseline.Total != nil && m.Current.Total != nil {
				fmt.Fprintf(b, "%s (%s, %s): база %s–%s, всего %.2f за %s нед.; текущий период %s–%s, всего %.2f за %s нед.\n",
					causeMetricLabel(m.MetricKey), m.Segment, m.Unit, m.Baseline.Period.From, m.Baseline.Period.To,
					*m.Baseline.Total, causeWeekCount(m.Baseline.WeekCount), m.Current.Period.From, m.Current.Period.To,
					*m.Current.Total, causeWeekCount(m.Current.WeekCount))
			}
		}
		if ratio := c.CheckoutPerVisit; ratio != nil && ratio.Baseline.RatePct != nil && ratio.Current.RatePct != nil &&
			ratio.RelativeChangePct != nil {
			fmt.Fprintf(b, "Доля оформлений среди визитов: %s–%s %.3f%%; %s–%s %.3f%%; относительное изменение %.3f%%. Это отношение, не число оформлений.\n",
				ratio.Baseline.Period.From, ratio.Baseline.Period.To, *ratio.Baseline.RatePct,
				ratio.Current.Period.From, ratio.Current.Period.To, *ratio.Current.RatePct, *ratio.RelativeChangePct)
		}
		if onset := c.MigrationOnset; onset != nil && onset.OldKeyLastWeek.Total != nil && onset.NewKeyFirstWeek.Total != nil &&
			len(onset.OldKeyLastWeek.Rows) > 0 && len(onset.NewKeyFirstWeek.Rows) > 0 {
			fmt.Fprintf(b, "%s (%s): последняя неделя старого ключа %s, %s–%s, всего %.2f; первая неделя нового ключа %s, %s–%s, всего %.2f. Это недельные числа, не средние нескольких недель.\n",
				causeMetricLabel(onset.MetricKey), onset.Unit, onset.OldKeyLastWeek.ProductKey,
				onset.OldKeyLastWeek.Period.From, onset.OldKeyLastWeek.Period.To, *onset.OldKeyLastWeek.Total,
				onset.NewKeyFirstWeek.ProductKey, onset.NewKeyFirstWeek.Period.From,
				onset.NewKeyFirstWeek.Period.To, *onset.NewKeyFirstWeek.Total)
			for _, week := range []handlers.CauseMigrationWeek{onset.OldKeyLastWeek, onset.NewKeyFirstWeek} {
				for _, row := range week.Rows {
					if row.SourceRef.SourceID != "" && row.SourceRef.Locator != "" {
						fmt.Fprintf(b, "Недельная строка: %s, %s.\n", row.SourceRef.SourceID, row.SourceRef.Locator)
					}
				}
			}
		}
		for _, e := range c.CauseEvidence {
			fmt.Fprintf(b, "Измерение %s (%s): среднее до %s, после %s, изменение %s.\n",
				causeMetricLabel(e.MetricKey), e.Segment, causeNumber(e.Before), causeNumber(e.After), causePercent(e.ChangePct))
			if e.ZScore != nil {
				z := strings.NewReplacer("-", "−", ".", ",").Replace(fmt.Sprintf("%.2f", *e.ZScore))
				fmt.Fprintf(b, "Статистика z=%s для этого измерения; это не величина изменения.\n", z)
			}
		}
		measurementRefs := 0
		for _, ref := range c.SourceRefs {
			if c.CauseRelease != nil && ref == c.CauseRelease.SourceRef {
				continue
			}
			fmt.Fprintf(b, "Локатор измерения: %s, %s.\n", ref.SourceID, ref.Locator)
			measurementRefs++
		}
		if measurementRefs == 0 {
			b.WriteString("Локаторы исходных измерений не предоставлены.\n")
		}
		if release := c.CauseRelease; release != nil {
			when := "Связанный релиз"
			if release.Period.From >= c.Period.From && release.Period.From <= c.Period.To {
				when = "Релиз в ту же неделю"
			}
			var title string
			_ = json.Unmarshal(release.Value, &title)
			if title == "" {
				title = release.ID
			}
			fmt.Fprintf(b, "%s: %s, %s; %s, %s. Совпадение по времени не доказывает причину.\n",
				when, title, release.Period.From, release.SourceRef.SourceID, release.SourceRef.Locator)
		}
	}
}

func causeNumber(value *float64) string {
	if value == nil {
		return "значение отсутствует"
	}
	return fmt.Sprintf("%.2f", *value)
}

func causeWeekCount(weeks *int) string {
	if weeks == nil {
		return "не указано"
	}
	return fmt.Sprintf("%d", *weeks)
}

func causePercent(value *float64) string {
	if value == nil {
		return "значение отсутствует"
	}
	return answerPercent(*value)
}

func causeStatus(status string) string {
	switch status {
	case "observed":
		return "наблюдается"
	case "missing":
		return "нет данных"
	case "partial":
		return "частично"
	default:
		return status
	}
}

func causeMetricLabel(metric string) string {
	if label, ok := map[string]string{
		"visits": "визиты", "card_views": "просмотры карточки", "cart_additions": "добавления в корзину",
		"checkouts": "оформления", "orders": "заказы", "revenue": "выручка",
		"bounce_rate": "доля отказов", "avg_visit_duration": "длительность визита",
	}[metric]; ok {
		return label
	}
	return metric
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
