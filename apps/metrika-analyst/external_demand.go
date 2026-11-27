package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

const (
	demandFixture     = "fixtures/external-demand.json"
	demandDirectory   = "external-demand"
	demandImportLimit = 512 << 10
	countMax          = 1<<53 - 1
)

type demandInput struct {
	ProductKey string `json:"product_key" jsonschema:"description=Exact catalog product key."`
	Query      string `json:"query" jsonschema:"description=Exact search phrase as used by the source."`
	Geo        string `json:"geo" jsonschema:"description=ALL for worldwide. Wordstat accepts RU or Yandex region ID. Trends uses Google Trends geo codes such as RU."`
	Monday     string `json:"monday" jsonschema:"description=UTC Monday after the two completed Monday to Sunday weeks."`
	SourcePath string `json:"source_path,omitempty" jsonschema:"description=Authorized evidence JSON in the caller's external-demand or evidence directory. Use fixtures/external-demand.json only for explicit synthetic use."`
}

type demandDetails struct {
	Query         string `json:"query"`
	Geo           string `json:"geo"`
	Origin        string `json:"origin"`
	Synthetic     bool   `json:"synthetic"`
	Normalization string `json:"normalization"`
}

type demandObservation struct {
	Observation
	Details demandDetails `json:"details"`
}

type demandBatch struct {
	SchemaVersion string              `json:"schema_version"`
	Source        string              `json:"source"`
	AsOf          string              `json:"as_of"`
	Status        string              `json:"status"`
	Observations  []demandObservation `json:"observations"`
	Warnings      []string            `json:"warnings"`
}

type demandSpec struct {
	metric, unit, sourceID string
}

var (
	trendsGeo    = regexp.MustCompile(`^[A-Z]{2}(?:-[A-Za-z0-9-]{1,12})?$`)
	wordstatSpec = demandSpec{"search_queries", "count", "yandex-wordstat:search-api"}
	trendsSpec   = demandSpec{"search_interest", "index", "google_trends"}
)

