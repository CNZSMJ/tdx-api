package lifecycle

import (
	"fmt"
	"time"
)

func RehydrateSegmentToHotPath(result SegmentExportResult, targetDBPath string) (RestoreResult, error) {
	data, err := readColdObject(result.Storage, result.ColdURI)
	if err != nil {
		return RestoreResult{}, err
	}
	rows, err := readSegmentRows(result.TableName, data)
	if err != nil {
		return RestoreResult{}, err
	}
	db, err := openHotPathRestoreSQLite(targetDBPath)
	if err != nil {
		return RestoreResult{}, err
	}
	defer db.Close()
	if err := createRestoreTable(db, result.TableName); err != nil {
		return RestoreResult{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return RestoreResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	minDate, maxDate := result.MinDate, result.MaxDate
	if minDate == "" || maxDate == "" {
		minDate, maxDate = rowDateRange(rows)
	}
	if err := deleteHotRowsForRestore(tx, result.TableName, minDate, maxDate); err != nil {
		return RestoreResult{}, err
	}
	for _, row := range rows {
		if err := insertRestoreRow(tx, result.TableName, row); err != nil {
			return RestoreResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RestoreResult{}, err
	}
	committed = true
	return RestoreResult{TargetDBPath: targetDBPath, TableName: result.TableName, RowCount: int64(len(rows))}, nil
}

func deleteHotRowsForRestore(db sqlExecer, table, minDate, maxDate string) error {
	if minDate == "" || maxDate == "" {
		return fmt.Errorf("restore date range is required for %s", table)
	}
	switch table {
	case "TradeHistory", "TradeMinute1Bar", "TradeMinute5Bar", "TradeMinute15Bar", "TradeMinute30Bar", "TradeMinute60Bar", "TradeLive", "MinuteLive", "OrderHistory":
		_, err := db.Exec(`DELETE FROM `+quoteIdent(table)+` WHERE TradeDate >= ? AND TradeDate <= ?`, minDate, maxDate)
		return err
	case "QuoteSnapshot":
		startUnix, err := cutoffUnixStart(minDate)
		if err != nil {
			return err
		}
		loc, err := shanghaiLocation()
		if err != nil {
			return err
		}
		end, err := time.ParseInLocation("20060102", maxDate, loc)
		if err != nil {
			return err
		}
		endUnix := dayStart(end, loc).AddDate(0, 0, 1).Unix()
		_, err = db.Exec(`DELETE FROM QuoteSnapshot WHERE CaptureTime >= ? AND CaptureTime < ?`, startUnix, endUnix)
		return err
	default:
		return fmt.Errorf("unsupported hot path restore table %s", table)
	}
}

func rowDateRange(rows []map[string]any) (string, string) {
	minDate, maxDate := "", ""
	for _, row := range rows {
		date, _ := row["trade_date"].(string)
		if date == "" {
			date, _ = row["capture_date"].(string)
		}
		if date == "" {
			continue
		}
		if minDate == "" || date < minDate {
			minDate = date
		}
		if maxDate == "" || date > maxDate {
			maxDate = date
		}
	}
	return minDate, maxDate
}
