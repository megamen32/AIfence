// Command softlinefull reconciles public family listings with the store's software product sitemap.
package main

import (
	"agent/collector"
	"agent/collector/softlinefamily"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gofrs/flock"
)

type inventory struct {
	At                 string
	Families, Products []string
}
type check struct {
	Page          string
	Title         string `json:",omitempty"`
	At            time.Time
	Products      []softlinefamily.Product
	State         string
	Error         string `json:",omitempty"`
	ParserVersion int    `json:"parser_version,omitempty"`
	BodyHash      string `json:"body_sha256,omitempty"`
}
type report struct {
	AsOf              time.Time      `json:"as_of"`
	InventoryAt       string         `json:"inventory_at"`
	Families          int            `json:"families"`
	FamiliesProcessed int            `json:"families_processed"`
	Inventory         int            `json:"inventory_products"`
	Covered           int            `json:"inventory_accounted"`
	Offers            int            `json:"priced_offers"`
	Unmatched         int            `json:"priced_offers_without_sku"`
	States            map[string]int `json:"states_by_inventory_url"`
	Extra             int            `json:"additional_off_sitemap_urls"`
	Retained          int            `json:"retained_from_previous_observations"`
	GroupedSkip       int64          `json:"requests_skipped_by_grouped_evidence"`
	Complete          bool           `json:"complete"`
}

func atomicFile(path string, write func(io.Writer) error) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = write(f); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func save(path string, v any) error {
	return atomicFile(path, func(w io.Writer) error { return json.NewEncoder(w).Encode(v) })
}
func read(path string, v any) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}
func sortedKeys[V any](items map[string]V) []string {
	out := make([]string, 0, len(items))
	for k := range items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var source = catalog.Source{ID: "softline", Host: softlinefamily.Host}

type fetch func(context.Context, catalog.Source, string) ([]byte, error)

func discover(ctx context.Context, get fetch) (inventory, error) {
	var index catalog.Sitemap
	b, e := get(ctx, source, "https://store.softline.ru/https-sitemap.xml")
	if e != nil {
		return inventory{}, e
	}
	if e = xml.Unmarshal(b, &index); e != nil {
		return inventory{}, e
	}
	families, products := map[string]bool{}, map[string]bool{}
	for _, child := range index.Children {
		var set map[string]bool
		switch {
		case strings.HasSuffix(child.Loc, "/families.xml"):
			set = families
		case strings.Contains(child.Loc, "/sitemap/softwareProducts-"):
			set = products
		default:
			continue
		}
		b, e = get(ctx, source, child.Loc)
		if e != nil {
			return inventory{}, e
		}
		var m catalog.Sitemap
		if e = xml.Unmarshal(b, &m); e != nil {
			return inventory{}, e
		}
		if m.XMLName.Local != "urlset" || len(m.Entries) == 0 {
			return inventory{}, fmt.Errorf("empty sitemap %s", child.Loc)
		}
		for _, entry := range m.Entries {
			if catalog.Allowed(source, entry.Loc) {
				set[entry.Loc] = true
			}
		}
	}
	if len(products) == 0 || len(families) == 0 {
		return inventory{}, fmt.Errorf("incomplete sitemap index")
	}
	return inventory{time.Now().UTC().Format(time.RFC3339), sortedKeys(families), sortedKeys(products)}, nil
}

func checkpoint(dir, page string) string {
	return filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(page))))
}

