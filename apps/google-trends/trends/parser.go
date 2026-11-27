package trends

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

var errShape = errors.New("unexpected provider shape")

// unwrapMCP accepts the actual MCP envelope and the provider's {status,json}
// envelope. Error details stay private; callers receive fixed status messages.
func unwrapMCP(body []byte) (map[string]any, string) {
	if len(body) == 0 || len(body) > 2<<20 {
		return nil, InvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, InvalidResponse
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, InvalidResponse
	}
	root, ok := object(value)
	if !ok {
		return nil, InvalidResponse
	}
	if v, present := root["result"]; present {
		root, ok = object(v)
		if !ok {
			return nil, InvalidResponse
		}
	}
	if _, present := root["error"]; present {
		return nil, errorStatus(root["error"])
	}
	if failed, _ := root["isError"].(bool); failed {
		return nil, errorStatus(root)
	}
	payload := root
	if value, present := root["structuredContent"]; present && value != nil {
		payload, ok = object(value)
		if !ok {
			return nil, InvalidResponse
		}
	} else if value, present := root["content"]; present {
		blocks, valid := value.([]any)
		if !valid {
			return nil, InvalidResponse
		}
		found := false
		for _, item := range blocks {
			block, valid := object(item)
			if !valid || text(block["type"]) != "text" {
				continue
			}
			raw := text(block["text"])
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.UseNumber()
			var decoded any
			if dec.Decode(&decoded) != nil {
				continue
			}
			var trailing any
			if dec.Decode(&trailing) != io.EOF {
				continue
			}
			payload, valid = object(decoded)
			if valid {
				found = true
				break
			}
		}
		if !found {
			return nil, InvalidResponse
		}
	}
	if value, present := payload["status"]; present {
		if code, numeric := number(value); numeric && (code < 200 || code >= 300) {
			return nil, statusFromCode(int(code))
		}
	}
	if value, present := payload["json"]; present {
		payload, ok = object(value)
		if !ok {
			return nil, InvalidResponse
		}
	}
	if _, present := payload["error"]; present {
		return nil, errorStatus(payload["error"])
	}
	if meta, valid := object(payload["requestMetadata"]); valid {
		switch strings.ToLower(text(meta["status"])) {
		case "", "ok", "success", "completed":
		default:
			return nil, errorStatus(meta)
		}
	}
	return payload, ""
}

func statusFromCode(code int) string {
	switch code {
	case 401:
		return CredentialsRequired
	case 402:
		return QuotaExhausted
	case 403:
		return AccessDenied
	case 429:
		return RateLimited
	default:
		return UpstreamError
	}
}

func errorStatus(value any) string {
	if m, ok := object(value); ok {
		for _, key := range []string{"status", "statusCode", "code"} {
			if n, valid := number(m[key]); valid && n >= 400 && n <= 599 && math.Trunc(n) == n {
				return statusFromCode(int(n))
			}
		}
		if nested, present := m["error"]; present {
			if status := errorStatus(nested); status != UpstreamError {
				return status
			}
		}
		for _, key := range []string{"message", "detail", "text"} {
			if status := errorStatus(m[key]); status != UpstreamError {
				return status
			}
		}
		if blocks, ok := m["content"].([]any); ok {
			for _, block := range blocks {
				if status := errorStatus(block); status != UpstreamError {
					return status
				}
			}
		}
		return UpstreamError
	}
	s, ok := value.(string)
	if !ok {
		return UpstreamError
	}
	// Some MCP servers encode a JSON error in a text block.
	decoder := json.NewDecoder(strings.NewReader(s))
	decoder.UseNumber()
	var parsed any
	if decoder.Decode(&parsed) == nil {
		if _, valid := object(parsed); valid {
			return errorStatus(parsed)
		}
	}
	s = strings.ToLower(s)
	// Never classify by a free-floating three-digit substring: a request ID or
	// reflected credential may contain the same digits as an HTTP code.
	switch {
	case strings.Contains(s, "rate limit"), strings.Contains(s, "too many requests"):
		return RateLimited
	case strings.Contains(s, "insufficient credit"), strings.Contains(s, "no credits"), strings.Contains(s, "quota exceeded"):
		return QuotaExhausted
	case strings.Contains(s, "unauthorized"), strings.Contains(s, "invalid api key"), strings.Contains(s, "api key is required"), strings.Contains(s, "missing api key"):
		return CredentialsRequired
	case strings.Contains(s, "forbidden"):
		return AccessDenied
	default:
		return UpstreamError
	}
}

