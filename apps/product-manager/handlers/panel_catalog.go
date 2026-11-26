package handlers

import (
	"agent/internal/catalogoverview"
	"agent/views"
	"context"
	"math"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/airlockrun/agentsdk"
)

type mapGroup struct {
	name, tone, signal, signalTitle, link string
	other                                 bool
	count                                 int64
	revenue                               float64
}

type mapBox struct{ x, y, w, h float64 }

func (h *Handler) loadOverview(ctx context.Context, m *views.Monitor, q url.Values, tab, mode string) {
	if tab != "overview" {
		return
	}
	m.Search, m.Mode = strings.TrimSpace(q.Get("q")), mode
	if base, err := agentsdk.AgentURLFromContext(ctx); err == nil {
		m.MarketURL = marketCatalogURL(base)
	}
	needle := strings.ToLower(m.Search)
	for i := range m.Pulse {
		if link, err := url.Parse(m.Pulse[i].URL); err == nil {
			params := link.Query()
			params.Set("mode", mode)
			link.RawQuery = params.Encode()
			m.Pulse[i].URL = link.String()
		}
	}
	if needle != "" {
		for _, product := range m.Products {
			if strings.Contains(strings.ToLower(product.Name), needle) {
				m.Matches = append(m.Matches, views.Link{Label: product.Name, URL: product.URL})
			}
		}
	}
	if h.catalogOverview == nil {
		return
	}
	doc, err := h.catalogOverview(ctx, catalogoverview.GetInput{Limit: 100})
	if err != nil || doc.AsOf.IsZero() {
		return
	}
	c := &views.CatalogMap{
		Date: doc.AsOf.UTC().Format("02.01.2006, 15:04 UTC"), Group: "Вендоры", Filter: m.Search,
		Count: compactInt(doc.TotalProducts),
	}
	if m.Updated == "" {
		m.Updated = c.Date
	}
	m.Catalog = c
	groups := make([]mapGroup, 0, len(doc.Vendors))
	useRevenue := len(doc.Vendors) != 0 && len(doc.Vendors) == doc.VendorCount
	totalRevenue, currency := 0.0, ""
	var covered int64
	for _, v := range doc.Vendors {
		g := mapGroup{name: v.Vendor, count: v.ProductCount, tone: ""}
		covered += v.ProductCount
		if r := v.Revenue; r != nil && r.Coverage == 1 && r.Amount >= 0 && (currency == "" || strings.EqualFold(currency, r.Currency)) {
			currency = r.Currency
			g.revenue = r.Amount
			totalRevenue += r.Amount
		} else {
			useRevenue = false
		}
		groups = append(groups, g)
	}
	outside := doc.OutsidePageProducts
	if outside != doc.TotalProducts-covered {
		outside = doc.TotalProducts - covered
	}
	c.Partial = outside > 0
	useRevenue = useRevenue && outside == 0 && totalRevenue > 0 && !math.IsInf(totalRevenue, 0)
	if useRevenue {
		c.Weight = "Фактическая выручка (" + currency + ")"
	} else {
		c.Weight = "Количество товаров"
	}
	if outside > 0 && needle == "" {
		groups = append(groups, mapGroup{name: "Другие", count: outside, other: true})
	}
	markVendors(groups, m.Pulse)
	if needle == "" {
		c.OtherGroups = collapsedCatalogGroups(groups, useRevenue, currency)
		if outside > 0 {
			c.OtherRemainder = compactInt(outside) + " товаров вне страницы агрегата не раскрыты"
		}
	}
	c.Tiles = catalogTiles(groups, useRevenue, currency, needle, mode)
}

func marketCatalogURL(base string) string {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return ""
	}
	suffix, ok := strings.CutPrefix(u.Hostname(), "product-manager.")
	if !ok || suffix == "" {
		return ""
	}
	host := "market-watch." + suffix
	if u.Port() != "" {
		host = net.JoinHostPort(host, u.Port())
	}
	return (&url.URL{Scheme: u.Scheme, Host: host, Path: "/"}).String()
}

func markVendors(groups []mapGroup, alerts []views.PulseItem) {
	for _, alert := range alerts {
		if i := matchVendor(groups, alert.ControlVendor, productName(alert.ControlKey)); i >= 0 && groups[i].tone == "" {
			groups[i].tone = "control-context"
			groups[i].signalTitle = "Контроль для " + alert.Product
		}
		if i := matchVendor(groups, alert.Vendor, alert.Product); i >= 0 && groups[i].link == "" {
			p := &groups[i]
			p.link = alert.URL
			p.signal = alert.Change
			p.signalTitle = "Сигнал у товара вендора: " + alert.Product
			if alert.Change != "" && alert.Comparison != "" {
				p.signalTitle += " · " + alert.Change + " " + alert.Comparison
			}
			p.tone = "signal-associated"
			if alert.Delta != nil {
				switch {
				case *alert.Delta <= -30:
					p.tone += " delta-down-high"
				case *alert.Delta < 0:
					p.tone += " delta-down"
				case *alert.Delta > 0:
					p.tone += " delta-up"
				}
			}
		}
	}
}

func matchVendor(groups []mapGroup, vendor, product string) int {
	normalize := func(s string) string {
		return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		}), " ")
	}
	exact, productWords := normalize(vendor), strings.Fields(normalize(product))
	if exact == "" && len(productWords) == 0 {
		return -1
	}
	score, best := 0, -1
	for i, g := range groups {
		if g.other {
			continue
		}
		name := normalize(g.name)
		if exact != "" && exact == name {
			return i
		}
		if exact != "" {
			continue
		}
		words := strings.Fields(name)
		if len(words) > 1 && (words[0] == "группа" || words[0] == "group") {
			words = words[1:]
		}
		if len(words) == 0 || len(words) > len(productWords) {
			continue
		}
		matches := true
		for j, token := range words {
			if token != productWords[j] && transliterate(token) != transliterate(productWords[j]) {
				matches = false
				break
			}
		}
		if matches && len(words) >= score {
			if len(words) == score {
				best = -1
			} else {
				score, best = len(words), i
			}
		}
	}
	return best
}

