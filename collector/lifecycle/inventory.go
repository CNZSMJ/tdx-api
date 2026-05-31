package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

type StorageInventoryReport struct {
	GeneratedAt time.Time
	Root        string
	Tables      []TableInventory
}

type CandidateDiscoveryOptions struct {
	MaxInventoryFiles int
	MaxCandidates     int
	Sort              string
}

type lifecycleDBFile struct {
	Domain     string
	Instrument string
	Path       string
	Bytes      int64
}

type TableInventory struct {
	Domain              string
	Instrument          string
	DBPath              string
	Table               string
	FileBytes           int64
	WALBytes            int64
	RowCount            int64
	MinDate             string
	MaxDate             string
	DateColumn          string
	IndexCount          int
	EstimatedIndexBytes int64
	PageCount           int64
	FreelistCount       int64
}

func InventoryStorage(root string) (StorageInventoryReport, error) {
	if strings.TrimSpace(root) == "" {
		return StorageInventoryReport{}, errors.New("inventory root is required")
	}
	report := StorageInventoryReport{
		GeneratedAt: time.Now(),
		Root:        root,
	}
	for _, domain := range []string{"trade", "live", "order_history", "auction"} {
		domainDir := filepath.Join(root, domain)
		if _, err := os.Stat(domainDir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return report, err
		}
		err := filepath.WalkDir(domainDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
				return nil
			}
			instrument := strings.TrimSuffix(entry.Name(), ".db")
			if entry.Name() == "quotes.db" || entry.Name() == "auction.db" {
				instrument = ""
			}
			rows, err := inventorySQLiteFile(path, domain, instrument)
			if err != nil {
				return err
			}
			report.Tables = append(report.Tables, rows...)
			return nil
		})
		if err != nil {
			return report, err
		}
	}
	sortInventory(report.Tables)
	return report, nil
}

func DiscoverLifecycleCandidates(ctx context.Context, root, hotCutoffDate string, opts CandidateDiscoveryOptions) ([]LifecycleCandidate, error) {
	files, err := discoverLifecycleDBFiles(root)
	if err != nil {
		return nil, err
	}
	sortLifecycleDBFiles(files, opts.Sort)

	maxFiles := opts.MaxInventoryFiles
	if maxFiles <= 0 || maxFiles > len(files) {
		maxFiles = len(files)
	}
	maxCandidates := opts.MaxCandidates
	out := make([]LifecycleCandidate, 0)
	for i := 0; i < maxFiles; i++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		file := files[i]
		rows, err := inventorySQLiteFile(file.Path, file.Domain, file.Instrument)
		if err != nil {
			return out, err
		}
		report := StorageInventoryReport{
			GeneratedAt: time.Now(),
			Root:        root,
			Tables:      rows,
		}
		for _, candidate := range PlanSteadyStateRetention(report, hotCutoffDate).Candidates {
			if !stageOneTableSupported(candidate.TableName) {
				continue
			}
			out = append(out, candidate)
			if maxCandidates > 0 && len(out) >= maxCandidates {
				return out, nil
			}
		}
	}
	return out, nil
}

func sortLifecycleDBFiles(files []lifecycleDBFile, sortMode string) {
	desc := strings.EqualFold(strings.TrimSpace(sortMode), "size_desc")
	sort.Slice(files, func(i, j int) bool {
		if files[i].Bytes != files[j].Bytes {
			if desc {
				return files[i].Bytes > files[j].Bytes
			}
			return files[i].Bytes < files[j].Bytes
		}
		return files[i].Path < files[j].Path
	})
}

func discoverLifecycleDBFiles(root string) ([]lifecycleDBFile, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("inventory root is required")
	}
	files := make([]lifecycleDBFile, 0)
	for _, domain := range []string{"trade", "live", "order_history", "auction"} {
		domainDir := filepath.Join(root, domain)
		if _, err := os.Stat(domainDir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return files, err
		}
		err := filepath.WalkDir(domainDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			instrument := strings.TrimSuffix(entry.Name(), ".db")
			if entry.Name() == "quotes.db" || entry.Name() == "auction.db" {
				instrument = ""
			}
			files = append(files, lifecycleDBFile{
				Domain:     domain,
				Instrument: instrument,
				Path:       path,
				Bytes:      info.Size(),
			})
			return nil
		})
		if err != nil {
			return files, err
		}
	}
	return files, nil
}

