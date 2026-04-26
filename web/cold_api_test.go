package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/tdx/collector/lifecycle"
)

func TestColdSegmentsAndTradeHistoryAPIAreExplicitAndLocalAdminGated(t *testing.T) {
	tmp := t.TempDir()
	originalDir := databaseDir
	databaseDir = tmp
	defer func() { databaseDir = originalDir }()

	sourceDB := filepath.Join(tmp, "source.db")
	mustCreateWebSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
	})
	storage := lifecycle.NewLocalColdStorage(tmp)
	uri := lifecycle.MustFormatColdURI(lifecycle.ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-api-000.parquet"})
	result, err := lifecycle.ExportSegment(lifecycle.ExportRequest{
		SourceDBPath: sourceDB,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600000",
		StartDate:    "20240101",
		EndDate:      "20240131",
		Storage:      storage,
		ColdURI:      uri,
	})
	if err != nil {
		t.Fatalf("export segment: %v", err)
	}
	store, err := lifecycle.OpenManifestStore(filepath.Join(tmp, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	if err := store.CreateSegment(lifecycle.ColdSegment{
		SegmentID:       "seg-api",
		ArchiveBatchID:  "batch-api",
		Domain:          "trade",
		TableName:       "TradeHistory",
		Instrument:      "sh600000",
		StartDate:       "20240101",
		EndDate:         "20240131",
		ColdURI:         uri,
		Status:          lifecycle.SegmentActive,
		RowCount:        result.RowCount,
		ByteSize:        result.ByteSize,
		FileChecksum:    result.FileChecksum,
		LogicalChecksum: result.LogicalChecksum,
		SchemaVersion:   1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}); err != nil {
		t.Fatalf("create manifest segment: %v", err)
	}
	_ = store.Close()

	blocked := httptest.NewRecorder()
	blockedReq := httptest.NewRequest(http.MethodGet, "/api/v1/cold/segments", nil)
	blockedReq.RemoteAddr = "192.0.2.10:1234"
	handleColdSegments(blocked, blockedReq)
	var blockedPayload Response
	if err := json.Unmarshal(blocked.Body.Bytes(), &blockedPayload); err != nil {
		t.Fatalf("unmarshal blocked: %v", err)
	}
	if blockedPayload.Code != -1 {
		t.Fatalf("expected gated cold API, got %+v", blockedPayload)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cold/trade-history?code=sh600000&start_date=20240101&end_date=20240131&limit=10", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	handleColdTradeHistory(rec, req)
	var payload Response
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal cold trade history: %v body=%s", err, rec.Body.String())
	}
	if payload.Code != 0 {
		t.Fatalf("unexpected cold trade response: %+v body=%s", payload, rec.Body.String())
	}
	data := payload.Data.(map[string]any)
	if int(data["count"].(float64)) != 1 {
		t.Fatalf("cold trade count data=%+v", data)
	}
}

func TestAllowColdAPIRejectsSpoofedLocalAdminHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cold/segments", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Local-Admin", "true")

	if allowColdAPI(req) {
		t.Fatalf("remote request with client-supplied X-Local-Admin must not be accepted")
	}
}

func mustCreateWebSQLite(t *testing.T, path string, stmts []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir sqlite dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}
