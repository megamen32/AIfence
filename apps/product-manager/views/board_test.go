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
		{"proposed", HomeView{Detail: &Decision{Title: "Сверить изменение", ID: "test-hyp", Version: "3", Groups: []EvidenceGroup{{Title: "Сигнал", Rows: []EvidenceRow{row}}}}}, []string{"./approve", `name="confirmed" value="yes"`, "Создать эту задачу", "./delivery/redmine", "Добавить в Redmine", "./delivery/dion/card", "Отправить в Dion", "Sales!B2:B3", "Сверить изменение"}, []string{"./investigate"}},
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
	v := HomeView{BoardURL: "./board?", Tab: "overview", Mode: "demo", Tabs: []Link{{Label: "Обзор"}}, Monitor: &Monitor{
		Products: []MonitorProduct{{Name: "Kaspersky Small Office Security", URL: "./?monitor=demo", Origin: "ДЕМО",
			Cells: [4]MonitorCell{{Label: "Тревога", Tone: "alarm"}}}},
		Pulse: []PulseItem{{Product: "Kaspersky Small Office Security", FirstDetected: "Впервые обнаружено 10:00", URL: "./?monitor=demo"}},
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
	for _, want := range []string{"Карта каталога", "./?monitor=demo", "Впервые обнаружено 10:00", "product-decisions"} {
		if !strings.Contains(overview, want) {
			t.Fatal(want)
		}
	}
	for _, banned := range []string{"Деморежим", "синтетическ", "Синтетическ"} {
		if strings.Contains(overview, banned) {
			t.Fatalf("panel shows %q", banned)
		}
	}
	v.Monitor.Selected = &Investigation{Product: "Kaspersky Small Office Security", Origin: "ДЕМО", Scale: "Не доказано",
		Chart: Chart{Target: "30,30 570,170", Control: "30,30 570,35", Fill: "30,30 570,170 570,35 30,30", ReleaseX: "300"}}
	v.Tab = "hypothesis"
	product := render(v)
	for _, want := range []string{"trend-fill", "trend-release", "ДЕМО", "Задать допущения"} {
		if !strings.Contains(product, want) {
			t.Fatal(want)
		}
	}
	v.Tab = "after"
	if !strings.Contains(render(v), "До и после релиза") {
		t.Fatal("measurement tab unavailable")
	}
	if strings.Contains(overview, "<table") || strings.Contains(product, "₽ / неделю") {
		t.Fatal("showed economics without inputs")
	}
}

func TestSavedCardPutsVisualDetailsBeforeNormalActions(t *testing.T) {
	v := HomeView{
		Tab: "hypothesis", Mode: "real", Tabs: []Link{{Label: "Гипотеза"}},
		Card: &Investigation{CardID: "saved", Product: "Kaspersky Small Office Security",
			Chart: Chart{Caption: "Средние индексы", Target: "30,30 570,170"}},
		Detail: &Decision{ID: "saved", Title: "Сохранённая гипотеза", Version: "1"},
	}
	var b bytes.Buffer
	if err := Index(v).Render(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	chart, action := strings.Index(html, "trend-target"), strings.Index(html, "./approve")
	if chart < 0 || action < chart || !strings.Contains(html, `id="hypothesis-actions"`) {
		t.Fatal("the saved-card path must expose graph and approval together")
	}
	if strings.Contains(html, `class="trend-control"`) {
		t.Fatal("plotted a control with no observations")
	}
}
