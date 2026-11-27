package trends

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	calls int
	args  map[string]any
	body  []byte
	err   error
}

func (f *fakeClient) Call(_ context.Context, args map[string]any) ([]byte, error) {
	f.calls++
	f.args = args
	return f.body, f.err
}
func fixedClock() time.Time { return time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC) }
func comparisonQuery() Query {
	return Query{Terms: []string{"Product A", "Product B"}, Geo: "RU", From: "2026-01-01", To: "2026-10-31"}
}
func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func wrapped(payload any) []byte {
	return encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encode(map[string]any{"status": 200, "json": payload}))}}})
}
func timeline(rows ...any) map[string]any {
	if rows == nil {
		rows = []any{}
	}
	return map[string]any{"requestMetadata": map[string]any{"status": "ok", "id": "offline-fixture"}, "interestOverTime": map[string]any{"timelineData": rows}}
}
func point(ts string, partial any, values ...any) map[string]any {
	p := map[string]any{"timestamp": ts, "date": "Offline fixture interval", "values": values}
	if partial != nil {
		p["isPartial"] = partial
	}
	return p
}
func score(term, raw string, index float64, has any) map[string]any {
	s := map[string]any{"query": term, "value": raw, "extractedValue": index}
	if has != nil {
		s["hasData"] = has
	}
	return s
}

