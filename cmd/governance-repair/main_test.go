package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestRunCLIApplyCreatesGovernanceBackup(t *testing.T) {
	baseDir := t.TempDir()
	paths := collectorpkg.ResolveGovernancePaths(baseDir)
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	var out bytes.Buffer
	err = runCLI([]string{
		"--governance-db", paths.DBPath,
		"--backup-dir", filepath.Join(baseDir, "backups"),
		"--apply",
	}, &out)
	if err != nil {
		t.Fatalf("run cli: %v", err)
	}

	var result collectorpkg.GovernanceRepairBatchResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	if result.Mode != collectorpkg.GovernanceRepairModeApply || result.BackupPath == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := collectorpkg.OpenGovernanceStore(result.BackupPath); err != nil {
		t.Fatalf("backup is not readable governance DB: %v", err)
	}
}

func TestRunCLIRepairsStaleGovernanceLockMetadata(t *testing.T) {
	baseDir := t.TempDir()
	paths := collectorpkg.ResolveGovernancePaths(baseDir)
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := time.Date(2026, 4, 28, 21, 0, 0, 0, time.UTC)
	if err := store.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       int64(os.Getpid()),
		HolderHostname:  "localhost",
		HolderJobName:   string(collectorpkg.GovernanceJobStartupRecovery),
		HolderRunID:     "startup-recovery-ended",
		AcquiredAt:      now.Add(-time.Hour),
		LastHeartbeatAt: now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("seed stale lock metadata: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	var out bytes.Buffer
	err = runCLI([]string{
		"--governance-db", paths.DBPath,
		"--governance-lock", paths.LockPath,
		"--backup-dir", filepath.Join(baseDir, "backups"),
		"--repair", "stale-lock",
		"--apply",
	}, &out)
	if err != nil {
		t.Fatalf("run cli: %v", err)
	}

	var result collectorpkg.GovernanceRepairBatchResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Name != "stale_lock_metadata" || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	store, err = collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	lock, err := store.LatestLockMetadata()
	if err != nil {
		t.Fatalf("latest lock metadata: %v", err)
	}
	if lock != nil {
		t.Fatalf("stale lock metadata was not cleared: %+v", lock)
	}
}
