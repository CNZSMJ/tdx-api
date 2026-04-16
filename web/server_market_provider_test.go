package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tdx "github.com/injoyai/tdx"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestBuildInstrumentReferenceMarksMissingFields(t *testing.T) {
	model := &tdx.CodeModel{
		Code:     "600000",
		Name:     "浦发银行",
		Exchange: "sh",
		Multiple: 100,
		Decimal:  2,
	}
	finance := &protocol.FinanceInfo{
		IpoDate:   19991110,
		Industry:  42,
		Zongguben: 1000,
	}

	ref := buildInstrumentReference(model, finance, "", "股份制商业银行", "银行", "", "active")

	if ref.FullCode != "sh600000" {
		t.Fatalf("full_code = %s, want sh600000", ref.FullCode)
	}
	if ref.ListingDate != "1999-11-10" {
		t.Fatalf("listing_date = %s, want 1999-11-10", ref.ListingDate)
	}
	if ref.IndustryCode != "42" {
		t.Fatalf("industry_code = %s, want 42", ref.IndustryCode)
	}
	if ref.TickSize != 0.01 {
		t.Fatalf("tick_size = %v, want 0.01", ref.TickSize)
	}
	if !containsString(ref.MissingFields, "issuer_name") {
		t.Fatalf("missing_fields = %#v, want issuer_name", ref.MissingFields)
	}
	if !containsString(ref.MissingFields, "subindustry_name") {
		t.Fatalf("missing_fields = %#v, want subindustry_name", ref.MissingFields)
	}
}

func TestFilterIntradayBarRowsDoesNotFallback(t *testing.T) {
	rows := []historyBarRow{
		{Time: time.Date(2026, 4, 14, 9, 31, 0, 0, time.Local), Open: 10.0, High: 10.1, Low: 9.9, Close: 10.0},
	}
	_, err := filterIntradayBarRows(rows, time.Date(2026, 4, 15, 0, 0, 0, 0, time.Local))
	if err == nil {
		t.Fatalf("expected exact-date error when requested trading_date has no rows")
	}
}

func TestBuildAdjustmentFactorItemsFiltersRange(t *testing.T) {
	items := buildAdjustmentFactorItems([]*extend.THSFactor{
		{Date: time.Date(2025, 6, 20, 15, 0, 0, 0, time.Local).Unix(), QFactor: 0.9821, HFactor: 1.0182},
		{Date: time.Date(2026, 1, 10, 15, 0, 0, 0, time.Local).Unix(), QFactor: 0.9733, HFactor: 1.0274},
	}, time.Date(2025, 12, 1, 0, 0, 0, 0, time.Local), time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local))

	if len(items) != 1 {
		t.Fatalf("adjustment factor count = %d, want 1", len(items))
	}
	if items[0].ExDate != "2026-01-10" {
		t.Fatalf("ex_date = %s, want 2026-01-10", items[0].ExDate)
	}
	if items[0].ForwardFactor != 0.9733 {
		t.Fatalf("forward_factor = %v, want 0.9733", items[0].ForwardFactor)
	}
}

func TestInferQuoteStatusMarksInferredSource(t *testing.T) {
	now := time.Date(2026, 4, 15, 14, 30, 0, 0, time.Local)
	quote := &protocol.Quote{
		K: protocol.K{
			Last:  protocol.Price(10000),
			Open:  protocol.Price(10000),
			High:  protocol.Price(11000),
			Low:   protocol.Price(10000),
			Close: protocol.Price(11000),
		},
		TotalHand: 1200,
	}

	status := inferQuoteStatus("sh600000", "浦发银行", quote, now)
	if status.Source != "inferred_from_quote" {
		t.Fatalf("status source = %s, want inferred_from_quote", status.Source)
	}
	if !status.IsLimitUp {
		t.Fatalf("expected limit-up inference, got %#v", status)
	}
	if status.IsHalted {
		t.Fatalf("expected active quote, got %#v", status)
	}
}

