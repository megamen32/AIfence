package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Upstream Airlock tools return objects. Existing file and CLI integrations send JSON text.
func (in *ImportInput) UnmarshalJSON(raw []byte) error {
	var args struct {
		Document json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return err
	}
	doc := bytes.TrimSpace(args.Document)
	if len(doc) == 0 {
		return errors.New("добавьте документ наблюдений")
	}
	switch doc[0] {
	case '{':
		in.Document = string(doc)
		return nil
	case '"':
		return json.Unmarshal(doc, &in.Document)
	default:
		return errors.New("документ должен быть объектом JSON или текстом JSON")
	}
}
