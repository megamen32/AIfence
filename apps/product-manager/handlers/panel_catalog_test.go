package handlers

import (
	"agent/internal/catalogoverview"
	"agent/views"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCatalogTailPreservesFullCountAndSignalVendors(t *testing.T) {
	vendors := make([]catalogoverview.Vendor, 23)
	for i := range vendors {
		vendors[i] = catalogoverview.Vendor{Vendor: fmt.Sprintf("Vendor %02d", i), ProductCount: int64(50 - i)}
	}
	vendors[21].Vendor, vendors[22].Vendor = "Kaspersky", "Группа Астра"
	var total int64
	for _, v := range vendors {
		total += v.ProductCount
	}
	const outside = 1000
	h := &Handler{catalogOverview: func(context.Context, catalogoverview.GetInput) (catalogoverview.Overview, error) {
		return catalogoverview.Overview{AsOf: time.Now(), TotalProducts: total + outside, VendorCount: 1326,
			OutsidePageProducts: outside, Vendors: vendors}, nil
	}}
	m := &views.Monitor{Pulse: []views.PulseItem{{ProductKey: ksosFamily, Product: productName(ksosFamily), ControlKey: "astra_linux", URL: "./?alert=target"}}}
	h.loadOverview(t.Context(), m, url.Values{}, "overview", "real")
	found := map[string]bool{}
	sum := int64(0)
	for _, tile := range m.Catalog.Tiles {
		if (tile.Tone == "signal-associated" || tile.Tone == "control-context") || tile.Name == "Другие" {
			found[tile.Name] = true
		}
		n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSuffix(tile.Value, " товаров"), " ", ""), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		sum += n
	}
	if sum != total+outside || len(found) != 3 || m.Catalog.Weight != "Количество товаров" {
		t.Fatalf("sum=%d, signal and tail=%v, map=%+v", sum, found, m.Catalog)
	}
}

func TestCatalogSearchUsesAirlockSiblingOrigin(t *testing.T) {
	if got := marketCatalogURL("http://product-manager.localhost:42080"); got != "http://market-watch.localhost:42080/" {
		t.Fatal(got)
	}
	if got := marketCatalogURL("https://elsewhere.example/"); got != "" {
		t.Fatal(got)
	}
}