func TestBuildSignalCheckPayloadFullMode(t *testing.T) {
	payload := buildSignalCheckPayload(
		[]string{"sh600000"},
		[]string{"new_high", "volume_spike"},
		"full",
		[]collectorpkg.SignalItem{{Code: "sh600000", Name: "浦发银行", SignalType: "new_high"}},
		time.Date(2026, 4, 15, 14, 31, 0, 0, time.Local),
	)

	items := payload["items"].([]map[string]interface{})
	if len(items) != 2 {
		t.Fatalf("item count = %d, want 2", len(items))
	}
	if items[0]["matched"] != true {
		t.Fatalf("first item matched = %v, want true", items[0]["matched"])
	}
	if items[1]["matched"] != false {
		t.Fatalf("second item matched = %v, want false", items[1]["matched"])
	}
}

func TestLookupFullCodeModelRequiresFullCode(t *testing.T) {
	models := []*tdx.CodeModel{
		{Code: "600000", Exchange: "sh", Name: "浦发银行"},
		{Code: "000001", Exchange: "sz", Name: "平安银行"},
	}

	_, err := lookupFullCodeModel("000001", models)
	if err == nil {
		t.Fatalf("expected bare code to be rejected")
	}
	if err.Error() != "full_code 参数无效，请传完整市场前缀代码，例如 sh600000：000001" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLookupFullCodeModelMatchesExactFullCode(t *testing.T) {
	models := []*tdx.CodeModel{
		{Code: "000001", Exchange: "sh", Name: "上证指数"},
		{Code: "000001", Exchange: "sz", Name: "平安银行"},
	}

	model, err := lookupFullCodeModel("sh000001", models)
	if err != nil {
		t.Fatalf("lookup full_code failed: %v", err)
	}
	if model.FullCode() != "sh000001" {
		t.Fatalf("full_code = %s, want sh000001", model.FullCode())
	}
}

func TestExtractF10FieldsRemoveNoise(t *testing.T) {
	text := `
┌────────────────────┐
│ 目录               │
├────────────────────┤
公司全称：上海浦东发展银行股份有限公司 │ 英文名称：Shanghai Pudong Development Bank
主营业务：商业银行及相关金融服务；吸收公众存款、发放贷款。└────────────────────┘
`

	issuer := extractIssuerName(text)
	if issuer != "上海浦东发展银行股份有限公司" {
		t.Fatalf("issuer_name = %q, want 上海浦东发展银行股份有限公司", issuer)
	}

	summary := extractBusinessSummary(text)
	if summary != "商业银行及相关金融服务" {
		t.Fatalf("business_summary = %q, want 商业银行及相关金融服务", summary)
	}
}

func TestParseBlockProviderKeyRequiresSource(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/blocks?block_type=concept", nil)

	_, err := parseBlockProviderKey(req, blockProviderKeyRequirement{RequireSource: true})
	if err == nil {
		t.Fatalf("expected source requirement error")
	}
	if err.Error() != "source 为必填参数" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFilterBlockRecordsAppliesKeywordBeforeLimit(t *testing.T) {
	records := []collectorpkg.BlockGroupRecord{
		{Source: "block_gn.dat", BlockType: "concept", Name: "A白酒", StockCount: 10},
		{Source: "block_gn.dat", BlockType: "concept", Name: "B白酒", StockCount: 11},
		{Source: "block_gn.dat", BlockType: "concept", Name: "C白酒", StockCount: 12},
		{Source: "block_gn.dat", BlockType: "concept", Name: "新能源", StockCount: 13},
		{Source: "block_fg.dat", BlockType: "industry", Name: "白酒", StockCount: 14},
	}

	allMatches := filterBlockRecords(records, blockProviderKey{Source: "block_gn.dat", BlockType: "concept"}, "白酒", 0)
	if len(allMatches) != 3 {
		t.Fatalf("full keyword match count = %d, want 3", len(allMatches))
	}
	for _, record := range allMatches {
		if !strings.Contains(record.Name, "白酒") {
			t.Fatalf("unexpected non-matching record in keyword result: %#v", record)
		}
	}

	limited := filterBlockRecords(records, blockProviderKey{Source: "block_gn.dat", BlockType: "concept"}, "白酒", 2)
	if len(limited) != 2 {
		t.Fatalf("limited keyword match count = %d, want 2", len(limited))
	}
	if limited[0].Name != "A白酒" || limited[1].Name != "B白酒" {
		t.Fatalf("unexpected limited order: %#v", limited)
	}
}

func TestNormalizeQuoteSnapshotZeroesDerivedMetricsWhenPriceIsZero(t *testing.T) {
	item := normalizeQuoteSnapshot(providerQuoteSnapshot{
		Price:        0,
		Change:       1.2,
		ChangePct:    3.4,
		Volume:       100,
		Amount:       2000,
		StatusReason: "inferred",
	})

	if item.Change != 0 || item.ChangePct != 0 || item.Volume != 0 || item.Amount != 0 {
		t.Fatalf("normalized snapshot still contains inconsistent metrics: %#v", item)
	}
}

func TestParseIntradayIntervalDefaultsToOneMinute(t *testing.T) {
	interval, err := parseIntradayInterval("")
	if err != nil {
		t.Fatalf("parseIntradayInterval returned error: %v", err)
	}
	if interval != 1 {
		t.Fatalf("interval = %d, want 1", interval)
	}
}

func TestFetchQuoteSnapshotsWithFetchersIncludesIndexItems(t *testing.T) {
	models := []*tdx.CodeModel{
		{Code: "600519", Exchange: "sh", Name: "贵州茅台", Multiple: 100, Decimal: 2},
		{Code: "510300", Exchange: "sh", Name: "300ETF", Multiple: 1, Decimal: 3},
		{Code: "000300", Exchange: "sh", Name: "沪深300", Multiple: 1, Decimal: 2},
	}
	now := time.Date(2026, 4, 16, 14, 35, 0, 0, time.Local)

	items, err := fetchQuoteSnapshotsWithFetchers(
		models,
		now,
		func(codes ...string) (protocol.QuotesResp, error) {
			if len(codes) != 2 {
				return nil, fmt.Errorf("quote fetch received %d codes, want 2", len(codes))
			}
			return protocol.QuotesResp{
				{
					Exchange:  protocol.ExchangeSH,
					Code:      "600519",
					TotalHand: 123,
					Amount:    45678,
					K: protocol.K{
						Last:  protocol.Price(1467500),
						Open:  protocol.Price(1468000),
						High:  protocol.Price(1472000),
						Low:   protocol.Price(1465000),
						Close: protocol.Price(1469000),
					},
				},
				{
					Exchange:  protocol.ExchangeSH,
					Code:      "510300",
					TotalHand: 321,
					Amount:    87654,
					K: protocol.K{
						Last:  protocol.Price(4100),
						Open:  protocol.Price(4110),
						High:  protocol.Price(4120),
						Low:   protocol.Price(4090),
						Close: protocol.Price(4115),
					},
				},
			}, nil
		},
		func(fullCode string) (*protocol.KlineResp, error) {
			if fullCode != "sh000300" {
				return nil, fmt.Errorf("unexpected index code %s", fullCode)
			}
			return &protocol.KlineResp{
				Count: 2,
				List: []*protocol.Kline{
					{Close: protocol.Price(3980000), Time: time.Date(2026, 4, 15, 15, 0, 0, 0, time.Local)},
					{
						Last:   protocol.Price(3980000),
						Open:   protocol.Price(3985000),
						High:   protocol.Price(4012000),
						Low:    protocol.Price(3978000),
						Close:  protocol.Price(4005000),
						Volume: 123456,
						Amount: protocol.Price(7890100),
						Time:   time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local),
					},
				},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("fetchQuoteSnapshotsWithFetchers returned error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("item count = %d, want 3", len(items))
	}
	if items[2].FullCode != "sh000300" {
		t.Fatalf("third item full_code = %s, want sh000300", items[2].FullCode)
	}
	if items[2].AssetType != "index" {
		t.Fatalf("third item asset_type = %s, want index", items[2].AssetType)
	}
	if items[2].Price != 4005 {
		t.Fatalf("third item price = %v, want 4005", items[2].Price)
	}
	if items[2].PrevClose != 3980 {
		t.Fatalf("third item prev_close = %v, want 3980", items[2].PrevClose)
	}
	if items[2].Change != 25 || items[2].ChangePct != 0.63 {
		t.Fatalf("third item change metrics = (%v, %v), want (25, 0.63)", items[2].Change, items[2].ChangePct)
	}
}

func TestSecurityIndustryResolverResolvesFromTDXFiles(t *testing.T) {
	dir := t.TempDir()
	dictPath := filepath.Join(dir, "incon.dat")
	content := strings.Join([]string{
		"#TDXNHY",
		"T1001|银行",
		"T030501|白酒",
		"######",
		"#TDXRSHY",
		"X500102|股份制银行",
		"X210205|白酒",
	}, "\n")
	if err := os.WriteFile(dictPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write incon.dat: %v", err)
	}

	resolver := newSecurityIndustryResolver()
	resolver.pathResolver = func() string { return dictPath }
	resolver.downloadAssignments = func() ([]byte, error) {
		return []byte(strings.Join([]string{
			"0|000001|T1001|||X500102",
			"1|600519|T030501|||X210205",
		}, "\n")), nil
	}

	industryName, subindustryName := resolver.Resolve("sz000001")
	if industryName != "银行" {
		t.Fatalf("industry_name = %q, want 银行", industryName)
	}
	if subindustryName != "股份制银行" {
		t.Fatalf("subindustry_name = %q, want 股份制银行", subindustryName)
	}

	industryName, subindustryName = resolver.Resolve("sh600519")
	if industryName != "白酒" {
		t.Fatalf("industry_name = %q, want 白酒", industryName)
	}
	if subindustryName != "白酒" {
		t.Fatalf("subindustry_name = %q, want 白酒", subindustryName)
	}
}

func TestParseSecurityIndustryDictionaryDecodesGBK(t *testing.T) {
	raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(strings.Join([]string{
		"#TDXNHY",
		"T1001|银行",
		"######",
		"#TDXRSHY",
		"X500102|股份制银行",
	}, "\n")))
	if err != nil {
		t.Fatalf("encode gbk: %v", err)
	}

	primaryNames, refinedNames := parseSecurityIndustryDictionary(raw)
	if primaryNames["T1001"] != "银行" {
		t.Fatalf("primary_names[T1001] = %q, want 银行", primaryNames["T1001"])
	}
	if refinedNames["X500102"] != "股份制银行" {
		t.Fatalf("refined_names[X500102] = %q, want 股份制银行", refinedNames["X500102"])
	}
}

func TestDefaultSecurityIndustryDictionaryPathPrefersExplicitEnv(t *testing.T) {
	t.Setenv("TDX_INCON_PATH", "/tmp/custom/incon.dat")
	databaseDir = t.TempDir()

	if got := defaultSecurityIndustryDictionaryPath(); got != "/tmp/custom/incon.dat" {
		t.Fatalf("dictionary path = %q, want explicit env path", got)
	}
}

func TestDefaultSecurityIndustryDictionaryPathFallsBackToMetadataDir(t *testing.T) {
	t.Setenv("TDX_INCON_PATH", "")
	dir := t.TempDir()
	databaseDir = dir

	metadataDir := filepath.Join(dir, "metadata")
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatalf("mkdir metadata: %v", err)
	}
	want := filepath.Join(metadataDir, "incon.dat")
	if err := os.WriteFile(want, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write incon.dat: %v", err)
	}

	if got := defaultSecurityIndustryDictionaryPath(); got != want {
		t.Fatalf("dictionary path = %q, want %q", got, want)
	}
}

func TestResolveTradingDayWithCoverageProjectsFutureDates(t *testing.T) {
	coverageEnd := time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local)
	historical := func(day time.Time) bool {
		return day.Format("2006-01-02") == "2026-04-16"
	}

	isTradingDay, err := resolveTradingDayWithCoverage(time.Date(2026, 4, 17, 9, 30, 0, 0, time.Local), coverageEnd, historical)
	if err != nil {
		t.Fatalf("resolveTradingDayWithCoverage future weekday error: %v", err)
	}
	if !isTradingDay {
		t.Fatalf("2026-04-17 should be projected as trading day")
	}

	isTradingDay, err = resolveTradingDayWithCoverage(time.Date(2026, 4, 18, 9, 30, 0, 0, time.Local), coverageEnd, historical)
	if err != nil {
		t.Fatalf("resolveTradingDayWithCoverage weekend error: %v", err)
	}
	if isTradingDay {
		t.Fatalf("2026-04-18 should not be a trading day")
	}

	isTradingDay, err = resolveTradingDayWithCoverage(time.Date(2026, 5, 1, 9, 30, 0, 0, time.Local), coverageEnd, historical)
	if err != nil {
		t.Fatalf("resolveTradingDayWithCoverage holiday error: %v", err)
	}
	if isTradingDay {
		t.Fatalf("2026-05-01 should not be a trading day")
	}
}

func TestResolveTradingDayWithCoverageRejectsUnsupportedFutureYear(t *testing.T) {
	coverageEnd := time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local)

	_, err := resolveTradingDayWithCoverage(time.Date(2027, 1, 4, 9, 30, 0, 0, time.Local), coverageEnd, func(time.Time) bool { return false })
	if err == nil {
		t.Fatalf("expected unsupported future year error")
	}
}

