package demand

import (
	"context"
	"encoding/json"
	"github.com/airlockrun/agentsdk"
	"testing"
)

type fake struct {
	calls int
	body  string
	args  any
	tool  string
}

func (f *fake) CallTool(_ context.Context, name string, args any) (*agentsdk.MCPToolCallResponse, error) {
	f.calls++
	f.args = args
	f.tool = name
	var r agentsdk.MCPToolCallResponse
	if err := json.Unmarshal([]byte(f.body), &r); err != nil {
		panic(err)
	}
	return &r, nil
}
func TestUnavailableMonthNeverCallsProvider(t *testing.T) {
	f := &fake{}
	s := New(f)
	r, e := s.dynamics(context.Background(), DynamicsInput{Phrase: "Kaspersky", FromMonth: "2026-11", ToMonth: "2026-11"})
	if e != nil || r.Status != "unavailable_period" || f.calls != 0 || r.RequestedPeriod.To != "2026-11" || len(r.Data) > 0 {
		t.Fatalf("bad unavailable result: %+v %v calls %d", r, e, f.calls)
	}
}
func TestMissingCredentialIsNotZeroDemand(t *testing.T) {
	f := &fake{body: `{"isError":true,"content":[{"type":"text","text":"Ошибка: Требуются WORDSTAT_API_KEY и WORDSTAT_FOLDER_ID"}]}`}
	r, e := New(f).top(context.Background(), TopInput{Phrase: "касперский для бизнеса"})
	if e != nil || r.Status != "credentials_required" || len(r.Rows) != 0 || len(r.Data) != 0 {
		t.Fatalf("bad credentials result %+v %v", r, e)
	}
}
func TestHistoricalTopKeepsTrueZeroAndUnknownWindow(t *testing.T) {
	f := &fake{body: `{"content":[{"type":"text","text":"{\"topRequests\":[{\"phrase\":\"test\",\"count\":\"0\"}]}"}]}`}
	r, e := New(f).top(context.Background(), TopInput{Phrase: "test"})
	if e != nil || r.Status != "ok" || r.Source.WindowKnown || len(r.Rows) != 1 || r.Rows[0].Count == nil || *r.Rows[0].Count != 0 {
		t.Fatalf("bad historical result %+v %v", r, e)
	}
}
func TestMonthlyBoundsAndFiltersPassExactly(t *testing.T) {
	f := &fake{body: `{"content":[{"type":"text","text":"{\"dynamics\":[]}"}]}`}
	r, e := New(f).dynamics(context.Background(), DynamicsInput{Phrase: "антивирус +для бизнеса", FromMonth: "2026-07", ToMonth: "2026-09", RegionIDs: []int{225}, Devices: []string{"phone"}})
	a := f.args.(map[string]any)
	if e != nil || r.Status != "ok" || a["fromDate"] != "2026-07-01T00:00:00Z" || a["toDate"] != "2026-09-30T23:59:59Z" || a["phrase"] != "антивирус +для бизнеса" {
		t.Fatalf("bad args %+v result %+v err %v", a, r, e)
	}
}

func TestActualRegionPayloadKeepsGeography(t *testing.T) {
	got := rows(json.RawMessage(`{"results":[{"region":"213","count":"12","share":"0.3","affinityIndex":"2.1"}]}`))
	if len(got) != 1 || got[0].Label != "213" || got[0].Count == nil || *got[0].Count != 12 {
		t.Fatalf("unexpected region rows: %+v", got)
	}
}

func TestMonthlyRejectsUnsupportedOperatorsWithoutChangingPhrase(t *testing.T) {
	for _, phrase := range []string{"!антивирус", "\"антивирус\"", "[антивирус]", "антивирус -домашний"} {
		f := &fake{}
		r, e := New(f).dynamics(context.Background(), DynamicsInput{Phrase: phrase, FromMonth: "2026-07", ToMonth: "2026-09"})
		if e != nil || r.Status != "invalid_request" || f.calls != 0 || r.Phrase != phrase {
			t.Fatalf("bad operator validation: %+v %v", r, e)
		}
	}
}

func TestSuccessfulRegionResponsePreservesJSONObjectContract(t *testing.T) {
	f := &fake{body: `{"content":[{"type":"text","text":"{\"results\":[{\"region\":\"213\",\"count\":\"12\",\"share\":\"0.3\",\"affinityIndex\":\"2.1\"}]}"}]}`}
	r, err := New(f).regions(context.Background(), RegionsInput{Phrase: "антивирус для бизнеса", RegionMode: "regions"})
	if err != nil || r.Status != "ok" || len(r.Rows) != 1 || r.Rows[0].Label != "Регион 213" || r.Rows[0].RegionID != "213" {
		t.Fatalf("bad fixture result: %+v %v", r, err)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	data, ok := response["data"].(map[string]any)
	if !ok {
		t.Fatalf("wire data must be an object, got %T", response["data"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("missing results: %v", data)
	}
	region, ok := results[0].(map[string]any)
	if !ok || region["region"] != "213" || region["count"] != "12" {
		t.Fatalf("original JSON changed: %v", data)
	}
}
