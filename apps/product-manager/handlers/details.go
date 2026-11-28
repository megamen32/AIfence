package handlers

import "encoding/json"

// decodeDetails reads the JSON detail bag of an evidence row. The same
// helpers live in the application package for the market intelligence tools;
// the handlers package needs its own copy because Go keeps packages apart.
func decodeDetails(raw string) map[string]any {
	var details map[string]any
	_ = json.Unmarshal([]byte(raw), &details)
	return details
}

// detailString returns one detail value as text. A missing or empty value is
// an empty string, never a guess.
func detailString(details map[string]any, key string) string {
	value, found := details[key]
	if !found || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := json.Marshal(value)
	return string(data)
}