func registerExternalDemand(a *agentsdk.Agent) {
	a.RegisterDirectory(demandDirectory, agentsdk.DirectoryOpts{
		Read: agentsdk.AccessUser, Write: agentsdk.AccessUser, List: agentsdk.AccessUser, Scope: agentsdk.ScopeUser,
		Description: "Private weekly Wordstat and Google Trends snapshots supplied by the current user",
	})
	fetch := registerWordstatConnection(a)
	read := func(ctx context.Context, path string) ([]byte, error) {
		if path == demandFixture {
			return assets.ReadFile(path)
		}
		user, ok := agentsdk.CallerFromContext(ctx).User()
		if !ok || user.ID == "" {
			return nil, agentsdk.ErrNotFound
		}
		resolved, err := a.ResolveFilePath(ctx, path, agentsdk.FileOperationRead)
		if err != nil || !ownedDemandPath(string(resolved), user.ID) {
			return nil, agentsdk.ErrNotFound
		}
		f, err := a.OpenFile(ctx, string(resolved))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, demandImportLimit+1))
	}
	a.RegisterTool(tool.Typed[wordstatInput, demandBatch]("compare_demand").
		Description("Read Wordstat counts for a phrase, all regions and devices, in the two completed UTC weeks before Monday. For the sourcewatch binding.").
		Execute(func(ctx context.Context, in wordstatInput) (demandBatch, error) {
			return readDemand(ctx, demandInput{ProductKey: in.Phrase, Query: in.Phrase, Geo: "ALL", Monday: in.Monday}, wordstatSpec, "wordstat", time.Now(), read, fetch)
		}).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
	a.RegisterTool(tool.Typed[demandInput, demandBatch]("compare_wordstat").
		Description("Compare counts from Wordstat for the exact query and geo across two completed weeks. Reads official API history or the explicitly selected authorized snapshot.").
		Execute(func(ctx context.Context, in demandInput) (demandBatch, error) {
			return readDemand(ctx, in, wordstatSpec, "research", time.Now(), read, fetch)
		}).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
	a.RegisterTool(tool.Typed[demandInput, demandBatch]("compare_google_trends").
		Description("Read two weekly 0–100 Google Trends indices for one query and geo from a single authorized export with a common normalization. Requires source_path.").
		Execute(func(ctx context.Context, in demandInput) (demandBatch, error) {
			return readDemand(ctx, in, trendsSpec, "research", time.Now(), read, fetch)
		}).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
}

func ownedDemandPath(path, userID string) bool {
	suffix := "/user-" + userID + "/"
	return strings.HasPrefix(path, demandDirectory+suffix) || strings.HasPrefix(path, "evidence"+suffix)
}

func readDemand(ctx context.Context, in demandInput, spec demandSpec, source string, now time.Time,
	read func(context.Context, string) ([]byte, error), fetch wordstatFetcher) (demandBatch, error) {
	out := demandBatch{"aifence.v1", source, now.UTC().Format(time.RFC3339), "unavailable", []demandObservation{}, []string{}}
	monday, err := time.Parse(time.DateOnly, in.Monday)
	if err != nil || monday.Weekday() != time.Monday || monday.After(now) {
		return out, fmt.Errorf("monday must be a UTC Monday at or before the current date")
	}
	if !demandText(in.ProductKey, 400) || !demandText(in.Query, 400) || !demandText(in.Geo, 32) {
		return out, fmt.Errorf("product_key, exact query and geo are required without leading or trailing whitespace")
	}
	if spec == trendsSpec && in.Geo != "ALL" && !trendsGeo.MatchString(in.Geo) {
		return out, fmt.Errorf("use ALL or a Google Trends geo code")
	}
	weeks := [2]Period{
		{monday.AddDate(0, 0, -14).Format(time.DateOnly), monday.AddDate(0, 0, -8).Format(time.DateOnly)},
		{monday.AddDate(0, 0, -7).Format(time.DateOnly), monday.AddDate(0, 0, -1).Format(time.DateOnly)},
	}
	switch {
	case in.SourcePath != "":
		return readDemandImport(ctx, in, spec, monday, now, weeks, out, read)
	case spec == wordstatSpec:
		return readWordstatWeeks(ctx, in, weeks, out, fetch)
	default:
		return out.unavailable("Google Trends requires an authorized single-export snapshot."), nil
	}
}

func (out demandBatch) unavailable(reason string) demandBatch {
	out.Warnings = append(out.Warnings, reason)
	return out
}

func (out demandBatch) observed(rows [2]demandObservation) demandBatch {
	out.Status = "ok"
	out.Observations = rows[:]
	return out
}

func demandText(s string, max int) bool {
	return s != "" && strings.TrimSpace(s) == s && utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}

func readDemandImport(ctx context.Context, in demandInput, spec demandSpec, monday, now time.Time, weeks [2]Period, out demandBatch, read func(context.Context, string) ([]byte, error)) (demandBatch, error) {
	if !strings.HasSuffix(in.SourcePath, ".json") {
		return out, fmt.Errorf("select an authorized evidence JSON")
	}
	raw, err := read(ctx, in.SourcePath)
	if err != nil {
		return out.unavailable("External demand snapshot could not be opened in the caller's scope."), nil
	}
	if len(raw) > demandImportLimit {
		return out, fmt.Errorf("external demand snapshot exceeds 512 KiB")
	}
	var snapshot demandBatch
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.SchemaVersion != "aifence.v1" ||
		snapshot.Source != "research" || snapshot.Observations == nil {
		return out, fmt.Errorf("invalid external demand evidence batch")
	}
	asOf, err := time.Parse(time.RFC3339, snapshot.AsOf)
	if err != nil || asOf.After(now) {
		return out, fmt.Errorf("invalid external demand snapshot timestamp")
	}
	out.AsOf = snapshot.AsOf
	if asOf.Before(monday) {
		return out.unavailable("The snapshot predates the end of the requested comparison."), nil
	}
	switch snapshot.Status {
	case "unavailable":
		return out.unavailable("The imported source reports unavailable demand data."), nil
	case "ok", "partial":
	default:
		return out, fmt.Errorf("invalid external demand snapshot status")
	}

	var rows [2]demandObservation
	var seen [2]bool
	for _, row := range snapshot.Observations {
		d := row.Details
		if row.ProductKey != in.ProductKey || row.Kind != "metric" || row.MetricKey != spec.metric ||
			d.Query != in.Query || d.Geo != in.Geo || !spec.acceptsSource(row.SourceRef.SourceID) {
			continue
		}
		for i, week := range weeks {
			if row.Period != week {
				continue
			}
			if seen[i] {
				return out, fmt.Errorf("duplicate external demand observation for a requested week")
			}
			seen[i] = true
			rows[i] = row
		}
	}
	if !seen[0] || !seen[1] || rows[0].Status != "observed" || rows[1].Status != "observed" {
		return out.unavailable("Two complete observed weeks at the exact query and geo are required."), nil
	}
	for _, row := range rows {
		if !validDemandImport(row, spec, in.SourcePath == demandFixture) {
			return out, fmt.Errorf("invalid external demand value or provenance")
		}
	}
	first, second := rows[0].Details, rows[1].Details
	if rows[0].ID == rows[1].ID || rows[0].SourceRef.Locator == rows[1].SourceRef.Locator ||
		rows[0].SourceRef.SourceID != rows[1].SourceRef.SourceID ||
		first.Origin != second.Origin || first.Normalization != second.Normalization ||
		(spec == trendsSpec && (rows[0].SourceRef.Revision == "" || rows[0].SourceRef.Revision != rows[1].SourceRef.Revision)) {
		return out.unavailable("The requested weeks do not share one source, origin and normalization basis."), nil
	}
	return out.observed(rows), nil
}

func (spec demandSpec) acceptsSource(sourceID string) bool {
	return sourceID == spec.sourceID || (spec == wordstatSpec && sourceID == "wordstat")
}

func validDemandImport(row demandObservation, spec demandSpec, fixture bool) bool {
	d := row.Details
	if !demandText(row.ID, 256) || strings.TrimSpace(row.SourceRef.Locator) == "" || row.Unit != spec.unit ||
		row.Value == nil || math.IsNaN(*row.Value) || math.IsInf(*row.Value, 0) || *row.Value < 0 ||
		(d.Origin != "authorized_import" && d.Origin != "synthetic_fixture") ||
		d.Synthetic != (d.Origin == "synthetic_fixture") || (fixture && !d.Synthetic) {
		return false
	}
	if spec == wordstatSpec {
		return *row.Value <= countMax && *row.Value == math.Trunc(*row.Value) && d.Normalization == wordstatNormalization
	}
	basis, ok := strings.CutPrefix(d.Normalization, "google_trends_0_100:")
	return *row.Value <= 100 && ok && demandText(basis, 128) && row.SourceRef.Revision == basis
}
