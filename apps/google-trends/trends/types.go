package trends

type Query struct {
	Terms    []string `json:"terms" jsonschema:"description=Public product names or search terms; compare 2–5, related queries one."`
	Geo      string   `json:"geo" jsonschema:"description=Google Trends location code; empty means worldwide."`
	From     string   `json:"from,omitempty" jsonschema:"description=Inclusive YYYY-MM-DD start; omit both dates for the last 12 complete months."`
	To       string   `json:"to,omitempty" jsonschema:"description=Inclusive YYYY-MM-DD end."`
	Property string   `json:"property,omitempty" jsonschema:"description=web (default), youtube, news, images or froogle."`
	Category string   `json:"category,omitempty" jsonschema:"description=Numeric Google Trends category, default 0."`
	Region   string   `json:"region,omitempty" jsonschema:"description=Geography only: country, region, dma or city."`
}
type Request struct {
	Terms           []string `json:"terms"`
	Geo             string   `json:"geo"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	Property        string   `json:"property"`
	Category        string   `json:"category"`
	Region          string   `json:"region,omitempty"`
	TimezoneMinutes int      `json:"timezone_minutes"`
	PeriodDefaulted bool     `json:"period_defaulted"`
}
type Source struct {
	Provider           string `json:"provider"`
	Origin             string `json:"origin"`
	Status             string `json:"status"`
	CheckedAt          string `json:"checked_at"`
	CapturedAt         string `json:"captured_at,omitempty"`
	RequestID          string `json:"request_id,omitempty"`
	RequestFingerprint string `json:"request_fingerprint"`
	ComparisonID       string `json:"comparison_id,omitempty"`
	ExploreURL         string `json:"explore_url"`
	Scale              string `json:"scale"`
}
type Score struct {
	Term    string   `json:"term"`
	Raw     string   `json:"raw"`
	Index   *float64 `json:"index"`
	HasData *bool    `json:"has_data"`
	State   string   `json:"state"`
}
type Point struct {
	Timestamp int64   `json:"timestamp"`
	Date      string  `json:"date"`
	IsPartial *bool   `json:"is_partial"`
	Values    []Score `json:"values"`
}
type Summary struct {
	Term                 string   `json:"term"`
	MeanIndex            *float64 `json:"mean_index"`
	CommonCompletePoints int      `json:"common_complete_points"`
	ExcludedPoints       int      `json:"excluded_points"`
	Reason               string   `json:"reason,omitempty"`
}
type RegionRow struct {
	Code   string  `json:"code"`
	Name   string  `json:"name"`
	Values []Score `json:"values"`
}
type RelatedRow struct {
	Query  string   `json:"query"`
	Raw    string   `json:"raw"`
	Metric string   `json:"metric"`
	Value  *float64 `json:"value"`
}
type Related struct {
	Top    []RelatedRow `json:"top"`
	Rising []RelatedRow `json:"rising"`
}
type Result struct {
	Status    string      `json:"status"`
	Message   string      `json:"message"`
	Mode      string      `json:"mode"`
	Request   Request     `json:"request"`
	Source    Source      `json:"source"`
	Points    []Point     `json:"points,omitempty"`
	Summaries []Summary   `json:"summaries,omitempty"`
	Regions   []RegionRow `json:"regions,omitempty"`
	Related   *Related    `json:"related,omitempty"`
	Notes     []string    `json:"notes,omitempty"`
}
