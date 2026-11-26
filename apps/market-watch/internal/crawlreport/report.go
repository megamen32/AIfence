// Package crawlreport summarizes local public collector checkpoints for Market Watch.
package crawlreport

import (
	"agent/collector"
	"agent/market"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

func load(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func classify(row *market.CrawlSource, state string, count int) {
	switch state {
	case "priced":
		row.Priced += count
	case "no_public_price":
		row.NoPublicPrice += count
	case "unavailable":
		row.Unavailable += count
	case "robots_excluded", "access_denied", "not_product":
		row.Excluded += count
	case "":
		row.Pending += count
	default:
		row.Unresolved += count
	}
}

func softline(root string) (market.CrawlSource, error) {
	// The writer computes these states only for keys in its unique sitemap URL set.
	// Imported historical offers and off-sitemap observations are outside this map.
	var stat struct {
		AsOf        string         `json:"as_of"`
		InventoryAt string         `json:"inventory_at"`
		Inventory   int            `json:"inventory_products"`
		States      map[string]int `json:"states_by_inventory_url"`
	}
	if err := load(filepath.Join(root, "softline", "inventory.json"), &stat); err != nil {
		return market.CrawlSource{}, err
	}
	row := market.CrawlSource{Source: "softline", AsOf: stat.AsOf, InventoryAt: stat.InventoryAt, Known: true, Total: stat.Inventory}
	row.Pending = row.Total
	if stat.States == nil {
		return row, errors.New("Softline URL classification missing")
	}
	for state, n := range stat.States {
		if n < 0 || state == "" {
			return row, errors.New("invalid Softline URL classification")
		}
		classify(&row, state, n)
		row.Pending -= n
	}
	return row, nil
}

type sitemap struct {
	Source   string   `json:"source"`
	At       string   `json:"discovered_at"`
	URLs     []string `json:"urls"`
	Variants []string `json:"variant_urls"`
}

type classification struct {
	at    time.Time
	state string
}

func softmap(root string) (market.CrawlSource, error) {
	dir := filepath.Join(root, "softmap")
	var inv sitemap
	if err := load(filepath.Join(dir, "inventory.json"), &inv); err != nil {
		return market.CrawlSource{}, err
	}
	since, err := time.Parse(time.RFC3339Nano, inv.At)
	if err != nil || inv.Source != "softmap" || len(inv.Variants) == 0 {
		return market.CrawlSource{}, errors.New("invalid SoftMap variant inventory")
	}
	families := map[string]bool{}
	for _, u := range inv.URLs {
		families[u] = true
	}
	expected := map[string]classification{}
	for _, u := range inv.Variants {
		if u == "" {
			return market.CrawlSource{}, errors.New("empty SoftMap sitemap URL")
		}
		expected[u] = classification{}
	}

	// Read one fixed prefix; the worker may be appending another JSONL record.
	file, err := os.Open(filepath.Join(dir, "pages.jsonl"))
	if err != nil {
		return market.CrawlSource{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return market.CrawlSource{}, err
	}
	reader := bufio.NewReader(io.LimitReader(file, stat.Size()))
	type capture struct {
		at   time.Time
		page catalog.PageResult
	}
	seen := map[string]capture{}
	newest := since
	for {
		raw, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return market.CrawlSource{}, err
		}
		var page catalog.PageResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return market.CrawlSource{}, fmt.Errorf("SoftMap journal: %w", err)
		}
		at, err := time.Parse(time.RFC3339Nano, page.At)
		if err != nil {
			return market.CrawlSource{}, err
		}
		_, detail := expected[page.URL]
		if at.Before(since) || (!families[page.URL] && !detail) {
			continue
		}
		if at.After(newest) {
			newest = at
		}
		if prior, ok := seen[page.URL]; ok && prior.at.After(at) {
			continue
		}
		page.Offers = nil
		seen[page.URL] = capture{at, page}
	}
	note := func(u, state string, at time.Time) {
		old, ok := expected[u]
		if !ok || old.at.After(at) {
			return
		}
		if old.at.Equal(at) && state != old.state {
			state = "unclassified"
		}
		if state == "" {
			state = "unclassified"
		}
		expected[u] = classification{at, state}
	}
	for _, entry := range seen {
		at, page := entry.at, entry.page
		note(page.URL, page.State, at)
		if page.State != "error" {
			for _, variant := range page.Variants {
				note(variant.URL, variant.Status, at)
			}
		}
	}
	row := market.CrawlSource{Source: "softmap", Known: true, InventoryAt: inv.At, AsOf: newest.UTC().Format(time.RFC3339Nano), Total: len(expected)}
	for _, v := range expected {
		classify(&row, v.state, 1)
	}
	return row, nil
}

func Build(root string) (market.CrawlReport, error) {
	line, err := softline(root)
	if err != nil {
		return market.CrawlReport{}, err
	}
	smap, err := softmap(root)
	if err != nil {
		return market.CrawlReport{}, err
	}
	// The SoftMagazin inventory tracks families and vendor listings, with no
	// license-only reconciliation of all article sitemap shards.
	mag := market.CrawlSource{Source: "softmagazin"}
	now := time.Now().UTC()
	out := market.CrawlReport{AsOf: now.Format(time.RFC3339Nano), Sources: []market.CrawlSource{line, mag, smap}}
	return out, out.Validate(now)
}

// LocalOrigin is the explicitly selected instance for scheduled crawl reports.
const LocalOrigin = "http://localhost:42080"
const LocalAgent = "market-watch"

type Target struct {
	CLI    string
	AppDir string
	Origin string
	Agent  string
}

// WithPrices attaches coverage only to the existing local installation and
// directories that have the actual checkpoint files. Other price uploads keep
// working even when their setup has no coverage feed.
func WithPrices(ctx context.Context, root string, target Target) (market.CrawlReport, error) {
	if target.Origin != LocalOrigin || target.Agent != LocalAgent {
		return market.CrawlReport{}, nil
	}
	for _, name := range []string{"softline/inventory.json", "softmap/progress.json", "softmagazin/progress.json"} {
		info, err := os.Stat(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			return market.CrawlReport{}, nil
		}
		if err != nil {
			return market.CrawlReport{}, err
		}
		if !info.Mode().IsRegular() {
			return market.CrawlReport{}, fmt.Errorf("invalid checkpoint: %s", name)
		}
	}
	report, err := Build(root)
	if err != nil {
		return market.CrawlReport{}, err
	}
	return report, Upload(ctx, report, target)
}

func Upload(ctx context.Context, data market.CrawlReport, target Target) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, target.CLI, "request", "POST", "/coverage/import", "--dir", target.AppDir, "--remote", "default", "--target", "production", "--url", target.Origin, "--agent", target.Agent, "--header", "Content-Type: application/json", "--data", string(payload))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("SDK request: %w: %.400s", err, stderr.Bytes())
	}
	var ack market.CrawlReport
	if json.Unmarshal(raw, &ack) != nil || ack.AsOf != data.AsOf || !slices.Equal(ack.Sources, data.Sources) {
		return errors.New("Market Watch did not acknowledge the report")
	}
	return nil
}
