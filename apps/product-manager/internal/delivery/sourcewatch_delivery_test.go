package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"agent/internal/sourcewatch"
)

const (
	syntheticMetrikaBinding  = "00000000-0000-4000-8000-000000000001"
	syntheticWordstatBinding = "00000000-0000-4000-8000-000000000002"
)

func TestSourceWatchCheckAndBacklogDeliveryAcrossEightMondays(t *testing.T) {
	ctx := context.Background()
	rm := &fakeRedmine{failures: map[string]int{}}
	d := &Redmine{MCP: rm, DB: newDB(t), Project: "personal", Tracker: "Улучшение", PublicURL: "http://redmine.localhost:42080"}
	rng := rand.New(rand.NewSource(20261127))
	owner := "source-watch-owner"
	pinned := make([]sourcewatch.DeliverySignal, 0, 8)

	for week := 0; week < 8; week++ {
		if week > 0 {
			syntheticHumanMutation(rm, rng, week)
		}
		monday := time.Date(2026, time.October, 5+7*week, 0, 0, 0, 0, time.UTC)
		var signal sourcewatch.Signal
		out, err := sourcewatch.Check(ctx, sourcewatch.Window{
			MetrikaBinding:  syntheticMetrikaBinding,
			WordstatBinding: syntheticWordstatBinding,
			Phrase:          "Kaspersky Endpoint Security",
			Metric:          "orders",
			DropPct:         10,
			Monday:          monday.Format(time.DateOnly),
		}, true, syntheticWatchRead(week), func(_ context.Context, got sourcewatch.Signal) (string, error) {
			got.SignalID = fmt.Sprintf("source-watch-occurrence-%02d", week)
			signal = got
			return "review-run-" + signal.SignalID, nil
		})
		if err != nil || out.Status != "submitted" {
			t.Fatalf("week %d Check() = %+v, err=%v", week, out, err)
		}
		if signal.FamilyChange == nil || signal.FamilyChange.Baseline != 100 || signal.FamilyChange.Current <= 0 {
			t.Fatalf("week %d did not preserve code-owned before/after values: %+v", week, signal)
		}
		pinned = append(pinned, sourcewatch.DeliverySignal{SignalID: signal.SignalID, Signal: signal})

		item, err := SourceWatchItem(pinned[week])
		if err != nil {
			t.Fatalf("week %d adapter: %v", week, err)
		}
		if week == 4 {
			// A failed remote call must leave this occurrence unreceipted so the
			// next sync retries the same occurrence instead of losing it.
			closeOpenSourceWatchIssues(rm)
			rm.failures["list_redmine_projects"] = 1
			failed := d.DeliverBatch(ctx, owner, []BacklogItem{item})
			if len(failed.Errors) != 1 || len(failed.Issues) != 0 {
				t.Fatalf("week %d failure result = %+v", week, failed)
			}
			delete(rm.failures, "list_redmine_projects")
			retried := d.DeliverBatch(ctx, owner, []BacklogItem{item})
			if len(retried.Errors) != 0 || len(retried.Issues) != 1 {
				t.Fatalf("week %d retry result = %+v", week, retried)
			}
			continue
		}
		result := d.DeliverBatch(ctx, owner, []BacklogItem{item})
		if len(result.Errors) != 0 || len(result.Issues) != 1 {
			t.Fatalf("week %d delivery result = %+v", week, result)
		}
		if week == 1 && (rm.creates != 1 || rm.notes == 0) {
			t.Fatalf("an open renamed issue must receive a comment, creates=%d notes=%d", rm.creates, rm.notes)
		}
	}

	foreign := findForeignIssue(rm)
	if foreign == nil {
		t.Fatal("seeded human mutation did not create a foreign issue")
	}
	foreignSubject, foreignDescription, foreignJournals := foreign["subject"], foreign["description"], foreign["journals"]
	if got := d.DeliverBatch(ctx, owner, []BacklogItem{}); len(got.Errors) != 0 || len(got.Issues) != 0 {
		t.Fatalf("empty sync result = %+v", got)
	}

	first := pinned[0]
	first.Review = &sourcewatch.ReviewOutput{
		Hypothesis:  "Проверить шаг оформления",
		EvidenceIDs: sourcewatch.ReviewSignalIDs(first.Signal)[:1],
		Missing:     []string{"Нет независимой проверки причины"},
		Test:        sourcewatch.ReviewTest{Metric: "checkout_rate", ThresholdPct: 5, Days: 7},
	}
	reviewItem, err := SourceWatchItem(first)
	if err != nil {
		t.Fatal(err)
	}
	notesBefore := rm.notes
	withReview := d.DeliverBatch(ctx, owner, []BacklogItem{reviewItem})
	if len(withReview.Errors) != 0 || len(withReview.Issues) != 1 || rm.notes != notesBefore+1 {
		t.Fatalf("late review delivery = %+v notes %d -> %d", withReview, notesBefore, rm.notes)
	}
	withoutDuplicate := d.DeliverBatch(ctx, owner, []BacklogItem{reviewItem})
	if len(withoutDuplicate.Errors) != 0 || rm.notes != notesBefore+1 {
		t.Fatalf("late review was duplicated: %+v notes=%d", withoutDuplicate, rm.notes)
	}
	if foreign["subject"] != foreignSubject || foreign["description"] != foreignDescription || foreign["journals"] != foreignJournals {
		t.Fatal("foreign Redmine issue was modified")
	}

	for _, signal := range pinned {
		item, err := SourceWatchItem(signal)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Lookup(ctx, owner, SignalKey(item.Record))
		if err != nil || got == nil {
			t.Fatalf("signal %s was lost after retry: receipt=%+v err=%v", SignalKey(item.Record), got, err)
		}
	}
}

