package main

import (
	"encoding/json"
	"errors"
	"testing"

	"agent/internal/evidence"
	"agent/internal/product"
	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
	"github.com/airlockrun/goai/tool"
)

func TestImportToolObjectAndLegacyTextReachSamePolicyGuard(t *testing.T) {
	u := wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	ctx := testutil.WithCaller(t.Context(), wire.Caller{Kind: "user", Access: wire.AccessUser, User: &u, Initiator: &u,
		Origin: wire.CallerOrigin{Interface: "chat", Execution: "request"}})
	importer := importEvidenceTool(product.Service{Evidence: &evidence.Service{}})
	var schema struct {
		Properties map[string]struct {
			AnyOf []struct{ Type string }
		}
	}
	if err := json.Unmarshal(importer.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	forms := schema.Properties["document"].AnyOf
	if len(forms) != 2 || forms[0].Type != "object" || forms[1].Type != "string" {
		t.Fatalf("import contract does not expose both forms: %s", importer.InputSchema)
	}
	const document = `{"schema_version":"aifence.case.v1","source_policy":"local_project_only","as_of":"2025-01-02T00:00:00Z","batches":[{"schema_version":"aifence.v1","source":"sales","as_of":"2025-01-02T00:00:00Z","status":"ok","observations":[{"id":"x","product_key":"kaspersky_small_office_security","metric_key":"orders","kind":"metric","period":{"from":"2025-01-01","to":"2025-01-01"},"source_ref":{"source_id":"example","locator":"row"},"status":"observed","value":1}]}]}`
	legacy, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []string{document, string(legacy)} {
		_, err := importer.Execute(ctx, json.RawMessage(`{"document":`+doc+`}`), tool.CallOptions{})
		if !errors.Is(err, evidence.ErrLocalOnly) {
			t.Fatalf("input did not reach access policy: %v", err)
		}
	}
}
