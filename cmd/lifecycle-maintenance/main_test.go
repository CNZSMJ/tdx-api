package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestParseConfigUsesTDXDataDirAndSafeDefaults(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	t.Setenv("TDX_DATA_DIR", stateDir)
	t.Setenv("TDX_LIFECYCLE_ENABLE", "")
	t.Setenv("TDX_LIFECYCLE_ALLOW_PRUNE", "")

	cfg, err := parseConfig([]string{"--enable", "--allow-prune", "--min-verified-segments", "1"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if !cfg.Enable || !cfg.AllowPrune {
		t.Fatalf("expected explicit enable and prune flags: %+v", cfg)
	}
	if cfg.DataDir != stateDir {
		t.Fatalf("data dir should come from env before flags are applied, got %q", cfg.DataDir)
	}
	if !strings.HasSuffix(cfg.ManifestPath, filepath.Join("state", "cold_manifest.db")) {
		t.Fatalf("manifest default = %q", cfg.ManifestPath)
	}
	if cfg.MinVerifiedSegments != 1 || cfg.MaxCandidates != 1 || cfg.MaxArchiveDays != 366 {
		t.Fatalf("unexpected lifecycle defaults: %+v", cfg)
	}
}

func TestRunCLIOnceArchivesAndPrunes(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	mustExecSQL(t, filepath.Join(dataDir, "trade", "sh600000.db"), []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})
	mustExecSQL(t, filepath.Join(dataDir, "workday.db"), []string{
		`CREATE TABLE workday(date TEXT PRIMARY KEY)`,
		`INSERT INTO workday VALUES('20230102')`,
		`INSERT INTO workday VALUES('20260401')`,
		`INSERT INTO workday VALUES('20260424')`,
	})

	var out bytes.Buffer
	err := runCLI(t.Context(), []string{
		"--data-dir", dataDir,
		"--enable",
		"--allow-prune",
		"--min-verified-segments", "1",
		"--max-runs", "1",
		"--hot-cutoff-date", "20260401",
		"--min-free-bytes", "0",
		"--safety-margin-bytes", "1",
		"--runtime-budget", "1m",
	}, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run cli: %v\noutput=%s", err, out.String())
	}
	var record commandRunRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	if record.MaintenanceResult.Status != "passed" || record.MaintenanceResult.PrunedSegments != 1 || record.MaintenanceResult.RowsArchived != 1 {
		t.Fatalf("unexpected record: %+v", record)
	}
	remaining := mustCountRows(t, filepath.Join(dataDir, "trade", "sh600000.db"), "TradeHistory")
	if remaining != 1 {
		t.Fatalf("remaining hot rows = %d, want 1", remaining)
	}
}

func mustExecSQL(t *testing.T, path string, statements []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

func mustCountRows(t *testing.T, path, table string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatalf("open sqlite ro: %v", err)
	}
	defer db.Close()
	var count int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
