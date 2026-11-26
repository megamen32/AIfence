package main

import (
	"agent/market"
	"context"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
	"net/http"
	"time"
)

func main() { newAgent().Serve() }
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Market Watch - live public prices and catalog snapshots", Icon: "📊"})
	a.RegisterDirectory("catalog", agentsdk.DirectoryOpts{Read: agentsdk.AccessUser, List: agentsdk.AccessUser, Write: agentsdk.AccessInternal, Description: "Featured-page refresh history"})
	s := market.NewWithCatalog(a, a.DB())
	a.RegisterTool(tool.Typed[market.ListInput, market.Evidence]("list_offers").Description("Read observed prices by exact SKU or title from the indexed store catalogs.").Execute(s.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[market.RefreshInput, market.Evidence]("refresh_market").Description("Fetch real public prices from the configured pages of Softline, SoftMagazin and SoftMap. Includes URLs, timestamps and quantities.").Execute(s.Refresh).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[struct{}, market.CatalogStatus]("catalog_status").Description("Count indexed observed offers by store and show latest capture times. Counts measure collected records only.").Execute(s.Coverage).Build(), agentsdk.AccessUser)
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodPost, Path: "/import", Handler: s.Import, Access: agentsdk.AccessAdmin, Description: "Import a bounded batch of observed public catalog prices"})
	a.RegisterTool(tool.Typed[market.ChangesInput, market.Evidence]("market_changes").Description("Compare recorded prices over an inclusive UTC date range. Missing prices are not changes.").Execute(s.Changes).Build(), agentsdk.AccessUser)
	job := agentsdk.RegisterJob(a, &agentsdk.Job[struct{}, market.Receipt]{Name: "refresh_catalog", Version: 1, Description: "Refresh configured featured offers", Timeout: 2 * time.Minute, MaxAttempts: 1, MaxConcurrency: 1, Handler: func(ctx context.Context, _ agentsdk.JobContext, _ struct{}) (market.Receipt, error) {
		e, err := s.Refresh(ctx, market.RefreshInput{})
		return market.Receipt{Status: e.Status, AsOf: e.AsOf, Count: len(e.Observations)}, err
	}})
	job.Cron(&agentsdk.JobCron[struct{}]{Slug: "refresh_catalog", Schedule: "0 */3 * * *", Description: "Update observed prices every three hours"})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/", Handler: s.Home, Access: agentsdk.AccessUser, Description: "Latest observations and source links"})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/catalog.csv", Handler: s.CSV, Access: agentsdk.AccessUser, Description: "Download observed prices as Excel-compatible CSV"})
	a.AddInstruction(&agentsdk.Instruction{Text: "Use list_offers first to search indexed catalogs by SKU or title. For an immediate check of known prices use refresh_market with exact SKU. Use catalog_status for indexed counts. Show exact SKU, seller, observed RUB amount, quantity if known, capture time and source URL. Do not compare different quantity bases or assume taxes. Report each observation time. Only attribute complete coverage if documented by a completed and reconciled crawl. Never invent missing prices. Answer in the user's language."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Compare real prices", Prompt: "Refresh KL4141RCEFS prices in all three stores and show source links."})
	return a
}
