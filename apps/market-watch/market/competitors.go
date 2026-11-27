package market

import (
	catalog "agent/collector"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/airlockrun/agentsdk"
)

const competitorRegistry = "catalog/competitors.json"

type AddCompetitorsInput struct {
	Sources []string `json:"sources" jsonschema:"One or more supported competitor IDs or domains: allsoft, syssoft"`
}
type CompetitorState struct {
	ID              string   `json:"id"`
	Host            string   `json:"host"`
	Enabled         bool     `json:"enabled"`
	Mode            string   `json:"mode"`
	Status          string   `json:"status"`
	Offers          int64    `json:"observed_offers"`
	LastObservation string   `json:"last_observation,omitempty"`
	Errors          []string `json:"errors,omitempty"`
	SeedURLs        []string `json:"seed_urls"`
}
type CompetitorResult struct {
	Sources      []CompetitorState `json:"sources"`
	NewlyEnabled []string          `json:"newly_enabled,omitempty"`
	Warnings     []string          `json:"warnings,omitempty"`
}

func normalizeCompetitor(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "www.")
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.TrimSuffix(raw, ".ru")
	return raw
}
func (s *Service) enabledCompetitors(ctx context.Context) (map[string]bool, error) {
	b, e := s.store.ReadFile(ctx, competitorRegistry)
	if errors.Is(e, agentsdk.ErrNotFound) {
		return map[string]bool{}, nil
	}
	if e != nil {
		return nil, e
	}
	var ids []string
	if e = json.Unmarshal(b, &ids); e != nil {
		return nil, e
	}
	supported := map[string]bool{}
	for _, p := range catalog.CompetitorProfiles() {
		supported[p.ID] = true
	}
	out := map[string]bool{}
	for _, id := range ids {
		if !supported[id] {
			return nil, fmt.Errorf("unknown persisted competitor %q", id)
		}
		out[id] = true
	}
	return out, nil
}
func (s *Service) configuredSources(ctx context.Context) ([]catalog.Source, error) {
	base, e := catalog.Sources()
	if e != nil {
		return nil, e
	}
	enabled, e := s.enabledCompetitors(ctx)
	if e != nil {
		return nil, e
	}
	for _, p := range catalog.CompetitorProfiles() {
		if enabled[p.ID] {
			base = append(base, p)
		}
	}
	dynamic, e := s.adaptiveSources(ctx)
	if e != nil {
		return nil, e
	}
	return append(base, dynamic...), nil
}
func (s *Service) enableCompetitors(ctx context.Context, in AddCompetitorsInput) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(in.Sources) < 1 || len(in.Sources) > len(catalog.CompetitorProfiles()) {
		return nil, fmt.Errorf("choose one or more supported competitors: allsoft, syssoft")
	}
	supported := map[string]bool{}
	for _, p := range catalog.CompetitorProfiles() {
		supported[p.ID] = true
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, raw := range in.Sources {
		id := normalizeCompetitor(raw)
		if !supported[id] {
			return nil, fmt.Errorf("no validated parser profile for %q; supported: allsoft, syssoft", raw)
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	enabled, e := s.enabledCompetitors(ctx)
	if e != nil {
		return nil, e
	}
	var added []string
	for _, id := range ids {
		if !enabled[id] {
			enabled[id] = true
			added = append(added, id)
		}
	}
	if len(added) == 0 {
		return added, nil
	}
	persisted := []string{}
	for _, p := range catalog.CompetitorProfiles() {
		if enabled[p.ID] {
			persisted = append(persisted, p.ID)
		}
	}
	b, e := json.Marshal(persisted)
	if e != nil {
		return nil, e
	}
	_, e = s.store.WriteFile(ctx, competitorRegistry, bytes.NewReader(b), "application/json")
	return added, e
}

// AddCompetitors persists actual configuration and performs a bounded first
// refresh through the same robots-aware fetcher and observation storage as cron.
// A repeated request does not re-enable or re-fetch an already enabled source.
func (s *Service) AddCompetitors(ctx context.Context, in AddCompetitorsInput) (CompetitorResult, error) {
	added, e := s.enableCompetitors(ctx, in)
	if e != nil {
		return CompetitorResult{}, e
	}
	var warnings []string
	if len(added) > 0 {
		ev, e := s.Refresh(ctx, RefreshInput{SourceIDs: added})
		if e != nil {
			warnings = append(warnings, "Source enabled; first collection failed: "+e.Error())
		} else {
			warnings = append(warnings, ev.Warnings...)
		}
	}
	out, e := s.Competitors(ctx, struct{}{})
	if e != nil {
		return out, e
	}
	out.NewlyEnabled = added
	out.Warnings = append(out.Warnings, warnings...)
	return out, nil
}
func (s *Service) Competitors(ctx context.Context, _ struct{}) (CompetitorResult, error) {
	enabled, e := s.enabledCompetitors(ctx)
	if e != nil {
		return CompetitorResult{}, e
	}
	snap, e := s.Snapshot(ctx)
	if e != nil {
		return CompetitorResult{}, e
	}
	runs := map[string]catalog.Run{}
	for _, r := range snap.Runs {
		runs[r.Source] = r
	}
	counts := map[string]SourceSummary{}
	if s.queries != nil {
		coverage, e := s.Coverage(ctx, struct{}{})
		if e != nil {
			return CompetitorResult{}, e
		}
		for _, c := range coverage.Sources {
			counts[c.Source] = c
		}
	}
	out := CompetitorResult{Sources: []CompetitorState{}}
	for _, p := range catalog.CompetitorProfiles() {
		state := CompetitorState{ID: p.ID, Host: p.Host, Enabled: enabled[p.ID], Mode: "seed_only_no_sitemap_coverage", Status: "not_enabled", SeedURLs: p.Seeds}
		if state.Enabled {
			state.Status = "enabled_awaiting_observations"
			if r, ok := runs[p.ID]; ok {
				state.Status = r.Status
				state.Errors = r.Errors
				state.Offers = int64(len(r.Offers))
			}
			if c, ok := counts[p.ID]; ok {
				state.Offers = c.Offers
				state.LastObservation = c.LastObservation
			}
			if state.Offers == 0 && state.Status == "ok" {
				state.Status = "enabled_no_prices"
			}
		}
		out.Sources = append(out.Sources, state)
	}
	return out, nil
}