func InventoryProfessionalFinance(dbPath string) (StorageInventoryReport, error) {
	if strings.TrimSpace(dbPath) == "" {
		return StorageInventoryReport{}, errors.New("professional finance db path is required")
	}
	rows, err := inventorySQLiteFile(dbPath, "professional_finance", "")
	if err != nil {
		return StorageInventoryReport{}, err
	}
	sortInventory(rows)
	return StorageInventoryReport{
		GeneratedAt: time.Now(),
		Root:        filepath.Dir(dbPath),
		Tables:      rows,
	}, nil
}

func ProfessionalFinanceEndpointDependencies() map[string][]string {
	raw := []string{
		"prof_finance_source_file",
		"prof_finance_source_report",
		"prof_finance_source_value_raw",
	}
	serving := []string{
		"prof_finance_field_catalog",
		"prof_finance_report_version",
		"prof_finance_report_payload",
		"prof_finance_source_watermark",
	}
	return map[string][]string{
		"/api/v1/prof-finance/fields":        {"prof_finance_field_catalog"},
		"/api/v1/prof-finance/history":       serving,
		"/api/v1/prof-finance/snapshot":      serving,
		"/api/v1/prof-finance/coverage":      serving,
		"/api/v1/prof-finance/cross-section": serving,
		"raw/source candidates":              raw,
	}
}

func inventorySQLiteFile(path, domain, instrument string) ([]TableInventory, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	pageCount := pragmaInt64(db, "page_count")
	freelistCount := pragmaInt64(db, "freelist_count")
	walBytes := fileSizeIfExists(path + "-wal")

	tableNames, err := sqliteTableNames(db)
	if err != nil {
		return nil, err
	}
	rows := make([]TableInventory, 0, len(tableNames))
	for _, table := range tableNames {
		columns, err := sqliteColumns(db, table)
		if err != nil {
			return nil, err
		}
		dateColumn, dateExpr := inventoryDateExpression(columns)
		row := TableInventory{
			Domain:              domain,
			Instrument:          instrument,
			DBPath:              path,
			Table:               table,
			FileBytes:           stat.Size(),
			WALBytes:            walBytes,
			DateColumn:          dateColumn,
			IndexCount:          sqliteIndexCount(db, table),
			EstimatedIndexBytes: -1,
			PageCount:           pageCount,
			FreelistCount:       freelistCount,
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + quoteIdent(table)).Scan(&row.RowCount); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		if dateExpr != "" && row.RowCount > 0 {
			query := fmt.Sprintf(`SELECT MIN(%s), MAX(%s) FROM %s`, dateExpr, dateExpr, quoteIdent(table))
			var minDate, maxDate sql.NullString
			if err := db.QueryRow(query).Scan(&minDate, &maxDate); err != nil {
				return nil, fmt.Errorf("date range %s: %w", table, err)
			}
			if minDate.Valid {
				row.MinDate = minDate.String
			}
			if maxDate.Valid {
				row.MaxDate = maxDate.String
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func sqliteTableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func sqliteColumns(db *sql.DB, table string) (map[string]string, error) {
	rows, err := db.Query(`PRAGMA table_info(` + quoteIdent(table) + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]string)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = strings.ToUpper(typ)
	}
	return columns, rows.Err()
}

func inventoryDateExpression(columns map[string]string) (string, string) {
	for _, name := range []string{"TradeDate", "trade_date", "report_date", "Date", "date"} {
		if _, ok := columns[name]; ok {
			return name, normalizedSQLiteDateExpr(name)
		}
	}
	for _, name := range []string{"CaptureTime", "capture_time"} {
		if _, ok := columns[name]; ok {
			return name, "strftime('%Y%m%d', " + quoteIdent(name) + ", 'unixepoch', '+8 hours')"
		}
	}
	return "", ""
}

func normalizedSQLiteDateExpr(name string) string {
	return "replace(" + quoteIdent(name) + ", '-', '')"
}

func sqliteIndexCount(db *sql.DB, table string) int {
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=? AND name NOT LIKE 'sqlite_autoindex%'`, table).Scan(&count)
	return count
}

func pragmaInt64(db *sql.DB, name string) int64 {
	var value int64
	_ = db.QueryRow(`PRAGMA ` + name).Scan(&value)
	return value
}

func fileSizeIfExists(path string) int64 {
	stat, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return stat.Size()
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func sortInventory(rows []TableInventory) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Domain != rows[j].Domain {
			return rows[i].Domain < rows[j].Domain
		}
		if rows[i].Instrument != rows[j].Instrument {
			return rows[i].Instrument < rows[j].Instrument
		}
		return rows[i].Table < rows[j].Table
	})
}
