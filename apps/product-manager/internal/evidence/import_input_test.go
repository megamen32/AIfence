package evidence

import (
	"encoding/json"
	"testing"
)

func TestCaseDocumentAcceptsObjectsAndJSONText(t *testing.T) {
	for _, payload := range []string{`{"document":{"schema_version":"aifence.case.v1"}}`, `{"document":"{\"schema_version\":\"aifence.case.v1\"}"}`} {
		var in ImportInput
		if err := json.Unmarshal([]byte(payload), &in); err != nil || in.Document != `{"schema_version":"aifence.case.v1"}` {
			t.Fatalf("unmarshal: %s %v", in.Document, err)
		}
	}
	for _, payload := range []string{`{"document":null}`, `{"document":12}`, `{"document":[]}`, `{}`} {
		var in ImportInput
		if json.Unmarshal([]byte(payload), &in) == nil {
			t.Fatal("accepted", payload)
		}
	}
}
