package profinance

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseDATReportTracksMinimalValuationFields(t *testing.T) {
	report := ReportFile{Filename: "gpcw20251231.zip", ReportDate: "20251231"}
	data := buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 10.5,
		fieldTotalShares:       2000000000,
		fieldFloatAShares:      1500000000,
		fieldNetProfitTTM:      1234567890,
		fieldWeightedROE:       12.6,
		fieldRevenueTTM:        987654.3,
	})

	rows, err := parseDATReport(data, report)
	if err != nil {
		t.Fatalf("parseDATReport: %v", err)
	}
	got, ok := rows["600000"]
	if !ok {
		t.Fatalf("expected snapshot for 600000")
	}
	if got.ReportDate != "20251231" {
		t.Fatalf("report date = %s, want 20251231", got.ReportDate)
	}
	if got.BookValuePerShare != 10.5 {
		t.Fatalf("book value = %v, want 10.5", got.BookValuePerShare)
	}
	if got.TotalShares != 2000000000 {
		t.Fatalf("total shares = %v, want 2000000000", got.TotalShares)
	}
	if got.FloatAShares != 1500000000 {
		t.Fatalf("float a shares = %v, want 1500000000", got.FloatAShares)
	}
	if got.NetProfitTTM != 1234567936 {
		t.Fatalf("net profit ttm = %v, want float32 rounded 1234567936", got.NetProfitTTM)
	}
	if got.RevenueTTMYuan != 987654.3125*10000 {
		t.Fatalf("revenue ttm yuan = %v, want %v", got.RevenueTTMYuan, 987654.3125*10000)
	}
}

func TestLatestForCodeUsesNewestAvailableReportForCode(t *testing.T) {
	listBody := "gpcw20260930.zip,future,164\n" +
		"gpcw20260331.zip,newhash,100\n" +
		"gpcw20251231.zip,oldhash,100\n"

	latestZip := buildZIPFixture(t, "gpcw20260331.dat", buildDATFixture(t, "000001", map[int]float32{
		fieldBookValuePerShare: 8.0,
		fieldTotalShares:       1000000000,
		fieldNetProfitTTM:      600000000,
		fieldRevenueTTM:        300000,
	}))
	olderZip := buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 10.0,
		fieldTotalShares:       2000000000,
		fieldFloatAShares:      1500000000,
		fieldNetProfitTTM:      1230000000,
		fieldRevenueTTM:        450000,
		fieldWeightedROE:       11.2,
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20260331.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(latestZip)
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(olderZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	service := NewService(cacheDir, Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
		},
	})

	got, err := service.LatestForCode(context.Background(), "sh600000")
	if err != nil {
		t.Fatalf("LatestForCode: %v", err)
	}
	if got.SourceReportFile != "gpcw20251231.zip" {
		t.Fatalf("source report = %s, want gpcw20251231.zip", got.SourceReportFile)
	}
	if got.ReportDate != "20251231" {
		t.Fatalf("report date = %s, want 20251231", got.ReportDate)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "artifacts", "zips", "gpcw20251231.zip")); err != nil {
		t.Fatalf("expected cached zip file: %v", err)
	}
}

