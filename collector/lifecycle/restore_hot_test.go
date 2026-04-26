package lifecycle

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenHotPathRestoreSQLiteDoesNotDisableJournal(t *testing.T) {
	db, err := openHotPathRestoreSQLite(filepath.Join(t.TempDir(), "hot.db"))
	if err != nil {
		t.Fatalf("open hot restore db: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if strings.EqualFold(mode, "off") {
		t.Fatalf("hot path restore must not disable SQLite journaling")
	}
}
