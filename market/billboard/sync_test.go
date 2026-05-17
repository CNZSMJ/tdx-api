package billboard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx/eastmoney/rpt"
)

type stubRPTClient struct {
	rows map[string][]map[string]any
}

func (s stubRPTClient) QueryAll(ctx context.Context, query rpt.Query) ([]map[string]any, error) {
	return append([]map[string]any(nil), s.rows[query.ReportName]...), nil
}

func TestSyncerCollectsCompleteBillboardDomainAndFiltersBeforeRaw(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.Local)
	syncer := NewSyncer(SyncerConfig{
		Store: store,
		Client: stubRPTClient{rows: map[string][]map[string]any{
			ReportDailyDetails: {
				{
					"SECURITY_CODE":       "000504",
					"SECUCODE":            "000504.SZ",
					"SECURITY_NAME_ABBR":  "*ST生物",
					"TRADE_DATE":          "2024-05-15 00:00:00",
					"BILLBOARD_NET_AMT":   -19453854.66,
					"BILLBOARD_BUY_AMT":   50185957,
					"BILLBOARD_SELL_AMT":  69639811.66,
					"BILLBOARD_DEAL_AMT":  119825768.66,
					"ACCUM_AMOUNT":        294127525,
					"DEAL_NET_RATIO":      -6.61408845,
					"DEAL_AMOUNT_RATIO":   40.73939311,
					"TURNOVERRATE":        10.0837,
					"FREE_MARKET_CAP":     2382817431.67,
					"EXPLANATION":         "连续三个交易日内，跌幅偏离值累计达到20%的证券",
					"CHANGE_TYPE":         "137001002002002",
					"TRADE_ID":            "4967145",
					"D1_CLOSE_ADJCHRATE":  1.1,
					"D2_CLOSE_ADJCHRATE":  2.2,
					"D5_CLOSE_ADJCHRATE":  5.5,
					"D10_CLOSE_ADJCHRATE": 10.1,
				},
				{
					"SECURITY_CODE":      "510300",
					"SECUCODE":           "510300.SH",
					"SECURITY_NAME_ABBR": "沪深300ETF",
					"TRADE_DATE":         "2024-05-15 00:00:00",
				},
			},
			ReportBuyDetails: {
				{
					"SECURITY_CODE":        "000504",
					"SECUCODE":             "000504.SZ",
					"TRADE_DATE":           "2024-05-15 00:00:00",
					"OPERATEDEPT_CODE":     "10115140",
					"OPERATEDEPT_NAME":     "中国银河证券股份有限公司北京中关村大街证券营业部",
					"OPERATEDEPT_CODE_OLD": "80113261",
					"EXPLANATION":          "连续三个交易日内，跌幅偏离值累计达到20%的证券",
					"CHANGE_TYPE":          "137001002002002",
					"TRADE_ID":             "4967145",
					"BUY":                  20443206,
					"SELL":                 311441,
					"NET":                  20131765,
					"ACCUM_AMOUNT":         294127525,
					"ACCUM_VOLUME":         37186193,
					"TOTAL_BUYRIO":         0.069504566089,
				},
			},
			ReportSellDetails: {
				{
					"SECURITY_CODE":    "000504",
					"SECUCODE":         "000504.SZ",
					"TRADE_DATE":       "2024-05-15 00:00:00",
					"OPERATEDEPT_CODE": "10140897",
					"OPERATEDEPT_NAME": "中信证券股份有限公司长兴明珠路证券营业部",
					"EXPLANATION":      "连续三个交易日内，跌幅偏离值累计达到20%的证券",
					"CHANGE_TYPE":      "137001002002002",
					"TRADE_ID":         "4967145",
					"BUY":              53418,
					"SELL":             16028446.8,
					"NET":              -15975028.8,
				},
			},
			ReportOrganizationTradeDetails: {
				{
					"SECUCODE":            "000504.SZ",
					"SECURITY_NAME_ABBR":  "*ST生物",
					"SECURITY_CODE":       "000504",
					"TRADE_DATE":          "2024-05-15 00:00:00",
					"BUY_TIMES":           1,
					"SELL_TIMES":          2,
					"BUY_AMT":             1001563,
					"SELL_AMT":            12736193,
					"NET_BUY_AMT":         -11734630,
					"ACCUM_AMOUNT":        530784050,
					"FREECAP":             44.66,
					"EXPLANATION":         "连续三个交易日内，跌幅偏离值累计达到20%的证券",
					"D1_CLOSE_ADJCHRATE":  1.1,
					"D10_CLOSE_ADJCHRATE": -5.9,
				},
			},
			ReportTradeAll: {
				{
					"SECUCODE":           "000504.SZ",
					"SECURITY_CODE":      "000504",
					"SECURITY_NAME_ABBR": "*ST生物",
					"LATEST_TDATE":       "2024-05-15 00:00:00",
					"PERIOD":             "近一年",
					"STATISTICS_CYCLE":   "04",
					"BILLBOARD_TIMES":    2,
					"BILLBOARD_DEAL_AMT": 144918010.87,
					"BILLBOARD_BUY_AMT":  53094097.9,
					"BILLBOARD_SELL_AMT": 91823912.97,
					"BILLBOARD_NET_BUY":  -38729815.07,
					"ORG_TIMES":          3,
					"ORG_DEAL_AMT":       43404089.77,
					"ORG_BUY_AMT":        0,
					"ORG_SELL_AMT":       43404089.77,
					"ORG_NET_BUY":        -43404089.77,
					"IPCT1M":             3.62,
				},
			},
		}},
		Now: func() time.Time { return now },
	})

	result, err := syncer.SyncDates(context.Background(), []string{"20240515"})
	if err != nil {
		t.Fatalf("sync dates: %v", err)
	}
	if result.EntryCount != 1 || result.SeatTradeCount != 2 || result.InstitutionCount != 1 || result.InstrumentStatCount != 1 || result.FilteredCount != 1 {
		t.Fatalf("unexpected sync result: %#v", result)
	}
	if raw, err := store.CountRawRows(); err != nil {
		t.Fatalf("count raw rows: %v", err)
	} else if raw != 5 {
		t.Fatalf("raw rows = %d, want 5 supported rows only", raw)
	}
	if coverage, err := store.Coverage("20240515", "20240515", CoreReports()); err != nil {
		t.Fatalf("coverage: %v", err)
	} else if coverage.Coverage != CoverageComplete {
		t.Fatalf("coverage = %#v, want complete", coverage)
	}
}

func TestSourceRowIDDoesNotDependOnUpstreamRowOrder(t *testing.T) {
	row := map[string]any{
		"SECUCODE":             "000504.SZ",
		"TRADE_DATE":           "2024-05-15 00:00:00",
		"TRADE_ID":             "4967145",
		"CHANGE_TYPE":          "137001002002002",
		"OPERATEDEPT_CODE":     "10115140",
		"OPERATEDEPT_NAME":     "中国银河证券股份有限公司北京中关村大街证券营业部",
		"OPERATEDEPT_CODE_OLD": "80113261",
		"EXPLANATION":          "连续三个交易日内，跌幅偏离值累计达到20%的证券",
	}
	first := sourceRowID(ReportBuyDetails, row, 0)
	second := sourceRowID(ReportBuyDetails, row, 99)
	if first != second {
		t.Fatalf("source row id changed with row order: %s vs %s", first, second)
	}
}
