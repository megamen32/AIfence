package views

import (
	"encoding/json"
	"strconv"
)

type Form struct {
	Mode, Geo, GeoLabel, From, To, Property, Category, Region, Today, PeriodPreset string
	Terms                                                                          []string
	RelatedTerm                                                                    int
}
type Cell struct {
	Label, Detail string
	Index         *float64
}
type Row struct {
	Label, Detail string
	Cells         []Cell
}
type Metric struct {
	Term, Value, Coverage string
	Index                 int
}
type ChartSeries struct {
	Name     string
	Index    int
	Segments []string
}
type ChartValue struct {
	Term    string   `json:"term"`
	Label   string   `json:"label"`
	Detail  string   `json:"detail,omitempty"`
	Index   *float64 `json:"index"`
	State   string   `json:"state"`
	HasData *bool    `json:"hasData"`
}
type ChartPoint struct {
	Date      string       `json:"date"`
	RawDate   string       `json:"rawDate"`
	Timestamp int64        `json:"timestamp"`
	Partial   *bool        `json:"partial"`
	Quality   string       `json:"quality"`
	Values    []ChartValue `json:"values"`
}
type ChartData struct {
	Terms  []string     `json:"terms"`
	Points []ChartPoint `json:"points"`
}
type Snapshot struct {
	Mode     string   `json:"mode"`
	Terms    []string `json:"terms"`
	Geo      string   `json:"geo"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Property string   `json:"property"`
	Category string   `json:"category"`
	Region   string   `json:"region"`
}
type RankedRow struct {
	Label string
	Cells []Cell
}
type RelatedItem struct {
	Query, Value, Metric string
	Index                *float64
}
type ResultModel struct {
	Status, Message, Mode, Title, Period, Geo, Property, Category, Checked, ExploreURL string
	RequestJSON, ChartJSON, MetadataJSON                                               string
	Terms                                                                              []string
	Metrics                                                                            []Metric
	Rows                                                                               []Row
	Series                                                                             []ChartSeries
	Regions                                                                            []RankedRow
	Popular, Rising                                                                    []RelatedItem
	CommonPoints, TotalPoints, ExcludedPoints                                          int
	Notes                                                                              []string
}

func Number(n int) string           { return strconv.Itoa(n) }
func JSON(v any) string             { b, _ := json.Marshal(v); return string(b) }
func (r ResultModel) HasData() bool { return r.Status == "ok" }
func (r ResultModel) IsError() bool {
	return r.Status != "" && r.Status != "ok" && r.Status != "insufficient_data"
}

func ScoreValue(v *float64) string {
	if v == nil {
		return "0"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}
