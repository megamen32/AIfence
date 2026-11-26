package main

import (
	"agent/internal/evidence"
	"agent/internal/product"
	"encoding/json"
	"github.com/airlockrun/goai/tool"
)

func importEvidenceTool(s product.Service) tool.Tool {
	t := tool.Typed[evidence.ImportInput, product.ImportReceipt]("import_evidence").
		Description("Import an authorized evidence document. Pass {document: case.document} directly from collect_selected_watchdog_case. Both JSON objects and legacy JSON text are supported. Repeated imports reuse the immutable snapshot. Complete measurements can update confirmed decisions.").
		Execute(s.Import).Build()
	t.InputSchema = json.RawMessage(`{"type":"object","properties":{"document":{"description":"Evidence document from a source tool, or its JSON text.","anyOf":[{"type":"object"},{"type":"string"}]}},"required":["document"],"additionalProperties":false}`)
	return t
}
