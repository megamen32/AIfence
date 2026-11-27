package competitor

import "github.com/airlockrun/agentsdk"

func Register(a *agentsdk.Agent) {
	registerCompetitorResearch(a)
	registerDiscovery(a)
}
