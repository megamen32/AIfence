package demand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/airlockrun/agentsdk"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const DocumentationURL = "https://aistudio.yandex.ru/ru/docs/search-api/concepts/wordstat"
const AvailableThrough = "2026-10-07"
const LastCompleteMonth = "2026-09"

type Caller interface {
	CallTool(context.Context, string, any) (*agentsdk.MCPToolCallResponse, error)
}
type Service struct {
	mcp          Caller
	regionsCache regionCache
}

func New(mcp Caller) *Service {
	if mcp == nil {
		panic("demand: MCP client required")
	}
	return &Service{mcp: mcp}
}

type TopInput struct {
	Phrase    string   `json:"phrase" jsonschema:"description=Exact search phrase including Wordstat operators"`
	RegionIDs []int    `json:"region_ids,omitempty" jsonschema:"description=Yandex region IDs. 225 is Russia. Omitted means no regional filter"`
	Devices   []string `json:"devices,omitempty" jsonschema:"description=Optional devices: all, desktop, phone, tablet"`
	Limit     int      `json:"limit,omitempty" jsonschema:"minimum=1,maximum=200,description=Number of phrases, default 20"`
}
type DynamicsInput struct {
	Phrase    string   `json:"phrase"`
	FromMonth string   `json:"from_month" jsonschema:"description=First requested month YYYY-MM"`
	ToMonth   string   `json:"to_month" jsonschema:"description=Last requested month YYYY-MM, inclusive"`
	RegionIDs []int    `json:"region_ids,omitempty"`
	Devices   []string `json:"devices,omitempty"`
}
type RegionsInput struct {
	Phrase     string `json:"phrase"`
	RegionMode string `json:"region_mode,omitempty" jsonschema:"description=all, cities or regions. Default regions"`
}
type Period struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Granularity string `json:"granularity"`
}
type Source struct {
	Name                 string `json:"name"`
	Documentation        string `json:"documentation_url"`
	DataAvailableThrough string `json:"data_available_through"`
	LastCompleteMonth    string `json:"last_complete_month"`
	CheckedAt            string `json:"checked_at"`
	WindowKnown          bool   `json:"window_known"`
	WindowNote           string `json:"window_note"`
}
type Row struct {
	Group            string `json:"group,omitempty"`
	Label            string `json:"label"`
	Count            *int64 `json:"count,omitempty"`
	RegionID         string `json:"region_id,omitempty"`
	RegionName       string `json:"region_name,omitempty"`
	RegionNameStatus string `json:"region_name_status,omitempty"`
}
type Result struct {
	RegionLookupStatus  string         `json:"region_lookup_status,omitempty"`
	RegionLookupMessage string         `json:"region_lookup_message,omitempty"`
	Status              string         `json:"status"`
	SourceStatus        string         `json:"source_status"`
	Message             string         `json:"message"`
	Kind                string         `json:"kind"`
	Phrase              string         `json:"phrase"`
	RequestedPeriod     *Period        `json:"requested_period,omitempty"`
	RegionIDs           []int          `json:"region_ids,omitempty"`
	Devices             []string       `json:"devices,omitempty"`
	RegionMode          string         `json:"region_mode,omitempty"`
	Source              Source         `json:"source"`
	Rows                []Row          `json:"rows,omitempty"`
	Data                map[string]any `json:"data,omitempty"`
}

func baseResult(kind, phrase string) Result {
	return Result{Status: "pending", SourceStatus: "historical_only", Kind: kind, Phrase: strings.TrimSpace(phrase), Source: Source{Name: "Яндекс Wordstat / Yandex Cloud Search API", Documentation: DocumentationURL, DataAvailableThrough: AvailableThrough, LastCompleteMonth: LastCompleteMonth, CheckedAt: time.Now().UTC().Format(time.RFC3339), WindowNote: "Точное окно top/regions API не сообщает. Эти данные нельзя называть статистикой за последние 30 дней от текущей даты."}}
}
func authorize(ctx context.Context) error {
	if _, ok := agentsdk.CallerFromContext(ctx).User(); !ok {
		return errors.New("Для Wordstat требуется вход в Airlock и доступ к приложению.")
	}
	return nil
}
func validatePhrase(s string) error {
	if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 400 {
		return errors.New("Введите фразу длиной от 1 до 400 символов.")
	}
	return nil
}
func validateFilters(regions []int, devices []string) error {
	if len(regions) > 30 {
		return errors.New("Не более 30 регионов за запрос.")
	}
	for _, id := range regions {
		if id < 1 {
			return errors.New("Коды регионов должны быть положительными числами.")
		}
	}
	if len(devices) > 4 {
		return errors.New("Недопустимый список устройств.")
	}
	for _, device := range devices {
		if device != "all" && device != "desktop" && device != "phone" && device != "tablet" {
			return errors.New("Устройство: all, desktop, phone или tablet.")
		}
	}
	if len(devices) > 1 {
		for _, d := range devices {
			if d == "all" {
				return errors.New("Устройство all нельзя сочетать с отдельными типами устройств.")
			}
		}
	}
	return nil
}
func invalid(out Result, err error) (Result, error) {
	out.Status = "invalid_request"
	out.Message = err.Error()
	return finish(out)
}

