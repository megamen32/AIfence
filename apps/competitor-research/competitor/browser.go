package competitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
	playwright "github.com/mxschmitt/playwright-go"
)

const (
	playwrightCLIDefault     = "/var/agent/playwright/node_modules/playwright-core/cli.js"
	playwrightNodeDefault    = "/usr/bin/node"
	chromiumDefault          = "/usr/bin/chromium"
	browserNavigationTimeout = 15 * time.Second
	browserMaxCompetitors    = 10
)

type siteInspectInput struct {
	StudyID     string   `json:"study_id" description:"Study identifier from plan_competitor_research"`
	Competitors []string `json:"competitors" description:"Public HTTPS competitor domains or URLs, maximum 10"`
	Product     string   `json:"product,omitempty" description:"Product or category being compared"`
}

type siteInspectResult struct {
	StudyID     string                  `json:"study_id"`
	Rows        []competitorObservation `json:"rows"`
	Sites       int                     `json:"sites"`
	Limitations []string                `json:"limitations"`
}

type siteBrowserService struct {
	mu      sync.RWMutex
	pw      *playwright.Playwright
	browser playwright.Browser
	sem     chan struct{}
}

func newSiteBrowserService() *siteBrowserService {
	return &siteBrowserService{sem: make(chan struct{}, 2)}
}

func (s *siteBrowserService) start(ctx context.Context) error {
	cliPath := getenvDefault("COMPETITOR_PLAYWRIGHT_CLI_PATH", playwrightCLIDefault)
	nodePath := getenvDefault("COMPETITOR_PLAYWRIGHT_NODE_PATH", playwrightNodeDefault)
	chromiumPath := getenvDefault("COMPETITOR_CHROMIUM_PATH", chromiumDefault)
	if _, err := os.Stat(cliPath); err != nil {
		return fmt.Errorf("playwright CLI is unavailable at %s: %w", cliPath, err)
	}
	if _, err := os.Stat(nodePath); err != nil {
		return fmt.Errorf("node.js is unavailable at %s: %w", nodePath, err)
	}
	if _, err := os.Stat(chromiumPath); err != nil {
		return fmt.Errorf("chromium is unavailable at %s: %w", chromiumPath, err)
	}
	if err := os.Setenv("PLAYWRIGHT_CLI_PATH", cliPath); err != nil {
		return fmt.Errorf("set PLAYWRIGHT_CLI_PATH: %w", err)
	}
	if err := os.Setenv("PLAYWRIGHT_NODEJS_PATH", nodePath); err != nil {
		return fmt.Errorf("set PLAYWRIGHT_NODEJS_PATH: %w", err)
	}

	pw, err := playwright.Run(&playwright.RunOptions{DriverDirectory: "/var/agent/playwright-driver"})
	if err != nil {
		return fmt.Errorf("start playwright driver: %w", err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless:       playwright.Bool(true),
		ExecutablePath: playwright.String(chromiumPath),
		Args:           []string{"--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"},
	})
	if err != nil {
		_ = pw.Stop()
		return fmt.Errorf("launch headless chromium: %w", err)
	}

	s.mu.Lock()
	s.pw = pw
	s.browser = browser
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		b := s.browser
		p := s.pw
		s.browser = nil
		s.pw = nil
		s.mu.Unlock()
		if b != nil {
			_ = b.Close()
		}
		if p != nil {
			_ = p.Stop()
		}
	}()
	return nil
}

