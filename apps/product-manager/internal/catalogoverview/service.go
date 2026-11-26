package catalogoverview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"agent/internal/db"
	"github.com/airlockrun/agentsdk"
)

const maxVendors = 10000
const maxDocument = 2 << 20

type Source struct {
	SourceID string `json:"source_id"`
	Locator  string `json:"locator"`
}
type Revenue struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Coverage float64 `json:"coverage"`
	Source   Source  `json:"source"`
}
type Vendor struct {
	Vendor       string   `json:"vendor"`
	ProductCount int64    `json:"product_count"`
	Source       Source   `json:"source"`
	Revenue      *Revenue `json:"revenue,omitempty"`
}
type ImportInput struct {
	Confirmed     bool      `json:"confirmed" jsonschema:"description=Explicitly confirm replacing this user's catalog overview"`
	AsOf          time.Time `json:"as_of"`
	TotalProducts int64     `json:"total_products"`
	Source        Source    `json:"source"`
	Vendors       []Vendor  `json:"vendors"`
}
type GetInput struct {
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}
type Overview struct {
	AsOf                time.Time `json:"as_of"`
	TotalProducts       int64     `json:"total_products"`
	Source              Source    `json:"source"`
	VendorCount         int       `json:"vendor_count"`
	Offset              int       `json:"offset"`
	OutsidePageProducts int64     `json:"outside_page_products"`
	Vendors             []Vendor  `json:"vendors"`
}
type Receipt struct {
	AsOf          time.Time `json:"as_of"`
	VendorCount   int       `json:"vendor_count"`
	TotalProducts int64     `json:"total_products"`
}
type Service struct{ database *agentsdk.AgentDB }

func New(database *agentsdk.AgentDB) *Service {
	if database == nil {
		panic("catalogoverview: database required")
	}
	return &Service{database: database}
}
func owner(ctx context.Context) (string, error) {
	caller := agentsdk.CallerFromContext(ctx)
	user, ok := caller.User()
	if !ok || user.ID == "" || (caller.Access() != agentsdk.AccessUser && caller.Access() != agentsdk.AccessAdmin) {
		return "", errors.New("catalog overview requires an authorized user")
	}
	return user.ID, nil
}
func validSource(s Source) bool {
	return strings.TrimSpace(s.SourceID) != "" && strings.TrimSpace(s.Locator) != "" && len(s.SourceID) <= 256 && len(s.Locator) <= 2048
}
func validate(in ImportInput) error {
	if !in.Confirmed {
		return errors.New("confirmation required")
	}
	if in.AsOf.IsZero() || in.TotalProducts < 0 || !validSource(in.Source) || len(in.Vendors) > maxVendors {
		return errors.New("invalid catalog overview")
	}
	seen := map[string]bool{}
	var total int64
	for _, v := range in.Vendors {
		key := strings.TrimSpace(v.Vendor)
		if key == "" || len(key) > 256 || v.ProductCount < 0 || !validSource(v.Source) || seen[strings.ToLower(key)] {
			return errors.New("invalid or duplicate vendor")
		}
		seen[strings.ToLower(key)] = true
		if v.ProductCount > math.MaxInt64-total {
			return errors.New("vendor count overflow")
		}
		total += v.ProductCount
		if v.Revenue != nil && (v.Revenue.Amount < 0 || v.Revenue.Coverage < 0 || v.Revenue.Coverage > 1 || len(v.Revenue.Currency) != 3 || !validSource(v.Revenue.Source)) {
			return errors.New("invalid revenue or coverage")
		}
	}
	if total != in.TotalProducts {
		return errors.New("vendor product counts do not match total_products")
	}
	return nil
}
func (s *Service) Import(ctx context.Context, in ImportInput) (Receipt, error) {
	id, err := owner(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if err = validate(in); err != nil {
		return Receipt{}, err
	}
	in.Confirmed = false
	data, err := json.Marshal(in)
	if err != nil || len(data) > maxDocument {
		return Receipt{}, errors.New("catalog overview too large or invalid")
	}
	n, err := db.New(s.database).UpsertCatalogOverview(ctx, db.UpsertCatalogOverviewParams{OwnerID: id, AsOf: in.AsOf, Document: data})
	if err != nil {
		return Receipt{}, fmt.Errorf("save catalog overview: %w", err)
	}
	if n == 0 {
		return Receipt{}, errors.New("newer catalog overview already exists")
	}
	return Receipt{AsOf: in.AsOf, VendorCount: len(in.Vendors), TotalProducts: in.TotalProducts}, nil
}
func (s *Service) Get(ctx context.Context, in GetInput) (Overview, error) {
	id, err := owner(ctx)
	if err != nil {
		return Overview{}, err
	}
	if in.Offset < 0 || in.Limit < 0 || in.Limit > 100 {
		return Overview{}, errors.New("invalid pagination")
	}
	if in.Limit == 0 {
		in.Limit = 30
	}
	data, err := db.New(s.database).GetCatalogOverview(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Overview{}, errors.New("catalog overview unavailable")
	}
	if err != nil {
		return Overview{}, err
	}
	var doc ImportInput
	if err = json.Unmarshal(data, &doc); err != nil {
		return Overview{}, err
	}
	return page(doc, in), nil
}

func page(doc ImportInput, in GetInput) Overview {
	start := min(in.Offset, len(doc.Vendors))
	end := min(start+in.Limit, len(doc.Vendors))
	var pageTotal int64
	for _, v := range doc.Vendors[start:end] {
		pageTotal += v.ProductCount
	}
	return Overview{AsOf: doc.AsOf, TotalProducts: doc.TotalProducts, Source: doc.Source, VendorCount: len(doc.Vendors), Offset: start, OutsidePageProducts: doc.TotalProducts - pageTotal, Vendors: doc.Vendors[start:end]}
}
