package views

type Link struct {
	Label, URL, Detail string
	Active             bool
}
type EvidenceRow struct {
	ID, Product, Metric, Value, Source, Date, SourceName, Locator, SourceURL, OpenURL, Note, Summary string
	Missing                                                                                          bool
}
type Signal struct{ Product, Change, Before, After, Period, URL string }
type EvidenceGroup struct {
	Title string
	Rows  []EvidenceRow
}
type Decision struct {
	Threshold, Operator, RuleLabel                                                                                        string
	ID, Title, Product, Status, Action, Metric, Baseline, Rule, Control, Window, TaskID, Assessment, Version, SnapshotURL string
	Groups                                                                                                                []EvidenceGroup
	Unknowns                                                                                                              []string
}
type HomeView struct {
	Records, Snapshots, Filters                       []Link
	Detail                                            *Decision
	Signals                                           []Signal
	Rows                                              []EvidenceRow
	Warnings                                          []string
	BoardURL, SnapshotID, AsOf, RowsLabel, Prev, Next string
	ShowSources, CanInvestigate                       bool
}