func TestListForCodeReturnsHistoricalReportsInDescendingOrder(t *testing.T) {
	listBody := "gpcw20260331.zip,newhash,100\n" +
		"gpcw20251231.zip,oldhash,100\n" +
		"gpcw20250930.zip,olderhash,100\n"

	q12026 := buildZIPFixture(t, "gpcw20260331.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 13.5,
		fieldTotalShares:       2000000000,
		fieldFloatAShares:      1500000000,
		fieldNetProfitTTM:      1500000000,
		fieldRevenueTTM:        500000,
		fieldWeightedROE:       12.1,
	}))
	fy2025 := buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 12.0,
		fieldTotalShares:       2000000000,
		fieldFloatAShares:      1500000000,
		fieldNetProfitTTM:      1230000000,
		fieldRevenueTTM:        450000,
		fieldWeightedROE:       11.2,
	}))
	q32025 := buildZIPFixture(t, "gpcw20250930.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 11.5,
		fieldTotalShares:       2000000000,
		fieldFloatAShares:      1500000000,
		fieldNetProfitTTM:      1100000000,
		fieldRevenueTTM:        420000,
		fieldWeightedROE:       10.9,
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20260331.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(q12026)
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(fy2025)
		case "/gpcw20250930.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(q32025)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(t.TempDir(), Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
		},
	})

	items, err := service.ListForCode(context.Background(), "sh600000", 2, "", "")
	if err != nil {
		t.Fatalf("ListForCode: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].ReportDate != "20260331" {
		t.Fatalf("items[0].ReportDate = %s, want 20260331", items[0].ReportDate)
	}
	if items[1].ReportDate != "20251231" {
		t.Fatalf("items[1].ReportDate = %s, want 20251231", items[1].ReportDate)
	}

	filtered, err := service.ListForCode(context.Background(), "600000", 10, "20251001", "20251231")
	if err != nil {
		t.Fatalf("ListForCode filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ReportDate != "20251231" {
		t.Fatalf("filtered reports = %#v, want only 20251231", filtered)
	}
}

func TestPrefetchAllDownloadsHistoricalZipsAndSkipsFuturePlaceholders(t *testing.T) {
	var downloaded int32
	listBody := "gpcw20260331.zip,newhash,100\n" +
		"gpcw20251231.zip,oldhash,100\n" +
		"gpcw20260930.zip,future,164\n"

	q12026 := buildZIPFixture(t, "gpcw20260331.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 13.5,
		fieldTotalShares:       2000000000,
		fieldFloatAShares:      1500000000,
		fieldNetProfitTTM:      1500000000,
		fieldRevenueTTM:        500000,
		fieldWeightedROE:       12.1,
	}))
	fy2025 := buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 12.0,
		fieldTotalShares:       2000000000,
		fieldFloatAShares:      1500000000,
		fieldNetProfitTTM:      1230000000,
		fieldRevenueTTM:        450000,
		fieldWeightedROE:       11.2,
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20260331.zip":
			atomic.AddInt32(&downloaded, 1)
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(q12026)
		case "/gpcw20251231.zip":
			atomic.AddInt32(&downloaded, 1)
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(fy2025)
		case "/gpcw20260930.zip":
			t.Fatalf("future placeholder report should not be downloaded")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	service := NewService(cacheDir, Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
		},
	})

	if err := service.PrefetchAll(context.Background()); err != nil {
		t.Fatalf("PrefetchAll: %v", err)
	}
	if got := atomic.LoadInt32(&downloaded); got != 2 {
		t.Fatalf("downloaded zips = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "artifacts", "zips", "gpcw20260331.zip")); err != nil {
		t.Fatalf("expected cached zip gpcw20260331.zip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "artifacts", "zips", "gpcw20251231.zip")); err != nil {
		t.Fatalf("expected cached zip gpcw20251231.zip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "artifacts", "zips", "gpcw20260930.zip")); !os.IsNotExist(err) {
		t.Fatalf("future placeholder zip should not exist, stat err=%v", err)
	}
}

func TestSyncPersistsRawFactsServingPayloadAndWatermark(t *testing.T) {
	listBody := "gpcw20251231.zip,oldhash,100\n"
	reportZip := buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 13.88,
		fieldTotalShares:       274006894,
		fieldFloatAShares:      168084707,
		fieldNetProfitTTM:      6123456789,
		fieldRevenueTTM:        2456789,
		fieldWeightedROE:       float32(math.NaN()),
		314:                    260328,
		304:                    12345,
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(reportZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(t.TempDir(), Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
		},
	})

	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	db, err := sql.Open("sqlite", service.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for table, wantAtLeast := range map[string]int{
		"prof_finance_source_file":      1,
		"prof_finance_source_report":    1,
		"prof_finance_source_value_raw": 1,
		"prof_finance_report_version":   1,
		"prof_finance_report_payload":   1,
		"prof_finance_source_watermark": 1,
	} {
		var got int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got < wantAtLeast {
			t.Fatalf("%s count = %d, want >= %d", table, got, wantAtLeast)
		}
	}

	var jsonValid int
	if err := db.QueryRow("SELECT json_valid('{}')").Scan(&jsonValid); err != nil {
		t.Fatalf("json_valid check: %v", err)
	}
	if jsonValid != 1 {
		t.Fatalf("json_valid('{}') = %d, want 1", jsonValid)
	}

	if _, err := os.Stat(filepath.Join(service.zipDir, "gpcw20251231.zip")); err != nil {
		t.Fatalf("expected zipped artifact in %s: %v", service.zipDir, err)
	}
}

func TestParseDATReportRawDeduplicatesExactDuplicateRows(t *testing.T) {
	data := buildDATRowsFixture(t, []datRowFixture{
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 14.25,
				fieldRevenueTTM:        2456789,
			},
		},
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 14.25,
				fieldRevenueTTM:        2456789,
			},
		},
	})

	parsed, err := parseDATReportRaw(data, ReportFile{Filename: "gpcw20251231.dat"}, DefaultRegistry())
	if err != nil {
		t.Fatalf("parseDATReportRaw: %v", err)
	}
	if parsed.RowCount != 1 {
		t.Fatalf("row_count = %d, want 1", parsed.RowCount)
	}
	if len(parsed.Rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(parsed.Rows))
	}
}

