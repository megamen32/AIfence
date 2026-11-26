package views

type Option struct {
	Value, Label string
}

type SourceRow struct {
	ID, Name, Count, Last                                                                           string
	CoverageAt, Expected, Priced, NoPrice, Unavailable, Unresolved, Excluded, Pending, CoverageNote string
}

type Row struct {
	SKU, Name, Store, Price, Basis, Quantity, At, URL   string
	CompareURL, HistoryURL, Previous, Delta, PreviousAt string
}

type Group struct {
	Rows                []Row
	Label, Note, Lowest string
}

type Page struct {
	Tab, Query, Source, License, From, To                    string
	Error, Notice, PrevURL, NextURL, Range, Total, Freshness string
	CatalogURL, CompareURL, ChangesURL, SourcesURL           string
	Sources                                                  []SourceRow
	Licenses                                                 []Option
	Rows                                                     []Row
	Groups                                                   []Group
}
