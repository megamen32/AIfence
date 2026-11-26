package views

type Option struct {
	Value, Label string
	Active       bool
}

type SourceRow struct {
	ID, Name, Count, Last                                                                           string
	CoverageAt, Expected, Priced, NoPrice, Unavailable, Unresolved, Excluded, Pending, CoverageNote string
}

type Row struct {
	SKU, Name, Store, Price, Basis, Quantity, At, URL   string
	CompareURL, HistoryURL, Previous, Delta, PreviousAt string
	Difference, ShortCondition, SourceID                string
	Best                                                bool
}

type Group struct {
	Rows                []Row
	Label, Note, Lowest string
	Comparable          bool
}

type Page struct {
	Tab, Query, Source, License, From, To                    string
	Error, Notice, PrevURL, NextURL, Range, Total, Freshness string
	CatalogURL, CompareURL, ChangesURL, SourcesURL           string
	BaserowURL, ProductName, Quantity, HistoryNotice         string
	Periods                                                  []Option
	Charts                                                   []Chart
	Tiers                                                    []TierSchedule
	HasQuantity                                              bool
	Sources                                                  []SourceRow
	Licenses                                                 []Option
	Rows                                                     []Row
	Groups                                                   []Group
}

// Chart contains observations with the same price basis and matching license conditions.
type Chart struct {
	Label, Series string
	Separate      bool
}

type TierSchedule struct {
	Seller, Title, Path string
	Steps               []TierStep
}
type TierStep struct {
	Range, Amount, URL string
	Active             bool
}
