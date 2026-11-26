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
		{"proposed", HomeView{Detail: &Decision{Title: "Сверить изменение", ID: "test-hyp", Version: "3", Groups: []EvidenceGroup{{Title: "Сигнал", Rows: []EvidenceRow{row}}}}}, []string{"./approve", "confirmed", "Sales!B2:B3", "Сверить изменение"}, []string{"./investigate"}},
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
