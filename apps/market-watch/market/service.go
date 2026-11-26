package market

import (
	catalog "agent/collector"
	"agent/internal/db"
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
	queries *db.Queries
	store   Store
	fetch   catalog.Fetch
	now     func() time.Time
	mu      sync.Mutex
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
	Offset      int    `json:"offset,omitempty"`
	ExactSKU    bool   `json:"exact_sku,omitempty"`
	LicenseType string `json:"license_type,omitempty"`
	Source      string `json:"source,omitempty"`
	PriceBasis  string `json:"price_basis,omitempty"`
	Quantity    *int64 `json:"quantity,omitempty"`
	MaxQuantity *int64 `json:"max_quantity,omitempty"`
	ProductKey  string `json:"product_key,omitempty"`
	Limit       int    `json:"limit,omitempty"`
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
	ev, _, err := s.Browse(ctx, in)
	return ev, err
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
	if s.queries != nil {
		if err := s.storeOffers(ctx, old.Rows()); err != nil {
			return Evidence{}, err
		}
	}
	by := map[string]catalog.Run{}
	for _, r := range old.Runs {
		by[r.Source] = r
	}
	at := s.now().UTC()
	snap := Snapshot{CapturedAt: at.Format(time.RFC3339Nano), Runs: []catalog.Run{}, Warnings: []string{}}
	ch := make(chan catalog.Run, len(sources))
	count := 0
	// An exact catalog lookup uses its observed public URLs. Store endpoints
	// that only expose variants on configured family pages retain those seeds.
	targets := map[string][]string{}
	if s.queries != nil && in.ProductKey != "" {
		rows, err := s.search(ctx, ListInput{ProductKey: in.ProductKey}, 51)
		if err != nil {
			return Evidence{}, err
		}
		if len(rows) > 50 {
			return Evidence{}, fmt.Errorf("please refresh an exact SKU with at most 50 matching offers")
		}
		for _, o := range rows {
			if strings.EqualFold(o.SKU, strings.TrimSpace(in.ProductKey)) {
				targets[o.Source] = append(targets[o.Source], o.URL)
			}
		}
	}
	for _, src := range sources {
		if len(sel) > 0 && !sel[src.ID] {
			continue
		}
		if len(targets[src.ID]) > 0 {
			seen := map[string]bool{}
			for _, u := range src.Seeds {
				seen[u] = true
			}
			for _, u := range targets[src.ID] {
				if !seen[u] {
					src.Seeds = append(src.Seeds, u)
					seen[u] = true
				}
			}
		}
		count++
		go func(src catalog.Source) { ch <- catalog.Collect(ctx, src, s.fetch, false, 10, at) }(src)
	}
	for range count {
		r := <-ch
		if s.queries != nil && len(r.Offers) > 0 {
			if err := s.storeOffers(ctx, r.Offers); err != nil {
				return Evidence{}, err
			}
		}
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
	if s.queries == nil && len(rows) > 0 {
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
	if s.queries != nil {
		ev, err := s.List(ctx, ListInput{ProductKey: in.ProductKey})
		if in.ProductKey != "" {
			fresh := map[string]bool{}
			for _, r := range snap.Runs {
				for _, o := range r.Offers {
					if o.CapturedAt == at.Format(time.RFC3339) && strings.EqualFold(o.SKU, strings.TrimSpace(in.ProductKey)) {
						fresh[o.Source] = true
					}
				}
			}
			for _, o := range ev.Observations {
				if !fresh[o.Details.Seller] {
					ev.Warnings = append(ev.Warnings, o.Details.Seller+": returning previously indexed observation; see its capture time.")
					ev.Status = "partial"
				}
			}
		}
		ev.Warnings = append(ev.Warnings, snap.Warnings...)
		if len(snap.Warnings) > 0 && ev.Status == "ok" {
			ev.Status = "partial"
		}
		return ev, err
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
	if s.queries != nil {
		return s.catalogChanges(ctx, in, f, t)
	}
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
