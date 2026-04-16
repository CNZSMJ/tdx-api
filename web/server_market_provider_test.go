package main

import (
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

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
