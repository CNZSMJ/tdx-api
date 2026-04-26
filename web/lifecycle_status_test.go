package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx/collector/lifecycle"
)

func TestHandleCollectorLifecycleStatusReturnsManifestSummary(t *testing.T) {
	tmp := t.TempDir()
	originalDir := databaseDir
	databaseDir = tmp
	defer func() { databaseDir = originalDir }()

	store, err := lifecycle.OpenManifestStore(filepath.Join(tmp, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	if err := store.CreateSegment(lifecycle.ColdSegment{
		SegmentID:      "seg-1",
		ArchiveBatchID: "batch-1",
		Domain:         "trade",
		TableName:      "TradeHistory",
		Instrument:     "sh600000",
		StartDate:      "20230101",
		EndDate:        "20230131",
		ColdURI:        "tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2023/part-test-000.parquet",
		Status:         lifecycle.SegmentActive,
		RowCount:       10,
		ByteSize:       2048,
		SchemaVersion:  1,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("create segment: %v", err)
	}
	_ = store.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/collector/lifecycle/status", nil)
	handleCollectorLifecycleStatus(rec, req)
	var payload Response
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Code != 0 {
		t.Fatalf("unexpected response: %+v body=%s", payload, rec.Body.String())
	}
	data, ok := payload.Data.(map[string]any)
	if !ok || data["segment_counts"] == nil {
		t.Fatalf("missing lifecycle status data: %+v", payload.Data)
	}
}
