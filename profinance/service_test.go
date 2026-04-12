package profinance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
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
	if _, err := os.Stat(filepath.Join(cacheDir, "gpcw20251231.zip")); err != nil {
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
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
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

	const reportDate uint32 = 20251231
	const reportFieldCount = minTrackedFieldID
	const reportSize = reportFieldCount * 4
	const headerSize = 20
	const stockItemSize = 11

	rowOffset := headerSize + stockItemSize
	buf := make([]byte, rowOffset+reportSize)

	binary.LittleEndian.PutUint16(buf[0:2], 1)
	binary.LittleEndian.PutUint32(buf[2:6], reportDate)
	binary.LittleEndian.PutUint16(buf[6:8], 1)
	binary.LittleEndian.PutUint32(buf[12:16], reportSize)

	copy(buf[20:26], []byte(code))
	buf[26] = '1'
	binary.LittleEndian.PutUint32(buf[27:31], uint32(rowOffset))

	for fieldID, value := range fields {
		fieldOffset := rowOffset + (fieldID-1)*4
		binary.LittleEndian.PutUint32(buf[fieldOffset:fieldOffset+4], mathFloat32bits(value))
	}
	return buf
}

func mathFloat32bits(v float32) uint32 {
	return math.Float32bits(v)
}