func object(value any) (map[string]any, bool) { v, ok := value.(map[string]any); return v, ok }
func text(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return ""
	}
}
func number(value any) (float64, bool) {
	var v float64
	var err error
	switch n := value.(type) {
	case json.Number:
		v, err = n.Float64()
	case float64:
		v = n
	case string:
		v, err = strconv.ParseFloat(strings.TrimSpace(n), 64)
	default:
		return 0, false
	}
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}
func boolFlag(row map[string]any, key string) (*bool, error) {
	v, present := row[key]
	if !present || v == nil {
		return nil, nil
	}
	flag, ok := v.(bool)
	if !ok {
		return nil, errShape
	}
	return &flag, nil
}
func array(row map[string]any, key string) ([]any, error) {
	v, present := row[key]
	if !present {
		return nil, errShape
	}
	a, ok := v.([]any)
	if !ok {
		return nil, errShape
	}
	return a, nil
}

func parseScores(value any, terms []string) ([]Score, error) {
	input, ok := value.([]any)
	if !ok {
		return nil, errShape
	}
	byTerm := map[string]map[string]any{}
	expected := map[string]bool{}
	for _, term := range terms {
		expected[strings.ToLower(term)] = true
	}
	for _, value := range input {
		row, ok := object(value)
		if !ok {
			return nil, errShape
		}
		key := strings.ToLower(strings.TrimSpace(text(row["query"])))
		if !expected[key] || byTerm[key] != nil {
			return nil, errShape
		}
		byTerm[key] = row
	}
	scores := make([]Score, 0, len(terms))
	for _, term := range terms {
		row := byTerm[strings.ToLower(term)]
		score := Score{Term: term, State: "missing"}
		if row == nil {
			scores = append(scores, score)
			continue
		}
		score.Raw = text(row["value"])
		var err error
		score.HasData, err = boolFlag(row, "hasData")
		if err != nil {
			return nil, err
		}
		if score.HasData != nil && !*score.HasData {
			scores = append(scores, score)
			continue
		}
		if strings.TrimSpace(score.Raw) == "<1" {
			score.State = "below_one"
			scores = append(scores, score)
			continue
		}
		n, valid := number(row["extractedValue"])
		if !valid {
			n, valid = number(row["value"])
		}
		if !valid || n < 0 || n > 100 {
			return nil, errShape
		}
		score.Index = &n
		score.State = "reported"
		if score.HasData != nil && *score.HasData {
			score.State = "observed"
		}
		scores = append(scores, score)
	}
	return scores, nil
}

