package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"agent/internal/evidence"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

type marketIntelligenceInput struct {
	ProductKey string `json:"product_key,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	Query      string `json:"query,omitempty" jsonschema:"description=Optional case-insensitive search over available product keys when product_key is omitted."`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type marketIntelligenceResult struct {
	Status            string                          `json:"status"`
	Source            string                          `json:"source"`
	SnapshotID        string                          `json:"snapshot_id,omitempty"`
	AsOf              string                          `json:"as_of,omitempty"`
	ProductKey        string                          `json:"product_key,omitempty"`
	AvailableProducts []string                        `json:"available_products,omitempty"`
	Observations      []marketIntelligenceObservation `json:"observations,omitempty"`
	Total             int                             `json:"total"`
	NextOffset        *int                            `json:"next_offset,omitempty"`
	Warnings          []string                        `json:"warnings,omitempty"`
}

type marketIntelligenceObservation struct {
	ID              string             `json:"id"`
	ProductKey      string             `json:"product_key"`
	MetricKey       string             `json:"metric_key"`
	Kind            string             `json:"kind"`
	Value           float64            `json:"value"`
	Unit            string             `json:"unit"`
	Period          evidence.Period    `json:"period"`
	SourceRef       evidence.SourceRef `json:"source_ref"`
	Status          string             `json:"status"`
	Origin          string             `json:"origin,omitempty"`
	Synthetic       bool               `json:"synthetic"`
	AuthenticAPI    bool               `json:"authentic_api,omitempty"`
	Normalization   string             `json:"normalization,omitempty"`
	Geo             string             `json:"geo,omitempty"`
	Name            string             `json:"name,omitempty"`
	Seller          string             `json:"seller,omitempty"`
	PriceBasis      string             `json:"price_basis,omitempty"`
	Quantity        *float64           `json:"quantity,omitempty"`
	MaxQuantity     *float64           `json:"max_quantity,omitempty"`
	CapturedAt      string             `json:"captured_at,omitempty"`
	ComparableTerms bool               `json:"comparable_terms,omitempty"`
}

type marketIntelligence struct {
	evidence *evidence.Service
}

type intelligenceSpec struct {
	name      string
	source    string
	metric    string
	unit      string
	sourceIDs []string
	market    bool
}

func registerMarketIntelligence(a *agentsdk.Agent, e *evidence.Service) {
	s := marketIntelligence{evidence: e}
	a.RegisterTool(tool.Typed[marketIntelligenceInput, marketIntelligenceResult]("read_wordstat").
		Description("Read Wordstat search-query observations from this user's authorized imported Product Manager snapshots. With no product_key, list available products. No live network request.").
		Execute(func(ctx context.Context, in marketIntelligenceInput) (marketIntelligenceResult, error) {
			return s.read(ctx, in, intelligenceSpec{name: "wordstat", source: "research", metric: "search_queries", unit: "count", sourceIDs: []string{"wordstat", "yandex-wordstat:search-api"}})
		}).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[marketIntelligenceInput, marketIntelligenceResult]("read_google_trends").
		Description("Read Google Trends search-interest observations from this user's authorized imported Product Manager snapshots. With no product_key, list available products. No live network request.").
		Execute(func(ctx context.Context, in marketIntelligenceInput) (marketIntelligenceResult, error) {
			return s.read(ctx, in, intelligenceSpec{name: "google_trends", source: "research", metric: "search_interest", unit: "index", sourceIDs: []string{"google_trends"}})
		}).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[marketIntelligenceInput, marketIntelligenceResult]("read_competitor_prices").
		Description("Read competitor offer prices from this user's authorized imported Product Manager snapshots. Each row keeps its SKU, seller, quantity and price basis; unknown quantities remain incomparable. With no product_key, list available SKUs. No live network request.").
		Execute(func(ctx context.Context, in marketIntelligenceInput) (marketIntelligenceResult, error) {
			return s.read(ctx, in, intelligenceSpec{name: "competitor_prices", source: "market", metric: "displayed_price", market: true})
		}).Build(), agentsdk.AccessUser)
}

func (s marketIntelligence) read(ctx context.Context, in marketIntelligenceInput, spec intelligenceSpec) (marketIntelligenceResult, error) {
	if in.Offset < 0 || in.Limit < 0 || in.Limit > 50 {
		return marketIntelligenceResult{}, errors.New("limit 1..50, offset >= 0")
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	snapshots, err := s.snapshotCandidates(ctx, in.SnapshotID)
	if err != nil {
		return marketIntelligenceResult{}, err
	}
	for _, snapshot := range snapshots {
		result, err := s.readSnapshot(ctx, snapshot, in, spec)
		if err != nil {
			return marketIntelligenceResult{}, err
		}
		if result.Status == "available" || in.SnapshotID != "" || len(result.Warnings) > 0 {
			return result, nil
		}
	}
	return marketIntelligenceResult{Status: "unavailable", Source: spec.name, Warnings: []string{"В авторизованных импортированных снимках источник не найден."}}, nil
}

func (s marketIntelligence) snapshotCandidates(ctx context.Context, requested string) ([]evidence.Summary, error) {
	if requested != "" {
		return []evidence.Summary{{SnapshotID: requested}}, nil
	}
	index, err := s.evidence.List(ctx, evidence.Empty{})
	return index.Snapshots, err
}

func (s marketIntelligence) readSnapshot(ctx context.Context, snapshot evidence.Summary, in marketIntelligenceInput, spec intelligenceSpec) (marketIntelligenceResult, error) {
	query := evidence.Query{SnapshotID: snapshot.SnapshotID, Source: spec.source, MetricKey: spec.metric, ProductKey: in.ProductKey, Limit: 100}
	var rows []evidence.Row
	var warnings []string
	var asOf string
	for {
		page, err := s.evidence.Read(ctx, query)
		if err != nil {
			return marketIntelligenceResult{}, err
		}
		asOf = page.AsOf
		warnings = append(warnings, page.Warnings...)
		rows = append(rows, page.Rows...)
		query.Offset += len(page.Rows)
		if query.Offset >= page.Total || len(page.Rows) == 0 {
			break
		}
	}
	observations := selectIntelligenceRows(rows, spec)
	products := availableProducts(observations, in.Query)
	result := marketIntelligenceResult{Status: "unavailable", Source: spec.name, SnapshotID: snapshot.SnapshotID, AsOf: asOf, ProductKey: in.ProductKey, Warnings: uniqueStrings(warnings)}
	if in.ProductKey == "" {
		result.Total = len(products)
		start, end := pageBounds(in.Offset, in.Limit, len(products))
		result.AvailableProducts = slices.Clone(products[start:end])
		result.NextOffset = nextOffset(end, len(products))
		if len(products) > 0 {
			result.Status = "available"
		}
		return result, nil
	}
	result.Total = len(observations)
	start, end := pageBounds(in.Offset, in.Limit, len(observations))
	result.Observations = slices.Clone(observations[start:end])
	result.NextOffset = nextOffset(end, len(observations))
	if len(observations) > 0 {
		result.Status = "available"
	}
	return result, nil
}

func selectIntelligenceRows(rows []evidence.Row, spec intelligenceSpec) []marketIntelligenceObservation {
	out := make([]marketIntelligenceObservation, 0, len(rows))
	for _, row := range rows {
		if row.Source != spec.source || row.Metric != spec.metric || row.Status != "observed" {
			continue
		}
		if !spec.market && !slices.Contains(spec.sourceIDs, row.Ref.SourceID) {
			continue
		}
		if spec.market && row.Kind != "offer" {
			continue
		}
		value, err := strconv.ParseFloat(row.Value, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || (!spec.market && row.Unit != spec.unit) {
			continue
		}
		if (spec.unit == "count" && (value > 9007199254740991 || value != math.Trunc(value))) || (spec.unit == "index" && value > 100) {
			continue
		}
		details := decodeDetails(row.Details)
		origin := detailString(details, "origin")
		item := marketIntelligenceObservation{
			ID: row.ID, ProductKey: row.Product, MetricKey: row.Metric, Kind: row.Kind, Value: value, Unit: row.Unit, Period: row.Period, SourceRef: row.Ref, Status: row.Status,
			Origin: origin, Synthetic: detailBool(details, "synthetic") || origin == "synthetic_fixture", Normalization: detailString(details, "normalization"), Geo: detailString(details, "geo"),
		}
		if row.Ref.SourceID == "yandex-wordstat:search-api" && row.Metric == "search_queries" && row.Unit == "count" {
			item.AuthenticAPI = true
		}
		if spec.market {
			item.Name = detailString(details, "name")
			item.Seller = detailString(details, "seller")
			if item.Seller == "" {
				item.Seller = row.Ref.SourceID
			}
			item.PriceBasis = detailString(details, "price_basis")
			item.Quantity = detailNumber(details, "quantity")
			item.MaxQuantity = detailNumber(details, "max_quantity")
			item.CapturedAt = detailString(details, "captured_at")
			item.ComparableTerms = item.PriceBasis != "" && item.Quantity != nil
		}
		out = append(out, item)
	}
	slices.SortStableFunc(out, func(a, b marketIntelligenceObservation) int {
		if n := strings.Compare(a.ProductKey, b.ProductKey); n != 0 {
			return n
		}
		if n := strings.Compare(a.Period.From, b.Period.From); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func availableProducts(rows []marketIntelligenceObservation, query string) []string {
	seen := map[string]bool{}
	needle := strings.ToLower(strings.TrimSpace(query))
	for _, row := range rows {
		if needle == "" || strings.Contains(strings.ToLower(row.ProductKey), needle) || strings.Contains(strings.ToLower(row.Name), needle) {
			seen[row.ProductKey] = true
		}
	}
	out := make([]string, 0, len(seen))
	for product := range seen {
		out = append(out, product)
	}
	slices.Sort(out)
	return out
}

func decodeDetails(raw string) map[string]any {
	var details map[string]any
	_ = json.Unmarshal([]byte(raw), &details)
	return details
}

func detailString(details map[string]any, key string) string {
	value, found := details[key]
	if !found || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func detailBool(details map[string]any, key string) bool {
	value, _ := details[key].(bool)
	return value
}

func detailNumber(details map[string]any, key string) *float64 {
	value, ok := details[key].(float64)
	if !ok || value < 0 {
		return nil
	}
	return &value
}

func pageBounds(offset, limit, total int) (int, int) {
	start := min(offset, total)
	return start, min(start+limit, total)
}

func nextOffset(end, total int) *int {
	if end >= total {
		return nil
	}
	next := end
	return &next
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
