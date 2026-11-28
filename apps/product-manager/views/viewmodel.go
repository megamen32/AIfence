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
	EvidenceLevel, Forecast, TaskURL, TaskTag, TaskSystem                                                                             string
	CompetingExplanations                                                                                                             []string
	MeasuredEvidence                                                                                                                  []Link
	Threshold, Operator, RuleLabel                                                                                                    string
	ID, Title, Product, Status, Action, Metric, Baseline, Rule, Control, Window, TaskID, Assessment, Version, SnapshotURL, BaserowURL string
	Groups                                                                                                                            []EvidenceGroup
	Unknowns                                                                                                                          []string
}
type HomeView struct {
	Monitor                                           *Monitor
	Tab, Mode, RealURL, DemoURL                       string
	Tabs                                              []Link
	Card                                              *Investigation
	Records, Snapshots, Filters                       []Link
	Detail                                            *Decision
	Signals                                           []Signal
	Rows                                              []EvidenceRow
	Warnings                                          []string
	BoardURL, SnapshotID, AsOf, RowsLabel, Prev, Next string
	ShowSources, CanInvestigate, IsReplay             bool
}

type Monitor struct {
	Catalog                                 *CatalogMap
	Search, Group, Updated, Mode, MarketURL string
	Matches, Records, Diagnostics           []Link
	ReviewCount                             int
	Products                                []MonitorProduct
	Pulse                                   []PulseItem
	Selected                                *Investigation
}

type MonitorProduct struct {
	Name, URL, Origin, AlertWeek, Freshness string
	Cells                                   [4]MonitorCell
}

type MonitorCell struct{ Label, Tone string }

type PulseItem struct {
	Product, URL, Week, Detected, Change, Comparison, Origin                                                                               string
	FirstDetected, Freshness, Weekly, WeeklyBasis, WeeklyLoss, WeeklyLossBasis, ProductKey, ControlKey, ControlName, Vendor, ControlVendor string
	Amount, Delta                                                                                                                          *float64
	Coverage                                                                                                                               []CoverageItem
}

type Investigation struct {
	ReleaseHistory                                            *ReleaseHistory
	CorrectionStatus, MeasuredBeforeWidth, MeasuredAfterWidth string

	Title, Mode, Status, Weekly, WeeklyBasis, FirstDetected                       string
	Coverage                                                                      []CoverageItem
	Magnitude                                                                     *SignalMagnitude
	Prices                                                                        *MarketPrices
	Product, BackURL, Origin, Detected, Week, Control, Scale, Cost, SnapshotURL   string
	Chart                                                                         Chart
	Demand                                                                        *DemandComparison
	Release, ReleaseDate, ReleaseURL                                              string
	Before, After, Assessment                                                     string
	Frames                                                                        []BrowserFrame
	Evidence                                                                      []EvidenceRow
	RecordURL, Orders, Margin, CostError, ProductKey, SnapshotID, CardID, AlertID string
}

type DemandComparison struct {
	Product, Control, Origin string
	Sources                  []DemandSource
}

type DemandSource struct {
	Name, Unit, Period, Status, Hint               string
	TargetBaseline, TargetCurrent, TargetChange    string
	ControlBaseline, ControlCurrent, ControlChange string
	TargetWidth, ControlWidth                      string
}

type BrowserFrame struct{ Device, Step, Verdict, Image, SourceURL, Tone string }

type Chart struct {
	Counterfactual                                                  string
	Target, Control, Fill, First, Last, Min, Max, ReleaseX, Caption string
	Dots                                                            []ChartDot
	Pins                                                            []ChartPin
}

type ChartDot struct{ X, TargetY, ControlY, Label string }
type ChartPin struct{ Kind, Title, Detail, X, Y, URL string }

type MarketPrices struct {
	SKU, Name, TaskStatus, TaskURL, SnapshotURL string
	Comparable, Separate                        []PriceGroup
}

type PriceGroup struct {
	Title, Reason string
	Comparable    bool
	Offers        []PriceOffer
}

type PriceOffer struct{ Seller, Price, Date, URL string }

type SignalMagnitude struct{ Change, Caption, Before, After, Baseline, Current string }

type CoverageItem struct {
	Name      string
	Available bool
}

type CatalogMap struct {
	Count, Date, Group, Weight, Filter string
	Partial                            bool
	VendorURL, CategoryURL             string
	OtherRemainder                     string
	OtherGroups                        []CatalogGroup
	Tiles                              []CatalogTile
}

type CatalogGroup struct {
	Name, Value string
}

type CatalogTile struct {
	Name, DisplayName, Label, Value, Tone, URL, LabelLine1, LabelLine2 string
	X, Y, W, H, LX, LY                                                 string
	ShowLabel, Focus                                                   bool
}

type ReleaseHistory struct {
	Path, First, Last, Pivot, BeforeRate, AfterRate, BeforeRange, AfterRange, BeforeCount, AfterCount string
	Grid                                                                                              []ReleaseTick
	Events                                                                                            []ReleaseMarker
	Dots                                                                                              []ReleasePoint
}
type ReleasePoint struct{ X, Y, Value, Period, Detail string }
type ReleaseTick struct{ Y, Label string }
type ReleaseMarker struct{ X, ID, Name, Date string }