func transliterate(s string) string {
	var out strings.Builder
	letters := []rune("абвгдежзийклмнопрстуфхцчшщъыьэюя")
	latin := []string{"a", "b", "v", "g", "d", "e", "zh", "z", "i", "y", "k", "l", "m", "n", "o", "p", "r", "s", "t", "u", "f", "kh", "ts", "ch", "sh", "shch", "", "y", "", "e", "yu", "ya"}
	for _, r := range s {
		if i := slices.Index(letters, r); i >= 0 {
			out.WriteString(latin[i])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func compactInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

func catalogGroupValue(g mapGroup, revenue bool, currency string) string {
	if revenue {
		return num(g.revenue) + " " + currency
	}
	return compactInt(g.count) + " товаров"
}

func collapsedCatalogGroups(groups []mapGroup, revenue bool, currency string) []views.CatalogGroup {
	type item struct {
		group mapGroup
		area  float64
	}
	items := make([]item, 0, len(groups))
	for _, g := range groups {
		area := float64(g.count)
		if revenue {
			area = g.revenue
		}
		if area > 0 {
			items = append(items, item{group: g, area: area})
		}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		if a.area > b.area {
			return -1
		}
		if a.area < b.area {
			return 1
		}
		return strings.Compare(a.group.name, b.group.name)
	})
	const maxVisible = 20
	visible := 0
	var out []views.CatalogGroup
	for _, it := range items {
		if it.group.other {
			continue
		}
		if visible < maxVisible || it.group.tone != "" {
			visible++
			continue
		}
		out = append(out, views.CatalogGroup{Name: it.group.name, Value: catalogGroupValue(it.group, revenue, currency)})
	}
	return out
}

func catalogTiles(groups []mapGroup, revenue bool, currency, needle, mode string) []views.CatalogTile {
	type item struct {
		group mapGroup
		area  float64
	}
	total := 0.0
	var items []item
	for _, g := range groups {
		w := float64(g.count)
		if revenue {
			w = g.revenue
		}
		if w <= 0 || (needle != "" && !strings.Contains(strings.ToLower(g.name), needle)) {
			continue
		}
		items = append(items, item{g, w})
		total += w
	}
	if total == 0 {
		return nil
	}
	const width, height = 640, 280
	slices.SortStableFunc(items, func(a, b item) int {
		if a.area > b.area {
			return -1
		}
		if a.area < b.area {
			return 1
		}
		return strings.Compare(a.group.name, b.group.name)
	})
	const maxVisible = 20
	var top []item
	var other item
	other.group = mapGroup{name: "Другие", other: true}
	for _, it := range items {
		if !it.group.other && (len(top) < maxVisible || it.group.tone != "") {
			top = append(top, it)
			continue
		}
		other.area += it.area
		other.group.count += it.group.count
		other.group.revenue += it.group.revenue
	}
	if other.area > 0 {
		top = append(top, other)
	}
	items = top
	slices.SortStableFunc(items, func(a, b item) int {
		if a.area > b.area {
			return -1
		}
		if a.area < b.area {
			return 1
		}
		return strings.Compare(a.group.name, b.group.name)
	})
	for i := range items {
		items[i].area = items[i].area / total * width * height
	}
	worst := func(short, sum, minArea, maxArea float64) float64 {
		return max(short*short*maxArea/(sum*sum), sum*sum/(short*short*minArea))
	}
	box := mapBox{w: width, h: height}
	f := func(n float64) string { return strconv.FormatFloat(n, 'f', 5, 64) }
	var tiles []views.CatalogTile
	for len(items) > 0 && min(box.w, box.h) > 0 {
		short := min(box.w, box.h)
		end, sum := 1, items[0].area
		for end < len(items) && worst(short, sum+items[end].area, items[end].area, items[0].area) <=
			worst(short, sum, items[end-1].area, items[0].area) {
			sum += items[end].area
			end++
		}
		thickness := sum / short
		at := 0.0
		for _, it := range items[:end] {
			length := it.area / thickness
			tile := mapBox{x: box.x, y: box.y + at, w: thickness, h: length}
			if box.w < box.h {
				tile = mapBox{x: box.x + at, y: box.y, w: length, h: thickness}
			}
			g := it.group
			value := catalogGroupValue(g, revenue, currency)
			link := ""
			if g.link != "" {
				link = g.link
			} else if !g.other {
				link = href("tab", "overview", "mode", mode, "q", g.name)
			}
			if g.signal != "" {
				value += " · " + g.signal
			}
			aria := g.name + " · " + value
			if g.signalTitle != "" {
				aria += " · " + g.signalTitle
			}
			tiles = append(tiles, views.CatalogTile{
				Name: g.name, Label: aria, Value: value, Tone: g.tone, URL: link,
				X: f(tile.x), Y: f(tile.y), W: f(tile.w), H: f(tile.h), LX: f(tile.x + 8), LY: f(tile.y + 17),
				ShowLabel: tile.w > float64(len([]rune(g.name))*7+16) && tile.h > 26,
			})
			at += length
		}
		if box.w >= box.h {
			box.x, box.w = box.x+thickness, box.w-thickness
		} else {
			box.y, box.h = box.y+thickness, box.h-thickness
		}
		items = items[end:]
	}
	return tiles
}
