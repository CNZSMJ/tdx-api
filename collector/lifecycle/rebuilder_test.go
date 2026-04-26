package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReplacementHotDBRetainsRowsAndIndexes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sh600000.db")
	replacement := filepath.Join(root, "sh600000.replacement.tmp")
	mustCreateSQLite(t, source, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER)`,
		`CREATE INDEX idx_trade_history_date ON TradeHistory(TradeDate, Seq)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1,1,11000)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',2,1,12000)`,
	})

	result, err := BuildReplacementHotDB(RebuildRequest{
		SourceDBPath:      source,
		ReplacementDBPath: replacement,
		TableName:         "TradeHistory",
		HotCutoffDate:     "20260401",
	})
	if err != nil {
		t.Fatalf("build replacement: %v", err)
	}
	if result.SourceRows != 2 || result.RetainedRows != 1 || result.PrunedRows != 1 {
		t.Fatalf("unexpected rebuild result: %+v", result)
	}
	if err := VerifyHotRetainedRows(source, replacement, "TradeHistory", "20260401"); err != nil {
		t.Fatalf("verify retained rows: %v", err)
	}
	indexes, err := SQLiteIndexNames(replacement)
	if err != nil {
		t.Fatalf("replacement indexes: %v", err)
	}
	if !containsString(indexes, "idx_trade_history_date") {
		t.Fatalf("replacement indexes = %#v, missing original index", indexes)
	}
}

func TestAtomicReplaceHotDBRollbackAndRecovery(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sh600000.db")
	replacement := filepath.Join(root, "sh600000.replacement.tmp")
	mustCreateSQLite(t, source, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1,1,11000)`,
	})
	mustCreateSQLite(t, replacement, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',2,1,12000)`,
	})

	replace, err := AtomicReplaceHotDB(source, replacement, "batch-1")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if replace.BackupPath == "" {
		t.Fatalf("missing backup path: %+v", replace)
	}
	got, err := countRows(source, "TradeHistory")
	if err != nil || got != 1 {
		t.Fatalf("replacement source rows=%d err=%v", got, err)
	}
	if _, err := os.Stat(replace.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	if err := RollbackHotDBReplacement(source, replace.BackupPath); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	rolledBack, err := countRows(source, "TradeHistory")
	if err != nil || rolledBack != 1 {
		t.Fatalf("rolled back rows=%d err=%v", rolledBack, err)
	}
}

func TestStartupRecoveryHandlesInterruptedReplacementArtifacts(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sh600000.db")
	backup := source + ".pre_lifecycle.batch-1.bak"
	tmp := source + ".replacement.batch-1.tmp"
	mustCreateSQLite(t, backup, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102')`,
	})
	mustCreateSQLite(t, tmp, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`,
	})

	action, err := RecoverHotReplacementArtifacts(source, "batch-1")
	if err != nil {
		t.Fatalf("recover missing source: %v", err)
	}
	if action.Action != "restored_backup" {
		t.Fatalf("action = %+v", action)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source not restored: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("tmp should be deleted, stat err=%v", err)
	}

	mustCreateSQLite(t, tmp, []string{`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`})
	action, err = RecoverHotReplacementArtifacts(source, "batch-1")
	if err != nil {
		t.Fatalf("recover tmp with source: %v", err)
	}
	if action.Action != "removed_tmp" {
		t.Fatalf("action = %+v", action)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
