package lifecycle

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestInventoryStorageReportsSQLiteTablesReadOnly(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "trade", "sh600000.db")
	mustCreateSQLite(t, dbPath, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, Price INTEGER)`,
		`CREATE INDEX idx_trade_history_date ON TradeHistory(TradeDate)`,
		`INSERT INTO TradeHistory(Code, TradeDate, Price) VALUES('sh600000', '20260102', 12000)`,
		`INSERT INTO TradeHistory(Code, TradeDate, Price) VALUES('sh600000', '20260424', 13000)`,
	})

	report, err := InventoryStorage(root)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	row, ok := findInventoryRow(report, "trade", "sh600000", "TradeHistory")
	if !ok {
		t.Fatalf("missing TradeHistory inventory row: %+v", report.Tables)
	}
	if row.FileBytes <= 0 || row.RowCount != 2 || row.MinDate != "20260102" || row.MaxDate != "20260424" {
		t.Fatalf("unexpected inventory row: %+v", row)
	}
	if row.IndexCount != 1 {
		t.Fatalf("index count = %d, want 1", row.IndexCount)
	}
	if row.PageCount <= 0 || row.FreelistCount < 0 {
		t.Fatalf("missing sqlite page metrics: %+v", row)
	}

	after, err := countRows(dbPath, "TradeHistory")
	if err != nil {
		t.Fatalf("count after inventory: %v", err)
	}
	if after != 2 {
		t.Fatalf("inventory mutated db row count to %d", after)
	}
}

func TestInventoryProfessionalFinanceReportsTablesAndDependencies(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "prof_finance.db")
	mustCreateSQLite(t, dbPath, []string{
		`CREATE TABLE prof_finance_source_value_raw(full_code TEXT, report_date TEXT, source_field_id INTEGER)`,
		`CREATE INDEX idx_prof_finance_source_value_report_date ON prof_finance_source_value_raw(report_date, source_field_id)`,
		`INSERT INTO prof_finance_source_value_raw(full_code, report_date, source_field_id) VALUES('sh600000', '20251231', 1)`,
	})

	report, err := InventoryProfessionalFinance(dbPath)
	if err != nil {
		t.Fatalf("professional finance inventory: %v", err)
	}
	row, ok := findInventoryRow(report, "professional_finance", "", "prof_finance_source_value_raw")
	if !ok {
		t.Fatalf("missing professional finance table inventory: %+v", report.Tables)
	}
	if row.RowCount != 1 || row.MinDate != "20251231" || row.MaxDate != "20251231" {
		t.Fatalf("unexpected professional finance inventory row: %+v", row)
	}

	deps := ProfessionalFinanceEndpointDependencies()
	if len(deps["/api/v1/prof-finance/history"]) == 0 {
		t.Fatalf("history dependency graph is empty: %+v", deps)
	}
}

func countRows(path, table string) (int64, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int64
	err = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count)
	return count, err
}
