// Command coverage writes a local Excel-compatible register of discovered URLs and observations.
package main

import (
	"agent/collector"
	"agent/collector/softlinefamily"
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type record struct {
	Source, Kind, URL, State, Name, SKU, At string
	Offer                                   *catalog.Offer
}
type register map[string]*record

func (r register) add(source, kind, url string) *record {
	key := source + "|" + kind + "|" + url
	if v := r[key]; v != nil {
		return v
	}
	v := &record{Source: source, Kind: kind, URL: url, State: "pending"}
	r[key] = v
	return v
}
func load(p string, v any) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}
func each[T any](p string, optional, live bool, use func(T)) error {
	f, e := os.Open(p)
	if optional && errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		raw, e := r.ReadBytes('\n')
		if errors.Is(e, io.EOF) && (len(raw) == 0 || live) {
			return nil
		}
		if e != nil && !errors.Is(e, io.EOF) {
			return e
		}
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		use(v)
		if errors.Is(e, io.EOF) {
			return nil
		}
	}
}
func (r register) competitors(root, source string) error {
	dir := filepath.Join(root, source)
	var inventory struct {
		URLs     []string
		Variants []string `json:"variant_urls"`
	}
	if err := load(filepath.Join(dir, "inventory.json"), &inventory); err != nil {
		return err
	}
	for _, v := range inventory.URLs {
		r.add(source, "семейство", v)
	}
	for _, v := range inventory.Variants {
		r.add(source, "вариант", v)
	}
	type variant struct{ URL, SKU, Name, Status string }
	type page struct {
		URL, At, State, Title, Category string
		Variants                        []variant
	}
	latest := map[string]page{}
	if err := each(filepath.Join(dir, "pages.jsonl"), true, true, func(p page) { latest[p.URL] = p }); err != nil {
		return err
	}
	for _, p := range latest {
		kind := "семейство"
		if r[source+"|вариант|"+p.URL] != nil {
			kind = "вариант"
		}
		row := r.add(source, kind, p.URL)
		row.State = p.State
		row.Name = p.Title
		row.At = p.At
		outside := source == "softmagazin" && p.Category != "software"
		if outside && p.State != "unavailable" && p.State != "error" {
			row.State = "category_pending"
			if p.Category == "hardware" {
				row.State = "out_of_scope"
			}
		} else if kind == "семейство" && row.State == "priced" {
			row.State = "family_with_prices"
		}
		for _, v := range p.Variants {
			if v.URL == "" {
				continue
			}
			item := r.add(source, "вариант", v.URL)
			item.State = v.Status
			item.SKU = v.SKU
			item.Name = v.Name
			item.At = p.At
			if outside {
				item.State = row.State
			}
		}
	}
	if source == "softmagazin" {
		type exclusion struct{ URL, At string }
		return each(filepath.Join(dir, "excluded.jsonl"), true, false, func(v exclusion) {
			row := r.add(source, "вариант", v.URL)
			row.State = "out_of_scope"
			row.At = v.At
		})
	}
	return nil
}

func (r register) softline(root string) error {
	dir := filepath.Join(root, "softline")
	cycle := "initial"
	if e := load(filepath.Join(dir, "active-generation.json"), &cycle); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if cycle == "" || cycle == ".." || cycle == "." || filepath.Base(cycle) != cycle {
		return fmt.Errorf("invalid Softline checkpoint")
	}
	var inventory struct{ Families, Products []string }
	if e := load(filepath.Join(dir, "checkpoints", cycle, "sitemap.json"), &inventory); e != nil {
		return e
	}
	if e := r.softlineFamilies(dir, cycle, inventory.Families); e != nil {
		return e
	}
	for _, u := range inventory.Products {
		r.add("softline", "вариант", u)
	}
	return each(filepath.Join(dir, "products.jsonl"), true, false, func(v softlinefamily.Product) {
		row := r.add("softline", "вариант", v.URL)
		row.State = v.State
		row.Name = v.Name
		row.SKU = v.SKU
		if v.Offer != nil {
			row.At = v.Offer.CapturedAt
		}
	})
}
func (r register) prices(root, source string) error {
	return each(filepath.Join(root, source, "offers.jsonl"), false, false, func(o catalog.Offer) {
		row := r.add(source, "вариант", o.URL)
		row.Offer = &o
		row.SKU = o.SKU
		row.Name = o.Name
		row.At = o.CapturedAt
		row.State = "priced"
	})
}
func field(s string) string {
	if s != "" && strings.ContainsAny(s[:1], "\t\r\n") {
		return "'" + s
	}
	v := strings.TrimSpace(s)
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + s
	}
	return s
}
func quantity(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}
func label(s string) string {
	switch s {
	case "category_pending":
		return "Категория ПО проверяется"
	case "out_of_scope":
		return "Оборудование, исключено"
	case "pending":
		return "В очереди"
	case "family_without_listed_variants":
		return "На странице нет вариантов лицензии"
	case "family_with_variants":
		return "Есть варианты без подтверждённой цены"
	case "family_with_prices":
		return "Есть варианты с ценой"
	case "priced":
		return "Цена опубликована"
	case "no_public_price":
		return "Нет публичной цены"
	case "not_product":
		return "Служебная страница"
	case "unavailable":
		return "Страница недоступна"
	case "error":
		return "Ошибка обработки"
	default:
		return s
	}
}
func (r register) write(path string) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".coverage-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.WriteString("\uFEFF"); e != nil {
		return e
	}
	out := csv.NewWriter(f)
	out.Comma = ';'
	out.UseCRLF = true
	if e = out.Write([]string{"Магазин", "Тип страницы", "Адрес", "Статус", "Артикул", "Название", "Цена", "Валюта", "Мин. кол-во", "Макс. кол-во", "Тип цены", "Наблюдение UTC"}); e != nil {
		return e
	}
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := r[k]
		x := []string{v.Source, v.Kind, v.URL, label(v.State), field(v.SKU), field(v.Name), "", "", "", "", "", v.At}
		if o := v.Offer; o != nil {
			x[6] = fmt.Sprintf("%d,%02d", o.PriceMinor/100, o.PriceMinor%100)
			x[7] = o.Currency
			x[8] = quantity(o.Quantity)
			x[9] = quantity(o.MaxQuantity)
			x[10] = o.PriceBasis
		}
		if e = out.Write(x); e != nil {
			return e
		}
	}
	out.Flush()
	if e = out.Error(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func run(root string) error {
	r := register{}
	if e := r.softline(root); e != nil {
		return e
	}
	for _, src := range []string{"softmap", "softmagazin"} {
		if e := r.competitors(root, src); e != nil {
			return e
		}
	}
	for _, src := range []string{"softline", "softmap", "softmagazin"} {
		if e := r.prices(root, src); e != nil {
			return e
		}
	}
	return r.write(filepath.Join(root, "coverage.csv"))
}
func main() {
	root := flag.String("catalog", ".runtime/market-catalog", "market catalog directory")
	flag.Parse()
	if e := run(*root); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