func syntheticWatchRead(week int) sourcewatch.Read {
	return func(_ context.Context, binding string, input, output any) error {
		args, _ := json.Marshal(input)
		var query map[string]any
		if err := json.Unmarshal(args, &query); err != nil {
			return err
		}
		baselineFrom, _ := query["baseline_from"].(string)
		baselineTo, _ := query["baseline_to"].(string)
		currentFrom, _ := query["current_from"].(string)
		currentTo, _ := query["current_to"].(string)
		if binding == syntheticWordstatBinding {
			monday, _ := query["monday"].(string)
			raw := map[string]any{"schema_version": "aifence.v1", "source": "wordstat", "status": "unavailable", "as_of": monday + "T12:00:00Z", "observations": []any{}, "warnings": []string{"synthetic source unavailable"}}
			encoded, _ := json.Marshal(raw)
			return json.Unmarshal(encoded, output)
		}
		if binding != syntheticMetrikaBinding {
			return fmt.Errorf("unexpected binding %s", binding)
		}
		baseline := 100.0
		current := 80.0 - float64(week)
		change := (current - baseline) / baseline * 100
		raw := map[string]any{
			"schema_version": "aifence.v1", "source": "metrika", "status": "ok", "as_of": currentTo + "T12:00:00Z", "warnings": []string{},
			"observations": []any{
				map[string]any{"id": fmt.Sprintf("watch-base-%02d", week), "kind": "metric", "product_key": "store", "metric_key": "orders", "status": "observed", "unit": "count", "value": baseline, "period": map[string]string{"from": baselineFrom, "to": baselineTo}, "source_ref": map[string]string{"source_id": "yandex-metrika:1337", "locator": fmt.Sprintf("synthetic/%02d/base", week), "revision": "synthetic-v1"}},
				map[string]any{"id": fmt.Sprintf("watch-current-%02d", week), "kind": "metric", "product_key": "store", "metric_key": "orders", "status": "observed", "unit": "count", "value": current, "period": map[string]string{"from": currentFrom, "to": currentTo}, "source_ref": map[string]string{"source_id": "yandex-metrika:1337", "locator": fmt.Sprintf("synthetic/%02d/current", week), "revision": "synthetic-v1"}},
				map[string]any{"id": fmt.Sprintf("watch-delta-%02d", week), "kind": "metric", "product_key": "store", "metric_key": "orders.relative_change_pct", "status": "observed", "unit": "percent", "value": change, "period": map[string]string{"from": currentFrom, "to": currentTo}, "source_ref": map[string]string{"source_id": "yandex-metrika:1337", "locator": fmt.Sprintf("synthetic/%02d/delta", week), "revision": "synthetic-v1"}},
			},
		}
		encoded, _ := json.Marshal(raw)
		return json.Unmarshal(encoded, output)
	}
}

func syntheticHumanMutation(rm *fakeRedmine, rng *rand.Rand, week int) {
	open := make([]map[string]any, 0)
	for _, issue := range rm.issues {
		if status, _ := issue["status_id"].(string); status == "" || status == "open" {
			open = append(open, issue)
		}
	}
	if week == 1 && len(open) > 0 {
		open[0]["subject"] = "Ручная переоценка сигнала"
	}
	if week == 1 {
		rm.issues = append(rm.issues, map[string]any{"id": 900, "subject": "Чужая задача", "description": "*Продукт:* Dr.Web\nручная задача", "fields": map[string]any{}, "status_id": "open"})
		return
	}
	if len(open) > 0 && rng.Intn(2) == 0 {
		open[rng.Intn(len(open))]["status_id"] = "closed"
	}
	if len(open) > 0 && rng.Intn(2) == 0 {
		open[rng.Intn(len(open))]["subject"] = fmt.Sprintf("Ручное имя недели %d", week)
	}
}

func closeOpenSourceWatchIssues(rm *fakeRedmine) {
	for _, issue := range rm.issues {
		fields, _ := issue["fields"].(map[string]any)
		if fields[FieldSignal] != nil {
			issue["status_id"] = "closed"
		}
	}
}

func findForeignIssue(rm *fakeRedmine) map[string]any {
	for _, issue := range rm.issues {
		if issue["id"] == 900 {
			return issue
		}
	}
	return nil
}
