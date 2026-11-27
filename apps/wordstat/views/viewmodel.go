package views

import (
	"encoding/json"
	"strconv"
)

type Form struct {
	Phrase, Kind, RegionIDs, RegionLabel, Device, RegionMode string
	FromMonth, ToMonth, PeriodPreset                         string
}
type HomeView struct {
	Form    Form
	Presets []string
	Result  *ResultView
}
type Row struct {
	Group                      string
	Label                      string
	Count                      string
	RawCount                   *int64
	RegionID, RegionNameStatus string
	Detail                     string
}
type Metric struct{ Label, Value, Detail string }
type RankedRow struct {
	Label, Count, BarValue, Detail string
	RawCount                       *int64
}
type ChartPoint struct {
	Month   string   `json:"month"`
	Label   string   `json:"label"`
	RawDate string   `json:"rawDate,omitempty"`
	Count   string   `json:"count"`
	Y       *float64 `json:"y"`
	State   string   `json:"state"`
}
type ChartDot struct{ Left, Top string }
type Chart struct {
	Phrase string       `json:"phrase"`
	Points []ChartPoint `json:"points"`
}
type Snapshot struct {
	Kind       string `json:"kind"`
	Phrase     string `json:"phrase"`
	RegionIDs  []int  `json:"region_ids,omitempty"`
	Device     string `json:"device,omitempty"`
	FromMonth  string `json:"from_month,omitempty"`
	ToMonth    string `json:"to_month,omitempty"`
	RegionMode string `json:"region_mode,omitempty"`
}
type ResultView struct {
	Status, SourceStatus, Message, Kind, Phrase string
	Period, WindowNote, SourceURL, CheckedAt    string
	Context, RegionLookupMessage                string
	RequestJSON, ChartJSON, Raw, Metadata       string
	Total, TotalDetail                          string
	HasTotal                                    bool
	Main, Associated, Regions                   []RankedRow
	MainMax, AssociatedMax, RegionMax           string
	Rows                                        []Row
	Metrics                                     []Metric
	Segments                                    []string
	Dots                                        []ChartDot
	Axis                                        []string
	PointCount, ObservedCount                   int
	HasChart                                    bool
}

func JSON(v any) string            { b, _ := json.Marshal(v); return string(b) }
func Number(v int) string          { return strconv.Itoa(v) }
func (v ResultView) IsError() bool { return v.Status != "ok" }
