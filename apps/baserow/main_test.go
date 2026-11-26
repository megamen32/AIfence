package main

import (
	"github.com/airlockrun/agentsdk/wire"
	"testing"
)

func TestManifest(t *testing.T) {
	m := newAgent().Manifest()
	if err := wire.CheckAppRuntimeProtocol(m.RuntimeProtocol); err != nil {
		t.Fatal(err)
	}
	if len(m.MCPServers) != 1 || len(m.Routes) != 1 {
		t.Fatal("missing native MCP or web route")
	}
}
func TestValidURL(t *testing.T) {
	for _, s := range []string{"javascript:alert(1)", "http://user:password@localhost/", ""} {
		if validURL(s) {
			t.Fatal(s)
		}
	}
	if !validURL("http://localhost:42081") {
		t.Fatal("local URL")
	}
}
