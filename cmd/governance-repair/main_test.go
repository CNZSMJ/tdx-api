package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

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
