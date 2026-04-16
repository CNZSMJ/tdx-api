package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

var collectorControl = newCollectorControlState()

type collectorControlSnapshot struct {
	Paused      bool      `json:"paused"`
	PauseReason string    `json:"pause_reason,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type collectorControlState struct {
	mu          sync.RWMutex
	paused      bool
	pauseReason string
	updatedAt   time.Time
}

type collectorPauseResult struct {
	Control   collectorControlSnapshot `json:"control"`
	ActiveJob string                   `json:"active_job,omitempty"`
	WasActive bool                     `json:"was_active"`
	Stopped   bool                     `json:"stopped"`
	TimedOut  bool                     `json:"timed_out"`
}

type collectorStartResult struct {
	Control        collectorControlSnapshot `json:"control"`
	StartNow       bool                     `json:"start_now"`
	Launched       bool                     `json:"launched"`
	AlreadyRunning bool                     `json:"already_running"`
	Trigger        string                   `json:"trigger,omitempty"`
}

func newCollectorControlState() *collectorControlState {
	state := &collectorControlState{}
	if collectorStartPausedFromEnv() {
		state.paused = true
		state.pauseReason = "startup paused via env"
		state.updatedAt = time.Now()
	}
	return state
}

func collectorStartPausedFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COLLECTOR_START_PAUSED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *collectorControlState) pause(reason string) collectorControlSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.paused = true
	s.pauseReason = strings.TrimSpace(reason)
	if s.pauseReason == "" {
		s.pauseReason = "manual pause"
	}
	s.updatedAt = time.Now()
	return s.snapshotLocked()
}

func (s *collectorControlState) resume() collectorControlSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.paused = false
	s.pauseReason = ""
	s.updatedAt = time.Now()
	return s.snapshotLocked()
}

func (s *collectorControlState) isPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.paused
}

func (s *collectorControlState) snapshot() collectorControlSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *collectorControlState) snapshotLocked() collectorControlSnapshot {
	return collectorControlSnapshot{
		Paused:      s.paused,
		PauseReason: s.pauseReason,
		UpdatedAt:   s.updatedAt,
	}
}

func collectorTriggerUsesStartupProfile(trigger string) bool {
	switch strings.TrimSpace(trigger) {
	case "startup", "manual-startup":
		return true
	default:
		return false
	}
}

func pauseCollectorExecution(reason string, wait time.Duration) (*collectorPauseResult, error) {
	if wait <= 0 {
		wait = 10 * time.Second
	}

	result := &collectorPauseResult{
		Control: collectorControl.pause(reason),
	}
	name, done, ok := collectorActiveRun.cancelActive()
	if !ok {
		return result, nil
	}

	result.ActiveJob = name
	result.WasActive = true
	select {
	case <-done:
		result.Stopped = true
	case <-time.After(wait):
		result.TimedOut = true
		if collectorRuntime != nil {
			if err := collectorRuntime.InterruptRunningScheduleRuns("collector manually paused"); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func startCollectorExecution(mode string, startNow bool) (*collectorStartResult, error) {
	trigger, err := manualCollectorCatchUpTrigger(mode)
	if err != nil {
		return nil, err
	}

	result := &collectorStartResult{
		Control:  collectorControl.resume(),
		StartNow: startNow,
		Trigger:  trigger,
	}
	if !startNow {
		return result, nil
	}
	if collectorRunActive.Load() {
		result.AlreadyRunning = true
		return result, nil
	}

	result.Launched = true
	go func() {
		if err := runCollectorCatchUp(trigger); err != nil {
			log.Printf("collector 手动启动失败: trigger=%s err=%v", trigger, err)
		}
	}()
	return result, nil
}

func runCollectorKlineGapReconcile(opts collectorpkg.KlineGapReconcileOptions) (*collectorpkg.KlineGapReconcileReport, error) {
	if isServiceShuttingDown() {
		return nil, fmt.Errorf("collector shutdown in progress, skip kline gap reconcile")
	}
	if collectorRuntime == nil {
		return nil, fmt.Errorf("collector runtime 未初始化，跳过 kline gap 定向补采")
	}
	if !collectorRunActive.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("collector 任务仍在运行，跳过 kline gap 定向补采")
	}
	defer collectorRunActive.Store(false)

	jobName := "manual_kline_gap_reconcile"
	dateLabel := strings.TrimSpace(opts.StartDate)
	if dateLabel == "" && strings.TrimSpace(opts.EndDate) != "" {
		dateLabel = strings.TrimSpace(opts.EndDate)
	}
	job := collectorJobState.start(jobName, "manual-gap-reconcile", dateLabel)
	ctx, cancel := context.WithTimeout(context.Background(), collectorGeneralRunTimeout())
	endRun := collectorActiveRun.begin(jobName, cancel)
	defer func() {
		cancel()
		endRun()
	}()

	started := time.Now()
	report, err := collectorRuntime.ReconcileKlineGaps(ctx, opts)
	if err != nil {
		collectorJobState.finish(job, collectorJobFailureStatus(err), "", err)
		log.Printf("collector kline gap 定向补采失败: duration=%s err=%v", time.Since(started), err)
		return nil, err
	}

	status := "passed"
	if len(report.Errors) > 0 {
		status = "partial"
	}
	collectorJobState.finish(job, status, "", nil)
	log.Printf("collector kline gap 定向补采完成: status=%s duration=%s planned=%d executed=%d failed=%d remaining=%d",
		status, time.Since(started), report.Planned, report.Executed, report.Failed, report.RemainingOpenGaps)
	return report, nil
}

func manualCollectorCatchUpTrigger(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", "startup":
		return "manual-startup", nil
	case "full_sync", "daily_full_sync":
		return "manual-full-sync", nil
	default:
		return "", fmt.Errorf("unsupported collector start mode: %s", mode)
	}
}

func handleCollectorControl(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		successResponse(w, map[string]any{
			"control": collectorControl.snapshot(),
			"active":  collectorRunActive.Load(),
		})
	case http.MethodPost:
		var req struct {
			Action      string `json:"action"`
			Reason      string `json:"reason"`
			StartNow    *bool  `json:"start_now"`
			WaitSeconds int    `json:"wait_seconds"`
			Mode        string `json:"mode"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		action := strings.TrimSpace(req.Action)
		if action == "" {
			action = strings.TrimSpace(r.URL.Query().Get("action"))
		}
		switch action {
		case "stop":
			wait := time.Duration(req.WaitSeconds) * time.Second
			result, err := pauseCollectorExecution(req.Reason, wait)
			if err != nil {
				errorResponse(w, "暂停 collector 失败: "+err.Error())
				return
			}
			successResponse(w, result)
		case "start":
			startNow := true
			if req.StartNow != nil {
				startNow = *req.StartNow
			}
			result, err := startCollectorExecution(req.Mode, startNow)
			if err != nil {
				errorResponse(w, "启动 collector 失败: "+err.Error())
				return
			}
			successResponse(w, result)
		default:
			errorResponse(w, "只支持 action=start 或 action=stop")
		}
	default:
		errorResponse(w, "只支持GET和POST请求")
	}
}

func handleCollectorKlineGapCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	if collectorRuntime == nil {
		errorResponse(w, "collector runtime 未初始化")
		return
	}

	var req struct {
		AssetType  string `json:"asset_type"`
		Instrument string `json:"instrument"`
		Period     string `json:"period"`
		Limit      int    `json:"limit"`
		DryRun     *bool  `json:"dry_run"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	if !dryRun {
		if !collectorControl.isPaused() {
			errorResponse(w, "正式清理前必须先暂停 collector")
			return
		}
		if collectorRunActive.Load() {
			errorResponse(w, "collector 任务仍在运行，不能执行正式清理")
			return
		}
	}

	opts, err := buildKlineGapCleanupOptions(req.AssetType, req.Instrument, req.Period, req.Limit, dryRun)
	if err != nil {
		errorResponse(w, "构建清理参数失败: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	report, err := collectorRuntime.CleanupKlineGaps(ctx, opts)
	if err != nil {
		errorResponse(w, "执行 kline gap 清理失败: "+err.Error())
		return
	}

	successResponse(w, map[string]any{
		"control": collectorControl.snapshot(),
		"report":  report,
	})
}

func handleCollectorKlineGapReconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	if collectorRuntime == nil {
		errorResponse(w, "collector runtime 未初始化")
		return
	}

	var req struct {
		AssetType  string `json:"asset_type"`
		Instrument string `json:"instrument"`
		Period     string `json:"period"`
		Date       string `json:"date"`
		StartDate  string `json:"start_date"`
		EndDate    string `json:"end_date"`
		Limit      int    `json:"limit"`
		DryRun     *bool  `json:"dry_run"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	if !collectorControl.isPaused() {
		errorResponse(w, "执行 kline gap 定向补采前必须先暂停 collector")
		return
	}
	if collectorRunActive.Load() {
		errorResponse(w, "collector 任务仍在运行，不能执行 kline gap 定向补采")
		return
	}

	opts, err := buildKlineGapReconcileOptions(req.AssetType, req.Instrument, req.Period, req.Date, req.StartDate, req.EndDate, req.Limit, dryRun)
	if err != nil {
		errorResponse(w, "构建定向补采参数失败: "+err.Error())
		return
	}

	report, err := runCollectorKlineGapReconcile(opts)
	if err != nil {
		errorResponse(w, "执行 kline gap 定向补采失败: "+err.Error())
		return
	}

	successResponse(w, map[string]any{
		"control": collectorControl.snapshot(),
		"report":  report,
	})
}

