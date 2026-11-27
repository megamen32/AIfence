package competitor

import "github.com/airlockrun/agentsdk"

func Register(a *agentsdk.Agent) {
	browser := newSiteBrowserService()
	a.OnStart("playwright_browser", browser.start)
	registerCompetitorResearch(a)
	registerDiscovery(a)
	registerCompetitorMatrix(a)
	registerSiteInspector(a, browser)
}
