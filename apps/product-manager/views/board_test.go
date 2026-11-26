package views

import (
	"bytes"
	"strings"
	"testing"
)

func TestCardsAndSnapshotsRenderActionsAndEvidence(t *testing.T) {
	row := EvidenceRow{ID: "synthetic", Product: "Synthetic", Metric: "Заказы", Value: "10,0 %", Source: "Продажи", SourceName: "sample.xlsx", Locator: "Sales!B2:B3", OpenURL: "./?obs=synthetic"}
	cases := []struct {
		name   string
		v      HomeView
		want   []string
		absent []string
	}{
		{"snapshot", HomeView{ShowSources: true, CanInvestigate: true, Rows: []EvidenceRow{row}, Warnings: []string{"fixture warning"}}, []string{"./investigate", "Sales!B2:B3", "10,0 %", "fixture warning"}, []string{"./approve"}},
		{"proposed", HomeView{Detail: &Decision{Title: "Сверить изменение", ID: "test-hyp", Version: "3", Groups: []EvidenceGroup{{Title: "Сигнал", Rows: []EvidenceRow{row}}}}}, []string{"./approve", `name="confirmed" value="yes"`, "Создать эту задачу", "Sales!B2:B3", "Сверить изменение"}, []string{"./investigate"}},
		{"created", HomeView{Detail: &Decision{Title: "Уже в бэклоге", TaskID: "PM-test", TaskURL: "./?id=test-hyp", TaskTag: "сгенерирована ИИ-помощником"}}, []string{"PM-test", "сгенерирована ИИ-помощником"}, []string{"./approve"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := Index(c.v).Render(t.Context(), &b); err != nil {
				t.Fatal(err)
			}
			s := b.String()
			for _, expected := range c.want {
				if !strings.Contains(s, expected) {
					t.Fatalf("missing %q", expected)
				}
			}
			for _, bad := range append(c.absent, "@lucide.", "@ActionIcon", "WelcomeWord", "for _,", "for _,row") {
				if strings.Contains(s, bad) {
					t.Fatalf("leaked or unexpected %q", bad)
				}
			}
		})
	}
}

func TestMonitorOverviewAndProductPanel(t *testing.T) {
	v := HomeView{BoardURL: "./board?", Monitor: &Monitor{
		Products: []MonitorProduct{{Name: "Учебный продукт", URL: "./?monitor=demo", Origin: "Учебные данные",
			Cells: [4]MonitorCell{{Label: "Тревога", Tone: "alarm"}}}},
		Pulse: []PulseItem{{Product: "Учебный продукт", Detected: "Время обнаружения неизвестно", URL: "./?monitor=demo"}},
	}}
	render := func(v HomeView) string {
		t.Helper()
		var b bytes.Buffer
		if err := Index(v).Render(t.Context(), &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	overview := render(v)
	for _, want := range []string{"Продукты по каналам и устройствам", "./?monitor=demo", "Время обнаружения неизвестно", "product-decisions"} {
		if !strings.Contains(overview, want) {
			t.Fatal(want)
		}
	}
	v.Monitor.Selected = &Investigation{Product: "Учебный продукт", Origin: "Учебные данные", Scale: "Не доказано",
		Chart: Chart{Target: "30,30 570,170", Control: "30,30 570,35", Fill: "30,30 570,170 570,35 30,30", ReleaseX: "300"}}
	product := render(v)
	for _, want := range []string{"trend-fill", "trend-release", "Учебные данные", "Задать допущения", "До и после релиза"} {
		if !strings.Contains(product, want) {
			t.Fatal(want)
		}
	}
	if strings.Contains(product, "₽ / неделю") {
		t.Fatal("showed economics without inputs")
	}
}

func TestSavedCardPutsVisualDetailsBeforeNormalActions(t *testing.T) {
	v := HomeView{
		Card: &Investigation{CardID: "saved", Product: "Учебный продукт",
			Chart: Chart{Caption: "Средние индексы", Target: "30,30 570,170"}},
		Detail: &Decision{ID: "saved", Title: "Сохранённая гипотеза", Version: "1"},
	}
	var b bytes.Buffer
	if err := Index(v).Render(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	chart, action := strings.Index(html, "trend-target"), strings.Index(html, "./approve")
	if chart < 0 || action < chart || !strings.Contains(html, `href="#hypothesis-actions"`) {
		t.Fatal("the saved-card path must expose graph and approval together")
	}
	if strings.Contains(html, `class="trend-control"`) {
		t.Fatal("plotted a control with no observations")
	}
}
