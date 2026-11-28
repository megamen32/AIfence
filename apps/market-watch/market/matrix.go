package market

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	catalog "agent/collector"
	"agent/internal/db"
	"github.com/airlockrun/agentsdk"
)

var matrixSources = []string{"softline", "softmap", "softmagazin"}
var matrixHeader = []string{"cursor", "key", "sku", "product", "conditions", "currency", "softline", "softmap", "softmagazin", "spread", "softline_change", "softmap_change", "softmagazin_change", "leader_changed", "as_of", "compare_url", "comparable", "biggest_drop", "softline_url", "softmap_url", "softmagazin_url", "softline_observed", "softmap_observed", "softmagazin_observed"}

type matrixOffer struct {
	latest Observation
	first  *Observation
}

func money(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func conditionsLabel(o Observation, label string) string {
	t := termsOf(o)
	var pieces []string
	labels := []string{"Новая лицензия", "Продление", "Обновление", "Подписка", "Бессрочная"}
	for i, l := range labels {
		if t.licenseTypes&(1<<i) != 0 {
			pieces = append(pieces, l)
		}
	}
	if t.months > 0 {
		pieces = append(pieces, fmt.Sprintf("%d мес.", t.months))
	}
	if t.packSize > 0 {
		pieces = append(pieces, fmt.Sprintf("%d мест", t.packSize))
	}
	d := o.Details
	switch {
	case label != "":
		pieces = append(pieces, label)
	case d.Quantity == nil:
		pieces = append(pieces, "Количество отсутствует")
	case d.PriceBasis == "per_unit":
		if d.MaxQuantity != nil {
			pieces = append(pieces, fmt.Sprintf("За единицу, %d…%d шт.", *d.Quantity, *d.MaxQuantity))
		} else {
		pieces = append(pieces, fmt.Sprintf("За единицу, минимум %d шт.; предел отсутствует", *d.Quantity))
		}
	case d.PriceBasis == "displayed_total":
		pieces = append(pieces, fmt.Sprintf("Сумма за %d шт.", *d.Quantity))
	default:
		pieces = append(pieces, "Основание цены отсутствует в источнике")
	}
	return strings.Join(pieces, " · ")
}

func matrixLines(cursor string, offers []matrixOffer, origin string) [][]string {
	if len(offers) == 0 {
		return nil
	}
	observations := make([]Observation, 0, len(offers))
	baselines := map[string]Observation{}
	for _, row := range offers {
		observations = append(observations, row.latest)
		if row.first != nil && seriesOf(*row.first) == seriesOf(row.latest) {
			baselines[row.latest.ID] = *row.first
		}
	}
	var lines [][]string
	for _, g := range groupOffers(observations, len(observations) > 100, false) {
		if g.Comparable && observations[0].ProductKey != "" {
			lines = append(lines, matrixLine(cursor, g, baselines, origin))
			continue
		}
		for _, o := range g.Rows {
			lines = append(lines, matrixLine(cursor, ComparisonGroup{Rows: []Observation{o}, Label: g.Label}, baselines, origin))
		}
	}
	return lines
}

func matrixLine(cursor string, g ComparisonGroup, first map[string]Observation, origin string) []string {
	o := g.Rows[0]
	asOf := time.Time{}
	chosen := map[string]Observation{}
	for _, row := range g.Rows {
		seller := row.Details.Seller
		at, _ := time.Parse(time.RFC3339Nano, row.Details.CapturedAt)
		if at.After(asOf) {
			asOf = at
		}
		if have, ok := chosen[seller]; !ok || row.Value < have.Value {
			chosen[seller] = row
		}
	}
	terms := termsOf(o)
	basis := o.Details.PriceBasis
	lo, hi := quantityValue(o.Details.Quantity), quantityValue(o.Details.MaxQuantity)
	if g.Label != "" {
		basis = "one_pack"
		lo, hi = 1, 1
	}
	identity := fmt.Sprintf("%s\x00%#v\x00%s\x00%d/%d", cursor, terms, basis, lo, hi)
	if !g.Comparable {
		identity += "\x00" + o.Details.Seller + "\x00" + o.SourceRef.URL
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	label := conditionsLabel(o, g.Label)
	line := []string{cursor, key, o.ProductKey, o.Details.Name, label, o.Currency}
	var current, previous, urls, dates []string
	nowPrices, oldPrices := map[string]float64{}, map[string]float64{}
	var bestDrop *float64
	firstDate, lastDate := "", ""
	canCompareLeaders := g.Comparable && len(g.Rows) == len(chosen)
	for _, seller := range matrixSources {
		row, ok := chosen[seller]
		value, change, link, at := "", "", "", ""
		if ok {
			value = money(row.Value)
			link = row.SourceRef.URL
			at = row.Details.CapturedAt
			nowPrices[seller] = row.Value
			if old, ok := first[row.ID]; ok {
				delta := math.Round((row.Value-old.Value)*100) / 100
				change = money(delta)
				drop := math.Max(0, -delta)
				if bestDrop == nil || drop > *bestDrop {
					bestDrop = &drop
				}
				oldPrices[seller] = old.Value
				fd, ld := dayUTC(old.Details.CapturedAt), dayUTC(row.Details.CapturedAt)
				if firstDate == "" {
					firstDate, lastDate = fd, ld
				} else if fd != firstDate || ld != lastDate {
					canCompareLeaders = false
				}
			} else {
				canCompareLeaders = false
			}
		}
		current = append(current, value)
		previous = append(previous, change)
		urls = append(urls, link)
		dates = append(dates, at)
	}
	line = append(line, current...)
	spread, leader, drop := "", "", ""
	if g.Comparable {
		vs := []float64{}
		for _, price := range nowPrices {
			vs = append(vs, price)
		}
		sort.Float64s(vs)
		if len(vs) > 1 {
			spread = money(vs[len(vs)-1] - vs[0])
		}
	}
	if canCompareLeaders {
		a, b := uniqueLeader(oldPrices), uniqueLeader(nowPrices)
		if a != "" && b != "" {
			leader = strconv.FormatBool(a != b)
		}
	}
	if bestDrop != nil {
		drop = money(*bestDrop)
	}
	query := url.Values{"tab": {"compare"}, "q": {o.ProductKey}}
	if o.ProductKey == "" {
		query.Set("tab", "catalog")
		query.Set("q", o.SourceRef.URL)
	}
	line = append(line, spread)
	line = append(line, previous...)
	line = append(line, leader, asOf.UTC().Format(time.RFC3339Nano), strings.TrimRight(origin, "/")+"/?"+query.Encode(), strconv.FormatBool(g.Comparable), drop)
	line = append(line, urls...)
	return append(line, dates...)
}

func dayUTC(s string) string {
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return ""
	}
	return at.UTC().Format("2006-01-02")
}
func uniqueLeader(prices map[string]float64) string {
	lowest := math.Inf(1)
	leader := ""
	for seller, v := range prices {
		switch {
		case v < lowest:
			lowest, leader = v, seller
		case v == lowest:
			leader = ""
		}
	}
	return leader
}

func (s *Service) matrixPage(ctx context.Context, after string, limit int, start time.Time, origin string) ([][]string, string, error) {
	rows, err := s.queries.MatrixPage(ctx, db.MatrixPageParams{AfterKey: after, ProductLimit: int32(limit), StartAt: start})
	if err != nil {
		return nil, "", err
	}
	cursor := ""
	var group []matrixOffer
	var lines [][]string
	for _, r := range rows {
		if r.CursorKey != cursor {
			lines = append(lines, matrixLines(cursor, group, origin)...)
			cursor = r.CursorKey
			group = nil
		}
		var latest catalog.Offer
		if err := json.Unmarshal(r.Offer, &latest); err != nil {
			return nil, "", err
		}
		item := matrixOffer{latest: obs(latest)}
		var baseline *catalog.Offer
		if err := json.Unmarshal(r.Baseline, &baseline); err != nil {
			return nil, "", err
		}
		if baseline != nil {
			b := obs(*baseline)
			item.first = &b
		}
		group = append(group, item)
	}
	lines = append(lines, matrixLines(cursor, group, origin)...)
	return lines, cursor, nil
}

// MatrixCSV streams whole license groups per page so quantity tiers cannot be
// accidentally compared across a page boundary. Empty cursors end pagination.
func (s *Service) MatrixCSV(w http.ResponseWriter, r *http.Request) error {
	if s.queries == nil {
		return agentsdk.NewHTTPError(503, "Catalog database unavailable", nil)
	}
	query := r.URL.Query()
	limit := 100
	single := query.Has("limit")
	if single {
		n, err := strconv.Atoi(query.Get("limit"))
		if err != nil || n < 1 || n > 500 {
			return agentsdk.NewHTTPError(400, "limit must be 1 to 500 product keys", nil)
		}
		limit = n
	}
	days := 7
	if query.Has("days") {
		n, err := strconv.Atoi(query.Get("days"))
		if err != nil || n < 1 || n > 365 {
			return agentsdk.NewHTTPError(400, "days must be 1 to 365", nil)
		}
		days = n
	}
	origin, _ := agentsdk.AgentURLFromContext(r.Context())
	after := query.Get("after")
	start := s.now().UTC().AddDate(0, 0, -days)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=market-matrix.csv")
	writer := csv.NewWriter(w)
	writer.Comma = ';'
	header := false
	for {
		lines, next, err := s.matrixPage(r.Context(), after, limit, start, origin)
		if err != nil {
			return err
		}
		if !header {
			if _, err := w.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
				return err
			}
			if err := writer.Write(matrixHeader); err != nil {
				return err
			}
			header = true
		}
		if err := writer.WriteAll(lines); err != nil {
			return err
		}
		if single || next == "" {
			return nil
		}
		after = next
	}
}