func TestComparisonUsesOneRequestAndPreservesCommonScale(t *testing.T) {
	f := &fakeClient{body: wrapped(timeline(point("1766880000", false, score("Product A", "0", 0, true), score("Product B", "20", 20, true))))}
	r, err := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
	if err != nil || r.Status != OK || f.calls != 1 {
		t.Fatalf("status=%s calls=%d err=%v", r.Status, f.calls, err)
	}
	if f.args["q"] != "Product A,Product B" || f.args["date"] != "2026-01-01 2026-10-31" || f.args["geo"] != "RU" || f.args["dataType"] != "timeseries" || f.args["tz"] != 0 {
		t.Fatalf("unexpected parameters: %#v", f.args)
	}
	if _, exists := f.args["gprop"]; exists {
		t.Fatal("web must omit gprop")
	}
	if r.Source.ComparisonID == "" || r.Source.Status != OK || r.Request.From != "2026-01-01" {
		t.Fatal("source/request provenance missing")
	}
	if r.Points[0].Values[0].Index == nil || *r.Points[0].Values[0].Index != 0 {
		t.Fatal("observed zero was lost")
	}
	if r.Summaries[0].CommonCompletePoints != 1 || *r.Summaries[0].MeanIndex != 0 {
		t.Fatal("zero not included in common cohort")
	}
}
func TestMeansUseSameCompleteUncensoredCohort(t *testing.T) {
	f := &fakeClient{body: wrapped(timeline(
		point("1766880000", false, score("Product A", "10", 10, true), score("Product B", "30", 30, true)),
		point("1767484800", false, score("Product A", "0", 0, false), score("Product B", "100", 100, true)),
		point("1768089600", true, score("Product A", "90", 90, true), score("Product B", "20", 20, true)),
		point("1768694400", nil, score("Product A", "70", 70, true), score("Product B", "20", 20, true)),
		point("1769299200", false, score("Product A", "<1", 0, true), score("Product B", "20", 20, true)),
		point("1769904000", false, score("Product A", "60", 60, nil), score("Product B", "20", 20, true)),
	))}
	r, _ := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
	if r.Status != OK {
		t.Fatal(r.Status)
	}
	for i, want := range []float64{10, 30} {
		s := r.Summaries[i]
		if s.MeanIndex == nil || *s.MeanIndex != want || s.CommonCompletePoints != 1 || s.ExcludedPoints != 5 {
			t.Fatalf("summary %#v", s)
		}
	}
	if r.Points[1].Values[0].Index != nil || r.Points[1].Values[0].State != "missing" {
		t.Fatal("missing data became zero")
	}
	if r.Points[3].IsPartial != nil {
		t.Fatal("unknown partial flag became complete")
	}
	if r.Points[4].Values[0].State != "below_one" || r.Points[4].Values[0].Index != nil {
		t.Fatal("censored value became zero")
	}
	if r.Points[5].Values[0].HasData != nil || r.Points[5].Values[0].Index == nil {
		t.Fatal("unknown flag must preserve reported number")
	}
}
func TestNoCommonCompleteIntervalsHasNoMean(t *testing.T) {
	f := &fakeClient{body: wrapped(timeline(point("1766880000", nil, score("Product A", "10", 10, nil), score("Product B", "30", 30, nil))))}
	r, _ := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
	if r.Status != OK || r.Summaries[0].MeanIndex != nil || r.Summaries[0].Reason == "" {
		t.Fatalf("%#v", r.Summaries)
	}
}
func TestProviderErrorsNeverBecomeEmptySuccessOrLeakDetails(t *testing.T) {
	for code, want := range map[int]string{401: CredentialsRequired, 402: QuotaExhausted, 403: AccessDenied, 429: RateLimited, 500: UpstreamError} {
		t.Run(want, func(t *testing.T) {
			f := &fakeClient{body: encode(map[string]any{"structuredContent": map[string]any{"status": code, "json": map[string]any{"secret": "DO_NOT_LEAK"}}})}
			r, _ := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
			if r.Status != want || r.Source.Status != want || len(r.Points) != 0 || strings.Contains(string(encode(r)), "DO_NOT_LEAK") || f.calls != 1 {
				t.Fatalf("status=%s calls=%d", r.Status, f.calls)
			}
		})
	}
	f := &fakeClient{body: encode(map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "401 invalid api key DO_NOT_LEAK"}}})}
	r, _ := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
	if r.Status != CredentialsRequired || strings.Contains(string(encode(r)), "DO_NOT_LEAK") {
		t.Fatal(r.Status)
	}
	f = &fakeClient{err: errors.Join(ErrCredentialsRequired, errors.New("DO_NOT_LEAK"))}
	r, _ = NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
	if r.Status != CredentialsRequired || strings.Contains(string(encode(r)), "DO_NOT_LEAK") {
		t.Fatal(r.Status)
	}
}
func TestFuturePeriodPreservedWithoutClientCall(t *testing.T) {
	f := &fakeClient{}
	q := comparisonQuery()
	q.From, q.To = "2027-01-01", "2027-01-31"
	r, _ := NewService(f, fixedClock).Compare(context.Background(), q)
	if r.Status != UnavailablePeriod || f.calls != 0 || r.Request.From != q.From || r.Request.To != q.To {
		t.Fatalf("%s calls=%d request=%#v", r.Status, f.calls, r.Request)
	}
}
func TestDefaultPeriodLastTwelveCompleteMonths(t *testing.T) {
	f := &fakeClient{body: wrapped(timeline())}
	q := comparisonQuery()
	q.From, q.To = "", ""
	r, _ := NewService(f, fixedClock).Compare(context.Background(), q)
	if r.Request.From != "2025-11-01" || r.Request.To != "2026-10-31" || !r.Request.PeriodDefaulted {
		t.Fatalf("%#v", r.Request)
	}
	if r.Status != InsufficientData {
		t.Fatal(r.Status)
	}
}
func TestUnexpectedShapeDiffersFromRecognizedNoData(t *testing.T) {
	for _, payload := range []any{map[string]any{"unexpected": []any{}}, timeline(point("1766880000", false, score("Product A", "101", 101, true), score("Product B", "20", 20, true)))} {
		f := &fakeClient{body: wrapped(payload)}
		r, _ := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
		if r.Status != InvalidResponse {
			t.Fatal(r.Status)
		}
	}
	f := &fakeClient{body: wrapped(timeline(point("1766880000", false, score("Product A", "0", 0, false), score("Product B", "0", 0, false))))}
	r, _ := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
	if r.Status != InsufficientData || len(r.Points) != 1 {
		t.Fatal(r.Status)
	}
}
func TestRegionOfficialHasDataShapeAndUnknownFlags(t *testing.T) {
	f := &fakeClient{body: wrapped(map[string]any{"geoMap": []any{map[string]any{"geo": "US-WY", "location": "Wyoming", "values": []any{score("Coffee", "100", 100, nil)}, "maxValueIndex": 0}}})}
	r, _ := NewService(f, fixedClock).Regional(context.Background(), Query{Terms: []string{"Coffee"}, Geo: "US", From: "2026-01-01", To: "2026-10-31", Region: "region"})
	if r.Status != OK || len(r.Regions) != 1 || r.Regions[0].Code != "US-WY" || r.Regions[0].Name != "Wyoming" {
		t.Fatal(r.Status)
	}
	v := r.Regions[0].Values[0]
	if v.HasData != nil || v.Index == nil || *v.Index != 100 {
		t.Fatalf("%#v", v)
	}
	if f.args["dataType"] != "geoMap" || f.args["region"] != "region" {
		t.Fatalf("%#v", f.args)
	}
}
func TestRelatedBreakoutIsNotAnIndexOrPercent(t *testing.T) {
	f := &fakeClient{body: wrapped(map[string]any{"relatedQueries": map[string]any{
		"top":    []any{map[string]any{"query": "coffee shop", "value": "100", "extractedValue": 100}},
		"rising": []any{map[string]any{"query": "new coffee", "value": "Breakout", "extractedValue": 95750}, map[string]any{"query": "cold coffee", "value": "+250%", "extractedValue": 250}},
	}})}
	r, _ := NewService(f, fixedClock).Related(context.Background(), Query{Terms: []string{"Coffee"}, From: "2026-01-01", To: "2026-10-31"})
	if r.Status != OK || r.Related.Top[0].Metric != "relative_index" || r.Related.Rising[0].Metric != "breakout" || r.Related.Rising[0].Value != nil || *r.Related.Rising[1].Value != 250 {
		t.Fatalf("%#v", r.Related)
	}
}
func TestNoPositionalRelabelingOfScores(t *testing.T) {
	f := &fakeClient{body: wrapped(timeline(point("1766880000", false, score("Unexpected", "10", 10, true), score("Product B", "20", 20, true))))}
	r, _ := NewService(f, fixedClock).Compare(context.Background(), comparisonQuery())
	if r.Status != InvalidResponse {
		t.Fatal(r.Status)
	}
}
func TestComparisonIDIdentifiesActualReply(t *testing.T) {
	f := &fakeClient{body: wrapped(timeline(point("1766880000", false, score("Product A", "10", 10, true), score("Product B", "20", 20, true))))}
	s := NewService(f, fixedClock)
	a, _ := s.Compare(context.Background(), comparisonQuery())
	f.body = wrapped(timeline(point("1766880000", false, score("Product A", "11", 11, true), score("Product B", "20", 20, true))))
	b, _ := s.Compare(context.Background(), comparisonQuery())
	if a.Source.RequestFingerprint != b.Source.RequestFingerprint || a.Source.ComparisonID == b.Source.ComparisonID {
		t.Fatal("request and reply identities conflated")
	}
}

func TestErrorClassificationUsesExplicitStatusNotRequestIDDigits(t *testing.T) {
	for _, item := range []struct {
		value any
		want  string
	}{
		{map[string]any{"code": 500, "message": "request id abc401def"}, UpstreamError},
		{map[string]any{"message": "request id abc429def failed"}, UpstreamError},
		{map[string]any{"code": 429, "message": "request id abc401def"}, RateLimited},
		{map[string]any{"content": []any{map[string]any{"type": "text", "text": "{\"status\":401,\"message\":\"denied\"}"}}}, CredentialsRequired},
	} {
		decoded := map[string]any{}
		body := encode(item.value)
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		if got := errorStatus(decoded); got != item.want {
			t.Fatalf("status=%s want=%s", got, item.want)
		}
	}
}
