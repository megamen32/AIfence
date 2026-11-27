package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/airlockrun/goai/tool"
)

func TestSubmitAnswerContract(t *testing.T) {
	fn := submitAnswerTool()
	base := map[string]any{"решение": "Цену пока сохраняем.", "величина_%": -12.3, "контроль": map[string]any{"название": "Astra Linux", "величина_%": -1.4}, "доказательства": []any{map[string]any{"источник": "продажи", "локатор": "ряд 1", "наблюдение": "Сравнены одинаковые окна."}}}
	run := func(v any) (string, error) {
		raw, _ := json.Marshal(v)
		r, err := fn.Execute(context.Background(), raw, tool.CallOptions{})
		if err != nil {
			return "", err
		}
		var answer acceptedAnswer
		err = json.Unmarshal([]byte(r.Output), &answer)
		return answer.Text, err
	}
	text, err := run(base)
	if err != nil || !strings.HasPrefix(text, "Цену пока сохраняем.\n") || !strings.Contains(text, "контроль Astra Linux −1,4%") || !strings.Contains(text, "ряд 1") {
		t.Fatal(text, err)
	}
	for k, v := range map[string]any{"решение": strings.Repeat("я", 161), "контроль": nil, "доказательства": []any{}, "extra": "unexpected", "величина_%": -101} {
		copy := make(map[string]any)
		for name, value := range base {
			copy[name] = value
		}
		copy[k] = v
		if _, err := run(copy); err == nil {
			t.Errorf("invalid %s accepted", k)
		}
	}
	base["решение"] = "Недостаточно данных."
	base["контроль"] = nil
	base["величина_%"] = nil
	base["доказательства"] = []any{}
	if _, err := run(base); err != nil {
		t.Fatal(err)
	}
	delete(base, "контроль")
	if _, err := run(base); err == nil {
		t.Error("omitted control accepted")
	}
}
