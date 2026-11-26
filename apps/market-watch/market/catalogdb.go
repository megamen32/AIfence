package market

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	catalog "agent/collector"
	"agent/internal/db"
	"github.com/airlockrun/agentsdk"
)

// In production both the background collector and interactive refresh write
// observations to the same append-only SQL history.
func NewWithCatalog(store Store, conn *agentsdk.AgentDB) *Service {
	if conn == nil {
		panic("market: catalog DB required")
	}
	s := New(store)
	s.queries = db.New(conn)
	return s
}

type SourceSummary struct {
	Source          string `json:"source"`
	Offers          int64  `json:"observed_offers"`
	LastObservation string `json:"last_observation,omitempty"`
}
type CatalogStatus struct {
	Sources []SourceSummary `json:"sources"`
	Total   int64           `json:"observed_offers"`
}

func (s *Service) Coverage(ctx context.Context, _ struct{}) (CatalogStatus, error) {
	rows, err := s.queries.CatalogCounts(ctx)
	if err != nil {
		return CatalogStatus{}, err
	}
	counts := map[string]SourceSummary{}
	for _, r := range rows {
		counts[r.Source] = SourceSummary{r.Source, r.Offers, r.ObservedAt.UTC().Format(time.RFC3339)}
	}
	sources, err := catalog.Sources()
	if err != nil {
		return CatalogStatus{}, err
	}
	var out CatalogStatus
	for _, src := range sources {
		row, ok := counts[src.ID]
		if !ok {
			row.Source = src.ID
		}
		out.Sources = append(out.Sources, row)
		out.Total += row.Offers
	}
	return out, nil
}

