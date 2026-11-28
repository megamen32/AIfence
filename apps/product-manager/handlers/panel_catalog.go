package handlers

import (
	"agent/internal/catalogoverview"
	"agent/internal/evidence"
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
	other, forceLabel                     bool
	count                                 int64
	revenue                               float64
	signalDelta                           *float64
}

type mapBox struct{ x, y, w, h float64 }

func (h *Handler) loadOverview(ctx context.Context, m *views.Monitor, q url.Values, tab, mode string, snapshots []evidence.Summary) {
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
	selectedVendor := strings.TrimSpace(q.Get("vendor"))
	if selectedVendor == "" && needle != "" {
		for _, g := range groups {
			if !g.other && strings.Contains(strings.ToLower(g.name), needle) {
				selectedVendor = g.name
				break
			}
		}
	}
	if selectedVendor != "" {
		for _, g := range groups {
			if !g.other && strings.EqualFold(g.name, selectedVendor) {
				selectedVendor = g.name
				c.Vendor, c.Group = g.name, g.name
				c.BackURL = href("tab", "overview", "mode", mode)
				rows, readErr := h.monitorRows(ctx, snapshots)
				if readErr == nil {
					c.Tiles = vendorProductTiles(g.name, g.count, rows, m.Pulse, m.Records, mode)
					m.VendorDetail = vendorDetail(g.name, g.count, rows, m.Pulse, m.Records, mode)
				}
				break
			}
		}
	}
	if c.Vendor == "" {
		c.Tiles = catalogTiles(groups, useRevenue, currency, needle, mode)
	}
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
		if i := matchVendor(groups, alert.Vendor, alert.Product); i >= 0 {
			p := &groups[i]
			delta := catalogSignalDelta(alert)
			if p.link != "" && !preferCatalogSignal(delta, p.signalDelta) {
				continue
			}
			p.link = alert.URL
			p.signal = alert.Change
			p.signalDelta = delta
			p.signalTitle = "Сигнал у товара вендора: " + alert.Product
			if alert.Change != "" && alert.Comparison != "" {
				p.signalTitle += " · " + alert.Change + " " + alert.Comparison
			}
			p.tone = "signal-associated"
			if delta := catalogSignalDelta(alert); delta != nil {
				switch {
				case *delta <= -30:
					p.tone += " delta-down-high"
				case *delta < 0:
					p.tone += " delta-down"
				case *delta > 0:
					p.tone += " delta-up"
				}
			}
		}
	}
}

func preferCatalogSignal(candidate, current *float64) bool {
	if current == nil {
		return candidate != nil
	}
	if candidate == nil {
		return false
	}
	return *candidate < *current
}

func catalogSignalDelta(alert views.PulseItem) *float64 {
	if alert.Delta != nil {
		return alert.Delta
	}
	value := strings.TrimSpace(strings.TrimSuffix(alert.Change, "%"))
	value = strings.ReplaceAll(value, "−", "-")
	value = strings.ReplaceAll(value, ",", ".")
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}
	return &parsed
}

func catalogFocusLabel(g mapGroup) string {
	first, second := catalogFocusLines(g)
	return strings.TrimSpace(first + " " + second)
}

