package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
)

var deepAuditBackfill *systemgov.DeepAuditBackfillRunner

func collectorDeepAuditBackfillSchedule() string {
	return strings.TrimSpace(os.Getenv("COLLECTOR_DEEP_AUDIT_SCHEDULE"))
}

func initDeepAuditBackfillRunner() {
	if governanceStore == nil || collectorRuntime == nil {
		return
	}
	runner, err := systemgov.NewDeepAuditBackfillRunner(systemgov.DeepAuditBackfillConfig{
		Store: governanceStore,
		Paths: governancePaths,
		Now:   time.Now,
		Execute: func(ctx context.Context, req systemgov.DeepAuditBackfillRequest, trigger string) (*systemgov.DeepAuditBackfillResult, error) {
			return executeDeepAuditBackfill(ctx, req, trigger)
		},
	})
	if err != nil {
		log.Printf("初始化 deep_audit_backfill runner 失败: %v", err)
		return
	}
	deepAuditBackfill = runner
}

func runDeepAuditBackfill(trigger string, req systemgov.DeepAuditBackfillRequest) (*collectorpkg.GovernanceRunRecord, error) {
	if isServiceShuttingDown() {
		return nil, fmt.Errorf("service shutdown in progress, skip deep_audit_backfill: trigger=%s", trigger)
	}
	if deepAuditBackfill == nil {
		return nil, fmt.Errorf("deep_audit_backfill runner 未初始化")
	}
	run, err := deepAuditBackfill.Run(context.Background(), trigger, req)
	if err != nil {
		return nil, err
	}
	log.Printf("deep_audit_backfill 完成: trigger=%s status=%s target=%s", trigger, run.Status, run.TargetWindow)
	return run, nil
}

func executeDeepAuditBackfill(ctx context.Context, req systemgov.DeepAuditBackfillRequest, trigger string) (*systemgov.DeepAuditBackfillResult, error) {
	if collectorRuntime == nil {
		return nil, fmt.Errorf("collector runtime 未初始化")
	}

	dates, err := collectorRuntime.ResolveDeepAuditDates(ctx, req.StartDate, req.EndDate, req.BacklogOnly, req.Limit)
	if err != nil {
		return nil, err
	}
	result := &systemgov.DeepAuditBackfillResult{
		Reports: make([]systemgov.AuditResult, 0, len(dates)),
	}
	for _, date := range dates {
		report, err := collectorRuntime.ReconcileDateWithTrigger(ctx, date, trigger)
		if err != nil {
			return nil, err
		}
		if audit := auditResultFromReconcileReport(report); audit != nil {
			result.Reports = append(result.Reports, *audit)
		}
	}

	summaryPath, err := writeDeepAuditSummary(trigger, req, result.Reports)
	if err != nil {
		return nil, err
	}
	result.SummaryPath = summaryPath
	return result, nil
}

func writeDeepAuditSummary(trigger string, req systemgov.DeepAuditBackfillRequest, reports []systemgov.AuditResult) (string, error) {
	if strings.TrimSpace(governancePaths.ReportsDir) == "" {
		return "", nil
	}
	if err := os.MkdirAll(governancePaths.ReportsDir, 0o755); err != nil {
		return "", err
	}

	filename := filepath.Join(governancePaths.ReportsDir, fmt.Sprintf("deep-audit-summary-%d.json", time.Now().UnixNano()))
	payload := map[string]any{
		"trigger":      trigger,
		"start_date":   req.StartDate,
		"end_date":     req.EndDate,
		"backlog_only": req.BacklogOnly,
		"limit":        req.Limit,
		"reports":      reports,
		"generated_at": time.Now().Format(time.RFC3339Nano),
	}
	bs, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filename, bs, 0o644); err != nil {
		return "", err
	}
	return filename, nil
}

func handleCollectorDeepAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}

	var req systemgov.DeepAuditBackfillRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.StartDate == "" {
		req.StartDate = strings.TrimSpace(r.URL.Query().Get("start_date"))
	}
	if req.EndDate == "" {
		req.EndDate = strings.TrimSpace(r.URL.Query().Get("end_date"))
	}
	if !req.BacklogOnly {
		req.BacklogOnly = strings.TrimSpace(r.URL.Query().Get("backlog_only")) == "1"
	}
	if req.Limit == 0 {
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			var parsed int
			if _, err := fmt.Sscanf(raw, "%d", &parsed); err == nil {
				req.Limit = parsed
			}
		}
	}

	run, err := runDeepAuditBackfill("manual-deep-audit", req)
	if err != nil {
		errorResponse(w, "执行 deep audit backfill 失败: "+err.Error())
		return
	}
	successResponse(w, run)
}