func (s *Service) Top(ctx context.Context, in TopInput) (Result, error) {
	if err := authorize(ctx); err != nil {
		return Result{}, err
	}
	return s.top(ctx, in)
}
func (s *Service) top(ctx context.Context, in TopInput) (Result, error) {
	out := baseResult("top", in.Phrase)
	out.RegionIDs = in.RegionIDs
	out.Devices = in.Devices
	if err := validatePhrase(in.Phrase); err != nil {
		return invalid(out, err)
	}
	if err := validateFilters(in.RegionIDs, in.Devices); err != nil {
		return invalid(out, err)
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Limit < 1 || in.Limit > 200 {
		return invalid(out, errors.New("Количество фраз: от 1 до 200."))
	}
	args := map[string]any{"phrase": out.Phrase, "numPhrases": in.Limit}
	if len(in.RegionIDs) > 0 {
		args["regionIds"] = in.RegionIDs
	}
	if len(in.Devices) > 0 {
		args["devices"] = in.Devices
	}
	return s.run(ctx, "top_requests", args, out)
}
func (s *Service) Dynamics(ctx context.Context, in DynamicsInput) (Result, error) {
	if err := authorize(ctx); err != nil {
		return Result{}, err
	}
	return s.dynamics(ctx, in)
}
func (s *Service) dynamics(ctx context.Context, in DynamicsInput) (Result, error) {
	out := baseResult("dynamics", in.Phrase)
	out.RegionIDs = in.RegionIDs
	out.Devices = in.Devices
	out.RequestedPeriod = &Period{From: in.FromMonth, To: in.ToMonth, Granularity: "monthly"}
	if strings.ContainsAny(in.Phrase, "!\"[]()|") || strings.HasPrefix(strings.TrimSpace(in.Phrase), "-") || strings.Contains(in.Phrase, " -") || strings.Contains(in.Phrase, "\t-") {
		return invalid(out, errors.New("Помесячная динамика Wordstat поддерживает только оператор +. Уберите другие операторы самостоятельно; фраза не изменена."))
	}
	if err := validatePhrase(in.Phrase); err != nil {
		return invalid(out, err)
	}
	if err := validateFilters(in.RegionIDs, in.Devices); err != nil {
		return invalid(out, err)
	}
	start, e1 := time.Parse("2006-01", in.FromMonth)
	end, e2 := time.Parse("2006-01", in.ToMonth)
	if e1 != nil || e2 != nil || start.After(end) {
		return invalid(out, errors.New("Укажите месяцы YYYY-MM, начало не позже конца."))
	}
	if end.Sub(start) > 4*366*24*time.Hour {
		return invalid(out, errors.New("Укажите период не длиннее четырёх лет."))
	}
	out.Source.WindowKnown = true
	out.Source.WindowNote = "Запрошены полные календарные месяцы. Фактически возвращённые периоды сохранены в исходном ответе API."
	if in.ToMonth > LastCompleteMonth {
		out.Status = "unavailable_period"
		out.SourceStatus = "unavailable_period"
		out.Message = fmt.Sprintf("Период %s–%s недоступен в API Wordstat. После 07.10.2026 статистика не обновляется; последний полный месяц: сентябрь 2026. Запрошенный период сохранён без замены.", in.FromMonth, in.ToMonth)
		return finish(out)
	}
	args := map[string]any{"phrase": out.Phrase, "period": "monthly", "fromDate": start.Format(time.RFC3339), "toDate": end.AddDate(0, 1, 0).Add(-time.Second).Format(time.RFC3339)}
	if len(in.RegionIDs) > 0 {
		args["regionIds"] = in.RegionIDs
	}
	if len(in.Devices) > 0 {
		args["devices"] = in.Devices
	}
	return s.run(ctx, "dynamics", args, out)
}
func (s *Service) Regions(ctx context.Context, in RegionsInput) (Result, error) {
	if err := authorize(ctx); err != nil {
		return Result{}, err
	}
	return s.regions(ctx, in)
}
func (s *Service) regions(ctx context.Context, in RegionsInput) (Result, error) {
	out := baseResult("regions", in.Phrase)
	if err := validatePhrase(in.Phrase); err != nil {
		return invalid(out, err)
	}
	if in.RegionMode == "" {
		in.RegionMode = "regions"
	}
	out.RegionMode = in.RegionMode
	if in.RegionMode != "all" && in.RegionMode != "regions" && in.RegionMode != "cities" {
		return invalid(out, errors.New("География: all, regions или cities."))
	}
	// One deadline covers statistics plus the optional dictionary request.
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	out, err := s.run(ctx, "regions", map[string]any{"phrase": out.Phrase, "regionMode": in.RegionMode}, out)
	if err != nil || out.Status != "ok" {
		return out, err
	}
	return s.enrichRegions(ctx, out), nil
}
func (s *Service) run(ctx context.Context, name string, args any, out Result) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	response, err := s.mcp.CallTool(ctx, name, args)
	if err != nil {
		out.Status = "connection_error"
		out.Message = "Не удалось связаться с источником Wordstat. Статистика не получена; повторите запрос после восстановления подключения."
		if _, ok := agentsdk.IsAuthRequired(err); ok {
			out.Status = "authorization_required"
			out.Message = "Для запроса требуется настроить подключение Wordstat в Airlock."
		}
		return finish(out)
	}
	if response == nil {
		out.Status = "upstream_error"
		out.Message = "Источник вернул пустой ответ. Статистика не получена."
		return finish(out)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return out, err
	}
	var envelope struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return out, err
	}
	var parts []string
	for _, c := range envelope.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if envelope.IsError {
		out.Status = "upstream_error"
		out.Message = "Wordstat отклонил запрос. Статистика не получена. Проверьте доступ и параметры."
		if strings.Contains(text, "WORDSTAT_API_KEY") || strings.Contains(text, "WORDSTAT_FOLDER_ID") {
			out.Status = "credentials_required"
			out.Message = "Нужен доступ к Wordstat в Yandex Cloud. Администратору необходимо добавить API-ключ и ID каталога. Статистика пока не получена."
		}
		return finish(out)
	}
	payload := envelope.Structured
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage(text)
	}
	if !json.Valid(payload) {
		out.Status = "upstream_error"
		out.Message = "Источник не вернул структурированную статистику. Частотность не определена."
		return finish(out)
	}
	var data map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if decoder.Decode(&data) != nil || data == nil {
		out.Status = "upstream_error"
		out.Message = "Источник не вернул JSON-объект статистики. Частотность не определена."
		return finish(out)
	}
	out.Status = "ok"
	out.Message = "Получен исторический ответ Wordstat. Частотность отражает поисковые запросы, не число покупателей или заказов."
	out.Data = data
	out.Rows = rows(payload)
	return finish(out)
}

