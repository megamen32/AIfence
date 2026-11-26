package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

type demandQuery struct {
	Phrase string `json:"phrase"`
	Monday string `json:"monday"`
}

type wordstatRequest struct {
	Phrase   string   `json:"phrase"`
	Period   string   `json:"period"`
	FromDate string   `json:"fromDate"`
	ToDate   string   `json:"toDate"`
	Devices  []string `json:"devices"`
	FolderID string   `json:"folderId"`
}

func registerWordstat(a *agentsdk.Agent) {
	conn := a.RegisterConnection(&agentsdk.Connection{
		Slug: "wordstat", Name: "Yandex Search API (Wordstat)", Description: "Weekly search frequency for an explicitly selected phrase.",
		BaseURL: "https://searchapi.api.cloud.yandex.net", AuthMode: agentsdk.ConnectionAuthToken, BindingMode: agentsdk.BindingPerUser,
		AuthInjection:     agentsdk.AuthInjection{Type: agentsdk.AuthInjectAPIKey, Name: "Authorization"},
		SetupInstructions: "Bind your Yandex Search API credential as the complete Authorization value (Api-Key followed by a space and the key). The key stays in Airlock.",
	})
	folder := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "wordstat_folder_id", Description: "Yandex Cloud folder ID for Wordstat queries", Pattern: "^[a-zA-Z0-9_-]{1,50}$"})
	a.RegisterTool(tool.Typed[demandQuery, EvidenceBatch[int64]]("compare_demand").
		Description("Return observed Yandex search counts for one phrase in the two completed UTC weeks before Monday, all regions and devices.").
		Execute(func(ctx context.Context, in demandQuery) (EvidenceBatch[int64], error) {
			id, err := folder.Get(ctx)
			if err != nil {
				return EvidenceBatch[int64]{}, err
			}
			return compareDemand(ctx, in, id, time.Now().UTC(), func(ctx context.Context, req wordstatRequest) ([]byte, error) {
				return conn.Request(ctx, agentsdk.RequestOpts{Method: http.MethodPost, Path: "/v2/wordstat/dynamics", Headers: map[string]string{"Content-Type": "application/json"}, Body: req})
			})
		}).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
}

func compareDemand(ctx context.Context, in demandQuery, folder string, now time.Time, fetch func(context.Context, wordstatRequest) ([]byte, error)) (EvidenceBatch[int64], error) {
	out := EvidenceBatch[int64]{SchemaVersion: "aifence.v1", Source: "wordstat", AsOf: now.UTC().Format(time.RFC3339), Status: "unavailable", Observations: []EvidenceObservation[int64]{}, Warnings: []string{}}
	monday, err := time.Parse(time.DateOnly, in.Monday)
	if err != nil || monday.Weekday() != time.Monday || monday.After(now.UTC()) {
		return out, errors.New("monday must start a completed reporting window")
	}
	if in.Phrase == "" || strings.TrimSpace(in.Phrase) != in.Phrase || utf8.RuneCountInString(in.Phrase) > 400 {
		return out, errors.New("phrase must contain 1 to 400 characters with no outer whitespace")
	}
	if folder == "" {
		out.Warnings = append(out.Warnings, "Wordstat folder has not been configured")
		return out, nil
	}
	first, second := monday.AddDate(0, 0, -14), monday.AddDate(0, 0, -7)
	raw, err := fetch(ctx, wordstatRequest{Phrase: in.Phrase, Period: "PERIOD_WEEKLY", FromDate: first.Format(time.RFC3339), ToDate: monday.AddDate(0, 0, -1).Format(time.RFC3339), Devices: []string{"DEVICE_ALL"}, FolderID: folder})
	if err != nil {
		return out, err
	}
	var response struct {
		Results []struct {
			Date  time.Time `json:"date"`
			Count string    `json:"count"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return out, errors.New("invalid Wordstat response")
	}
	counts := map[time.Time]int64{}
	for _, item := range response.Results {
		d := item.Date.UTC()
		n, err := strconv.ParseInt(item.Count, 10, 64)
		if err != nil || n < 0 || (d != first && d != second) {
			return out, errors.New("unexpected Wordstat weekly observation")
		}
		if _, duplicate := counts[d]; duplicate {
			return out, errors.New("duplicate Wordstat week")
		}
		counts[d] = n
	}
	if len(counts) != 2 {
		out.Warnings = append(out.Warnings, "Wordstat has not reported both requested weeks")
		return out, nil
	}
	for _, start := range []time.Time{first, second} {
		value := counts[start]
		from := start.Format(time.DateOnly)
		key := hash([]string{"wordstat", in.Phrase, from, "all"})
		out.Observations = append(out.Observations, EvidenceObservation[int64]{
			ID: "wordstat:" + key, Kind: "metric", ProductKey: in.Phrase, MetricKey: "search_queries", Value: &value, Unit: "count",
			Period: Period{From: from, To: start.AddDate(0, 0, 6).Format(time.DateOnly)}, Status: "observed",
			SourceRef: Ref{SourceID: "yandex-wordstat:search-api", Locator: fmt.Sprintf("Wordstat.GetDynamics/%s/%s", from, key)},
		})
	}
	out.Status = "ok"
	return out, nil
}
