package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
	"github.com/google/uuid"
)

type BlindInput struct{}

type BlindDimensions struct {
	ProductKey string `json:"product_key"`
	MetricKey  string `json:"metric_key"`
	Segment    string `json:"segment"`
	Unit       string `json:"unit"`
	Currency   string `json:"currency,omitempty"`
}

type BlindSeries struct {
	BlindDimensions
	Period        Period `json:"period"`
	ComparisonKey string `json:"comparison_key,omitempty"`
	BasePeriod    string `json:"base_period,omitempty"`
	Rows          int    `json:"rows"`
	WeeklyRows    int    `json:"weekly_rows"`
	UsableWeeks   int    `json:"usable_weeks"`
}

type BlindDiscovery struct {
	Status    string        `json:"status"`
	DatasetID string        `json:"dataset_id,omitempty"`
	AsOf      string        `json:"as_of,omitempty"`
	Series    []BlindSeries `json:"series"`
	Warnings  []string      `json:"warnings"`
}

type BlindCandidate struct {
	BlindDimensions
	ID         string   `json:"candidate_id"`
	Kind       string   `json:"kind"`
	Period     Period   `json:"period"`
	Reason     string   `json:"reason"`
	Control    string   `json:"control_product_key,omitempty"`
	ChangePct  *float64 `json:"relative_change_pct,omitempty"`
	ControlPct *float64 `json:"control_change_pct,omitempty"`
	Adjusted   *float64 `json:"control_adjusted_change_pct,omitempty"`
	SourceRefs []Ref    `json:"source_refs"`
}

type BlindScan struct {
	Status           string           `json:"status"`
	DatasetID        string           `json:"dataset_id,omitempty"`
	AsOf             string           `json:"as_of,omitempty"`
	ComparedWindows  int              `json:"compared_windows"`
	UncertainWindows int              `json:"uncertain_windows"`
	Candidates       []BlindCandidate `json:"candidates"`
	Warnings         []string         `json:"warnings"`
}

type BlindCaseInput struct {
	DatasetID   string   `json:"dataset_id"`
	CandidateID string   `json:"candidate_id"`
	Keywords    []string `json:"keywords,omitempty"`
}

type blindDataset struct {
	id      uuid.UUID
	series  Batch
	context WatchContext
	raw     []byte
}

type blindPeerKey struct {
	metric, segment, unit, currency, base, comparison string
}

type blindMeasure struct {
	meta  BlindSeries
	peer  blindPeerKey
	rows  []Observation
	weeks map[string][]Observation
}

type blindHit struct {
	candidate BlindCandidate
	scope     blindPeerKey
}

const maxBlindSeries = 64
const maxBlindCandidates = 20
const maxBlindWeeks = 104

var errBlindSelection = errors.New("select a snapshot for blind analysis on the closed /watchdog page")

func (s *privateWatch) registerBlindTools(a *agentsdk.Agent) {
	approval := a.RegisterEnvVar(&agentsdk.EnvVar{
		Slug: "watchdog_blind_model_route", Default: "off", Pattern: "^(off|approved)$",
		Description: "Enable imported historical snapshots in blind chat tools only after approval of the closed model route",
	})
	load := func(ctx context.Context) (blindDataset, error) {
		approved, _ := approval.Get(ctx)
		return s.loadBlind(ctx, approved == "approved")
	}
	a.RegisterTool(tool.Typed[BlindInput, BlindDiscovery]("discover_watchdog_series").
		Description("List measured products, metrics, segments and coverage in the active snapshot selected by this user. Takes {}.").
		Execute(func(ctx context.Context, _ BlindInput) (BlindDiscovery, error) {
			dataset, err := load(ctx)
			if errors.Is(err, errBlindSelection) {
				return BlindDiscovery{Status: "needs_selection", Series: []BlindSeries{}, Warnings: []string{err.Error()}}, nil
			}
			if err != nil {
				return BlindDiscovery{}, err
			}
			return dataset.discover(), nil
		}).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[BlindInput, BlindScan]("scan_watchdog_series").
		Description("Find independently comparable declines, measurement breaks and possible campaign or spike effects in the user's selected snapshot. Takes {}.").
		Execute(func(ctx context.Context, _ BlindInput) (BlindScan, error) {
			dataset, err := load(ctx)
			if errors.Is(err, errBlindSelection) {
				return BlindScan{Status: "needs_selection", Candidates: []BlindCandidate{}, Warnings: []string{err.Error()}}, nil
			}
			if err != nil {
				return BlindScan{}, err
			}
			result, _, err := dataset.scan(ctx)
			return result, err
		}).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[BlindCaseInput, WatchCaseResult]("collect_selected_watchdog_case").
		Description("Collect a controlled-decline case from scan_watchdog_series using its dataset_id and candidate_id. Pass document directly to Product Manager.").
		Execute(func(ctx context.Context, in BlindCaseInput) (WatchCaseResult, error) {
			dataset, err := load(ctx)
			if err != nil {
				return WatchCaseResult{}, err
			}
			return dataset.collect(ctx, in)
		}).Build(), agentsdk.AccessUser)
}