func buildKlineGapCleanupOptions(assetTypeRaw, instrument, periodRaw string, limit int, dryRun bool) (collectorpkg.KlineGapCleanupOptions, error) {
	opts := collectorpkg.KlineGapCleanupOptions{
		Instrument: strings.TrimSpace(instrument),
		Limit:      limit,
		DryRun:     dryRun,
	}

	switch strings.TrimSpace(assetTypeRaw) {
	case "", "all":
		opts.AssetType = collectorpkg.AssetTypeUnknown
	case string(collectorpkg.AssetTypeStock):
		opts.AssetType = collectorpkg.AssetTypeStock
	case string(collectorpkg.AssetTypeETF):
		opts.AssetType = collectorpkg.AssetTypeETF
	case string(collectorpkg.AssetTypeIndex):
		opts.AssetType = collectorpkg.AssetTypeIndex
	default:
		return opts, fmt.Errorf("unsupported asset_type: %s", assetTypeRaw)
	}

	switch strings.TrimSpace(periodRaw) {
	case "", "all":
		opts.Period = ""
	case string(collectorpkg.PeriodMinute):
		opts.Period = collectorpkg.PeriodMinute
	case string(collectorpkg.Period5Minute):
		opts.Period = collectorpkg.Period5Minute
	case string(collectorpkg.Period15Minute):
		opts.Period = collectorpkg.Period15Minute
	case string(collectorpkg.Period30Minute):
		opts.Period = collectorpkg.Period30Minute
	case string(collectorpkg.Period60Minute):
		opts.Period = collectorpkg.Period60Minute
	case string(collectorpkg.PeriodDay):
		opts.Period = collectorpkg.PeriodDay
	case string(collectorpkg.PeriodWeek):
		opts.Period = collectorpkg.PeriodWeek
	case string(collectorpkg.PeriodMonth):
		opts.Period = collectorpkg.PeriodMonth
	case string(collectorpkg.PeriodQuarter):
		opts.Period = collectorpkg.PeriodQuarter
	case string(collectorpkg.PeriodYear):
		opts.Period = collectorpkg.PeriodYear
	default:
		return opts, fmt.Errorf("unsupported period: %s", periodRaw)
	}

	return opts, nil
}

func buildKlineGapReconcileOptions(assetTypeRaw, instrument, periodRaw, dateRaw, startDateRaw, endDateRaw string, limit int, dryRun bool) (collectorpkg.KlineGapReconcileOptions, error) {
	cleanupOpts, err := buildKlineGapCleanupOptions(assetTypeRaw, instrument, periodRaw, limit, dryRun)
	if err != nil {
		return collectorpkg.KlineGapReconcileOptions{}, err
	}

	date := strings.TrimSpace(dateRaw)
	startDate := strings.TrimSpace(startDateRaw)
	endDate := strings.TrimSpace(endDateRaw)
	if date != "" && (startDate != "" || endDate != "") {
		return collectorpkg.KlineGapReconcileOptions{}, fmt.Errorf("date 与 start_date/end_date 不能同时提供")
	}
	if date != "" {
		startDate = date
		endDate = date
	}

	return collectorpkg.KlineGapReconcileOptions{
		AssetType:  cleanupOpts.AssetType,
		Instrument: cleanupOpts.Instrument,
		Period:     cleanupOpts.Period,
		StartDate:  startDate,
		EndDate:    endDate,
		Limit:      limit,
		DryRun:     dryRun,
	}, nil
}
