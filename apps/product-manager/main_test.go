package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/airlockrun/agentsdk/wire"
)

// TestAgentComposition checks the offline definition without starting a runtime.
// HTTP admission, migrations, sync and OnStart execute on real Airlock previews.
func TestAgentComposition(t *testing.T) {
	manifest := newAgent().Manifest()
	// The app description is visible before the model has inspected the detailed instructions.
	for _, contract := range []string{"console.log((await", "review_sales_and_prices", "observations", "опусти request_confirmation"} {
		if !strings.Contains(manifest.Description, contract) {
			t.Fatalf("visible description omits %q", contract)
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
	for _, name := range []string{"review_sales_and_prices", "save_hypothesis", "investigate_orders", "send_to_backlog", "evaluate_hypothesis", "get_product_focus", "set_product_focus", "rank_hypotheses", "set_review_estimate", "private_review_link", "get_catalog_overview", "import_catalog_overview"} {
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
			strings.Contains(instruction.Text, "console.log(await apps.product_manager.tools.review_sales_and_prices({}))") &&
			strings.Contains(instruction.Text, "«Создать эту задачу»") &&
			strings.Contains(instruction.Text, "Не проси ответить «да»") &&
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