// The publisher may redirect a standalone license URL to its family. Use the
// returned family model to collect exact child URLs, never transfer a price
// from a different canonical product to the requested URL.
func direct(page string, b []byte, at time.Time) ([]softlinefamily.Product, error) {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	sku, _ := doc.Find(".js-sku-btn").First().Attr("data-code")
	name := strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	p := softlinefamily.Product{URL: page, SKU: strings.ToUpper(strings.TrimSpace(sku)), Name: name, State: "unrecognized_product"}
	if canonical, ok := doc.Find("link[rel='canonical']").First().Attr("href"); ok && strings.TrimSpace(canonical) != "" {
		base, err := url.Parse(page)
		if err != nil {
			return nil, err
		}
		target, err := base.Parse(strings.TrimSpace(canonical))
		if err != nil || !catalog.Allowed(source, target.String()) {
			return []softlinefamily.Product{{URL: page, State: "unrecognized_product"}}, nil
		}
		if target.String() != page {
			products, err := softlinefamily.Extract(target.String(), b, at)
			if err == nil && len(products) > 0 {
				for _, v := range products {
					if v.URL == page {
						return products, nil
					}
				}
				return append(products, softlinefamily.Product{URL: page, State: "unrecognized_product"}), nil
			}
			return []softlinefamily.Product{{URL: page, State: "unrecognized_product"}}, nil
		}
	}
	offer, err := catalog.SoftlineDetail(doc, page, at)
	if err != nil {
		return nil, err
	}
	if offer != nil {
		p.State = "priced"
		p.Offer = offer
		return []softlinefamily.Product{p}, nil
	}
	if name != "" {
		// Explicit null prices in the exact software product's own published
		// configuration are positive evidence of no public quote, not parse failure.
		// Absent keys, unrelated IDs and redirected pages stay unresolved.
		var cfg struct {
			Product struct {
				ID      json.RawMessage
				Type    string
				Price   json.RawMessage
				Current json.RawMessage `json:"priceWithDiscount"`
			}
		}
		if _, data, ok := strings.Cut(doc.Find("#js-front-config").Text(), "window.temporaryFrontConfig ="); ok && json.NewDecoder(strings.NewReader(data)).Decode(&cfg) == nil {
			id := strings.Trim(string(cfg.Product.ID), "\\\" ")
			suffix := strings.TrimSuffix(page, "/")
			i := strings.LastIndex(suffix, "-")
			if i >= 0 && id != "" && suffix[i+1:] == id && cfg.Product.Type == "software" && string(cfg.Product.Price) == "null" && string(cfg.Product.Current) == "null" {
				p.State = "no_public_price"
				return []softlinefamily.Product{p}, nil
			}
		}
		main := strings.ToLower(strings.Join(strings.Fields(doc.Find("main,.b-product-card,.b-product-detail").Text()), " "))
		for _, marker := range []string{"цена по запросу", "цена предоставляется по запросу", "цена на данный продукт предоставляется по запросу", "узнать цену"} {
			if strings.Contains(main, marker) {
				p.State = "no_public_price"
				break
			}
		}
	}
	return []softlinefamily.Product{p}, nil
}

type survey struct {
	cfg               inventory
	dir, out          string
	families          int
	covered           map[string]softlinefamily.Product
	known             map[string]bool
	prior             map[string]catalog.Offer
	verified          verifiedBatch
	batchByURL        map[string]catalog.Offer
	skippedByEvidence atomic.Int64
}