func (s *siteBrowserService) inspect(ctx context.Context, in siteInspectInput) (siteInspectResult, error) {
	if strings.TrimSpace(in.StudyID) == "" {
		return siteInspectResult{}, errors.New("study_id is required")
	}
	if len(in.Competitors) == 0 {
		return siteInspectResult{StudyID: in.StudyID, Limitations: []string{"Нет доменов для проверки."}}, nil
	}
	if len(in.Competitors) > browserMaxCompetitors {
		return siteInspectResult{}, fmt.Errorf("competitors must contain at most %d domains", browserMaxCompetitors)
	}

	s.mu.RLock()
	browser := s.browser
	s.mu.RUnlock()
	if browser == nil {
		return siteInspectResult{}, errors.New("background Playwright browser is unavailable")
	}

	rows := make([]competitorObservation, 0, len(in.Competitors)*6)
	limitations := []string{
		"Проверены только публичные HTTPS-страницы; формы не отправлялись, логины и cookies не использовались.",
		"Оценки purchase/account/service/ux — эвристика по наблюдаемым DOM-сигналам, а не подтверждение коммерческих условий.",
		"Блок search здесь означает наличие поиска на сайте; это не оценка SEO-видимости в публичной выдаче.",
		"Цена этим инструментом не собирается; блок price остаётся not_connected и заполняется внешним источником Mac M1.",
	}
	for _, raw := range in.Competitors {
		if err := ctx.Err(); err != nil {
			return siteInspectResult{}, err
		}
		siteRows, err := s.inspectOne(ctx, browser, raw, in.Product)
		if err != nil {
			limitations = append(limitations, fmt.Sprintf("%s: %v", raw, err))
			siteRows = unknownSiteRows(raw, in.Product, err.Error())
		}
		rows = append(rows, siteRows...)
	}
	return siteInspectResult{StudyID: in.StudyID, Rows: rows, Sites: len(in.Competitors), Limitations: uniqueStrings(limitations)}, nil
}

func (s *siteBrowserService) inspectOne(ctx context.Context, browser playwright.Browser, rawURL, product string) ([]competitorObservation, error) {
	siteURL, host, err := normalizePublicSite(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	browserCtx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		AcceptDownloads:   playwright.Bool(false),
		JavaScriptEnabled: playwright.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("create isolated browser context: %w", err)
	}
	defer browserCtx.Close()
	browserCtx.SetDefaultTimeout(float64(browserNavigationTimeout / time.Millisecond))

	page, err := browserCtx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	defer page.Close()
	// Never submit forms or send mutation requests. Navigation and resource reads
	// are GET/HEAD only; no clicks are issued by this collector.
	if err := browserCtx.Route("**/*", func(route playwright.Route) {
		method := strings.ToUpper(route.Request().Method())
		requestURL := route.Request().URL()
		parsedRequest, parseErr := url.Parse(requestURL)
		if method != "GET" && method != "HEAD" || parseErr != nil || unsafeHostLiteral(parsedRequest.Hostname()) {
			_ = route.Abort()
			return
		}
		_ = route.Continue()
	}); err != nil {
		return nil, fmt.Errorf("install read-only request guard: %w", err)
	}

	response, err := page.Goto(siteURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(browserNavigationTimeout / time.Millisecond)),
	})
	if err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}
	finalURL := page.URL()
	finalHost, err := publicFinalHost(finalURL)
	if err != nil {
		return nil, err
	}
	if !sameSiteHost(host, finalHost) {
		return nil, fmt.Errorf("redirected outside candidate host: %s", finalURL)
	}

	rawSnapshot, err := page.Evaluate(snapshotScript)
	if err != nil {
		return nil, fmt.Errorf("read DOM snapshot: %w", err)
	}
	var snapshot siteSnapshot
	payload, err := json.Marshal(rawSnapshot)
	if err != nil {
		return nil, fmt.Errorf("encode DOM snapshot: %w", err)
	}
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, fmt.Errorf("decode DOM snapshot: %w", err)
	}
	status := 200
	if response != nil {
		status = response.Status()
	}
	return rowsFromSnapshot(host, finalURL, product, status, snapshot), nil
}

type siteLink struct {
	Text string `json:"text"`
	Href string `json:"href"`
}

