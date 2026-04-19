package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
)

func TestHandleCollectorDeepAuditTriggersIndependentBackfillRunner(t *testing.T) {
	originalRunner := deepAuditBackfill
	originalStore := governanceStore
	originalPaths := governancePaths
	defer func() {
		deepAuditBackfill = originalRunner
		governanceStore = originalStore
		governancePaths = originalPaths
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store

	deepAuditBackfill, err = systemgov.NewDeepAuditBackfillRunner(systemgov.DeepAuditBackfillConfig{
		Store: store,
		Paths: governancePaths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 3, 0, 0, 0, time.Local)
		},
		Execute: func(ctx context.Context, req systemgov.DeepAuditBackfillRequest, trigger string) (*systemgov.DeepAuditBackfillResult, error) {
			if trigger != "manual-deep-audit" {
				t.Fatalf("trigger = %s, want manual-deep-audit", trigger)
			}
			if req.StartDate != "20260301" || req.EndDate != "20260331" || req.Limit != 5 {
				t.Fatalf("request = %+v, want start/end/limit", req)
			}
			return &systemgov.DeepAuditBackfillResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("new deep audit runner: %v", err)
	}

	body := bytes.NewBufferString(`{"start_date":"20260301","end_date":"20260331","limit":5}`)
	req := httptest.NewRequest(http.MethodPost, "/api/collector/deep-audit", body)
	rec := httptest.NewRecorder()
	handleCollectorDeepAudit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Code int `json:"code"`
		Data struct {
			JobName      string `json:"job_name"`
			TargetWindow string `json:"target_window"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Code != 0 {
		t.Fatalf("response code = %d, want 0", payload.Code)
	}
	if payload.Data.JobName != string(collectorpkg.GovernanceJobDeepAuditBackfill) {
		t.Fatalf("job name = %s, want %s", payload.Data.JobName, collectorpkg.GovernanceJobDeepAuditBackfill)
	}
	if payload.Data.TargetWindow != "20260301:20260331" {
		t.Fatalf("target window = %s, want 20260301:20260331", payload.Data.TargetWindow)
	}
}
