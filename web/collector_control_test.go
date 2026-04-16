package main

import (
	"os"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestPauseCollectorExecutionCancelsActiveRun(t *testing.T) {
	originalControl := collectorControl
	originalActiveRun := collectorActiveRun
	defer func() {
		collectorControl = originalControl
		collectorActiveRun = originalActiveRun
	}()

	collectorControl = newCollectorControlState()
	collectorActiveRun = newCollectorActiveRunState()

	var end func()
	cancelCalled := false
	end = collectorActiveRun.begin("startup_catchup", func() {
		cancelCalled = true
		end()
	})

	result, err := pauseCollectorExecution("maintenance window", time.Second)
	if err != nil {
		t.Fatalf("pauseCollectorExecution: %v", err)
	}
	if !cancelCalled {
		t.Fatalf("expected active collector run to be canceled")
	}
	if !result.WasActive || !result.Stopped {
		t.Fatalf("expected active collector run to stop, got %+v", result)
	}
	if !collectorControl.isPaused() {
		t.Fatalf("expected collector to be paused")
	}
	if got := collectorControl.snapshot().PauseReason; got != "maintenance window" {
		t.Fatalf("pause reason = %q, want maintenance window", got)
	}
}

func TestStartCollectorExecutionResumeOnly(t *testing.T) {
	originalControl := collectorControl
	defer func() {
		collectorControl = originalControl
	}()

	collectorControl = newCollectorControlState()
	collectorControl.pause("maintenance window")

	result, err := startCollectorExecution("startup", false)
	if err != nil {
		t.Fatalf("startCollectorExecution: %v", err)
	}
	if result.Launched {
		t.Fatalf("expected resume-only start not to launch collector")
	}
	if collectorControl.isPaused() {
		t.Fatalf("expected collector pause to be cleared")
	}
}

func TestBuildKlineGapCleanupOptions(t *testing.T) {
	opts, err := buildKlineGapCleanupOptions("etf", "sh513623", "15minute", 25, true)
	if err != nil {
		t.Fatalf("buildKlineGapCleanupOptions: %v", err)
	}
	if opts.AssetType != collectorpkg.AssetTypeETF {
		t.Fatalf("asset type = %s, want %s", opts.AssetType, collectorpkg.AssetTypeETF)
	}
	if opts.Period != collectorpkg.Period15Minute {
		t.Fatalf("period = %s, want %s", opts.Period, collectorpkg.Period15Minute)
	}
	if opts.Limit != 25 || !opts.DryRun || opts.Instrument != "sh513623" {
		t.Fatalf("unexpected cleanup options: %+v", opts)
	}

	if _, err := buildKlineGapCleanupOptions("weird", "", "", 0, true); err == nil {
		t.Fatalf("expected invalid asset type error")
	}
	if _, err := buildKlineGapCleanupOptions("", "", "weird", 0, true); err == nil {
		t.Fatalf("expected invalid period error")
	}
}

func TestBuildKlineGapReconcileOptions(t *testing.T) {
	opts, err := buildKlineGapReconcileOptions("etf", "sh513623", "15minute", "20260407", "", "", 10, true)
	if err != nil {
		t.Fatalf("buildKlineGapReconcileOptions: %v", err)
	}
	if opts.AssetType != collectorpkg.AssetTypeETF || opts.Period != collectorpkg.Period15Minute {
		t.Fatalf("unexpected type filters: %+v", opts)
	}
	if opts.StartDate != "20260407" || opts.EndDate != "20260407" || opts.Limit != 10 || !opts.DryRun {
		t.Fatalf("unexpected reconcile options: %+v", opts)
	}

	opts, err = buildKlineGapReconcileOptions("", "", "", "", "20260407", "20260410", 0, false)
	if err != nil {
		t.Fatalf("buildKlineGapReconcileOptions range: %v", err)
	}
	if opts.StartDate != "20260407" || opts.EndDate != "20260410" || opts.DryRun {
		t.Fatalf("unexpected ranged reconcile options: %+v", opts)
	}

	if _, err := buildKlineGapReconcileOptions("", "", "", "20260407", "20260408", "", 0, true); err == nil {
		t.Fatalf("expected conflicting date filters error")
	}
}

func TestNewCollectorControlStateHonorsStartPausedEnv(t *testing.T) {
	old := os.Getenv("COLLECTOR_START_PAUSED")
	defer os.Setenv("COLLECTOR_START_PAUSED", old)

	if err := os.Setenv("COLLECTOR_START_PAUSED", "1"); err != nil {
		t.Fatalf("set env: %v", err)
	}
	state := newCollectorControlState()
	if !state.isPaused() {
		t.Fatalf("expected collector to start paused")
	}
	if got := state.snapshot().PauseReason; got != "startup paused via env" {
		t.Fatalf("pause reason = %q, want startup paused via env", got)
	}
}
