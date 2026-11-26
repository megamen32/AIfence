package main

import (
	"agent/internal/catalogoverview"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

func registerCatalogOverview(a *agentsdk.Agent) *catalogoverview.Service {
	s := catalogoverview.New(a.DB())
	a.RegisterTool(tool.Typed[catalogoverview.ImportInput, catalogoverview.Receipt]("import_catalog_overview").Description("After explicit confirmation, replace your own catalog aggregate with a sourced Market Watch export.").Execute(s.Import).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[catalogoverview.GetInput, catalogoverview.Overview]("get_catalog_overview").Description("Read your own latest sourced catalog aggregate, with bounded vendor pagination.").Execute(s.Get).Build(), agentsdk.AccessUser)
	return s
}
