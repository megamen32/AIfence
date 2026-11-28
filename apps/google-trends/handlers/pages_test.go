package handlers

import (
	"agent/trends"
	"agent/views"
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestViewKeepsMissingZeroAndUnknownDistinct(t *testing.T) {
	zero := 0.0
	yes, no := true, false
	a := scoreCell(trends.Score{Raw: "0", Index: &zero, HasData: &yes, State: "observed"})
	b := scoreCell(trends.Score{Raw: "0", HasData: &no, State: "missing"})
	c := scoreCell(trends.Score{Raw: "0", Index: &zero, State: "reported"})
	if a.Label != "0" || b.Label != "Источник не сообщил значение" || c.Label != "0" || c.Detail == "" {
		t.Fatalf("%#v %#v %#v", a, b, c)
	}
}
func TestChartBreaksAtMissingAndPartialValues(t *testing.T) {
	yes, no := true, false
	zero, ten := 0.0, 10.0
	points := []trends.Point{
		{IsPartial: &no, Values: []trends.Score{{Index: &zero, HasData: &yes}}},
		{IsPartial: &no, Values: []trends.Score{{HasData: &no}}},
		{IsPartial: &no, Values: []trends.Score{{Index: &ten, HasData: &yes}}},
		{IsPartial: &yes, Values: []trends.Score{{Index: &ten, HasData: &yes}}},
	}
	series := chartSeries(points, []string{"Kaspersky Endpoint Security"})
	if len(series) != 1 || len(series[0].Segments) != 2 || !strings.Contains(series[0].Segments[0], ",300.00") {
		t.Fatalf("%#v", series)
	}
}
func TestResultTemplateEscapesProviderText(t *testing.T) {
	var buf bytes.Buffer
	m := views.ResultModel{Message: "<script>alert(1)</script>", Rows: []views.Row{{Label: "<script>bad</script>", Cells: []views.Cell{{Label: "0"}}}}, Terms: []string{"Astra Linux"}}
	if err := views.Results(m).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<script>") || !strings.Contains(buf.String(), "&lt;script&gt;") {
		t.Fatal("provider text was not escaped")
	}
}

func TestDeepLinkFormParsesComparison(t *testing.T) {
	now := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	form, ok := deepLinkForm(url.Values{"q": {"Kaspersky Endpoint Security,Astra Linux"}, "geo": {"RU"}, "from": {"2026-08-01"}, "to": {"2026-10-31"}, "view": {"compare"}}, now)
	if !ok || form.Mode != "compare" || len(form.Terms) != 2 || form.Geo != "RU" || form.From != "2026-08-01" || form.To != "2026-10-31" || form.PeriodPreset != "custom" {
		t.Fatalf("unexpected deep-link form: %+v, ok=%v", form, ok)
	}
}

func TestInvalidDeepLinkFallsBackToNormalForm(t *testing.T) {
	now := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	form, ok := deepLinkForm(url.Values{"q": {"Kaspersky Endpoint Security,Astra Linux"}, "geo": {"bad"}, "view": {"compare"}}, now)
	if ok || form.Mode != "compare" || len(form.Terms) != 2 {
		t.Fatalf("invalid deep link was accepted: %+v, ok=%v", form, ok)
	}
}
