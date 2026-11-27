package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/airlockrun/agentsdk"
)

const (
	wordstatEndpoint         = "/v2/wordstat/dynamics"
	wordstatDocs             = "https://aistudio.yandex.ru/en/docs/search-api/api-ref/Wordstat/getDynamics"
	wordstatAvailableThrough = "2026-10-07"
	wordstatNormalization    = "weekly_count_all_devices"
)

type wordstatInput struct {
	Phrase string `json:"phrase"`
	Monday string `json:"monday"`
}

type wordstatRequest struct {
	Phrase    string   `json:"phrase"`
	Period    string   `json:"period"`
	FromDate  string   `json:"fromDate"`
	ToDate    string   `json:"toDate"`
	Devices   []string `json:"devices"`
	RegionIDs []string `json:"regionIds,omitempty"`
	FolderID  string   `json:"folderId"`
}

// A nil body without an error indicates an unconfigured binding.
type wordstatFetcher func(context.Context, wordstatRequest) ([]byte, error)

func registerWordstatConnection(a *agentsdk.Agent) wordstatFetcher {
	conn := a.RegisterConnection(&agentsdk.Connection{
		Slug: "wordstat", Name: "Yandex Search API (Wordstat)",
		Description: "Read historical weekly request counts for an exact search phrase.",
		BaseURL:     "https://searchapi.api.cloud.yandex.net", AuthMode: agentsdk.ConnectionAuthToken, BindingMode: agentsdk.BindingPerUser,
		AuthInjection:     agentsdk.AuthInjection{Type: agentsdk.AuthInjectAPIKey, Name: "Authorization"},
		SetupInstructions: "Use the complete Authorization value for the Yandex Search API credential. The credential stays in Airlock.",
	})
	folder := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "wordstat_folder_id", Description: "Yandex Cloud folder ID for Wordstat queries", Pattern: "^([a-zA-Z0-9_-]{1,50})?$"})
	return func(ctx context.Context, req wordstatRequest) ([]byte, error) {
		id, err := folder.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot read the Wordstat folder configuration")
		}
		if id == "" {
			return nil, nil
		}
		req.FolderID = id
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		raw, err := conn.Request(ctx, agentsdk.RequestOpts{Method: http.MethodPost, Path: wordstatEndpoint,
			Headers: map[string]string{"Content-Type": "application/json"}, Body: req})
		if _, needsAuth := agentsdk.IsAuthRequired(err); needsAuth {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("Wordstat API request failed")
		}
		return raw, nil
	}
}

func readWordstatWeeks(ctx context.Context, in demandInput, weeks [2]Period, out demandBatch, fetch wordstatFetcher) (demandBatch, error) {
	if weeks[1].To > wordstatAvailableThrough {
		return out.unavailable("Yandex Search API supplies Wordstat history through 2026-10-07. This request requires a later week."), nil
	}
	req := wordstatRequest{
		Phrase: in.Query, Period: "PERIOD_WEEKLY",
		FromDate: weeks[0].From + "T00:00:00Z", ToDate: weeks[1].To + "T00:00:00Z",
		Devices: []string{"DEVICE_ALL"},
	}
	if in.Geo != "ALL" {
		id := in.Geo
		if id == "RU" {
			id = "225"
		}
		n, err := strconv.ParseUint(id, 10, 32)
		if err != nil || n == 0 || id != strconv.FormatUint(n, 10) {
			return out, fmt.Errorf("Wordstat geo must be ALL, RU or a Yandex region ID")
		}
		req.RegionIDs = []string{id}
	}
	raw, err := fetch(ctx, req)
	if err != nil {
		return out, err
	}
	if raw == nil {
		return out.unavailable("The authorized Wordstat connection or its folder is unconfigured."), nil
	}
	var response struct {
		Results []struct {
			Date  time.Time `json:"date"`
			Count string    `json:"count"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Results == nil {
		return out, fmt.Errorf("invalid Wordstat dynamics response")
	}
	var rows [2]demandObservation
	for _, item := range response.Results {
		date := item.Date.UTC()
		from := date.Format(time.DateOnly)
		var i int
		switch from {
		case weeks[0].From:
			i = 0
		case weeks[1].From:
			i = 1
		default:
			return out, fmt.Errorf("unexpected Wordstat observation date")
		}
		count, err := strconv.ParseInt(item.Count, 10, 64)
		if err != nil || count < 0 || count > countMax || rows[i].Value != nil || date != date.Truncate(24*time.Hour) {
			return out, fmt.Errorf("invalid or duplicate Wordstat weekly observation")
		}
		period := weeks[i]
		parameters := url.Values{"query": {in.Query}, "geo": {in.Geo}, "from": {period.From}, "to": {period.To}}
		id := "wordstat:" + hash([]string{in.ProductKey, in.Query, in.Geo, period.From})
		rows[i] = demandObservation{
			Observation: Observation{
				ID: id, Kind: "metric", ProductKey: in.ProductKey, MetricKey: wordstatSpec.metric,
				Value: num(float64(count)), Unit: wordstatSpec.unit, Period: period, Status: "observed",
				SourceRef: Ref{SourceID: wordstatSpec.sourceID, Locator: "Wordstat.GetDynamics/weekly?" + parameters.Encode(), URL: wordstatDocs},
			},
			Details: demandDetails{Query: in.Query, Geo: in.Geo, Origin: "live_api", Normalization: wordstatNormalization},
		}
	}
	if rows[0].Value == nil || rows[1].Value == nil {
		return out.unavailable("Wordstat did not return both requested weeks."), nil
	}
	return out.observed(rows), nil
}
