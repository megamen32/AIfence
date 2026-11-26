package main

import (
	"github.com/airlockrun/agentsdk/wire"
	"net/url"
	"testing"
)

func TestManifest(t *testing.T) {
	m := newAgent().Manifest()
	if err := wire.CheckAppRuntimeProtocol(m.RuntimeProtocol); err != nil {
		t.Fatal(err)
	}
	if len(m.Routes) < 6 || len(m.Tools) < 5 {
		t.Fatal("missing authenticated native routes or tools")
	}
	if len(m.MCPServers) != 0 {
		t.Fatal("must not use a shared MCP token")
	}
}
func TestOrigin(t *testing.T) {
	for raw, want := range map[string]string{"http://baserow.localhost:42080": "http://localhost:42080", "https://baserow.airlock.example.com": "https://airlock.example.com", "http://baserow.airlock.localhost:42080": "http://airlock.localhost:42080"} {
		u, _ := url.Parse(raw)
		if got := dashboardOrigin(u); got != want {
			t.Fatalf("%s: %s", raw, got)
		}
	}
}
func TestIdentitySeparation(t *testing.T) {
	seed := make([]byte, 32)
	a, b := credentials(seed, "baserow-user", "airlock-a")
	c, d := credentials(seed, "baserow-user", "airlock-b")
	e, f := credentials(seed, "baserow-system", "system")
	aa, bb := credentials(seed, "baserow-user", "airlock-a")
	if a == c || b == d || a == e || b == f || a != aa || b != bb {
		t.Fatal("identity collision or unstable mapping")
	}
}