func TestParseDATReportRawDeduplicatesRowsWhenOnlyUntrackedFieldsDiffer(t *testing.T) {
	data := buildDATRowsFixture(t, []datRowFixture{
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 14.25,
				fieldRevenueTTM:        2456789,
				337:                    13.67,
			},
		},
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 14.25,
				fieldRevenueTTM:        2456789,
				337:                    30.0,
			},
		},
	})

	parsed, err := parseDATReportRaw(data, ReportFile{Filename: "gpcw20251231.dat"}, DefaultRegistry())
	if err != nil {
		t.Fatalf("parseDATReportRaw: %v", err)
	}
	if parsed.RowCount != 1 {
		t.Fatalf("row_count = %d, want 1", parsed.RowCount)
	}
}

func TestParseDATReportRawRejectsRowsWhenTrackedFieldsConflict(t *testing.T) {
	data := buildDATRowsFixture(t, []datRowFixture{
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 14.25,
				fieldRevenueTTM:        2456789,
			},
		},
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 15.01,
				fieldRevenueTTM:        2456789,
			},
		},
	})

	_, err := parseDATReportRaw(data, ReportFile{Filename: "gpcw20251231.dat"}, DefaultRegistry())
	if err == nil {
		t.Fatal("parseDATReportRaw error = nil, want duplicate tracked field conflict")
	}
	if !strings.Contains(err.Error(), "duplicate full_code") {
		t.Fatalf("error = %v, want duplicate full_code", err)
	}
}

