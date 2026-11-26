package main

import (
	"agent/handlers"
	"agent/market"
	"agent/views"
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
	configuredCatalog := a.RegisterEnvVar(&agentsdk.EnvVar{
		Slug: "baserow_catalog_url", Description: "Authenticated native Baserow price matrix URL for this deployment",
	})
	catalogLink := market.CatalogLink{Location: configuredCatalog}
	pages := handlers.Pages{Market: s, CatalogLink: catalogLink.URL}
	a.RegisterTool(tool.Typed[struct{}, market.CatalogDestination]("open_price_catalog").Description("Return the authenticated native Baserow price matrix URL for this installation. The user can compare seller columns and drill into one SKU.").Execute(catalogLink.Open).Build(), agentsdk.AccessUser)
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.PriceHistoryJSName, ContentType: "text/javascript; charset=utf-8", Data: views.PriceHistoryJS})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/matrix.csv", Handler: s.MatrixCSV, Access: agentsdk.AccessUser, Description: "Export the license comparison matrix with comparable terms and seven-day changes"})
	a.RegisterTool(tool.Typed[market.ChangesInput, market.PriceHistory]("price_history").Description("Read actual dated observations for the selected SKU and period. Distinguish a single observation or a gap from a trend.").Execute(s.PriceHistory).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[market.QuoteInput, market.OrderQuote]("quote_quantity").Description("Compare the requested order quantity using only published compatible tiers.").Execute(s.OrderQuote).Build(), agentsdk.AccessUser)
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.AppCSSName, ContentType: "text/css; charset=utf-8", Data: views.AppCSS})
	a.RegisterTool(tool.Typed[market.CompareInput, market.Comparison]("compare_offers").Description("Compare observed public prices for one exact SKU. Only comparable groups can have a lowest displayed price. Includes a link to the matching web view.").Execute(s.CompareSKU).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[struct{}, market.CrawlReport]("crawl_coverage").Description("Latest imported dated reconciliation of distinct sitemap license URLs, including unknown denominators and missing prices.").Execute(s.CrawlCoverage).Build(), agentsdk.AccessUser)
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodPost, Path: "/coverage/import", Handler: s.ImportCoverage, Access: agentsdk.AccessAdmin, Description: "Import a dated sitemap reconciliation"})

	a.RegisterTool(tool.Typed[market.ListInput, market.Evidence]("list_offers").Description("Search the entire indexed catalog by title, URL or SKU, seller and license words from the title. Use offset/limit to continue.").Execute(s.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[market.AddCompetitorsInput, market.CompetitorResult]("add_competitors").Description("Enable vetted competitor sources (allsoft, syssoft) persistently and run the first bounded public-page collection. This is a real activation, not a draft; no complete catalog coverage is claimed. Idempotent.").Execute(s.AddCompetitors).Build(), agentsdk.AccessAdmin)
	a.RegisterTool(tool.Typed[struct{}, market.CompetitorResult]("list_competitors").Description("Show activation, actual indexed observations, seed URLs and explicit sample-only coverage for supported competitor profiles.").Execute(s.Competitors).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[market.RefreshInput, market.Evidence]("refresh_market").Description("Fetch real public prices from configured and enabled competitor pages. Includes URLs, timestamps and quantities.").Execute(s.Refresh).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[struct{}, market.CatalogStatus]("catalog_status").Description("Count indexed observed offers by store and show latest capture times. Counts measure collected records only.").Execute(s.Coverage).Build(), agentsdk.AccessUser)
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodPost, Path: "/import", Handler: s.Import, Access: agentsdk.AccessAdmin, Description: "Import a bounded batch of observed public catalog prices"})
	a.RegisterTool(tool.Typed[market.ChangesInput, market.Evidence]("market_changes").Description("Compare recorded prices over an inclusive UTC date range. Missing prices are not changes.").Execute(s.Changes).Build(), agentsdk.AccessUser)
	job := agentsdk.RegisterJob(a, &agentsdk.Job[struct{}, market.Receipt]{Name: "refresh_catalog", Version: 1, Description: "Refresh configured featured offers", Timeout: 2 * time.Minute, MaxAttempts: 1, MaxConcurrency: 1, Handler: func(ctx context.Context, _ agentsdk.JobContext, _ struct{}) (market.Receipt, error) {
		e, err := s.Refresh(ctx, market.RefreshInput{})
		return market.Receipt{Status: e.Status, AsOf: e.AsOf, Count: len(e.Observations)}, err
	}})
	job.Cron(&agentsdk.JobCron[struct{}]{Slug: "refresh_catalog", Schedule: "0 */3 * * *", Description: "Update observed prices every three hours"})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/", Handler: pages.Home, Access: agentsdk.AccessUser, Description: "Latest observations and source links"})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/catalog.csv", Handler: s.CSV, Access: agentsdk.AccessUser, Description: "Download observed prices as Excel-compatible CSV"})
	a.AddInstruction(&agentsdk.Instruction{Text: "When an administrator asks to add Allsoft or Syssoft, call add_competitors with their IDs. Use list_competitors to verify activation and observed offers. A seed-only profile is not a full catalog; if a profile is unavailable, report the exact blocker. For unsupported sources do not claim activation. When the user wants to explore the whole market, link the configured Baserow matrix from open_price_catalog. Use list_offers to search indexed catalogs by SKU or title. For historical questions use price_history. For a requested number of license packs use quote_quantity. If the user refers to the current page, a selected date or sellers, use the attached Airlock app view and its URL parameters for SKU, quantity and dates; never invent a selection that was not supplied. For a price comparison use compare_offers with the exact SKU. Respect its groups, reasons and truncation flag. Link the returned view_url in the answer so the user can open the same comparison. Use crawl_coverage for dated sitemap counts and distinguish it from catalog_status. For an immediate check of known prices use refresh_market with exact SKU. Use catalog_status for indexed counts. Show exact SKU, seller, observed RUB amount, quantity if known, capture time and source URL. Do not compare different quantity bases or assume taxes. Report each observation time. Only attribute complete coverage if documented by a completed and reconciled crawl. Never invent missing prices. Answer in the user's language."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Сравнить цены", Prompt: "Сравни опубликованные цены по артикулу KL4141RCEFS, укажи различия в условиях лицензий и дай ссылку на это сравнение."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Изменения за неделю", Prompt: "Какие изменения цен за последние семь дней подтверждены двумя наблюдениями? Покажи источники и даты."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Добавить конкурентов", Prompt: "Добавь Allsoft и Syssoft и проверь, какие реальные цены удалось получить. Не утверждай полноту каталогов."})
	return a
}
