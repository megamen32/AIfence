package market

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLegacyMigrationPreservesExactTaskAndFailure(t *testing.T) {
	u := "https://store.softline.ru/vendor/abc-123/"
	g := "2026-11-26T09:34:51Z"
	now := time.Now().UTC().UnixNano()
	task := LegacyTask{Source: "softline", URL: u, Kind: "product", Generation: g, Priority: 100, State: "retry", UpdatedAt: now, EvidenceID: 19, Lane: ""}
	failure := LegacyFailure{Source: "softline", URL: u, Kind: "product", Generation: g, ErrorType: "parse_unclassified", Attempts: 3, NextAttempt: now + 123456789, EvidenceID: 19, State: "retry", Detail: "exact old error", UpdatedAt: now}
	in := LegacyStageInput{Source: "softline", Snapshot: strings.Repeat("a", 64), Tasks: []LegacyTaskInput{{Task: task, Failure: &failure}}}
	_, b, _, n, kind, e := normalizeLegacyStage(in)
	if e != nil || n != 1 || kind != "tasks" {
		t.Fatal(n, kind, e)
	}
	var decoded []struct {
		Legacy   LegacyTask    `json:"legacy"`
		Failure  LegacyFailure `json:"failure"`
		State    string        `json:"state"`
		Attempts int           `json:"attempts"`
		Next     time.Time     `json:"next_attempt"`
	}
	if e = json.Unmarshal(b, &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded[0].Legacy != task || decoded[0].Failure != failure || decoded[0].Attempts != 3 || decoded[0].Next.UnixNano() != failure.NextAttempt || decoded[0].State != "retry" {
		t.Fatal("lost original record", decoded)
	}
	in.Tasks = append(in.Tasks, in.Tasks[0])
	if _, _, _, _, _, e = normalizeLegacyStage(in); e == nil {
		t.Fatal("ambiguous task identity accepted")
	}
	in.Tasks = in.Tasks[:1]
	in.Tasks[0].Failure.URL = "https://store.softline.ru/other/"
	if _, _, _, _, _, e = normalizeLegacyStage(in); e == nil {
		t.Fatal("mismatched failure accepted")
	}
}
func TestLegacyResponseRemainsPendingUntilRawImported(t *testing.T) {
	r := LegacyResponse{ID: 1, Source: "softmap", URL: "https://softmap.ru/vendor/license/", ResponseURL: "https://softmap.ru/vendor/license/", CapturedAt: time.Now().UTC().UnixNano(), Status: 200, Hash: strings.Repeat("b", 64), Bytes: 12345}
	_, _, b, n, kind, e := normalizeLegacyStage(LegacyStageInput{Source: r.Source, Snapshot: strings.Repeat("a", 64), Responses: []LegacyResponse{r}})
	if e != nil || n != 1 || kind != "responses" {
		t.Fatal(n, kind, e)
	}
	var out []struct {
		Legacy       LegacyResponse `json:"legacy"`
		ArchiveState string         `json:"archive_state"`
		HTML         bool           `json:"html"`
	}
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	if out[0].Legacy != r || out[0].ArchiveState != "local_pending" || out[0].HTML {
		t.Fatal("untransferred body counted as native evidence", out)
	}
}