func TestSyncDeduplicatesRowsWhenOnlyUntrackedFieldsDiffer(t *testing.T) {
	listBody := "gpcw20251231.zip,dup-untracked-hash,100\n"
	reportZip := buildZIPFixture(t, "gpcw20251231.dat", buildDATRowsFixture(t, []datRowFixture{
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 14.25,
				fieldTotalShares:       4563803648,
				fieldFloatAShares:      2789381888,
				fieldRevenueTTM:        31238330,
				337:                    13.67,
			},
		},
		{
			Code: "300750",
			Fields: map[int]float32{
				fieldBookValuePerShare: 14.25,
				fieldTotalShares:       4563803648,
				fieldFloatAShares:      2789381888,
				fieldRevenueTTM:        31238330,
				337:                    30.0,
			},
		},
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(reportZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(t.TempDir(), Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
		},
	})

	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	db, err := sql.Open("sqlite", service.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var reportVersionCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prof_finance_report_version WHERE full_code = ?`, "sz300750").Scan(&reportVersionCount); err != nil {
		t.Fatalf("count report_version: %v", err)
	}
	if reportVersionCount != 1 {
		t.Fatalf("report_version count = %d, want 1", reportVersionCount)
	}
}

func TestHistoryUsesServingVisibilityAndMissingFields(t *testing.T) {
	listBody := "gpcw20260331.zip,newhash,100\n" +
		"gpcw20251231.zip,oldhash,100\n"

	q12026 := buildZIPFixture(t, "gpcw20260331.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 14.12,
		fieldTotalShares:       274006894,
		fieldFloatAShares:      168084707,
		fieldNetProfitTTM:      7123456789,
		fieldRevenueTTM:        2754321,
		fieldWeightedROE:       13.1,
		314:                    260430,
	}))
	fy2025 := buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 13.88,
		fieldTotalShares:       274006894,
		fieldFloatAShares:      168084707,
		fieldNetProfitTTM:      6123456789,
		fieldRevenueTTM:        2456789,
		fieldWeightedROE:       float32(math.NaN()),
		314:                    260328,
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20260331.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(q12026)
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(fy2025)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(t.TempDir(), Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
		},
	})

	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	result, err := service.History(context.Background(), HistoryQuery{
		FullCode:   "sh600000",
		FieldCodes: []string{"book_value_per_share", "operating_revenue_ttm", "weighted_roe"},
		AsOfDate:   "20260417",
		Limit:      10,
		Period:     "all",
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	if result.FullCode != "sh600000" {
		t.Fatalf("full_code = %s, want sh600000", result.FullCode)
	}
	if result.KnowledgeCutoff != "20260417" {
		t.Fatalf("knowledge_cutoff = %s, want 20260417", result.KnowledgeCutoff)
	}
	if len(result.List) != 1 {
		t.Fatalf("history count = %d, want 1 visible report", len(result.List))
	}
	if result.List[0].ReportDate != "20251231" {
		t.Fatalf("report_date = %s, want 20251231", result.List[0].ReportDate)
	}
	if result.List[0].AnnounceDate != "20260328" {
		t.Fatalf("announce_date = %s, want 20260328", result.List[0].AnnounceDate)
	}
	if _, ok := result.List[0].FieldValues["weighted_roe"]; ok {
		t.Fatalf("weighted_roe should be absent when source value is missing")
	}
	if got := result.List[0].MissingFields; len(got) != 1 || got[0] != "weighted_roe" {
		t.Fatalf("missing_fields = %#v, want [weighted_roe]", got)
	}
}

func TestSnapshotHonorsRestatementVisibilityByAsOfDate(t *testing.T) {
	var (
		mu       sync.RWMutex
		listBody = "gpcw20251231.zip,oldhash,100\n"
		zipBody  = buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
			fieldBookValuePerShare: 13.88,
			fieldTotalShares:       274006894,
			fieldFloatAShares:      168084707,
			fieldNetProfitTTM:      6123456789,
			fieldRevenueTTM:        2456789,
			314:                    260328,
		}))
		currentNow = time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(t.TempDir(), Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return currentNow
		},
	})

	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	mu.Lock()
	listBody = "gpcw20251231.zip,newhash,100\n"
	zipBody = buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 15.01,
		fieldTotalShares:       274006894,
		fieldFloatAShares:      168084707,
		fieldNetProfitTTM:      6500000000,
		fieldRevenueTTM:        2500000,
		314:                    260501,
	}))
	mu.Unlock()
	currentNow = time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)

	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	beforeCutoff, err := service.Snapshot(context.Background(), SnapshotQuery{
		FullCode:   "sh600000",
		ReportDate: "20251231",
		FieldCodes: []string{"book_value_per_share"},
		AsOfDate:   "20260417",
		PeriodMode: "exact",
	})
	if err != nil {
		t.Fatalf("Snapshot before cutoff: %v", err)
	}
	assertFloatApprox(t, beforeCutoff.FieldValues["book_value_per_share"].(float64), 13.88, "book_value_per_share before cutoff")

	afterCutoff, err := service.Snapshot(context.Background(), SnapshotQuery{
		FullCode:   "sh600000",
		ReportDate: "20251231",
		FieldCodes: []string{"book_value_per_share"},
		AsOfDate:   "20260510",
		PeriodMode: "exact",
	})
	if err != nil {
		t.Fatalf("Snapshot after cutoff: %v", err)
	}
	assertFloatApprox(t, afterCutoff.FieldValues["book_value_per_share"].(float64), 15.01, "book_value_per_share after cutoff")

	latestReport, err := service.Snapshot(context.Background(), SnapshotQuery{
		FullCode:   "sh600000",
		FieldCodes: []string{"book_value_per_share"},
		PeriodMode: "latest_report",
	})
	if err != nil {
		t.Fatalf("Snapshot latest_report: %v", err)
	}
	assertFloatApprox(t, latestReport.FieldValues["book_value_per_share"].(float64), 15.01, "book_value_per_share latest_report")

	db, err := sql.Open("sqlite", service.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var versionCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM prof_finance_report_version WHERE full_code = ? AND report_date = ?", "sh600000", "20251231").Scan(&versionCount); err != nil {
		t.Fatalf("count report versions: %v", err)
	}
	if versionCount != 2 {
		t.Fatalf("report_version count = %d, want 2", versionCount)
	}
}

func TestRebuildRestoresServingLayerFromRawFacts(t *testing.T) {
	listBody := "gpcw20251231.zip,oldhash,100\n"
	reportZip := buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 13.88,
		fieldTotalShares:       274006894,
		fieldFloatAShares:      168084707,
		fieldNetProfitTTM:      6123456789,
		fieldRevenueTTM:        2456789,
		314:                    260328,
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(reportZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(t.TempDir(), Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
		},
	})

	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	db, err := sql.Open("sqlite", service.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("DELETE FROM prof_finance_report_payload"); err != nil {
		t.Fatalf("delete payload: %v", err)
	}
	if _, err := db.Exec("DELETE FROM prof_finance_report_version"); err != nil {
		t.Fatalf("delete version: %v", err)
	}

	if err := service.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	result, err := service.History(context.Background(), HistoryQuery{
		FullCode:   "sh600000",
		FieldCodes: []string{"book_value_per_share", "operating_revenue_ttm"},
		AsOfDate:   "20260417",
		Limit:      10,
		Period:     "all",
	})
	if err != nil {
		t.Fatalf("History after rebuild: %v", err)
	}
	if len(result.List) != 1 {
		t.Fatalf("history count after rebuild = %d, want 1", len(result.List))
	}
	assertFloatApprox(t, result.List[0].FieldValues["book_value_per_share"].(float64), 13.88, "book_value_per_share after rebuild")
}

func TestRebuildPreservesFallbackVisibilityAndWatermark(t *testing.T) {
	listBody := "gpcw20251231.zip,oldhash,100\n"
	reportZip := buildZIPFixture(t, "gpcw20251231.dat", buildDATFixture(t, "600000", map[int]float32{
		fieldBookValuePerShare: 13.88,
		fieldTotalShares:       274006894,
		fieldFloatAShares:      168084707,
		fieldNetProfitTTM:      6123456789,
		fieldRevenueTTM:        2456789,
	}))

	currentNow := time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(reportZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(t.TempDir(), Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return currentNow
		},
	})

	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	before, err := service.Snapshot(context.Background(), SnapshotQuery{
		FullCode:   "sh600000",
		FieldCodes: []string{"book_value_per_share"},
		AsOfDate:   "20260510",
		PeriodMode: "latest_available",
	})
	if err != nil {
		t.Fatalf("Snapshot before rebuild: %v", err)
	}
	if before.AnnounceDate != "20260418" || before.KnowledgeCutoff != "20260418" {
		t.Fatalf("unexpected pre-rebuild visibility %#v", before)
	}

	currentNow = time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	if err := service.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	after, err := service.Snapshot(context.Background(), SnapshotQuery{
		FullCode:   "sh600000",
		FieldCodes: []string{"book_value_per_share"},
		AsOfDate:   "20260510",
		PeriodMode: "latest_available",
	})
	if err != nil {
		t.Fatalf("Snapshot after rebuild: %v", err)
	}
	if after.AnnounceDate != "20260418" || after.KnowledgeCutoff != "20260418" {
		t.Fatalf("unexpected post-rebuild visibility %#v", after)
	}

	db, err := sql.Open("sqlite", service.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var watermarkDate string
	if err := db.QueryRow("SELECT watermark_date FROM prof_finance_source_watermark WHERE source_name = ?", "gpcw").Scan(&watermarkDate); err != nil {
		t.Fatalf("query watermark: %v", err)
	}
	if watermarkDate != "20260418" {
		t.Fatalf("watermark_date = %s, want 20260418", watermarkDate)
	}
}

func buildZIPFixture(t *testing.T, filename string, dat []byte) []byte {
	t.Helper()

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	file, err := zw.Create(filename)
	if err != nil {
		t.Fatalf("Create zip entry: %v", err)
	}
	if _, err := file.Write(dat); err != nil {
		t.Fatalf("Write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip writer: %v", err)
	}
	return buf.Bytes()
}

func buildDATFixture(t *testing.T, code string, fields map[int]float32) []byte {
	t.Helper()
	return buildDATRowsFixture(t, []datRowFixture{{Code: code, Fields: fields}})
}

type datRowFixture struct {
	Code   string
	Fields map[int]float32
}

func buildDATRowsFixture(t *testing.T, rows []datRowFixture) []byte {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("rows must not be empty")
	}

	const reportDate uint32 = 20251231
	const headerSize = 20
	const stockItemSize = 11

	reportFieldCount := minTrackedFieldID
	for _, row := range rows {
		for fieldID := range row.Fields {
			if fieldID > reportFieldCount {
				reportFieldCount = fieldID
			}
		}
	}
	reportSize := reportFieldCount * 4

	rowOffset := headerSize + len(rows)*stockItemSize
	buf := make([]byte, rowOffset+len(rows)*reportSize)

	binary.LittleEndian.PutUint16(buf[0:2], 1)
	binary.LittleEndian.PutUint32(buf[2:6], reportDate)
	binary.LittleEndian.PutUint16(buf[6:8], uint16(len(rows)))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(reportSize))

	for idx, row := range rows {
		headerOffset := headerSize + idx*stockItemSize
		copy(buf[headerOffset:headerOffset+6], []byte(row.Code))
		buf[headerOffset+6] = '1'
		currentRowOffset := rowOffset + idx*reportSize
		binary.LittleEndian.PutUint32(buf[headerOffset+7:headerOffset+11], uint32(currentRowOffset))

		for fieldID := 1; fieldID <= reportFieldCount; fieldID++ {
			fieldOffset := currentRowOffset + (fieldID-1)*4
			binary.LittleEndian.PutUint32(buf[fieldOffset:fieldOffset+4], mathFloat32bits(float32(math.NaN())))
		}

		for fieldID, value := range row.Fields {
			fieldOffset := currentRowOffset + (fieldID-1)*4
			binary.LittleEndian.PutUint32(buf[fieldOffset:fieldOffset+4], mathFloat32bits(value))
		}
	}
	return buf
}

func mathFloat32bits(v float32) uint32 {
	return math.Float32bits(v)
}

func assertFloatApprox(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}
