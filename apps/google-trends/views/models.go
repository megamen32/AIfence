package views

type Form struct{ Mode, Terms, Geo, From, To, Property, Category, Region string }
type Cell struct{ Label, Detail string }
type Row struct {
	Label, Detail string
	Cells         []Cell
}
type Metric struct{ Term, Value, Coverage string }
type ChartSeries struct {
	Name, Class string
	Segments    []string
}
type ResultModel struct {
	Status, Message, Mode, Period, Geo, Property, Category, Checked, ExploreURL string
	Terms                                                                       []string
	Metrics                                                                     []Metric
	Rows                                                                        []Row
	Series                                                                      []ChartSeries
	FirstInterval, LastInterval                                                 string
	Notes                                                                       []string
}
