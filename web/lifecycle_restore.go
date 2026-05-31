package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/collector/lifecycle"
	systemgov "github.com/injoyai/tdx/governance"
)

var dataLifecycleRestore *systemgov.DataLifecycleRestoreRunner

func initDataLifecycleRestoreRunner() {
	dataLifecycleRestore = nil
	if governanceStore == nil {
		return
	}
	runner, err := systemgov.NewDataLifecycleRestoreRunner(systemgov.DataLifecycleRestoreConfig{
		Store: governanceStore,
		Paths: governancePaths,
		Now:   time.Now,
		Execute: func(ctx context.Context, req systemgov.DataLifecycleRestoreRequest) (lifecycle.RestoreResult, error) {
			return executeDataLifecycleRestore(ctx, req)
		},
	})
	if err != nil {
		log.Printf("初始化 data_lifecycle_restore runner 失败: %v", err)
		return
	}
	dataLifecycleRestore = runner
}

func runDataLifecycleRestoreWithContext(ctx context.Context, trigger string, req systemgov.DataLifecycleRestoreRequest) (*collectorpkg.GovernanceRunRecord, error) {
	if dataLifecycleRestore == nil {
		return nil, fmt.Errorf("data_lifecycle_restore runner 未初始化")
	}
	run, err := dataLifecycleRestore.Run(ctx, trigger, req)
	if err != nil {
		return nil, err
	}
	log.Printf("data_lifecycle_restore 完成: trigger=%s status=%s target=%s details=%s", trigger, run.Status, run.TargetWindow, run.Details)
	return run, nil
}

func executeDataLifecycleRestore(ctx context.Context, req systemgov.DataLifecycleRestoreRequest) (lifecycle.RestoreResult, error) {
	if err := ctx.Err(); err != nil {
		return lifecycle.RestoreResult{}, err
	}
	manifest, err := lifecycle.OpenManifestStore(filepath.Join(databaseDir, "cold_manifest.db"))
	if err != nil {
		return lifecycle.RestoreResult{}, err
	}
	defer manifest.Close()

	segment, export, err := verifiedRestoreExport(manifest, req.SegmentID)
	if err != nil {
		return lifecycle.RestoreResult{}, err
	}
	mode := strings.TrimSpace(req.Mode)
	result, err := restoreColdSegment(mode, *segment, export, strings.TrimSpace(req.TargetPath))
	if err != nil {
		return lifecycle.RestoreResult{}, err
	}
	_ = manifest.CreateRestore(lifecycle.LifecycleRestore{
		RestoreID:              fmt.Sprintf("%s:%s:%d", segment.SegmentID, mode, time.Now().UnixNano()),
		SegmentID:              segment.SegmentID,
		RestoreMode:            mode,
		Status:                 "passed",
		TargetPath:             result.TargetDBPath,
		ExpiresAt:              restoreExpiry(mode),
		RetentionOverrideUntil: retentionOverrideUntil(mode),
		CreatedAt:              time.Now().UTC(),
		UpdatedAt:              time.Now().UTC(),
	})
	return result, nil
}

func verifiedRestoreExport(manifest *lifecycle.ManifestStore, segmentID string) (*lifecycle.ColdSegment, lifecycle.SegmentExportResult, error) {
	segment, err := manifest.GetSegment(segmentID)
	if err != nil {
		return nil, lifecycle.SegmentExportResult{}, err
	}
	if segment == nil {
		return nil, lifecycle.SegmentExportResult{}, fmt.Errorf("cold segment not found: %s", segmentID)
	}
	if segment.Status != lifecycle.SegmentActive {
		return nil, lifecycle.SegmentExportResult{}, fmt.Errorf("cold segment %s is not active: status=%s", segmentID, segment.Status)
	}
	export := lifecycle.SegmentExportResult{
		TableName:       segment.TableName,
		Domain:          segment.Domain,
		Instrument:      segment.Instrument,
		StartDate:       segment.StartDate,
		EndDate:         segment.EndDate,
		ColdURI:         segment.ColdURI,
		Storage:         lifecycle.NewLocalColdStorage(databaseDir),
		RowCount:        segment.RowCount,
		MinDate:         segment.StartDate,
		MaxDate:         segment.EndDate,
		FileChecksum:    segment.FileChecksum,
		LogicalChecksum: segment.LogicalChecksum,
		SchemaVersion:   segment.SchemaVersion,
	}
	if err := lifecycle.VerifySegment(export); err != nil {
		return nil, lifecycle.SegmentExportResult{}, err
	}
	return segment, export, nil
}

func restoreColdSegment(mode string, segment lifecycle.ColdSegment, export lifecycle.SegmentExportResult, target string) (lifecycle.RestoreResult, error) {
	switch mode {
	case "temporary_query_restore":
		if target == "" {
			target = filepath.Join(databaseDir, "cold_restore", segment.SegmentID+"-"+segment.TableName+".db")
		}
		return lifecycle.RestoreSegment(export, target)
	case "hot_path_restore":
		if target == "" {
			var err error
			target, err = hotDBPathForColdSegment(segment)
			if err != nil {
				return lifecycle.RestoreResult{}, err
			}
		}
		return lifecycle.RehydrateSegmentToHotPath(export, target)
	default:
		return lifecycle.RestoreResult{}, fmt.Errorf("unsupported lifecycle restore mode: %s", mode)
	}
}

func hotDBPathForColdSegment(segment lifecycle.ColdSegment) (string, error) {
	switch segment.Domain {
	case "trade":
		return filepath.Join(databaseDir, "trade", segment.Instrument+".db"), nil
	case "order_history":
		return filepath.Join(databaseDir, "order_history", segment.Instrument+".db"), nil
	case "auction":
		return filepath.Join(databaseDir, "auction", "auction.db"), nil
	case "live":
		if segment.TableName == "QuoteSnapshot" {
			return filepath.Join(databaseDir, "live", "quotes.db"), nil
		}
		return filepath.Join(databaseDir, "live", segment.Instrument+".db"), nil
	default:
		return "", fmt.Errorf("unsupported hot path restore domain: %s", segment.Domain)
	}
}

func restoreExpiry(mode string) time.Time {
	if mode != "temporary_query_restore" {
		return time.Time{}
	}
	return time.Now().Add(24 * time.Hour)
}

func retentionOverrideUntil(mode string) time.Time {
	if mode != "hot_path_restore" {
		return time.Time{}
	}
	return time.Now().Add(7 * 24 * time.Hour)
}

func handleColdRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	if !allowColdAPI(r) {
		errorResponse(w, "cold restore requires local-admin access")
		return
	}
	var req systemgov.DataLifecycleRestoreRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.SegmentID == "" {
		req.SegmentID = strings.TrimSpace(r.URL.Query().Get("segment_id"))
	}
	if req.Mode == "" {
		req.Mode = strings.TrimSpace(r.URL.Query().Get("mode"))
	}
	if req.TargetPath == "" {
		req.TargetPath = strings.TrimSpace(r.URL.Query().Get("target_path"))
	}
	run, err := runDataLifecycleRestoreWithContext(r.Context(), "manual-cold-restore", req)
	if err != nil {
		errorResponse(w, "执行 cold restore 失败: "+err.Error())
		return
	}
	successResponse(w, run)
}
