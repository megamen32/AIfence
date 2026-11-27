package main

import (
	"slices"
	"strings"
	"testing"

	"agent/internal/sourcewatch"
	"github.com/airlockrun/agentsdk/wire"
)

// TestAgentComposition checks the offline definition without starting a runtime.
// HTTP admission, migrations, sync and OnStart execute on real Airlock previews.
func TestAgentComposition(t *testing.T) {
	manifest := newAgent().Manifest()
	// The app description is visible before the model has inspected the detailed instructions.
	for _, contract := range []string{"return await", "review_sales_and_prices", "imported_price_observations", `"request_confirmation":false`, "director:true"} {
		if !strings.Contains(manifest.Description, contract) {
			t.Fatalf("visible description omits %q", contract)
		}
	}
	for _, contract := range []string{"submit_answer", "director_replies", "доказательства"} {
		if !strings.Contains(manifest.Description, contract) {
			t.Fatalf("visible response contract omits %q", contract)
		}
	}
	if manifest.Icon == "" {
		t.Fatal("app must declare an Icon emoji")
	}
	if err := wire.CheckAppRuntimeProtocol(manifest.RuntimeProtocol); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) == 0 {
		t.Fatal("app must declare its HTTP routes")
	}
	for _, name := range []string{"read_wordstat", "read_google_trends", "read_competitor_prices", "submit_answer", "review_sales_and_prices", "save_hypothesis", "investigate_orders", "send_to_backlog", "evaluate_hypothesis", "get_product_focus", "set_product_focus", "rank_hypotheses", "set_review_estimate", "private_review_link", "get_catalog_overview", "import_catalog_overview"} {
		if !slices.ContainsFunc(manifest.Tools, func(tool wire.ToolDef) bool {
			return tool.Name == name && tool.Access == wire.AccessUser && len(tool.InputSchema) > 0
		}) {
			t.Errorf("missing typed user tool %s", name)
		}
	}
	for _, tool := range manifest.Tools {
		if tool.Name == "send_to_backlog" && strings.Contains(string(tool.InputSchema), "confirmed") {
			t.Fatal("chat receipt schema must not expose an approval flag")
		}
	}

	if len(manifest.SuggestedPrompts) != 3 {
		t.Fatalf("expected three suggested prompts, got %d", len(manifest.SuggestedPrompts))
	}
	for i, want := range []struct{ title, prompt string }{
		{"Что требует внимания?", "Что сейчас требует внимания в продажах?"},
		{"Что стоит проверить?", "Посмотри, где могло возникнуть изменение, и предложи, что проверить первым."},
	} {
		got := manifest.SuggestedPrompts[i]
		if got.Title != want.title || got.Prompt != want.prompt {
			t.Fatalf("unexpected user prompt: %+v", got)
		}
	}
	if !slices.ContainsFunc(manifest.Instructions, func(instruction wire.InstructionDef) bool {
		return strings.HasPrefix(instruction.Text, "Начинай разрешённые чтения сразу") &&
			strings.Contains(instruction.Text, "run_js с полями code и description") &&
			strings.Contains(instruction.Text, "return await apps.product_manager.tools.review_sales_and_prices({summary:true,director:true});") &&
			strings.Contains(instruction.Text, "«Создать эту задачу»") &&
			strings.Contains(instruction.Text, "Не проси ответить «да»") &&
			strings.Contains(instruction.Text, "Не заменяй контроль общим словом и не опускай его") &&
			!strings.Contains(instruction.Text, "fixtures/")
	}) {
		t.Fatal("watchdog must discover the available series and require one confirmation on the card")
	}

	if !slices.ContainsFunc(manifest.Topics, func(topic wire.TopicDef) bool {
		return topic.Slug == "product_signals" && topic.PerUser && topic.Enrollment == wire.TopicEnrollmentDefaultOff
	}) {
		t.Fatal("missing opt-in signal topic")
	}
}

func TestSourceWatchComposition(t *testing.T) {
	m := newAgent().Manifest()
	if len(m.JobCrons) != 0 {
		t.Fatal("source checks require an owner's explicit schedule")
	}
	if !slices.ContainsFunc(m.JobHandlers, func(h wire.JobHandlerDef) bool { return h.Name == "check_source_watch" && h.Version == 1 }) {
		t.Fatal("missing source watch job")
	}
	for _, name := range []string{"start_source_watch", "stop_source_watch", "list_source_watches", "review_source_watch", "approve_source_signal"} {
		if !slices.ContainsFunc(m.Tools, func(t wire.ToolDef) bool { return t.Name == name && t.Access == wire.AccessUser }) {
			t.Errorf("missing user tool %s", name)
		}
	}
	if !slices.ContainsFunc(m.AgentDefinitions, func(d wire.AgentDefinition) bool {
		return d.Slug == "review_source_signal" && d.ModelSlot == "source_watch_analysis" && d.Capabilities != nil && len(d.Capabilities.Allow) == 0 && len(d.Tools) == 0 && len(d.InputSchema) > 0 && len(d.OutputSchema) > 0
	}) {
		t.Fatal("signal agent must be restricted to supplied evidence")
	}
	if sourcewatch.HostSupportsAppRead {
		t.Fatal("pinned SDK v0.11.0 must report owner-scoped job reads as unsupported")
	}
}