func parseTimeline(payload map[string]any, terms []string) ([]Point, error) {
	timeline, ok := object(payload["interestOverTime"])
	if !ok {
		return nil, errShape
	}
	items, err := array(timeline, "timelineData")
	if err != nil {
		return nil, err
	}
	points := make([]Point, 0, len(items))
	var previous int64
	for i, value := range items {
		row, ok := object(value)
		if !ok {
			return nil, errShape
		}
		timestamp, valid := number(row["timestamp"])
		if !valid || timestamp <= 0 || math.Trunc(timestamp) != timestamp {
			return nil, errShape
		}
		p := Point{Timestamp: int64(timestamp), Date: text(row["date"])}
		if p.Date == "" || i > 0 && p.Timestamp <= previous {
			return nil, errShape
		}
		previous = p.Timestamp
		p.IsPartial, err = boolFlag(row, "isPartial")
		if err != nil {
			return nil, err
		}
		p.Values, err = parseScores(row["values"], terms)
		if err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, nil
}

func summarize(points []Point, terms []string) []Summary {
	sums := make([]float64, len(terms))
	common := 0
	for _, point := range points {
		eligible := point.IsPartial != nil && !*point.IsPartial && len(point.Values) == len(terms)
		for _, score := range point.Values {
			if score.HasData == nil || !*score.HasData || score.Index == nil || score.State != "observed" {
				eligible = false
				break
			}
		}
		if !eligible {
			continue
		}
		common++
		for i, score := range point.Values {
			sums[i] += *score.Index
		}
	}
	out := make([]Summary, len(terms))
	for i, term := range terms {
		out[i] = Summary{Term: term, CommonCompletePoints: common, ExcludedPoints: len(points) - common}
		if common > 0 {
			mean := sums[i] / float64(common)
			out[i].MeanIndex = &mean
		} else {
			out[i].Reason = "no_common_complete_uncensored_intervals"
		}
	}
	return out
}

func parseRegions(payload map[string]any, terms []string) ([]RegionRow, error) {
	items, err := array(payload, "geoMap")
	if err != nil {
		return nil, err
	}
	regions := make([]RegionRow, 0, len(items))
	seen := map[string]bool{}
	for _, value := range items {
		row, ok := object(value)
		if !ok {
			return nil, errShape
		}
		r := RegionRow{Code: text(row["geo"]), Name: text(row["location"])}
		if r.Code == "" || r.Name == "" || seen[r.Code] {
			return nil, errShape
		}
		seen[r.Code] = true
		r.Values, err = parseScores(row["values"], terms)
		if err != nil {
			return nil, err
		}
		regions = append(regions, r)
	}
	return regions, nil
}

func parseRelated(payload map[string]any) (Related, error) {
	var related Related
	root, ok := object(payload["relatedQueries"])
	if !ok {
		return related, errShape
	}
	_, topPresent := root["top"]
	_, risingPresent := root["rising"]
	if !topPresent && !risingPresent {
		return related, errShape
	}
	if topPresent {
		var err error
		related.Top, err = parseRelatedRows(root, "top")
		if err != nil {
			return Related{}, err
		}
	}
	if risingPresent {
		var err error
		related.Rising, err = parseRelatedRows(root, "rising")
		if err != nil {
			return Related{}, err
		}
	}
	return related, nil
}
func parseRelatedRows(root map[string]any, key string) ([]RelatedRow, error) {
	items, err := array(root, key)
	if err != nil {
		return nil, err
	}
	out := make([]RelatedRow, 0, len(items))
	for _, item := range items {
		row, ok := object(item)
		if !ok {
			return nil, errShape
		}
		r := RelatedRow{Query: text(row["query"]), Raw: text(row["value"]), Metric: "relative_index"}
		if r.Query == "" {
			return nil, errShape
		}
		if key == "rising" && strings.EqualFold(strings.TrimSpace(r.Raw), "Breakout") {
			r.Metric = "breakout"
			out = append(out, r)
			continue
		}
		n, valid := number(row["extractedValue"])
		if !valid {
			n, valid = number(strings.TrimSuffix(strings.ReplaceAll(r.Raw, ",", ""), "%"))
		}
		if !valid || n < 0 || key == "top" && n > 100 {
			return nil, errShape
		}
		if key == "rising" {
			r.Metric = "growth_percent"
		}
		r.Value = &n
		out = append(out, r)
	}
	return out, nil
}
func hasObservations(r Result) bool {
	for _, p := range r.Points {
		for _, v := range p.Values {
			if v.Index != nil || v.State == "below_one" {
				return true
			}
		}
	}
	for _, p := range r.Regions {
		for _, v := range p.Values {
			if v.Index != nil || v.State == "below_one" {
				return true
			}
		}
	}
	return r.Related != nil && (len(r.Related.Top) > 0 || len(r.Related.Rising) > 0)
}
