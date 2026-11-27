package main

import (
	collector "agent/collector"
	"agent/handlers"
	"agent/internal/db"
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
	a.RegisterDirectory("tmp/attachments", agentsdk.DirectoryOpts{Read: agentsdk.AccessUser, Write: agentsdk.AccessUser, List: agentsdk.AccessUser, Scope: agentsdk.ScopeUser, RetentionHours: 24, Description: "Caller-scoped temporary price evidence for hosted chats"})
	a.RegisterDirectory("catalog", agentsdk.DirectoryOpts{Read: agentsdk.AccessUser, List: agentsdk.AccessUser, Write: agentsdk.AccessInternal, Description: "Featured-page refresh history"})
	a.RegisterDirectory("adapter-state", agentsdk.DirectoryOpts{Read: agentsdk.AccessInternal, List: agentsdk.AccessInternal, Write: agentsdk.AccessInternal, Description: "Private versioned adaptive store rules and captured HTML evidence"})
	s := market.NewWithCatalog(a, a.DB())
	s.SetAdapterProposer(proposeStoreAdapter(a))
	a.RegisterTool(tool.Typed[market.LegacyAuditInput, market.LegacyAuditResult]("audit_legacy_archive").Description("Read-only verification of migrated source HTML and exact specialized parsing. No HTTP requests to stores and no activation.").Execute(s.AuditLegacyArchive).Build(), agentsdk.AccessAdmin)

	a.RegisterRoute(&agentsdk.Route{Method: http.MethodPost, Path: "/migration/stage", Handler: s.StageLegacyMigration, Access: agentsdk.AccessAdmin, Description: "Stage an idempotent verified batch of legacy task metadata; no network collection or activation"})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodPost, Path: "/migration/archive", Handler: s.StageLegacyArchive, Access: agentsdk.AccessAdmin, Description: "Transfer only raw gzip objects belonging to the verified staged snapshot; verify storage and update offsets without activating a crawler"})

	a.RegisterTool(tool.Typed[struct{}, []db.LegacyMigrationCountsRow]("legacy_migration_status").Description("Count staged legacy task/failure/response metadata and pending raw archive objects. Staging never enables a crawler.").Execute(s.LegacyMigrationStatus).Build(), agentsdk.AccessAdmin)

	a.RegisterTool(tool.Typed[market.AdapterActionInput, market.StudyEvidence]("inspect_store_study").Description("Inspect archived training evidence and held-out price-evidence counts. Does not fetch or activate a store.").Execute(s.InspectAdaptiveStudy).Build(), agentsdk.AccessAdmin)

	agentsdk.RegisterAsyncTool(a, &agentsdk.AsyncTool[market.AddStoreInput, market.AdaptiveJobResult]{Access: agentsdk.AccessAdmin, Job: &agentsdk.Job[market.AddStoreInput, market.AdaptiveJobResult]{Name: "add_store", Version: 1, Description: "Study a new public software store and validate its adapter; returns a durable task handle.", Timeout: 10 * time.Minute, MaxAttempts: 2, MaxConcurrency: 1, Handler: s.AddAdaptiveJobResult}})
	a.RegisterTool(tool.Typed[struct{}, []market.AdaptiveStoreView]("list_adaptive_stores").Description("Inspect adaptive stores, active versions, unfinished URL tasks, archive and validation state.").Execute(s.AdaptiveStores).Build(), agentsdk.AccessUser)
	agentsdk.RegisterAsyncTool(a, &agentsdk.AsyncTool[market.AdapterActionInput, market.AdaptiveJobResult]{Access: agentsdk.AccessAdmin, Job: &agentsdk.Job[market.AdapterActionInput, market.AdaptiveJobResult]{Name: "repair_store_adapter", Version: 1, Description: "Resume learning from the store archive and validate a new adapter in a durable job.", Timeout: 10 * time.Minute, MaxAttempts: 2, MaxConcurrency: 1, Handler: s.RepairAdaptiveJobResult}})
	a.RegisterTool(tool.Typed[market.AdapterRollbackInput, market.AdaptiveStoreView]("rollback_store_adapter").Description("Restore a previously validated adapter version without rewriting historical price observations.").Execute(s.RollbackAdaptiveOwned).Build(), agentsdk.AccessAdmin)
	a.RegisterTool(tool.Typed[collector.AdapterTraining, collector.AdapterSpec]("propose_store_adapter").Description("Internal admin diagnostic: propose declarative store adapter from provided training markup; this tool neither activates a store nor decides acceptance.").Execute(proposeStoreAdapter(a)).Build(), agentsdk.AccessAdmin)

	tick := agentsdk.RegisterJob(a, &agentsdk.Job[struct{}, market.AdaptiveTickResult]{Name: "advance_adaptive_stores", Version: 1, Description: "Advance one adaptive store in a bounded leased window", Timeout: 10 * time.Minute, MaxAttempts: 2, MaxConcurrency: 1, Handler: s.AdvanceAdaptiveJob})
	tick.Cron(&agentsdk.JobCron[struct{}]{Slug: "advance_adaptive_stores", Schedule: "*/3 * * * *", Description: "Advance due URL tasks using the app database"})
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
	a.RegisterTool(withMarketEvidence(tool.Typed[market.CompareInput, market.Comparison]("compare_offers").Description("Compare observed public prices for one exact SKU. Only comparable groups can have a lowest displayed price. Includes a link to the matching web view.").Execute(s.CompareSKU).Build()), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[collector.RepairBatch, collector.RuleProposal]("propose_parser_repair").Description("Internal admin-only parser repair proposal over a bounded batch of public HTML evidence. Returns declarative selectors; does not activate or execute code.").Execute(proposeParserRepair(a)).Build(), agentsdk.AccessAdmin)
	a.RegisterTool(tool.Typed[struct{}, market.CrawlReport]("crawl_coverage").Description("Latest imported dated reconciliation of distinct sitemap license URLs, including unknown denominators and missing prices.").Execute(s.CrawlCoverage).Build(), agentsdk.AccessUser)
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodPost, Path: "/coverage/import", Handler: s.ImportCoverage, Access: agentsdk.AccessAdmin, Description: "Import a dated sitemap reconciliation"})

	a.RegisterTool(withMarketEvidence(tool.Typed[market.ListInput, market.Evidence]("list_offers").Description("Search the entire indexed catalog by title, URL or SKU, seller and license words from the title. Use offset/limit to continue.").Execute(s.List).Build()), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[market.AddCompetitorsInput, market.CompetitorResult]("add_competitors").Description("Enable vetted competitor sources (allsoft, syssoft) persistently and run the first bounded public-page collection. This is a real activation, not a draft; no complete catalog coverage is claimed. Idempotent.").Execute(s.AddCompetitors).Build(), agentsdk.AccessAdmin)
	a.RegisterTool(tool.Typed[struct{}, market.CompetitorResult]("list_competitors").Description("Show activation, actual indexed observations, seed URLs and explicit sample-only coverage for supported competitor profiles.").Execute(s.Competitors).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[market.RefreshInput, market.Evidence]("refresh_market").Description("Fetch real public prices from configured and enabled competitor pages. Includes URLs, timestamps and quantities.").Execute(s.Refresh).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[struct{}, market.CatalogStatus]("catalog_status").Description("Count indexed observed offers by store and show latest capture times. Counts measure collected records only.").Execute(s.Coverage).Build(), agentsdk.AccessUser)
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodPost, Path: "/import", Handler: s.Import, Access: agentsdk.AccessAdmin, Description: "Import a bounded batch of observed public catalog prices"})
	a.RegisterTool(tool.Typed[market.ChangesInput, market.Evidence]("market_changes").Description("Compare recorded prices over an inclusive UTC date range. Missing prices are not changes.").Execute(s.Changes).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[market.WeekDeltaInput, market.WeekDelta]("price_week_delta").Description("Read the latest stored price of each comparable offer series in two complete Monday..Sunday weeks and return the week-to-week delta. One series is one SKU, seller, URL, currency, price basis and quantity tier. Offers seen in only one week are listed as missing periods and never counted as a change. No network request.").Execute(s.WeekDelta).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
	a.RegisterTool(tool.Typed[market.FamilyDeltaInput, market.FamilyDelta]("family_price_week_delta").Description("Read two complete weeks of stored public prices for 1 to 20 explicit SKUs or discover candidate SKUs from indexed family titles. Compare only the same seller, URL, currency, price basis and quantity tier within each SKU. Return coverage denominators and evidence IDs; do not average unlike absolute prices. No network request.").Execute(s.FamilyWeekDelta).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
	job := agentsdk.RegisterJob(a, &agentsdk.Job[struct{}, market.Receipt]{Name: "refresh_catalog", Version: 1, Description: "Refresh configured featured offers", Timeout: 2 * time.Minute, MaxAttempts: 1, MaxConcurrency: 1, Handler: func(ctx context.Context, _ agentsdk.JobContext, _ struct{}) (market.Receipt, error) {
		e, err := s.Refresh(ctx, market.RefreshInput{})
		return market.Receipt{Status: e.Status, AsOf: e.AsOf, Count: len(e.Observations)}, err
	}})
	job.Cron(&agentsdk.JobCron[struct{}]{Slug: "refresh_catalog", Schedule: "0 */3 * * *", Description: "Update observed prices every three hours"})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/", Handler: pages.Home, Access: agentsdk.AccessUser, Description: "Latest observations and source links"})
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/catalog.csv", Handler: s.CSV, Access: agentsdk.AccessUser, Description: "Download observed prices as Excel-compatible CSV"})
	a.AddInstruction(&agentsdk.Instruction{Text: "When an administrator asks to add Allsoft or Syssoft, call add_competitors with their IDs. Use list_competitors to verify activation and observed offers. A seed-only profile is not a full catalog; if a profile is unavailable, report the exact blocker. For a user explicitly asking to add a new store by its public HTTPS URL, call add_store, observe its durable task with tasks.get or tasks.subscribe, then inspect list_adaptive_stores. If validation fails or a website blocks access, report that exact state; never claim activation from a proposal. Existing built-in sources keep their original adapters. For a user asking to fix an existing adaptive store use repair_store_adapter. For unsupported sources do not claim activation. When the user wants to explore the whole market, link the configured Baserow matrix from open_price_catalog. Use list_offers to search indexed catalogs by SKU or title. For historical questions use price_history. For a requested number of license packs use quote_quantity. If the user refers to the current page, a selected date or sellers, use the attached Airlock app view and its URL parameters for SKU, quantity and dates; never invent a selection that was not supplied. For a price comparison use compare_offers with the exact SKU. For a week-to-week price change use price_week_delta with two complete Monday..Sunday weeks; report comparable_pairs and missing_periods, and never treat a series seen in one week as a change. Respect its groups, reasons and truncation flag. Link the returned view_url in the answer so the user can open the same comparison. Use crawl_coverage for dated sitemap counts and distinguish it from catalog_status. For an immediate check of known prices use refresh_market with exact SKU. Use catalog_status for indexed counts. Show exact SKU, seller, observed RUB amount, quantity if known, capture time and source URL. Do not compare different quantity bases or assume taxes. Report each observation time. Only attribute complete coverage if documented by a completed and reconciled crawl. Never invent missing prices. Answer in the user's language."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Сравнить цены", Prompt: "Сравни опубликованные цены по артикулу KL4141RCEFS, укажи различия в условиях лицензий и дай ссылку на это сравнение."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Изменения за неделю", Prompt: "Какие изменения цен за последние семь дней подтверждены двумя наблюдениями? Покажи источники и даты."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Добавить конкурентов", Prompt: "Добавь Allsoft и Syssoft и проверь, какие реальные цены удалось получить. Не утверждай полноту каталогов."})
	return a
}
