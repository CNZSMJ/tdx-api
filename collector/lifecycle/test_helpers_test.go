package lifecycle

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func mustShanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	return loc
}

func parseLifecycleDays(t *testing.T, loc *time.Location, dates ...string) []time.Time {
	t.Helper()
	out := make([]time.Time, 0, len(dates))
	for _, date := range dates {
		parsed, err := time.ParseInLocation("20060102", date, loc)
		if err != nil {
			t.Fatalf("parse date %s: %v", date, err)
		}
		out = append(out, parsed)
	}
	return out
}

func parseLifecycleDateSet(t *testing.T, loc *time.Location, dates ...string) map[string]bool {
	t.Helper()
	out := make(map[string]bool, len(dates))
	for _, day := range parseLifecycleDays(t, loc, dates...) {
		out[day.Format("20060102")] = true
	}
	return out
}

func writeWorkdayFixture(t *testing.T, dbPath string, loc *time.Location, dates ...string) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE workday(id INTEGER PRIMARY KEY AUTOINCREMENT, unix INTEGER, date TEXT)`,
	}
	for _, day := range parseLifecycleDays(t, loc, dates...) {
		stmts = append(stmts, `INSERT INTO workday(unix, date) VALUES(`+
			itoa(dayAtClose(day, loc).Unix())+`, '`+day.Format("20060102")+`')`)
	}
	mustCreateSQLite(t, dbPath, stmts)
}

func mustCreateSQLite(t *testing.T, path string, stmts []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir sqlite dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

func dayAtClose(day time.Time, loc *time.Location) time.Time {
	y, m, d := day.In(loc).Date()
	return time.Date(y, m, d, 15, 0, 0, 0, loc)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func findInventoryRow(report StorageInventoryReport, domain, instrument, table string) (TableInventory, bool) {
	for _, row := range report.Tables {
		if row.Domain == domain && row.Instrument == instrument && row.Table == table {
			return row, true
		}
	}
	return TableInventory{}, false
}
