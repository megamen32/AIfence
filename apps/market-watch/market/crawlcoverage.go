package market

import (
	"agent/internal/db"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/airlockrun/agentsdk"
)

// CrawlReport describes distinct license URLs in a dated sitemap inventory.
type CrawlReport struct {
	AsOf    string        `json:"as_of"`
	Sources []CrawlSource `json:"sources"`
}

type CrawlSource struct {
	Source        string `json:"source"`
	AsOf          string `json:"as_of,omitempty"`
	InventoryAt   string `json:"inventory_at,omitempty"`
	Known         bool   `json:"known"`
	Total         int    `json:"total"`
	Priced        int    `json:"priced"`
	NoPublicPrice int    `json:"no_public_price"`
	Unavailable   int    `json:"unavailable"`
	Unresolved    int    `json:"unresolved"`
	Excluded      int    `json:"excluded"`
	Pending       int    `json:"pending"`
}

// Snapshot-only services without an app DB use the same local test store as other read models.
const crawlSnapshot = "catalog/crawl-coverage.json"

func (r CrawlReport) Validate(now time.Time) error {
	at, err := time.Parse(time.RFC3339Nano, r.AsOf)
	if err != nil || at.After(now.Add(2*time.Minute)) {
		return fmt.Errorf("invalid report timestamp")
	}
	if len(r.Sources) != 3 {
		return fmt.Errorf("three sources required")
	}
	ids := map[string]bool{}
	for _, row := range r.Sources {
		if ids[row.Source] || (row.Source != "softline" && row.Source != "softmagazin" && row.Source != "softmap") {
			return fmt.Errorf("invalid or duplicate source")
		}
		ids[row.Source] = true
		var snapshot time.Time
		if row.AsOf != "" || row.Known {
			snapshot, err = time.Parse(time.RFC3339Nano, row.AsOf)
			if err != nil || snapshot.After(at) {
				return fmt.Errorf("%s: invalid observation timestamp", row.Source)
			}
		}
		if row.InventoryAt != "" || row.Known {
			inv, err := time.Parse(time.RFC3339Nano, row.InventoryAt)
			if err != nil || inv.After(at) || (!snapshot.IsZero() && inv.After(snapshot)) {
				return fmt.Errorf("%s: invalid inventory timestamp", row.Source)
			}
		}
		if row.Total < 0 || row.Total > 100000000 || (!row.Known && row.Total != 0) || (row.Known && row.Total == 0) {
			return fmt.Errorf("%s: invalid sitemap denominator", row.Source)
		}
		sum := 0
		for _, v := range []int{row.Priced, row.NoPublicPrice, row.Unavailable, row.Unresolved, row.Excluded, row.Pending} {
			if v < 0 || v > row.Total {
				return fmt.Errorf("%s: invalid category count", row.Source)
			}
			sum += v
		}
		if sum != row.Total {
			return fmt.Errorf("%s: categories must partition the sitemap", row.Source)
		}
	}
	return nil
}

func (s *Service) CrawlCoverage(ctx context.Context, _ struct{}) (CrawlReport, error) {
	report, _, err := s.readCrawlReport(ctx)
	return report, err
}

func (s *Service) readCrawlReport(ctx context.Context) (CrawlReport, int64, error) {
	var raw []byte
	var version int64
	if s.queries != nil {
		row, err := s.queries.CurrentCrawlCoverage(ctx)
		if err != nil {
			return CrawlReport{}, 0, err
		}
		version, raw = row.Version, row.Report
	} else {
		b, err := s.store.ReadFile(ctx, crawlSnapshot)
		if errors.Is(err, agentsdk.ErrNotFound) {
			return CrawlReport{}, 0, nil
		}
		if err != nil {
			return CrawlReport{}, 0, err
		}
		raw = b
	}
	var report CrawlReport
	err := json.Unmarshal(raw, &report)
	return report, version, err
}

// Compare-and-swap protects the singleton against writers on separate app instances.
func (s *Service) saveCrawlReport(ctx context.Context, data []byte, version int64) error {
	if s.queries == nil {
		_, err := s.store.WriteFile(ctx, crawlSnapshot, bytes.NewReader(data), "application/json")
		return err
	}
	changed, err := s.queries.ReplaceCrawlCoverage(ctx, db.ReplaceCrawlCoverageParams{NewReport: data, ExpectedVersion: version})
	if err != nil {
		return err
	}
	if changed != 1 {
		return agentsdk.NewHTTPError(409, "A concurrent coverage import changed the report", nil)
	}
	return nil
}

func (s *Service) ImportCoverage(w http.ResponseWriter, r *http.Request) error {
	body := http.MaxBytesReader(w, r.Body, 8192)
	defer body.Close()
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	var report CrawlReport
	if err := dec.Decode(&report); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		return agentsdk.NewHTTPError(400, "Invalid coverage report", nil)
	}
	if err := report.Validate(s.now()); err != nil {
		return agentsdk.NewHTTPError(400, err.Error(), nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, version, err := s.readCrawlReport(r.Context())
	if err != nil {
		return err
	}
	stamp, _ := time.Parse(time.RFC3339Nano, report.AsOf)
	if current.AsOf != "" {
		prev, err := time.Parse(time.RFC3339Nano, current.AsOf)
		if err != nil {
			return err
		}
		if stamp.Before(prev) || (stamp.Equal(prev) && !slices.Equal(current.Sources, report.Sources)) {
			return agentsdk.NewHTTPError(409, "A newer or conflicting coverage report already exists", nil)
		}
		// Avoid replacing fresher per-store evidence with an older collector checkpoint.
		for _, next := range report.Sources {
			for _, old := range current.Sources {
				if old.Source != next.Source || !old.Known {
					continue
				}
				oldAt, _ := time.Parse(time.RFC3339Nano, old.AsOf)
				newAt, _ := time.Parse(time.RFC3339Nano, next.AsOf)
				if !next.Known || newAt.Before(oldAt) {
					return agentsdk.NewHTTPError(409, "Older source checkpoint", nil)
				}
			}
		}
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err = s.saveCrawlReport(r.Context(), data, version); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(report)
}
