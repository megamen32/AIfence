package demand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type regionIndex struct {
	names     map[string]string
	ambiguous map[string]bool
}

type regionFlight struct {
	done  chan struct{}
	index *regionIndex
	err   error
}

type regionCache struct {
	mu      sync.Mutex
	index   *regionIndex
	loading *regionFlight
}

// Public region names belong to the shared provider, not to an individual user.
// Initialization stays lazy, after Regions has authorized the caller.
func (s *Service) loadRegionIndex(ctx context.Context) (*regionIndex, error) {
	s.regionsCache.mu.Lock()
	if s.regionsCache.index != nil {
		index := s.regionsCache.index
		s.regionsCache.mu.Unlock()
		return index, nil
	}
	if flight := s.regionsCache.loading; flight != nil {
		s.regionsCache.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			return flight.index, flight.err
		}
	}
	flight := &regionFlight{done: make(chan struct{})}
	s.regionsCache.loading = flight
	s.regionsCache.mu.Unlock()

	index, err := s.fetchRegionIndex(ctx)
	s.regionsCache.mu.Lock()
	flight.index, flight.err = index, err
	if err == nil {
		s.regionsCache.index = index
	}
	s.regionsCache.loading = nil
	close(flight.done)
	s.regionsCache.mu.Unlock()
	return index, err
}

func (s *Service) fetchRegionIndex(ctx context.Context) (*regionIndex, error) {
	response, err := s.mcp.CallTool(ctx, "list_regions", struct{}{})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("Wordstat region directory returned no response")
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.IsError {
		return nil, errors.New("Wordstat region directory is unavailable")
	}
	payload := envelope.Structured
	if len(payload) == 0 || string(payload) == "null" {
		var parts []string
		for _, content := range envelope.Content {
			if content.Type == "text" {
				parts = append(parts, content.Text)
			}
		}
		payload = json.RawMessage(strings.Join(parts, "\n"))
	}
	return parseRegionIndex(payload)
}

// Wire schema: regions[] with string id, label and recursive children.
// https://aistudio.yandex.ru/en/docs/search-api/api-ref/Wordstat/getRegionsTree
func parseRegionIndex(raw json.RawMessage) (*regionIndex, error) {
	type node struct {
		ID       string            `json:"id"`
		Label    string            `json:"label"`
		Children []json.RawMessage `json:"children"`
	}
	var tree struct {
		Regions []json.RawMessage `json:"regions"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	index := &regionIndex{names: make(map[string]string), ambiguous: make(map[string]bool)}
	var visit func(json.RawMessage) error
	visit = func(raw json.RawMessage) error {
		var current node
		if err := json.Unmarshal(raw, &current); err != nil {
			return err
		}
		id, label := strings.TrimSpace(current.ID), strings.TrimSpace(current.Label)
		if id != "" && label != "" && !index.ambiguous[id] {
			if previous, exists := index.names[id]; exists && previous != label {
				delete(index.names, id)
				index.ambiguous[id] = true
			} else {
				index.names[id] = label
			}
		}
		for _, child := range current.Children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range tree.Regions {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	if len(index.names)+len(index.ambiguous) == 0 {
		return nil, errors.New("Wordstat region directory contained no labels")
	}
	return index, nil
}

func (s *Service) enrichRegions(ctx context.Context, out Result) Result {
	if len(out.Rows) == 0 {
		out.RegionLookupStatus = "not_needed"
		return out
	}
	for i := range out.Rows {
		row := &out.Rows[i]
		if row.RegionID != "" {
			row.Label = "Регион " + row.RegionID
		}
		row.RegionNameStatus = "unavailable"
	}
	index, err := s.loadRegionIndex(ctx)
	if err != nil {
		out.RegionLookupStatus = "unavailable"
		out.RegionLookupMessage = "Справочник названий регионов недоступен. Частотность сохранена, регионы показаны по кодам."
		out.Message += " " + out.RegionLookupMessage
		return out
	}
	unresolved := 0
	for i := range out.Rows {
		row := &out.Rows[i]
		switch {
		case index.ambiguous[row.RegionID]:
			row.RegionNameStatus = "ambiguous"
			unresolved++
		case index.names[row.RegionID] != "":
			row.RegionName = index.names[row.RegionID]
			row.RegionNameStatus = "resolved"
			row.Label = row.RegionName
		default:
			row.RegionNameStatus = "unknown"
			unresolved++
		}
	}
	out.RegionLookupStatus = "resolved"
	if unresolved > 0 {
		out.RegionLookupStatus = "partial"
		out.RegionLookupMessage = fmt.Sprintf("Для %d регионов справочник не вернул однозначное название. Эти строки сохранены с кодами регионов.", unresolved)
		out.Message += " " + out.RegionLookupMessage
	}
	return out
}