type siteSnapshot struct {
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	H1           []string   `json:"h1"`
	Links        []siteLink `json:"links"`
	NavLinks     int        `json:"nav_links"`
	SearchInputs int        `json:"search_inputs"`
	Forms        int        `json:"forms"`
	Body         string     `json:"body"`
}

const snapshotScript = `() => {
  const clean = (value, max = 400) => String(value || "").replace(/\s+/g, " ").trim().slice(0, max);
  const body = clean(document.body ? document.body.innerText : "", 6000);
  const links = Array.from(document.querySelectorAll("a")).slice(0, 250).map(a => ({
    text: clean(a.innerText || a.getAttribute("aria-label") || "", 180),
    href: a.href || ""
  }));
  return {
    title: clean(document.title),
    description: clean(document.querySelector('meta[name="description"]')?.content || "", 500),
    h1: Array.from(document.querySelectorAll("h1")).slice(0, 8).map(e => clean(e.innerText)),
    links,
    nav_links: document.querySelectorAll("nav a").length,
    search_inputs: document.querySelectorAll('input[type="search"], input[name*="search" i], input[placeholder*="поиск" i], input[placeholder*="search" i]').length,
    forms: document.forms.length,
    body
  };
}`

var (
	purchaseSignal = regexp.MustCompile(`(?i)(купить|заказ|корзин|cart|buy|purchase|catalog|каталог|лиценз)`)
	accountSignal  = regexp.MustCompile(`(?i)(личн|войти|login|sign\s*in|account|регистрац)`)
	serviceSignal  = regexp.MustCompile(`(?i)(контакт|поддерж|support|help|чат|телефон|email|service|сервис)`)
)

func rowsFromSnapshot(host, sourceURL, product string, status int, s siteSnapshot) []competitorObservation {
	now := time.Now().UTC().Format(time.RFC3339)
	purchase := matchingLinks(s.Links, purchaseSignal)
	account := matchingLinks(s.Links, accountSignal)
	service := matchingLinks(s.Links, serviceSignal)
	titleH1 := append([]string{s.Title}, s.H1...)
	titleH1 = compactNonEmpty(titleH1)
	uxScore := 2.0
	if s.Title != "" {
		uxScore = 3
	}
	if s.Title != "" && len(s.H1) > 0 && s.NavLinks >= 3 {
		uxScore = 4
	}
	baseNotes := fmt.Sprintf("Playwright headless DOM; HTTP %d; формы не отправлялись.", status)
	rows := []competitorObservation{
		{Competitor: host, URL: sourceURL, Product: product, Block: "search", Criterion: "site search UI", Value: fmt.Sprintf("search_inputs=%d", s.SearchInputs), Score: floatPtr(scoreForCount(s.SearchInputs, 4, 2)), Status: "observed", SourceURL: sourceURL, CheckedAt: now, Notes: baseNotes},
		{Competitor: host, URL: sourceURL, Product: product, Block: "purchase", Criterion: "public purchase signals", Value: signalValue(purchase, "ссылок"), Score: floatPtr(scoreForSignals(len(purchase))), Status: "observed", SourceURL: sourceURL, CheckedAt: now, Notes: baseNotes},
		{Competitor: host, URL: sourceURL, Product: product, Block: "account", Criterion: "public account signals", Value: signalValue(account, "ссылок"), Score: floatPtr(scoreForSignals(len(account))), Status: "observed", SourceURL: sourceURL, CheckedAt: now, Notes: baseNotes},
		{Competitor: host, URL: sourceURL, Product: product, Block: "service", Criterion: "public service signals", Value: signalValue(service, "ссылок"), Score: floatPtr(scoreForService(len(service))), Status: "observed", SourceURL: sourceURL, CheckedAt: now, Notes: baseNotes},
		{Competitor: host, URL: sourceURL, Product: product, Block: "ux", Criterion: "homepage structure", Value: fmt.Sprintf("title=%t; h1=%d; nav_links=%d; body_chars=%d", s.Title != "", len(s.H1), s.NavLinks, len(s.Body)), Score: floatPtr(uxScore), Status: "observed", SourceURL: sourceURL, CheckedAt: now, Notes: baseNotes},
		{Competitor: host, URL: sourceURL, Product: product, Block: "price", Criterion: "external price source", Value: "Н/Д", Status: "not_connected", SourceURL: sourceURL, CheckedAt: now, Notes: "Цена не собирается этим приложением; передайте проверенные строки из существующего источника Mac M1."},
	}
	return rows
}