func (s *privateWatch) loadBlind(ctx context.Context, approved bool) (blindDataset, error) {
	if !closedOrigin(s.origin(ctx)) {
		return blindDataset{}, errBlindSelection
	}
	owner, err := watchOwner(ctx, agentsdk.ExecutionRequest)
	if err != nil {
		return blindDataset{}, err
	}
	out := blindDataset{}
	err = s.db.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		if err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM watchdog_owners
		    WHERE owner_id=$1 AND blind_selected FOR SHARE`, owner).Scan(&out.id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errBlindSelection
			}
			return errors.New("blind snapshot selection unavailable")
		}
		w := s.snapshotWatch(owner, out.id)
		raw, err := w.series.read(ctx, privateContext)
		if err != nil {
			return errors.New("selected context unavailable")
		}
		out.raw = raw
		readSnapshot := w.series.read
		w.series.read = func(ctx context.Context, name string) ([]byte, error) {
			if name == privateContext {
				return raw, nil
			}
			return readSnapshot(ctx, name)
		}
		out.context, err = w.loadContext(ctx, privateContext)
		if err != nil {
			return err
		}
		if !*out.context.Synthetic && !approved {
			return errors.New("historical blind analysis requires approval of the closed model route")
		}
		out.series, err = w.series.historical(ctx, privateSeries)
		if err != nil {
			return err
		}
		for _, row := range out.series.Observations {
			if synthetic, known := row.Details["synthetic"].(bool); !known || synthetic != *out.context.Synthetic {
				return errors.New("selected snapshot origin is inconsistent")
			}
		}
		return nil
	})
	return out, err
}

func blindDimensions(o Observation) (BlindDimensions, blindPeerKey) {
	m := BlindDimensions{o.ProductKey, o.MetricKey, chosen(o.Segment, "all"), o.Unit, o.Currency}
	return m, blindPeerKey{m.MetricKey, m.Segment, m.Unit, m.Currency, detailString(o.Details, "base_period"), o.ComparisonKey}
}

func (d blindDataset) measures() []*blindMeasure {
	type key struct {
		product string
		peer    blindPeerKey
	}
	index := map[key]*blindMeasure{}
	for _, o := range d.series.Observations {
		dims, peer := blindDimensions(o)
		k := key{dims.ProductKey, peer}
		m := index[k]
		if m == nil {
			m = &blindMeasure{meta: BlindSeries{BlindDimensions: dims, Period: o.Period, ComparisonKey: peer.comparison, BasePeriod: peer.base}, peer: peer, weeks: map[string][]Observation{}}
			index[k] = m
		}
		m.meta.Period.From = min(m.meta.Period.From, o.Period.From)
		m.meta.Period.To = max(m.meta.Period.To, o.Period.To)
		m.meta.Rows++
		dt, _ := time.Parse(time.DateOnly, o.Period.From)
		if dt.Weekday() == time.Monday && week(dt) == o.Period {
			m.rows = append(m.rows, o)
			m.weeks[o.Period.From] = append(m.weeks[o.Period.From], o)
			m.meta.WeeklyRows++
		}
	}
	out := make([]*blindMeasure, 0, len(index))
	cutoff := blindCutoff(d.series.AsOf)
	for _, m := range index {
		for _, rows := range m.weeks {
			if len(rows) == 1 && weeklyIssue(rows[0], cutoff) == "" {
				m.meta.UsableWeeks++
			}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.meta.UsableWeeks != b.meta.UsableWeeks {
			return a.meta.UsableWeeks > b.meta.UsableWeeks
		}
		return fmt.Sprint(a.peer, a.meta.ProductKey) < fmt.Sprint(b.peer, b.meta.ProductKey)
	})
	return out
}

func blindCutoff(asOf string) string {
	d, _ := time.Parse(time.RFC3339, asOf)
	return d.UTC().Format(time.DateOnly)
}

func (d blindDataset) discover() BlindDiscovery {
	out := BlindDiscovery{Status: "ok", DatasetID: d.id.String(), AsOf: d.series.AsOf, Series: []BlindSeries{}, Warnings: []string{}}
	m := d.measures()
	if len(m) > maxBlindSeries {
		out.Status = "partial"
		out.Warnings = append(out.Warnings, "Series inventory truncated to 64 dimensions, ordered by usable weekly coverage.")
	}
	for _, item := range m[:min(len(m), maxBlindSeries)] {
		out.Series = append(out.Series, item.meta)
	}
	return out
}

func (d blindDataset) collect(ctx context.Context, in BlindCaseInput) (WatchCaseResult, error) {
	if in.DatasetID != d.id.String() {
		return WatchCaseResult{}, errors.New("selected dataset changed; run scan_watchdog_series again")
	}
	_, hits, err := d.scan(ctx)
	if err != nil {
		return WatchCaseResult{}, err
	}
	hit, ok := hits[in.CandidateID]
	if !ok || hit.candidate.Kind != "controlled_decline" {
		return WatchCaseResult{}, errors.New("select a controlled decline from the active scan")
	}
	c := hit.candidate
	src := d.series
	src.Observations = []Observation{}
	for _, o := range d.series.Observations {
		_, group := blindDimensions(o)
		if group == hit.scope && (o.ProductKey == c.ProductKey || o.ProductKey == c.Control) {
			src.Observations = append(src.Observations, o)
		}
	}
	src.Warnings = []string{}
	series, err := json.Marshal(src)
	if err != nil {
		return WatchCaseResult{}, err
	}
	w := Watch{series: Service{read: func(_ context.Context, name string) ([]byte, error) {
		switch name {
		case privateSeries:
			return series, nil
		case privateContext:
			return d.raw, nil
		default:
			return nil, errors.New("active snapshot only")
		}
	}, config: func(context.Context) Settings { return Settings{} }}, contextPath: func(context.Context) string { return "" }}
	return w.CollectCase(ctx, WatchCaseInput{Series: SignalInput{
		ProductKey: c.ProductKey, ControlProductKey: c.Control, MetricKey: c.MetricKey, Segment: c.Segment,
		From: c.Period.From, To: c.Period.To, SourcePath: privateSeries, ContextPath: privateContext,
	}, Keywords: in.Keywords})
}