func catalogFocusLines(g mapGroup) (string, string) {
	if strings.Contains(g.tone, "control-context") {
		return "Astra", "контроль"
	}
	if strings.Contains(g.tone, "signal-associated") {
		name := g.name
		if strings.Contains(strings.ToLower(name), "kaspersky") {
			name = "KES"
		}
		if g.signal != "" {
			return name, g.signal
		}
		return name, "сигнал"
	}
	return g.name, ""
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
				link = href("tab", "overview", "mode", mode, "vendor", g.name)
			}
			if g.signal != "" {
				value += " · " + g.signal
			}
			focus := strings.Contains(g.tone, "signal-associated") || strings.Contains(g.tone, "control-context")
			displayName := g.name
			line1, line2 := g.name, ""
			if focus {
				displayName = catalogFocusLabel(g)
				line1, line2 = catalogFocusLines(g)
			}
			aria := g.name + " · " + value
			if g.signalTitle != "" {
				aria += " · " + g.signalTitle
			}
			tiles = append(tiles, views.CatalogTile{
				Name: g.name, DisplayName: displayName, Label: aria, Value: value, Tone: g.tone, URL: link, LabelLine1: line1, LabelLine2: line2,
				X: f(tile.x), Y: f(tile.y), W: f(tile.w), H: f(tile.h), LX: f(tile.x + 8), LY: f(tile.y + 17),
				ShowLabel: g.forceLabel || focus || tile.w > float64(len([]rune(g.name))*7+16) && tile.h > 26, Focus: focus,
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

func vendorProductTiles(vendor string, vendorCount int64, rows []monitorObservation, pulses []views.PulseItem, records []views.Link, mode string) []views.CatalogTile {
	groups := vendorProductGroups(vendor, vendorCount, rows, pulses, records, mode)
	return catalogTiles(groups, false, "", "", mode)
}

func vendorProductGroups(vendor string, vendorCount int64, rows []monitorObservation, pulses []views.PulseItem, records []views.Link, mode string) []mapGroup {
	groups := make([]mapGroup, 0, 5)
	byName := map[string]int{}
	for _, row := range rows {
		if !monitorRowBelongsToVendor(row, vendor) || row.Product == "" {
			continue
		}
		name := observedProductName(row.Product, row)
		if _, ok := byName[name]; ok {
			continue
		}
		g := mapGroup{name: name, count: 1, forceLabel: true}
		if window := evidence.WatchdogWindow(row.Row); window != nil {
			if delta := signalDelta(window); delta != nil {
				g.signalDelta, g.signal = delta, signalPercent(*delta)
				g.tone = catalogToneForDelta(*delta)
			}
		}
		for _, pulse := range pulses {
			if strings.EqualFold(pulse.Product, name) || strings.EqualFold(pulse.ProductKey, row.Product) {
				g.link = withTab(pulse.URL, "hypothesis")
				break
			}
		}
		if g.link == "" {
			for _, record := range records {
				if strings.EqualFold(record.Label, name) {
					g.link = withTab(record.URL, "hypothesis")
					break
				}
			}
		}
		byName[name] = len(groups)
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		groups = seededVendorGroups(vendor, mode)
	} else if strings.Contains(strings.ToLower(vendor), "kaspersky") && len(groups) < 2 {
		groups = append(groups, seededVendorGroups(vendor, mode)[1:]...)
	}
	if vendorCount > int64(len(groups)) {
		groups[0].count = maxInt64(1, vendorCount/2)
		if len(groups) > 1 {
			groups[1].count = maxInt64(1, vendorCount/3)
		}
	}
	return groups
}

func seededVendorGroups(vendor, mode string) []mapGroup {
	if strings.Contains(strings.ToLower(vendor), "kaspersky") {
		return []mapGroup{
			{name: "Kaspersky Endpoint Security", count: 1200, signal: "−32,7 %", signalDelta: floatPtr(-32.7), tone: "signal-associated delta-down-high", forceLabel: true, link: href("monitor", "kaspersky_endpoint_security", "mode", mode, "tab", "hypothesis")},
			{name: "Kaspersky Small Office Security", count: 780, signal: "−8,4 %", signalDelta: floatPtr(-8.4), tone: "delta-down", forceLabel: true},
			{name: "Kaspersky Anti-Ransomware Tool for Business", count: 420, signal: "4,1 %", signalDelta: floatPtr(4.1), tone: "delta-up", forceLabel: true},
		}
	}
	return []mapGroup{
		{name: vendor + " Платформа", count: 720, signal: "−6,8 %", signalDelta: floatPtr(-6.8), tone: "delta-down", forceLabel: true},
		{name: vendor + " Защита рабочих мест", count: 480, signal: "2,6 %", signalDelta: floatPtr(2.6), tone: "delta-up", forceLabel: true},
		{name: vendor + " Сервер", count: 260, signal: "−1,9 %", signalDelta: floatPtr(-1.9), tone: "delta-down", forceLabel: true},
	}
}

func monitorRowBelongsToVendor(row monitorObservation, vendor string) bool {
	if row.details.Vendor != "" && strings.EqualFold(strings.TrimSpace(row.details.Vendor), strings.TrimSpace(vendor)) {
		return true
	}
	name := strings.ToLower(observedProductName(row.Product, row))
	vendorName := strings.ToLower(vendor)
	return strings.Contains(name, vendorName) || strings.Contains(vendorName, "kaspersky") && strings.Contains(name, "kaspersky")
}

func catalogToneForDelta(delta float64) string {
	switch {
	case delta <= -30:
		return "signal-associated delta-down-high"
	case delta < 0:
		return "delta-down"
	case delta > 0:
		return "delta-up"
	default:
		return ""
	}
}

func vendorDetail(vendor string, vendorCount int64, rows []monitorObservation, pulses []views.PulseItem, records []views.Link, mode string) *views.VendorDetail {
	groups := vendorProductGroups(vendor, vendorCount, rows, pulses, records, mode)
	detail := &views.VendorDetail{Name: vendor, Count: compactInt(vendorCount), Origin: "ДЕМО"}
	for _, group := range groups {
		detail.Top = append(detail.Top, views.VendorProduct{Name: group.name, Change: group.signal, Value: compactInt(group.count) + " SKU"})
	}
	slices.SortStableFunc(detail.Top, func(a, b views.VendorProduct) int { return strings.Compare(a.Change, b.Change) })
	if len(detail.Top) > 5 {
		detail.Top = detail.Top[:5]
	}
	for _, pulse := range pulses {
		if !strings.Contains(strings.ToLower(pulse.Product), strings.ToLower(vendor)) && !strings.Contains(strings.ToLower(vendor), "kaspersky") {
			continue
		}
		if pulse.WeeklyLoss != "" {
			detail.Alerts = append(detail.Alerts, views.VendorAlert{Name: pulse.Product, Change: pulse.Change, Loss: pulse.WeeklyLoss, URL: withTab(pulse.URL, "hypothesis")})
		}
	}
	if strings.Contains(strings.ToLower(vendor), "kaspersky") {
		detail.Sparkline = sparkline([]float64{100, 99, 97, 96, 94, 90, 83, 67.3})
	} else {
		detail.Sparkline = sparkline([]float64{100, 101, 99, 102, 100, 103, 101, 104})
	}
	for _, record := range records {
		if strings.Contains(strings.ToLower(record.Label), strings.ToLower(vendor)) || strings.Contains(strings.ToLower(vendor), "kaspersky") && strings.Contains(strings.ToLower(record.Label), "kaspersky") {
			detail.HypothesisURL = record.URL
			break
		}
	}
	return detail
}

func sparkline(values []float64) []views.SparkPoint {
	points := make([]views.SparkPoint, 0, len(values))
	for i, value := range values {
		x := float64(i) * 40
		y := 42 - (value-60)*0.7
		points = append(points, views.SparkPoint{X: strconv.FormatFloat(x, 'f', 1, 64), Y: strconv.FormatFloat(y, 'f', 1, 64), Label: strconv.FormatFloat(value, 'f', 1, 64) + " %"})
	}
	return points
}

func floatPtr(value float64) *float64 { return &value }

func withTab(raw, tab string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	query.Set("tab", tab)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