func rank(p softlinefamily.Product) int {
	switch p.State {
	case "priced":
		return 4
	case "no_public_price":
		return 3
	case "unavailable":
		return 2
	}
	return 1
}
func (s *survey) merge(c check) {
	for _, p := range c.Products {
		if p.Offer != nil {
			p.Offer.MaxQuantity = catalog.PublicMaxQuantity(p.Offer.Quantity, p.Offer.MaxQuantity)
		}
		v, ok := s.covered[p.URL]
		if ok && p.Offer != nil && v.Offer != nil && p.Offer.CapturedAt < v.Offer.CapturedAt {
			continue
		}
		if !ok || rank(p) >= rank(v) {
			s.covered[p.URL] = p
		}
	}
}
func (s *survey) snapshot() error {
	stat := report{AsOf: time.Now().UTC(), InventoryAt: s.cfg.At, Families: len(s.cfg.Families), FamiliesProcessed: s.families, Inventory: len(s.known), States: map[string]int{}}
	names := sortedKeys(s.covered)
	e := atomicFile(filepath.Join(s.out, "offers.jsonl"), func(w io.Writer) error {
		enc := json.NewEncoder(w)
		for _, k := range names {
			p := s.covered[k]
			if s.known[k] {
				stat.States[p.State]++
				if rank(p) >= 2 {
					stat.Covered++
				}
			} else {
				stat.Extra++
			}
			if p.Offer != nil {
				stat.Offers++
				if p.SKU == "" {
					stat.Unmatched++
				}
				if e := enc.Encode(p.Offer); e != nil {
					return e
				}
			}
		}
		for _, key := range sortedKeys(s.prior) {
			found, observed := s.covered[key]
			if observed && rank(found) >= 2 {
				continue
			}
			if !s.known[key] {
				continue
			}
			if err := enc.Encode(s.prior[key]); err != nil {
				return err
			}
			stat.Retained++
		}
		return nil
	})
	if e != nil {
		return e
	}
	if err := atomicFile(filepath.Join(s.out, "products.jsonl"), func(w io.Writer) error {
		enc := json.NewEncoder(w)
		for _, k := range names {
			if err := enc.Encode(s.covered[k]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	stat.GroupedSkip = s.skippedByEvidence.Load()
	stat.Complete = stat.Covered == stat.Inventory && stat.FamiliesProcessed == stat.Families
	return save(filepath.Join(s.out, "inventory.json"), stat)
}
func run() error {
	out := flag.String("out", ".runtime/market-catalog/softline", "export folder")
	runID := flag.String("cycle", "", "explicit checkpoint generation; normally resume automatically")
	batchFile := flag.String("verified-batch", "", "authorized normalized price batch with exact sitemap URLs; persisted in current generation")
	flag.Parse()
	if *runID != "" && (filepath.Base(*runID) != *runID || *runID == "." || *runID == "..") {
		return fmt.Errorf("invalid cycle")
	}
	if e := os.MkdirAll(*out, 0755); e != nil {
		return e
	}
	lock := flock.New(filepath.Join(*out, "writer.lock"))
	ok, e := lock.TryLock()
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("another survey is active")
	}
	defer lock.Unlock()
	if err := resumeAllowed(*out, time.Now()); err != nil {
		return err
	}
	cycle := *runID
	if cycle == "" {
		active := filepath.Join(*out, "active-generation.json")
		if err := read(active, &cycle); errors.Is(err, os.ErrNotExist) {
			cycle = "initial"
		} else if err != nil {
			return err
		}
		if filepath.Base(cycle) != cycle || cycle == "." || cycle == ".." || cycle == "" {
			return fmt.Errorf("invalid stored cycle")
		}
		var ended time.Time
		e := read(filepath.Join(*out, "checkpoints", cycle, "finished.json"), &ended)
		if e == nil {
			cycle = time.Now().UTC().Format("20060102T150405Z")
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e := save(active, cycle); e != nil {
			return e
		}
	}
	dir := filepath.Join(*out, "checkpoints", cycle)
	if e := os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	http := catalog.NewHTTP()
	var inv inventory
	path := filepath.Join(dir, "sitemap.json")
	if e = read(path, &inv); errors.Is(e, os.ErrNotExist) {
		inv, e = discover(ctx, http.Fetch)
		if e == nil {
			e = save(path, inv)
		}
	}
	if e != nil {
		return e
	}
	s := survey{cfg: inv, dir: dir, out: *out, known: map[string]bool{}, covered: map[string]softlinefamily.Product{}}
	s.prior = map[string]catalog.Offer{}
	prior, err := os.Open(filepath.Join(*out, "offers.jsonl"))
	if err == nil {
		dec := json.NewDecoder(prior)
		for {
			var offer catalog.Offer
			err = dec.Decode(&offer)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				prior.Close()
				return err
			}
			offer.MaxQuantity = catalog.PublicMaxQuantity(offer.Quantity, offer.MaxQuantity)
			s.prior[offer.URL] = offer
		}
		prior.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, u := range inv.Products {
		s.known[u] = true
	}
	// Make a familiar cross-store license available in the first published snapshot.
	for i, v := range inv.Families {
		if strings.HasSuffix(v, "/kaspersky-small-office-security/") {
			inv.Families[0], inv.Families[i] = v, inv.Families[0]
			break
		}
	}
	if e = s.loadVerifiedBatch(*batchFile); e != nil {
		return e
	}
	if e = s.scan(ctx, inv.Families, true, http.Fetch); e == nil {
		e = s.scan(ctx, inv.Products, false, http.Fetch)
	}
	if err := s.snapshot(); err != nil {
		return err
	}
	if e == nil {
		return save(filepath.Join(dir, "finished.json"), time.Now().UTC())
	}
	return e
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
