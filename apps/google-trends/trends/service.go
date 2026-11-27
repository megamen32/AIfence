package trends

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const Endpoint = "https://mcp.hasdata.com/mcp?apis=google_trends"
const UpstreamTool = "hasdata_google_trends_search_getTrendsData"
const (
	OK                  = "ok"
	CredentialsRequired = "credentials_required"
	RateLimited         = "rate_limited"
	QuotaExhausted      = "quota_exhausted"
	AccessDenied        = "access_denied"
	InvalidInput        = "invalid_input"
	UnavailablePeriod   = "unavailable_period"
	InsufficientData    = "insufficient_data"
	UpstreamError       = "upstream_error"
	InvalidResponse     = "invalid_provider_response"
)

var ErrCredentialsRequired = errors.New("hasdata credentials required")
var ErrRateLimited = errors.New("hasdata rate limited")
var ErrQuotaExhausted = errors.New("hasdata quota exhausted")
var ErrAccessDenied = errors.New("hasdata access denied")

// Client preserves the raw MCP response; Airlock owns credential injection.
type Client interface {
	Call(context.Context, map[string]any) ([]byte, error)
}
type Service struct {
	client Client
	now    func() time.Time
}

func NewService(client Client, now func() time.Time) *Service {
	if client == nil {
		panic("trends: client required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{client: client, now: now}
}
func (s *Service) Compare(ctx context.Context, q Query) (Result, error) {
	return s.run(ctx, q, "compare", "timeseries", 2, 5)
}
func (s *Service) Regional(ctx context.Context, q Query) (Result, error) {
	return s.run(ctx, q, "regions", "geoMap", 1, 5)
}
func (s *Service) Related(ctx context.Context, q Query) (Result, error) {
	return s.run(ctx, q, "related", "relatedQueries", 1, 1)
}

func (s *Service) run(ctx context.Context, q Query, mode, dataType string, minTerms, maxTerms int) (Result, error) {
	now := s.now().UTC()
	req, status, message := normalize(q, now, mode, minTerms, maxTerms)
	r := Result{Mode: mode, Request: req, Source: Source{
		Provider: "hasdata", Origin: "google_trends", CheckedAt: now.Format(time.RFC3339Nano),
		RequestFingerprint: fingerprint(req), ExploreURL: exploreURL(req), Scale: "relative_index_0_100_within_this_response",
	}}
	if mode == "related" {
		r.Source.Scale = "top_relative_index_and_rising_growth"
	}
	if status != "" {
		return finish(r, status, message), nil
	}
	args := map[string]any{"q": strings.Join(req.Terms, ","), "geo": req.Geo, "date": req.From + " " + req.To, "tz": 0, "cat": req.Category, "dataType": dataType}
	if req.Property != "web" {
		args["gprop"] = req.Property
	}
	if mode == "regions" && req.Region != "" {
		args["region"] = req.Region
	}
	// Exactly one call for every term in the comparison. No automatic retries.
	callCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	body, err := s.client.Call(callCtx, args)
	if err != nil {
		switch {
		case errors.Is(err, ErrCredentialsRequired):
			return finish(r, CredentialsRequired, statusMessage(CredentialsRequired)), nil
		case errors.Is(err, ErrRateLimited):
			return finish(r, RateLimited, statusMessage(RateLimited)), nil
		case errors.Is(err, ErrQuotaExhausted):
			return finish(r, QuotaExhausted, statusMessage(QuotaExhausted)), nil
		case errors.Is(err, ErrAccessDenied):
			return finish(r, AccessDenied, statusMessage(AccessDenied)), nil
		default:
			return finish(r, UpstreamError, "Источник не ответил. Статистика не получена."), nil
		}
	}
	payload, status := unwrapMCP(body)
	if status != "" {
		return finish(r, status, statusMessage(status)), nil
	}
	captured := s.now().UTC().Format(time.RFC3339Nano)
	r.Source.CapturedAt = captured
	if meta, ok := object(payload["requestMetadata"]); ok {
		r.Source.RequestID = text(meta["id"])
	}
	if r.Source.RequestID == "" {
		r.Source.RequestID = text(payload["requestId"])
	}
	identity := sha256.Sum256(append([]byte(r.Source.RequestFingerprint+"|"+captured+"|"+r.Source.RequestID+"|"), body...))
	r.Source.ComparisonID = hex.EncodeToString(identity[:16])
	switch mode {
	case "compare":
		r.Points, err = parseTimeline(payload, req.Terms)
		if err == nil {
			r.Summaries = summarize(r.Points, req.Terms)
		}
	case "regions":
		r.Regions, err = parseRegions(payload, req.Terms)
	case "related":
		var related Related
		related, err = parseRelated(payload)
		if err == nil {
			r.Related = &related
		}
	}
	if err != nil {
		return finish(r, InvalidResponse, statusMessage(InvalidResponse)), nil
	}
	if !hasObservations(r) {
		return finish(r, InsufficientData, "Google Trends не дал достаточно данных для выбранных фраз и периода."), nil
	}
	r.Notes = []string{"Индекс показывает относительный поисковый интерес. Это не число запросов, продажи или доля рынка."}
	if mode == "compare" {
		r.Notes = append(r.Notes, "Все фразы получены одним запросом и имеют общую шкалу. Данные разных сравнений не объединяются.", "Средние используют общие завершенные интервалы с подтвержденными данными всех фраз. Значения <1 исключены.")
	}
	if mode == "regions" {
		r.Notes = append(r.Notes, "Индекс региона не равен объему рынка или числу потенциальных клиентов.")
	}
	if mode == "related" {
		r.Notes = append(r.Notes, "Top показывает относительный индекс; Rising показывает рост в процентах или метку Breakout.")
	}
	return finish(r, OK, "Данные получены из Google Trends через HasData."), nil
}

func normalize(q Query, now time.Time, mode string, minTerms, maxTerms int) (Request, string, string) {
	r := Request{Terms: append([]string(nil), q.Terms...), Geo: strings.TrimSpace(q.Geo), From: q.From, To: q.To, Property: q.Property, Category: q.Category, Region: q.Region, TimezoneMinutes: 0}
	if r.Property == "" {
		r.Property = "web"
	}
	if r.Category == "" {
		r.Category = "0"
	}
	if len(r.Terms) < minTerms || len(r.Terms) > maxTerms {
		return r, InvalidInput, fmt.Sprintf("Для этого режима укажите от %d до %d фраз.", minTerms, maxTerms)
	}
	seen := map[string]bool{}
	for i, v := range r.Terms {
		v = strings.TrimSpace(v)
		if v == "" || utf8.RuneCountInString(v) > 200 || strings.ContainsAny(v, ",\r\n\x00") {
			return r, InvalidInput, "Фразы должны быть непустыми, до 200 символов и без запятых или переносов строк."
		}
		key := strings.ToLower(v)
		if seen[key] {
			return r, InvalidInput, "Укажите разные поисковые фразы."
		}
		seen[key] = true
		r.Terms[i] = v
	}
	if r.From == "" && r.To == "" {
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		r.From, r.To, r.PeriodDefaulted = start.AddDate(-1, 0, 0).Format(time.DateOnly), start.AddDate(0, 0, -1).Format(time.DateOnly), true
	}
	from, e1 := time.Parse(time.DateOnly, r.From)
	to, e2 := time.Parse(time.DateOnly, r.To)
	if e1 != nil || e2 != nil || from.After(to) {
		return r, InvalidInput, "Укажите начало и конец периода в формате ГГГГ-ММ-ДД."
	}
	if from.Before(time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)) || to.After(now) {
		return r, UnavailablePeriod, "Этот период недоступен: Google Trends поддерживает даты с 2004 года до текущего дня."
	}
	if r.Geo != "" && !regexp.MustCompile(`^[A-Z]{2}(?:-[A-Za-z0-9-]{1,12})?$`).MatchString(r.Geo) {
		return r, InvalidInput, "Используйте код региона Google Trends, например RU, либо пустую строку для всего мира."
	}
	if !regexp.MustCompile(`^[0-9]{1,5}$`).MatchString(r.Category) {
		return r, InvalidInput, "Категория должна быть числовым кодом Google Trends."
	}
	switch r.Property {
	case "web", "youtube", "news", "images", "froogle":
	default:
		return r, InvalidInput, "Неизвестный источник поиска."
	}
	if mode != "regions" && r.Region != "" {
		return r, InvalidInput, "Уровень региона доступен только в режиме географии."
	}
	if mode == "regions" && r.Region != "" {
		switch r.Region {
		case "country", "region", "dma", "city":
		default:
			return r, InvalidInput, "Неизвестный уровень географии."
		}
	}
	return r, "", ""
}
func finish(r Result, status, message string) Result {
	r.Status, r.Message, r.Source.Status = status, message, status
	return r
}
func statusMessage(status string) string {
	switch status {
	case CredentialsRequired:
		return "Подключите действующий API-ключ HasData. Статистика пока не получена."
	case RateLimited:
		return "HasData ограничил частоту запросов. Автоматический повтор не выполнялся."
	case QuotaExhausted:
		return "Кредиты HasData закончились. Статистика не получена."
	case AccessDenied:
		return "HasData не разрешил этот запрос."
	case InvalidResponse:
		return "Источник вернул ответ неожиданного формата. График не построен."
	default:
		return "HasData не смог выполнить запрос. Статистика не получена."
	}
}
func fingerprint(r Request) string {
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}
func exploreURL(r Request) string {
	q := url.Values{"q": {strings.Join(r.Terms, ",")}, "date": {r.From + " " + r.To}, "geo": {r.Geo}, "hl": {"ru"}}
	if r.Property != "" && r.Property != "web" {
		q.Set("gprop", r.Property)
	}
	if r.Category != "" && r.Category != "0" {
		q.Set("cat", r.Category)
	}
	return "https://trends.google.com/trends/explore?" + q.Encode()
}
