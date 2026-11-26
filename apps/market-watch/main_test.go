package main

import (
	"github.com/airlockrun/agentsdk/wire"
	"testing"
)

func TestAgentComposition(t *testing.T) {
	m := newAgent().Manifest()
	if err := wire.CheckAppRuntimeProtocol(m.RuntimeProtocol); err != nil {
		t.Fatal(err)
	}
	if len(m.Routes) < 2 || len(m.Tools) < 3 {
		t.Fatal("missing runtime declarations")
	}
}
