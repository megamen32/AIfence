package handlers

import (
	"agent/views"
	"net/url"
)

func panelDemo(q url.Values, fallback bool) bool {
	switch q.Get("mode") {
	case "demo":
		return true
	case "real":
		return false
	default:
		return fallback
	}
}

func panelMode(demo bool) string {
	if demo {
		return "demo"
	}
	return "real"
}

func clonePanelQuery(q url.Values) url.Values {
	copy := url.Values{}
	for key, values := range q {
		copy[key] = values
	}
	return copy
}

func setupPanel(v *views.HomeView, q url.Values, demo, hasSelection bool) {
	mode := panelMode(demo)
	tab := q.Get("tab")
	switch tab {
	case "overview", "hypothesis", "path", "after":
	default:
		tab = "overview"
		if hasSelection {
			tab = "hypothesis"
		}
	}
	v.Tab, v.Mode = tab, mode
	v.RealURL, v.DemoURL = href("tab", "overview", "mode", "real"), href("tab", "overview", "mode", "demo")
	for _, t := range []struct{ key, label string }{
		{"overview", "Утро"}, {"hypothesis", "Гипотеза KES"}, {"path", "Путь покупки"}, {"after", "После релиза"},
	} {
		link := href("tab", t.key, "mode", mode, "id", q.Get("id"), "monitor", q.Get("monitor"),
			"snapshot", q.Get("snapshot"), "alert", q.Get("alert"))
		v.Tabs = append(v.Tabs, views.Link{Label: t.label, URL: link, Active: t.key == tab})
	}
}

func monitorEmpty(m *views.Monitor) bool {
	return m == nil || (len(m.Pulse) == 0 && len(m.Products) == 0 && m.Selected == nil)
}
