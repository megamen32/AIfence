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
	Overview                                                 *Overview
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

// Overview is the block above the catalog search: status tiles, offers per store and the week's price moves.
type Overview struct {
	Tiles       []Tile
	Bars        []Bar
	BarsHeight  int
	Changes     []ChangeRow
	ShowChanges bool
	Source      string
}

type Tile struct {
	Label, Value, Note, Tone string
}

type Bar struct {
	Label, Count, Y, Width, CountX, TextY string
	Delay                                 string
}

type ChangeRow struct {
	Name, SKU, Store, Old, New, Percent, Tone, URL string
}