func TestParseMarketStatsAssetTypeDefaultsToStock(t *testing.T) {
	got, err := parseMarketStatsAssetType("")
	if err != nil {
		t.Fatalf("parseMarketStatsAssetType returned error: %v", err)
	}
	if got != "stock" {
		t.Fatalf("asset_type = %q, want stock", got)
	}
}

func TestBuildMarketStatsDataUsesRequestedAssetTypeForExchanges(t *testing.T) {
	resp := buildMarketStatsData([]collectorpkg.StockTick{
		{Code: "sh600000", Exchange: "sh", AssetType: "stock", PctChange: 1.2, Amount: 100, Volume: 10, IsLimitUp: true},
		{Code: "sh510300", Exchange: "sh", AssetType: "etf", PctChange: 0.5, Amount: 50, Volume: 5},
		{Code: "sz000001", Exchange: "sz", AssetType: "stock", PctChange: -0.3, Amount: 80, Volume: 8},
	}, "stock")

	summary := resp["summary"].(map[string]interface{})
	stock := summary["stock"].(map[string]interface{})
	sh := resp["sh"].(map[string]interface{})
	sz := resp["sz"].(map[string]interface{})
	bj := resp["bj"].(map[string]interface{})

	if stock["total"] != 2 {
		t.Fatalf("summary.stock.total = %v, want 2", stock["total"])
	}
	if sh["total"] != 1 {
		t.Fatalf("sh.total = %v, want 1", sh["total"])
	}
	if sz["total"] != 1 {
		t.Fatalf("sz.total = %v, want 1", sz["total"])
	}
	if bj["total"] != 0 {
		t.Fatalf("bj.total = %v, want 0", bj["total"])
	}
	if sh["limit_up"] != 1 {
		t.Fatalf("sh.limit_up = %v, want 1", sh["limit_up"])
	}

	upTotal := sh["up"].(int) + sz["up"].(int) + bj["up"].(int)
	downTotal := sh["down"].(int) + sz["down"].(int) + bj["down"].(int)
	flatTotal := sh["flat"].(int) + sz["flat"].(int) + bj["flat"].(int)
	if stock["up"] != upTotal || stock["down"] != downTotal || stock["flat"] != flatTotal {
		t.Fatalf("summary.stock does not align with per-exchange totals: stock=%v sh=%v sz=%v bj=%v", stock, sh, sz, bj)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