func rows(raw json.RawMessage) []Row {
	var value any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return nil
	}
	var result []Row
	var visit func(any, string)
	visit = func(v any, group string) {
		switch x := v.(type) {
		case []any:
			for _, child := range x {
				visit(child, group)
			}
		case map[string]any:
			label := ""
			for _, k := range []string{"phrase", "query", "date", "name", "regionName", "regionId", "region"} {
				if v, ok := x[k]; ok {
					label = fmt.Sprint(v)
					break
				}
			}
			var count *int64
			for _, k := range []string{"count", "queryCount", "requestCount", "requests", "shows"} {
				if v, ok := x[k]; ok {
					if n, e := strconv.ParseInt(fmt.Sprint(v), 10, 64); e == nil {
						count = &n
						break
					}
				}
			}
			if label != "" && count != nil {
				row := Row{Group: group, Label: label, Count: count}
				for _, key := range []string{"region", "regionId"} {
					if value, exists := x[key]; exists && value != nil {
						row.RegionID = fmt.Sprint(value)
						break
					}
				}
				result = append(result, row)
			}
			for _, k := range []string{"topRequests", "associations", "dynamics", "regions", "results", "items", "data", "result"} {
				if child, ok := x[k]; ok {
					visit(child, k)
				}
			}
		}
	}
	visit(value, "")
	return result
}

func finish(out Result) (Result, error) {
	if out.Status != "ok" {
		out.SourceStatus = out.Status
	}
	return out, nil
}