func unknownSiteRows(rawURL, product, note string) []competitorObservation {
	host := strings.TrimSpace(rawURL)
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		host = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows := make([]competitorObservation, 0, 6)
	for _, block := range []string{"search", "purchase", "account", "service", "ux"} {
		rows = append(rows, competitorObservation{Competitor: host, URL: rawURL, Product: product, Block: block, Criterion: "public DOM observation", Value: "Н/Д", Status: "unknown", SourceURL: rawURL, CheckedAt: now, Notes: note})
	}
	rows = append(rows, competitorObservation{Competitor: host, URL: rawURL, Product: product, Block: "price", Criterion: "external price source", Value: "Н/Д", Status: "not_connected", SourceURL: rawURL, CheckedAt: now, Notes: "Цена не собирается этим приложением; внешний источник Mac M1."})
	return rows
}

func unsafeHostLiteral(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
	}
	return false
}

func normalizePublicSite(ctx context.Context, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("empty competitor URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", "", errors.New("invalid competitor URL")
	}
	if strings.ToLower(u.Scheme) != "https" {
		return "", "", errors.New("only HTTPS competitor URLs are allowed")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if unsafeHostLiteral(host) {
		return "", "", errors.New("private or IP competitor host is not allowed")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return "", "", fmt.Errorf("resolve host: %w", err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return "", "", errors.New("competitor host resolves to a private address")
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), host, nil
}

func publicFinalHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("browser returned invalid final URL")
	}
	if strings.ToLower(u.Scheme) != "https" {
		return "", errors.New("browser left HTTPS")
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."), nil
}

func sameSiteHost(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a)
}

func matchingLinks(links []siteLink, re *regexp.Regexp) []string {
	out := make([]string, 0, 8)
	for _, link := range links {
		candidate := safeText(link.Text+" "+link.Href, 180)
		if re.MatchString(candidate) {
			out = append(out, candidate)
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func signalValue(signals []string, noun string) string {
	if len(signals) == 0 {
		return "Публичный сигнал не найден"
	}
	return fmt.Sprintf("Найдено %d %s: %s", len(signals), noun, strings.Join(signals, " | "))
}

func scoreForSignals(n int) float64 {
	switch {
	case n >= 3:
		return 4
	case n >= 1:
		return 3
	default:
		return 2
	}
}

func scoreForService(n int) float64 {
	switch {
	case n >= 3:
		return 4
	case n >= 1:
		return 3
	default:
		return 2
	}
}

func scoreForCount(n int, found, missing float64) float64 {
	if n > 0 {
		return found
	}
	return missing
}

func compactNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func safeText(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func getenvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func floatPtr(value float64) *float64 {
	return &value
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func registerSiteInspector(a *agentsdk.Agent, browser *siteBrowserService) {
	t := tool.Typed[siteInspectInput, siteInspectResult]("inspect_competitor_sites").
		Description("Use the background headless Playwright browser to inspect public HTTPS competitor pages without submitting forms or using login/cookies. Return evidence rows for search UI, purchase path, account, service and UX; price remains external. Reject private/IP hosts and redirects outside the candidate site.").
		Execute(func(ctx context.Context, in siteInspectInput) (siteInspectResult, error) {
			return browser.inspect(ctx, in)
		}).Build()
	a.RegisterTool(t, agentsdk.AccessUser)
}