func (s *Service) search(ctx context.Context, in ListInput, limit int) ([]catalog.Offer, error) {
	minQty, maxQty := int64(-1), int64(-1)
	if in.Quantity != nil {
		minQty = *in.Quantity
	}
	if in.MaxQuantity != nil {
		maxQty = *in.MaxQuantity
	}
	raw, err := s.queries.SearchOffers(ctx, db.SearchOffersParams{Query: strings.TrimSpace(in.ProductKey), Source: in.Source, Basis: in.PriceBasis, MinQty: minQty, MaxQty: maxQty, Maxrows: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]catalog.Offer, len(raw))
	for i, b := range raw {
		if err := json.Unmarshal(b, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) catalogList(ctx context.Context, in ListInput) (Evidence, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.search(ctx, in, limit+1)
	if err != nil {
		return Evidence{}, err
	}
	ev := empty(s.now())
	if len(rows) > limit {
		ev.Warnings = append(ev.Warnings, "Showing the first matching offers. Narrow your query or download the full CSV.")
		rows = rows[:limit]
	}
	for _, row := range rows {
		ev.Observations = append(ev.Observations, obs(row))
	}
	if len(rows) > 0 {
		latest := time.Time{}
		for _, row := range rows {
			t, err := time.Parse(time.RFC3339Nano, row.CapturedAt)
			if err == nil && t.After(latest) {
				latest = t
			}
		}
		if !latest.IsZero() {
			ev.AsOf = latest.UTC().Format(time.RFC3339Nano)
		}
	}
	if len(rows) == 0 {
		ev.Status = "unavailable"
		ev.Warnings = append(ev.Warnings, "No observed price matches this query.")
	}
	return ev, nil
}

func (s *Service) storeOffers(ctx context.Context, rows []catalog.Offer) error {
	if len(rows) == 0 {
		return nil
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return s.queries.ImportObservations(ctx, b)
}

func checkBatch(rows []catalog.Offer, sources []catalog.Source) error {
	if len(rows) == 0 || len(rows) > 500 {
		return fmt.Errorf("send between 1 and 500 observations per request")
	}
	sourceByID := map[string]catalog.Source{}
	for _, src := range sources {
		sourceByID[src.ID] = src
	}
	for i, r := range rows {
		src, ok := sourceByID[r.Source]
		if !ok || !catalog.Allowed(src, r.URL) || len(r.URL) > 2048 || len(r.SKU) > 300 || strings.TrimSpace(r.Name) == "" || len(r.Name) > 10000 || (r.MaxQuantity != nil && (*r.MaxQuantity <= 0 || (r.Quantity != nil && *r.MaxQuantity < *r.Quantity))) || r.Currency != "RUB" || r.PriceMinor <= 0 || r.PriceMinor > 1000000000000000 || strings.TrimSpace(r.PriceBasis) == "" || len(r.PriceBasis) > 80 || (r.Quantity != nil && *r.Quantity <= 0) {
			return fmt.Errorf("invalid source, price or product fields at row %d", i+1)
		}
		if _, err := time.Parse(time.RFC3339Nano, r.CapturedAt); err != nil {
			return fmt.Errorf("invalid capture time at row %d", i+1)
		}
	}
	return nil
}

func (s *Service) Import(w http.ResponseWriter, r *http.Request) error {
	body := http.MaxBytesReader(w, r.Body, 4<<20)
	defer body.Close()
	var rows []catalog.Offer
	d := json.NewDecoder(body)
	if err := d.Decode(&rows); err != nil {
		return agentsdk.NewHTTPError(400, "Invalid JSON observations", nil)
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return agentsdk.NewHTTPError(400, "Unexpected data following observations", nil)
	}
	sources, err := catalog.Sources()
	if err != nil {
		return err
	}
	if err := checkBatch(rows, sources); err != nil {
		return agentsdk.NewHTTPError(400, err.Error(), nil)
	}
	if err := s.storeOffers(r.Context(), rows); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(struct {
		Accepted int `json:"accepted"`
	}{len(rows)})
}

func (s *Service) exportCatalog(w http.ResponseWriter, r *http.Request) error {
	q := db.CatalogExportParams{}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=market-watch.csv")
	first := true
	for {
		batch, err := s.queries.CatalogExport(r.Context(), q)
		if err != nil {
			return err
		}
		rows := make([]catalog.Offer, len(batch))
		for i, b := range batch {
			if err := json.Unmarshal(b, &rows[i]); err != nil {
				return err
			}
		}
		var buf bytes.Buffer
		if err := catalog.CSV(&buf, rows); err != nil {
			return err
		}
		if first {
			if _, err := w.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
				return err
			}
			first = false
		} else {
			_, _ = buf.ReadString('\n')
		}
		if _, err := io.Copy(w, &buf); err != nil {
			return err
		}
		if len(rows) < 500 {
			return nil
		}
		last := rows[len(rows)-1]
		var qty int64
		if last.Quantity != nil {
			qty = *last.Quantity
		}
		var maxQty int64
		if last.MaxQuantity != nil {
			maxQty = *last.MaxQuantity
		}
		q = db.CatalogExportParams{AfterSource: last.Source, AfterSku: last.SKU, AfterUrl: last.URL, AfterBasis: last.PriceBasis, AfterQuantity: qty, AfterMaxQuantity: maxQty}
	}
}

func (s *Service) catalogChanges(ctx context.Context, in ChangesInput, from, to time.Time) (Evidence, error) {
	rows, err := s.queries.OfferChanges(ctx, db.OfferChangesParams{StartAt: from, EndAt: to, Query: strings.TrimSpace(in.ProductKey)})
	if err != nil {
		return Evidence{}, err
	}
	ev := empty(s.now())
	if len(rows) > 100 {
		rows = rows[:100]
		ev.Warnings = append(ev.Warnings, "Showing the 100 most recent price changes.")
	}
	for _, pair := range rows {
		var a, b catalog.Offer
		if err := json.Unmarshal(pair.OldOffer, &a); err != nil {
			return Evidence{}, err
		}
		if err := json.Unmarshal(pair.NewOffer, &b); err != nil {
			return Evidence{}, err
		}
		ev.Observations = append(ev.Observations, changeObs(a, b))
	}
	if len(rows) == 0 {
		ev.Warnings = append(ev.Warnings, "No recorded change between comparable observations in the requested period.")
	}
	return ev, nil
}
