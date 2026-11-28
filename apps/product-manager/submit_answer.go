package main

import (
	"agent/internal/answer"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/airlockrun/goai/tool"
	"github.com/google/jsonschema-go/jsonschema"
)

type acceptedAnswer struct {
	Text string `json:"текст"`
}

const answerSchema = `{
  "type":"object","additionalProperties":false,
  "required":["решение","величина_%","контроль","доказательства"],
  "properties":{
    "решение":{"type":"string","minLength":1,"maxLength":160},
    "величина_%":{"type":["number","null"],"minimum":-100},
    "контроль":{"anyOf":[{"type":"null"},{"type":"object","additionalProperties":false,
      "required":["название","величина_%"],"properties":{
        "название":{"type":"string","minLength":1,"maxLength":90},
        "величина_%":{"type":"number","minimum":-100}}}]},
    "доказательства":{"type":"array","maxItems":4,"items":{
      "type":"object","additionalProperties":false,"required":["источник","локатор","наблюдение"],
      "properties":{"источник":{"type":"string","minLength":1,"maxLength":80},
        "локатор":{"type":"string","minLength":1,"maxLength":240},
        "наблюдение":{"type":"string","minLength":1,"maxLength":130}}}}
  }
}`

func submitAnswerTool() tool.Tool {
	t := tool.Typed[answer.Input, acceptedAnswer]("submit_answer").
		Description("Validate and return the final short director answer. Decision comes first; pass measured change and named control together, or null for both when unavailable. Cite observations actually read. This formats a reply without creating a backlog task.").
		Execute(renderAnswer).Build()
	t.InputSchema = json.RawMessage(answerSchema)
	var schema jsonschema.Schema
	if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
		panic(err)
	}
	validator, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	execute := t.Execute
	t.Execute = func(ctx context.Context, raw json.RawMessage, opts tool.CallOptions) (tool.Result, error) {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return tool.Result{}, err
		}
		if err := validator.Validate(value); err != nil {
			return tool.Result{}, fmt.Errorf("Проверьте поля submit_answer: %w", err)
		}
		return execute(ctx, raw, opts)
	}
	return t
}

func answerField(s string) bool {
	return strings.TrimSpace(s) != "" && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) < 0
}

func answerPercent(n float64) string {
	return strings.NewReplacer("-", "−", ".", ",").Replace(strconv.FormatFloat(n, 'f', 1, 64)) + "%"
}

func renderAnswer(_ context.Context, a answer.Input) (acceptedAnswer, error) {
	if !answerField(a.Decision) {
		return acceptedAnswer{}, errors.New("решение должно занимать одну непустую строку")
	}
	if (a.Change == nil) != (a.Control == nil) {
		return acceptedAnswer{}, errors.New("для сравнения укажите изменение и измеренный контроль вместе; если пары нет, передайте null для обоих")
	}
	lines := []string{strings.TrimSpace(a.Decision)}
	if a.Control != nil {
		if !answerField(a.Control.Product) || len(a.Evidence) == 0 {
			return acceptedAnswer{}, errors.New("для сравнения нужны название контрольного товара и ссылка на наблюдения")
		}
		lines = append(lines, fmt.Sprintf("Изменение %s, контроль %s %s.", answerPercent(*a.Change), a.Control.Product, answerPercent(a.Control.Change)))
	}
	for _, e := range a.Evidence {
		if !answerField(e.Source) || !answerField(e.Ref) || !answerField(e.Fact) {
			return acceptedAnswer{}, errors.New("свидетельство требует источник, локатор и однострочное наблюдение")
		}
		ref := strings.TrimSpace(e.Ref)
		if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			ref = "[источник](" + ref + ")"
		}
		lines = append(lines, fmt.Sprintf("%s (%s, %s)", strings.TrimSpace(e.Fact), e.Source, ref))
	}
	text := strings.Join(lines, "\n")
	if utf8.RuneCountInString(text) > 800 || len(lines) > 8 {
		return acceptedAnswer{}, errors.New("сократите ответ до 800 знаков и 8 строк")
	}
	return acceptedAnswer{Text: text}, nil
}
