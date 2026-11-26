package market

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
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

const crawlDir = "catalog/crawl-reports"
const crawlFileTime = "20060102T150405.000000000Z"

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

// Each capture has its own path. Reading the greatest time also prevents
// overlapping workers on different instances from rolling the feed back.
func (s *Service) CrawlCoverage(ctx context.Context, _ struct{}) (CrawlReport, error) {
	files, err := s.store.ListDir(ctx, crawlDir, agentsdk.ListOpts{})
	if errors.Is(err, agentsdk.ErrNotFound) {
		return CrawlReport{}, nil
	}
	if err != nil {
		return CrawlReport{}, err
	}
	latest := ""
	for _, f := range files {
		stamp, ok := strings.CutSuffix(f.Filename, ".json")
		if _, err := time.Parse(crawlFileTime, stamp); ok && err == nil && stamp > latest {
			latest = stamp
		}
	}
	if latest == "" {
		return CrawlReport{}, nil
	}
	data, err := s.store.ReadFile(ctx, crawlDir+"/"+latest+".json")
	if err != nil {
		return CrawlReport{}, err
	}
	var report CrawlReport
	err = json.Unmarshal(data, &report)
	return report, err
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
	current, err := s.CrawlCoverage(r.Context(), struct{}{})
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
	path := crawlDir + "/" + stamp.UTC().Format(crawlFileTime) + ".json"
	if _, err = s.store.WriteFile(r.Context(), path, bytes.NewReader(data), "application/json"); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(report)
}
