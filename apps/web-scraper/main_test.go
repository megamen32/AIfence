package main

import (
	"bytes"
	"github.com/airlockrun/agentsdk/wire"
	"testing"
)

func TestManifest(t *testing.T) {
	m := newAgent().Manifest()
	if e := wire.CheckAppRuntimeProtocol(m.RuntimeProtocol); e != nil {
		t.Fatal(e)
	}
	if len(m.Tools) != 2 || len(m.Routes) != 4 {
		t.Fatal("missing tools or routes")
	}
}
func TestCoverage(t *testing.T) {
	if percent(Coverage{Done: 25, Total: 100, Known: true}) != 25 || percent(Coverage{Done: 7}) != 0 {
		t.Fatal("bad coverage")
	}
	if validate(Snapshot{GeneratedAt: "2026-11-26T12:00:00Z", Sources: []Source{{ID: "x", Coverage: []Coverage{{Done: 101, Total: 100, Known: true}}}}}) == nil {
		t.Fatal("invalid coverage accepted")
	}
}
func TestRender(t *testing.T) {
	s := Source{ID: "x", Name: "Demo", Status: "running", Coverage: []Coverage{{Label: "Pages", Done: 4, Total: 10, Known: true}}}
	for _, p := range []pageData{{Snapshot: Snapshot{Sources: []Source{s}}, Tab: "overview"}, {Selected: &s, Tab: "source"}, {Tab: "new"}, {Tab: "export"}} {
		var b bytes.Buffer
		if e := view.Execute(&b, p); e != nil {
			t.Fatal(e)
		}
		if b.Len() < 100 {
			t.Fatal("empty")
		}
	}
}
