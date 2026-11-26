package catalogoverview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
)

func TestValidateImport(t *testing.T) {
	source := Source{SourceID: "market-watch", Locator: "export/summary"}
	valid := ImportInput{Confirmed: true, AsOf: time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC), TotalProducts: 12, Source: source, Vendors: []Vendor{{Vendor: "Vendor", ProductCount: 12, Source: source}}}
	cases := []struct {
		name   string
		mutate func(*ImportInput)
	}{
		{"confirmation", func(x *ImportInput) { x.Confirmed = false }},
		{"missing source", func(x *ImportInput) { x.Source.Locator = "" }},
		{"negative count", func(x *ImportInput) { x.TotalProducts = -1 }},
		{"counts do not match", func(x *ImportInput) { x.TotalProducts = 13 }},
		{"duplicate vendor", func(x *ImportInput) { x.Vendors = append(x.Vendors, Vendor{Vendor: " vendor ", Source: source}) }},
		{"invalid coverage", func(x *ImportInput) {
			x.Vendors = []Vendor{{Vendor: "Vendor", Source: source, Revenue: &Revenue{Amount: 1, Currency: "RUB", Coverage: 1.1, Source: source}}}
		}},
	}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := valid
			tc.mutate(&x)
			if validate(x) == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
func TestOwnerRequiresAdmittedHuman(t *testing.T) {
	human := wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	ctx := testutil.WithCaller(context.Background(), wire.Caller{Kind: "user", Access: wire.AccessUser, User: &human, Initiator: &human, Origin: wire.CallerOrigin{Interface: "chat", Execution: "request"}})
	if got, err := owner(ctx); err != nil || got != human.ID {
		t.Fatalf("owner = %s %v", got, err)
	}
	app := testutil.WithCaller(context.Background(), wire.Caller{Kind: "application", Access: wire.AccessAdmin, Origin: wire.CallerOrigin{Interface: "application", Execution: "background"}})
	if _, err := owner(app); err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatal("application caller accepted")
	}
}

func TestPaginationKeepsUnshownProducts(t *testing.T) {
	doc := ImportInput{TotalProducts: 100, Vendors: []Vendor{{ProductCount: 60}, {ProductCount: 30}, {ProductCount: 10}}}
	got := page(doc, GetInput{Limit: 2})
	if got.TotalProducts != 100 || got.OutsidePageProducts != 10 || got.VendorCount != 3 || len(got.Vendors) != 2 {
		t.Fatalf("wrong totals: %+v", got)
	}
	got = page(doc, GetInput{Offset: 2, Limit: 2})
	if got.OutsidePageProducts != 90 || len(got.Vendors) != 1 {
		t.Fatalf("wrong second page: %+v", got)
	}
}

func TestPanelPreservesEveryVendor(t *testing.T) {
	doc := ImportInput{TotalProducts: 1326, Vendors: make([]Vendor, 1326)}
	for i := range doc.Vendors {
		doc.Vendors[i].ProductCount = 1
	}
	got := page(doc, GetInput{Limit: len(doc.Vendors)})
	if len(got.Vendors) != 1326 || got.OutsidePageProducts != 0 || got.TotalProducts != 1326 {
		t.Fatalf("panel omitted vendors: %+v", got)
	}
}
