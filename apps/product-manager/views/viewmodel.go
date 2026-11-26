package views

type Link struct {
	Label, URL, Detail string
	Active             bool
}
type EvidenceRow struct {
	Related                                                                                          []string
	ID, Product, Metric, Value, Source, Date, SourceName, Locator, SourceURL, OpenURL, Note, Summary string
	Missing                                                                                          bool
}
type Signal struct{ Product, Change, Before, After, Period, URL string }
type EvidenceGroup struct {
	Title string
	Rows  []EvidenceRow
}
type Decision struct {
	EvidenceLevel, Forecast, TaskURL, TaskTag                                                                             string
	CompetingExplanations                                                                                                 []string
	MeasuredEvidence                                                                                                      []Link
	Threshold, Operator, RuleLabel                                                                                        string
	ID, Title, Product, Status, Action, Metric, Baseline, Rule, Control, Window, TaskID, Assessment, Version, SnapshotURL string
	Groups                                                                                                                []EvidenceGroup
	Unknowns                                                                                                              []string
}
type HomeView struct {
	Monitor                                           *Monitor
	Records, Snapshots, Filters                       []Link
	Detail                                            *Decision
	Signals                                           []Signal
	Rows                                              []EvidenceRow
	Warnings                                          []string
	BoardURL, SnapshotID, AsOf, RowsLabel, Prev, Next string
	ShowSources, CanInvestigate, IsReplay             bool
}

type Monitor struct {
	Products []MonitorProduct
	Pulse    []PulseItem
	Selected *Investigation
}

type MonitorProduct struct {
	Name, URL, Origin string
	Cells             [4]MonitorCell
}

type MonitorCell struct{ Label, Tone string }

type PulseItem struct {
	Product, URL, Week, Detected, Change, Origin string
}

type Investigation struct {
	Product, BackURL, Origin, Detected, Week, Control, Scale, Cost, SnapshotURL string
	Chart                                                                       Chart
	Release, ReleaseDate, ReleaseURL                                            string
	Before, After, Assessment                                                   string
	Frames                                                                      []BrowserFrame
	Evidence                                                                    []EvidenceRow
	RecordURL, Orders, Margin, CostError, ProductKey, SnapshotID                string
}

type BrowserFrame struct{ Device, Step, Verdict, Image, SourceURL string }

type Chart struct {
	Target, Control, Fill, First, Last, Min, Max, ReleaseX string
	Dots                                                   []ChartDot
}

type ChartDot struct{ X, TargetY, ControlY, Label string }
