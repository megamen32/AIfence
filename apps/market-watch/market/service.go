package market

import (
	catalog "agent/collector"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/airlockrun/agentsdk"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store interface {
	ReadFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, io.Reader, string) (agentsdk.FileInfo, error)
	ListDir(context.Context, string, agentsdk.ListOpts) ([]agentsdk.FileInfo, error)
}
type Service struct {
	store Store
	fetch catalog.Fetch
	now   func() time.Time
	mu    sync.Mutex
}

func New(store Store) *Service {
	if store == nil {
		panic("market: store required")
	}
	return &Service{store: store, fetch: catalog.NewHTTP().Fetch, now: time.Now}
}

type Snapshot struct {
	CapturedAt string        `json:"captured_at"`
	Runs       []catalog.Run `json:"runs"`
	Warnings   []string      `json:"warnings"`
}
type ListInput struct {
	ProductKey string `json:"product_key,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}
type RefreshInput struct {
	ProductKey string   `json:"product_key,omitempty"`
	SourceIDs  []string `json:"source_ids,omitempty"`
}
type ChangesInput struct {
	ProductKey string `json:"product_key,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
}
type Receipt struct {
	Status string `json:"status"`
	AsOf   string `json:"as_of"`
	Count  int    `json:"count"`
}

const current = "catalog/current.json"

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	b, e := s.store.ReadFile(ctx, current)
	if errors.Is(e, agentsdk.ErrNotFound) {
		return Snapshot{Runs: []catalog.Run{}, Warnings: []string{"No snapshot yet; call refresh_market."}}, nil
	}
	if e != nil {
		return Snapshot{}, e
	}
	var out Snapshot
	e = json.Unmarshal(b, &out)
	return out, e
}
func (s *Service) List(ctx context.Context, in ListInput) (Evidence, error) {
	snap, e := s.Snapshot(ctx)
	if e != nil {
		return Evidence{}, e
	}
	return FromSnapshot(snap, in.ProductKey, in.Limit, s.now()), nil
}
func (s *Service) Refresh(ctx context.Context, in RefreshInput) (Evidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sources, e := catalog.Sources()
	if e != nil {
		return Evidence{}, e
	}
	sel := map[string]bool{}
	for _, id := range in.SourceIDs {
		found := false
		for _, src := range sources {
			if src.ID == id {
				found = true
			}
		}
		if !found {
			return Evidence{}, fmt.Errorf("unknown source %q", id)
		}
		sel[id] = true
	}
	old, e := s.Snapshot(ctx)
	if e != nil {
		return Evidence{}, e
	}
	by := map[string]catalog.Run{}
	for _, r := range old.Runs {
		by[r.Source] = r
	}
	at := s.now().UTC()
	snap := Snapshot{CapturedAt: at.Format(time.RFC3339Nano), Runs: []catalog.Run{}, Warnings: []string{}}
	ch := make(chan catalog.Run, len(sources))
	count := 0
	for _, src := range sources {
		if len(sel) > 0 && !sel[src.ID] {
			continue
		}
		count++
		go func(src catalog.Source) { ch <- catalog.Collect(ctx, src, s.fetch, false, 10, at) }(src)
	}
	for range count {
		r := <-ch
		if r.Status == "ok" {
			by[r.Source] = r
			continue
		}
		snap.Warnings = append(snap.Warnings, r.Source+": "+r.Status+"; "+strings.Join(r.Errors, "; "))
		if prev, ok := by[r.Source]; ok && len(prev.Offers) > 0 {
			prev.Status = "stale"
			by[r.Source] = prev
		} else {
			by[r.Source] = r
		}
	}
	for _, src := range sources {
		if r, ok := by[src.ID]; ok {
			snap.Runs = append(snap.Runs, r)
		} else {
			snap.Warnings = append(snap.Warnings, src.ID+": no observation")
		}
	}
	if ctx.Err() != nil {
		return Evidence{}, ctx.Err()
	}
	b, e := json.Marshal(snap)
	if e != nil {
		return Evidence{}, e
	}
	history := "catalog/history/" + at.Format("20060102T150405.000000000Z") + ".json"
	if _, e = s.store.WriteFile(ctx, history, bytes.NewReader(b), "application/json"); e != nil {
		return Evidence{}, e
	}
	rows := snap.Rows()
	if len(rows) > 0 {
		var csv bytes.Buffer
		csv.Write([]byte{0xef, 0xbb, 0xbf})
		if e = catalog.CSV(&csv, rows); e != nil {
			return Evidence{}, e
		}
		if _, e = s.store.WriteFile(ctx, "catalog/latest.csv", bytes.NewReader(csv.Bytes()), "text/csv; charset=utf-8"); e != nil {
			return Evidence{}, e
		}
	}
	if _, e = s.store.WriteFile(ctx, current, bytes.NewReader(b), "application/json"); e != nil {
		return Evidence{}, e
	}
	return FromSnapshot(snap, in.ProductKey, 100, s.now()), nil
}
func (s Snapshot) Rows() []catalog.Offer {
	var rows []catalog.Offer
	for _, r := range s.Runs {
		rows = append(rows, r.Offers...)
	}
	return rows
}
func (s *Service) Changes(ctx context.Context, in ChangesInput) (Evidence, error) {
	f, e := time.Parse("2006-01-02", in.From)
	if e != nil {
		return Evidence{}, fmt.Errorf("from must be YYYY-MM-DD")
	}
	t, e := time.Parse("2006-01-02", in.To)
	if e != nil || t.Before(f) {
		return Evidence{}, fmt.Errorf("to must be YYYY-MM-DD >= from")
	}
	t = t.AddDate(0, 0, 1)
	files, e := s.store.ListDir(ctx, "catalog/history", agentsdk.ListOpts{})
	if e != nil && !errors.Is(e, agentsdk.ErrNotFound) {
		return Evidence{}, e
	}
	sort.Slice(files, func(i, j int) bool { return string(files[i].Path) < string(files[j].Path) })
	snaps := []Snapshot{}
	for _, file := range files {
		if !strings.HasSuffix(file.Filename, ".json") {
			continue
		}
		when, e := time.Parse("20060102T150405.000000000Z", strings.TrimSuffix(file.Filename, ".json"))
		if e != nil || when.Before(f) || !when.Before(t) {
			continue
		}
		if len(snaps) >= 200 {
			return Evidence{}, fmt.Errorf("select a range of at most 200 snapshots")
		}
		b, e := s.store.ReadFile(ctx, string(file.Path))
		if e != nil {
			return Evidence{}, e
		}
		var snap Snapshot
		if e = json.Unmarshal(b, &snap); e != nil {
			return Evidence{}, e
		}
		snaps = append(snaps, snap)
	}
	return Compare(snaps, in, s.now()), nil
}
