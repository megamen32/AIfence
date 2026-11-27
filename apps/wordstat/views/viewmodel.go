package views

type HomeView struct{ Presets []string }
type Row struct {
	Group string
	Label string
	Count string
}
type ResultView struct {
	Status       string
	SourceStatus string
	Message      string
	Phrase       string
	Period       string
	WindowNote   string
	SourceURL    string
	CheckedAt    string
	Raw          string
	Rows         []Row
}
