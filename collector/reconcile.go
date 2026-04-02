package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ReconcileReport struct {
	Date         string                  `json:"date"`
	Trigger      string                  `json:"trigger"`
	Status       string                  `json:"status"`
	IsTradingDay bool                    `json:"is_trading_day"`
	OpenGapCount int64                   `json:"open_gap_count"`
	StartedAt    time.Time               `json:"started_at"`
	CompletedAt  time.Time               `json:"completed_at"`
	ReportPath   string                  `json:"report_path"`
	Domains      []ReconcileDomainReport `json:"domains"`
	Errors       []string                `json:"errors,omitempty"`
}

type ReconcileDomainReport struct {
	Domain          string           `json:"domain"`
	Status          string           `json:"status"`
	RepairAttempted bool             `json:"repair_attempted"`
	Items           int              `json:"items"`
	Details         string           `json:"details"`
	BeforeRows      int64            `json:"before_rows,omitempty"`
	AfterRows       int64            `json:"after_rows,omitempty"`
	ExpectedItems   int              `json:"expected_items,omitempty"`
	CoveredItems    int              `json:"covered_items,omitempty"`
	TargetCovered   bool             `json:"target_covered,omitempty"`
	CursorSummary   string           `json:"cursor_summary,omitempty"`
	BeforeTables    map[string]int64 `json:"before_tables,omitempty"`
	AfterTables     map[string]int64 `json:"after_tables,omitempty"`
	Errors          []string         `json:"errors,omitempty"`
}

type reconcileObservation struct {
	rows          int64
	expectedItems int
	coveredItems  int
	cursorSummary string
	tables        map[string]int64
}

func (r *Runtime) ReconcileDate(ctx context.Context, date string) (*ReconcileReport, error) {
	return r.ReconcileDateWithTrigger(ctx, date, "manual")
}

func (r *Runtime) ReconcileDateWithTrigger(ctx context.Context, date, trigger string) (*ReconcileReport, error) {
	return r.reconcileDate(ctx, date, trigger)
}

func (r *Runtime) reconcileDate(ctx context.Context, date, trigger string) (_ *ReconcileReport, err error) {
	target, err := normalizeReconcileDate(date, r.cfg.Now)
	if err != nil {
		return nil, err
	}

	report := &ReconcileReport{
		Date:      target,
		Trigger:   trigger,
		Status:    "running",
		StartedAt: r.cfg.Now(),
		Domains:   make([]ReconcileDomainReport, 0, 8),
	}

	run := &ScheduleRunRecord{
		ScheduleName: r.cfg.ReconcileScheduleName,
		Status:       "running",
		StartedAt:    report.StartedAt,
		Details:      fmt.Sprintf("trigger=%s date=%s", trigger, target),
	}
	if err := r.store.AddScheduleRun(run); err != nil {
		return nil, err
	}

	defer func() {
		report.CompletedAt = r.cfg.Now()
		if err != nil {
			report.Status = "failed"
			if len(report.Errors) == 0 {
				report.Errors = append(report.Errors, err.Error())
			}
			run.Status = "failed"
		} else if len(report.Errors) > 0 {
			report.Status = "partial"
			run.Status = "failed"
		} else {
			report.Status = "passed"
			run.Status = "passed"
		}
		path, writeErr := r.writeReconcileReport(report)
		if writeErr != nil {
			report.Errors = append(report.Errors, "write report: "+writeErr.Error())
			report.Status = "failed"
			run.Status = "failed"
		} else {
			report.ReportPath = path
		}
		run.EndedAt = report.CompletedAt
		run.Details = fmt.Sprintf("trigger=%s date=%s status=%s report=%s", trigger, target, report.Status, report.ReportPath)
		_ = r.store.UpdateScheduleRun(run)
	}()

	targetTime, _ := parseTradeCursor(target)
	trading, err := r.provider.IsTradingDay(ctx, targetTime)
	if err != nil {
		return nil, err
	}
	report.IsTradingDay = trading

	metadataBefore, _ := r.observeMetadata()
	quoteBefore, _ := r.observeQuoteSnapshots(target)

	if domainErr := r.metadata.RefreshAll(ctx); domainErr != nil {
		index := report.addFailure("metadata", true, 0, "refresh codes/workday failed", domainErr)
		metadataAfter, _ := r.observeMetadata()
		report.applyObservation(index, metadataBefore, metadataAfter)
	} else {
		index := report.addDomain("metadata", "reconciled", true, 2, "codes/workday refreshed via staging publish")
		metadataAfter, _ := r.observeMetadata()
		report.applyObservation(index, metadataBefore, metadataAfter)
	}

	instruments, domainErr := r.provider.Instruments(ctx, InstrumentQuery{
		AssetTypes: []AssetType{AssetTypeStock, AssetTypeETF, AssetTypeIndex},
	})
	if domainErr != nil {
		return nil, domainErr
	}
	instruments = normalizeInstruments(instruments)
	klineBefore, _ := r.observeKline(target, instruments)
	tradeBefore, _ := r.observeTradeHistory(target, instruments)
	orderBefore, _ := r.observeOrderHistory(target, instruments)
	liveBefore, _ := r.observeLiveCapture(target, instruments)
	financeBefore, _ := r.observeFinance(target, instruments)
	f10Before, _ := r.observeF10(instruments)

	quoteCodes := quoteCaptureCodes(instruments)
	items := 0
	if target == r.cfg.Now().Format("20060102") {
		if domainErr := r.live.CaptureQuotes(ctx, QuoteCaptureQuery{
			Codes:       quoteCodes,
			CaptureTime: r.cfg.Now(),
		}); domainErr != nil {
			index := report.addFailure("quote_snapshot", true, 0, "final quote snapshot capture failed", domainErr)
			quoteAfter, _ := r.observeQuoteSnapshots(target)
			report.applyObservation(index, quoteBefore, quoteAfter)
		} else {
			items = len(quoteCodes)
			index := report.addDomain("quote_snapshot", "best_effort", true, items, "captured one final snapshot at reconciliation time; intraday snapshot completeness is not reconstructible after the fact")
			quoteAfter, _ := r.observeQuoteSnapshots(target)
			report.applyObservation(index, quoteBefore, quoteAfter)
		}
	} else {
		index := report.addDomain("quote_snapshot", "unsupported_historical_rebuild", false, 0, "historical intraday quote snapshots cannot be rebuilt for non-current dates with the current provider APIs")
		report.applyObservation(index, quoteBefore, quoteBefore)
	}

	klineItems := 0
	klineErrors := make([]string, 0)
	for _, instrument := range instruments {
		for _, period := range r.cfg.KlinePeriods {
			if domainErr := r.kline.ReconcileDate(ctx, KlineCollectQuery{
				Code:      instrument.Code,
				AssetType: instrument.AssetType,
				Period:    period,
			}, target); domainErr != nil {
				klineErrors = append(klineErrors, fmt.Sprintf("%s/%s: %v", instrument.Code, period, domainErr))
				continue
			}
			klineItems++
		}
	}
	klineIndex := report.addDomainWithErrors("kline", true, klineItems, "republished requested date window and later rows for configured periods", klineErrors)
	klineAfter, _ := r.observeKline(target, instruments)
	report.applyObservation(klineIndex, klineBefore, klineAfter)

	if trading {
		tradeItems := 0
		tradeErrors := make([]string, 0)
		liveItems := 0
		liveErrors := make([]string, 0)
		orderItems := 0
		orderErrors := make([]string, 0)

		for _, instrument := range instruments {
			if instrument.AssetType == AssetTypeStock || instrument.AssetType == AssetTypeETF {
				if domainErr := r.trade.RefreshDay(ctx, TradeCollectQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      target,
				}); domainErr != nil {
					tradeErrors = append(tradeErrors, fmt.Sprintf("%s: %v", instrument.Code, domainErr))
				} else {
					tradeItems++
				}

				if domainErr := r.live.ReconcileDay(ctx, SessionCaptureQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      target,
				}); domainErr != nil {
					liveErrors = append(liveErrors, fmt.Sprintf("%s: %v", instrument.Code, domainErr))
				} else {
					liveItems++
				}
			}

			if instrument.AssetType == AssetTypeStock {
				if domainErr := r.orderHistory.RefreshDay(ctx, OrderHistoryCollectQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      target,
				}); domainErr != nil {
					orderErrors = append(orderErrors, fmt.Sprintf("%s: %v", instrument.Code, domainErr))
				} else {
					orderItems++
				}
			}
		}

		tradeIndex := report.addDomainWithErrors("trade_history", true, tradeItems, "republished requested trade day from provider source", tradeErrors)
		tradeAfter, _ := r.observeTradeHistory(target, instruments)
		report.applyObservation(tradeIndex, tradeBefore, tradeAfter)

		orderIndex := report.addDomainWithErrors("order_history", true, orderItems, "republished requested order-history day from provider source", orderErrors)
		orderAfter, _ := r.observeOrderHistory(target, instruments)
		report.applyObservation(orderIndex, orderBefore, orderAfter)

		liveIndex := report.addDomainWithErrors("live_capture", true, liveItems, "reconciled requested live day by replacing minute/trade rows for the trading date", liveErrors)
		liveAfter, _ := r.observeLiveCapture(target, instruments)
		report.applyObservation(liveIndex, liveBefore, liveAfter)
	} else {
		tradeIndex := report.addDomain("trade_history", "skipped_non_trading_day", false, 0, "requested date is not a trading day")
		report.applyObservation(tradeIndex, tradeBefore, tradeBefore)
		orderIndex := report.addDomain("order_history", "skipped_non_trading_day", false, 0, "requested date is not a trading day")
		report.applyObservation(orderIndex, orderBefore, orderBefore)
		liveIndex := report.addDomain("live_capture", "skipped_non_trading_day", false, 0, "requested date is not a trading day")
		report.applyObservation(liveIndex, liveBefore, liveBefore)
	}

	financeItems := 0
	financeErrors := make([]string, 0)
	f10Items := 0
	f10Errors := make([]string, 0)
	for _, instrument := range instruments {
		if instrument.AssetType != AssetTypeStock {
			continue
		}
		if domainErr := r.fundamentals.RefreshFinance(ctx, instrument.Code); domainErr != nil {
			financeErrors = append(financeErrors, fmt.Sprintf("%s: %v", instrument.Code, domainErr))
		} else {
			financeItems++
		}
		if domainErr := r.fundamentals.SyncF10(ctx, instrument.Code); domainErr != nil {
			f10Errors = append(f10Errors, fmt.Sprintf("%s: %v", instrument.Code, domainErr))
		} else {
			f10Items++
		}
	}
	financeIndex := report.addDomainWithErrors("finance", true, financeItems, "refreshed current finance snapshots; provider state is date-agnostic at reconcile time", financeErrors)
	financeAfter, _ := r.observeFinance(target, instruments)
	report.applyObservation(financeIndex, financeBefore, financeAfter)

	f10Index := report.addDomainWithErrors("f10", true, f10Items, "refreshed current F10 directory/content; provider state is date-agnostic at reconcile time", f10Errors)
	f10After, _ := r.observeF10(instruments)
	report.applyObservation(f10Index, f10Before, f10After)

	openGapCount, gapErr := r.store.CountOpenCollectGaps()
	if gapErr != nil {
		report.addFailure("collector_gap", false, 0, "count open collector gaps failed", gapErr)
		return report, nil
	}
	report.OpenGapCount = openGapCount
	if openGapCount > 0 {
		report.Errors = append(report.Errors, fmt.Sprintf("collector_gap: open gaps remaining=%d", openGapCount))
		report.Domains = append(report.Domains, ReconcileDomainReport{
			Domain:          "collector_gap",
			Status:          "partial",
			RepairAttempted: false,
			Items:           int(openGapCount),
			AfterRows:       openGapCount,
			Details:         "open gaps remain after reconciliation; inspect collector_gap before treating the day as fully healthy",
			Errors:          []string{fmt.Sprintf("open gaps=%d", openGapCount)},
		})
	} else {
		index := report.addDomain("collector_gap", "reconciled", false, 0, "no open collector gaps remain after reconciliation")
		report.applyObservation(index, reconcileObservation{rows: 0}, reconcileObservation{rows: 0})
	}

	return report, nil
}

func normalizeReconcileDate(date string, now func() time.Time) (string, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		date = now().Format("20060102")
	}
	if _, err := parseTradeCursor(date); err != nil {
		return "", fmt.Errorf("invalid reconcile date %q: %w", date, err)
	}
	return date, nil
}

func (r *Runtime) writeReconcileReport(report *ReconcileReport) (string, error) {
	if report == nil {
		return "", errors.New("nil reconcile report")
	}
	if r.cfg.ReportDir == "" {
		return "", errors.New("runtime report directory is empty")
	}
	if err := os.MkdirAll(r.cfg.ReportDir, 0o777); err != nil {
		return "", err
	}
	path := filepath.Join(r.cfg.ReportDir, "reconcile-"+report.Date+".json")
	bs, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, bs, 0o666); err != nil {
		return "", err
	}
	return path, nil
}

func ReadReconcileReport(reportDir, date string) (*ReconcileReport, error) {
	date, err := normalizeReconcileDate(date, time.Now)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(reportDir, "reconcile-"+date+".json")
	bs, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	report := new(ReconcileReport)
	if err := json.Unmarshal(bs, report); err != nil {
		return nil, err
	}
	return report, nil
}

func (r *ReconcileReport) addDomain(domain, status string, repairAttempted bool, items int, details string) int {
	r.Domains = append(r.Domains, ReconcileDomainReport{
		Domain:          domain,
		Status:          status,
		RepairAttempted: repairAttempted,
		Items:           items,
		Details:         details,
	})
	return len(r.Domains) - 1
}

func (r *ReconcileReport) addFailure(domain string, repairAttempted bool, items int, details string, err error) int {
	msg := err.Error()
	r.Errors = append(r.Errors, fmt.Sprintf("%s: %s", domain, msg))
	r.Domains = append(r.Domains, ReconcileDomainReport{
		Domain:          domain,
		Status:          "failed",
		RepairAttempted: repairAttempted,
		Items:           items,
		Details:         details,
		Errors:          []string{msg},
	})
	return len(r.Domains) - 1
}

func (r *ReconcileReport) addDomainWithErrors(domain string, repairAttempted bool, items int, details string, errs []string) int {
	if len(errs) == 0 {
		return r.addDomain(domain, "reconciled", repairAttempted, items, details)
	}
	r.Errors = append(r.Errors, fmt.Sprintf("%s: %s", domain, strings.Join(errs, "; ")))
	r.Domains = append(r.Domains, ReconcileDomainReport{
		Domain:          domain,
		Status:          "partial",
		RepairAttempted: repairAttempted,
		Items:           items,
		Details:         details,
		Errors:          errs,
	})
	return len(r.Domains) - 1
}

func (r *ReconcileReport) applyObservation(index int, before, after reconcileObservation) {
	if index < 0 || index >= len(r.Domains) {
		return
	}
	r.Domains[index].BeforeRows = before.rows
	r.Domains[index].AfterRows = after.rows
	if len(before.tables) > 0 {
		r.Domains[index].BeforeTables = before.tables
	}
	if len(after.tables) > 0 {
		r.Domains[index].AfterTables = after.tables
	}
	r.Domains[index].ExpectedItems = after.expectedItems
	r.Domains[index].CoveredItems = after.coveredItems
	r.Domains[index].TargetCovered = after.expectedItems == 0 || after.coveredItems >= after.expectedItems
	r.Domains[index].CursorSummary = after.cursorSummary
}

func (r *Runtime) observeMetadata() (reconcileObservation, error) {
	codesRows, err := countSQLiteTableRows(r.cfg.Metadata.CodesDBPath, "codes", "")
	if err != nil {
		return reconcileObservation{}, err
	}
	workdayRows, err := countSQLiteTableRows(r.cfg.Metadata.WorkdayDBPath, "workday", "")
	if err != nil {
		return reconcileObservation{}, err
	}
	codesCursor, err := r.store.GetCollectCursor("codes", MetadataAssetType, MetadataAllKey, "")
	if err != nil {
		return reconcileObservation{}, err
	}
	workdayCursor, err := r.store.GetCollectCursor("workday", MetadataAssetType, MetadataAllKey, "")
	if err != nil {
		return reconcileObservation{}, err
	}
	covered := 0
	summary := make([]string, 0, 2)
	if codesCursor != nil && codesCursor.Cursor != "" {
		covered++
		summary = append(summary, "codes="+codesCursor.Cursor)
	}
	if workdayCursor != nil && workdayCursor.Cursor != "" {
		covered++
		summary = append(summary, "workday="+workdayCursor.Cursor)
	}
	return reconcileObservation{
		rows:          codesRows + workdayRows,
		expectedItems: 2,
		coveredItems:  covered,
		cursorSummary: strings.Join(summary, ", "),
		tables: map[string]int64{
			"codes":   codesRows,
			"workday": workdayRows,
		},
	}, nil
}

func (r *Runtime) observeQuoteSnapshots(date string) (reconcileObservation, error) {
	start, err := parseTradeCursor(date)
	if err != nil {
		return reconcileObservation{}, err
	}
	end := start.Add(24 * time.Hour)
	rows, err := countSQLiteTableRows(filepath.Join(r.cfg.Live.BaseDir, "quotes.db"), "QuoteSnapshot", "CaptureTime >= ? AND CaptureTime < ?", start.Unix(), end.Unix())
	if err != nil {
		return reconcileObservation{}, err
	}
	return reconcileObservation{
		rows: rows,
		tables: map[string]int64{
			"QuoteSnapshot": rows,
		},
	}, nil
}

func (r *Runtime) observeKline(date string, instruments []Instrument) (reconcileObservation, error) {
	target, err := parseTradeCursor(date)
	if err != nil {
		return reconcileObservation{}, err
	}
	var rows int64
	expected := 0
	covered := 0
	cursorSummary := make([]string, 0, len(instruments))
	tables := make(map[string]int64)
	for _, instrument := range instruments {
		filename := filepath.Join(r.cfg.Kline.BaseDir, instrument.Code+".db")
		for _, period := range r.cfg.KlinePeriods {
			spec, err := klinePeriodSpec(period)
			if err != nil {
				return reconcileObservation{}, err
			}
			count, err := countSQLiteTableRows(filename, spec.PublishedTable, "Date >= ?", target.Unix())
			if err != nil {
				return reconcileObservation{}, err
			}
			tableKey := instrument.Code + "/" + spec.PublishedTable
			tables[tableKey] = count
			rows += count
			expected++
			cursor, err := r.store.GetCollectCursor("kline", string(instrument.AssetType), instrument.Code, string(period))
			if err != nil {
				return reconcileObservation{}, err
			}
			if cursor != nil && cursor.Cursor != "" && unixCursorAtOrAfter(cursor.Cursor, target.Unix()) {
				covered++
			}
			if cursor != nil && cursor.Cursor != "" {
				cursorSummary = append(cursorSummary, fmt.Sprintf("%s/%s=%s", instrument.Code, period, cursor.Cursor))
			}
		}
	}
	return reconcileObservation{rows: rows, expectedItems: expected, coveredItems: covered, cursorSummary: strings.Join(cursorSummary, ", "), tables: tables}, nil
}

func (r *Runtime) observeTradeHistory(date string, instruments []Instrument) (reconcileObservation, error) {
	return r.observeTradeLikeDomain("trade_history", r.cfg.Trade.BaseDir, "TradeHistory", date, instruments, func(item Instrument) bool {
		return item.AssetType == AssetTypeStock || item.AssetType == AssetTypeETF
	})
}

func (r *Runtime) observeOrderHistory(date string, instruments []Instrument) (reconcileObservation, error) {
	return r.observeTradeLikeDomain("order_history", r.cfg.OrderHistory.BaseDir, "OrderHistory", date, instruments, func(item Instrument) bool {
		return item.AssetType == AssetTypeStock
	})
}

func (r *Runtime) observeTradeLikeDomain(domain, baseDir, table, date string, instruments []Instrument, include func(Instrument) bool) (reconcileObservation, error) {
	var rows int64
	expected := 0
	covered := 0
	cursorSummary := make([]string, 0, len(instruments))
	tables := make(map[string]int64)
	for _, instrument := range instruments {
		if !include(instrument) {
			continue
		}
		expected++
		filename := filepath.Join(baseDir, instrument.Code+".db")
		count, err := countSQLiteTableRows(filename, table, "TradeDate = ?", date)
		if err != nil {
			return reconcileObservation{}, err
		}
		tables[instrument.Code+"/"+table] = count
		rows += count
		cursor, err := r.store.GetCollectCursor(domain, string(instrument.AssetType), instrument.Code, "")
		if err != nil {
			return reconcileObservation{}, err
		}
		if cursor != nil && cursor.Cursor != "" && tradeDateAtOrAfter(cursor.Cursor, date) {
			covered++
			cursorSummary = append(cursorSummary, instrument.Code+"="+cursor.Cursor)
		}
	}
	return reconcileObservation{rows: rows, expectedItems: expected, coveredItems: covered, cursorSummary: strings.Join(cursorSummary, ", "), tables: tables}, nil
}

func (r *Runtime) observeLiveCapture(date string, instruments []Instrument) (reconcileObservation, error) {
	var rows int64
	expected := 0
	covered := 0
	cursorSummary := make([]string, 0, len(instruments))
	tables := make(map[string]int64)
	for _, instrument := range instruments {
		if instrument.AssetType != AssetTypeStock && instrument.AssetType != AssetTypeETF {
			continue
		}
		expected++
		filename := filepath.Join(r.cfg.Live.BaseDir, instrument.Code+".db")
		minuteRows, err := countSQLiteTableRows(filename, "MinuteLive", "TradeDate = ?", date)
		if err != nil {
			return reconcileObservation{}, err
		}
		tradeRows, err := countSQLiteTableRows(filename, "TradeLive", "TradeDate = ?", date)
		if err != nil {
			return reconcileObservation{}, err
		}
		tables[instrument.Code+"/MinuteLive"] = minuteRows
		tables[instrument.Code+"/TradeLive"] = tradeRows
		rows += minuteRows + tradeRows
		cursor, err := r.store.GetCollectCursor("live_capture", string(instrument.AssetType), instrument.Code, "")
		if err != nil {
			return reconcileObservation{}, err
		}
		if cursor != nil && cursor.Cursor != "" && tradeDateAtOrAfter(cursor.Cursor, date) {
			covered++
			cursorSummary = append(cursorSummary, instrument.Code+"="+cursor.Cursor)
		}
	}
	return reconcileObservation{rows: rows, expectedItems: expected, coveredItems: covered, cursorSummary: strings.Join(cursorSummary, ", "), tables: tables}, nil
}

func (r *Runtime) observeFinance(date string, instruments []Instrument) (reconcileObservation, error) {
	var expected int
	for _, instrument := range instruments {
		if instrument.AssetType == AssetTypeStock {
			expected++
		}
	}
	rows, err := countSQLiteTableRows(filepath.Join(r.cfg.Fundamentals.BaseDir, "finance.db"), "Finance", "UpdatedDate >= ?", date)
	if err != nil {
		return reconcileObservation{}, err
	}
	covered := 0
	cursorSummary := make([]string, 0, expected)
	for _, instrument := range instruments {
		if instrument.AssetType != AssetTypeStock {
			continue
		}
		cursor, err := r.store.GetCollectCursor("finance", MetadataAssetType, instrument.Code, "")
		if err != nil {
			return reconcileObservation{}, err
		}
		if cursor != nil && cursor.Cursor != "" && tradeDateAtOrAfter(cursor.Cursor, date) {
			covered++
			cursorSummary = append(cursorSummary, instrument.Code+"="+cursor.Cursor)
		}
	}
	return reconcileObservation{
		rows:          rows,
		expectedItems: expected,
		coveredItems:  covered,
		cursorSummary: strings.Join(cursorSummary, ", "),
		tables:        map[string]int64{"Finance": rows},
	}, nil
}

func (r *Runtime) observeF10(instruments []Instrument) (reconcileObservation, error) {
	var expected int
	for _, instrument := range instruments {
		if instrument.AssetType == AssetTypeStock {
			expected++
		}
	}
	categories, err := countSQLiteTableRows(filepath.Join(r.cfg.Fundamentals.BaseDir, "f10.db"), "F10Category", "")
	if err != nil {
		return reconcileObservation{}, err
	}
	contents, err := countSQLiteTableRows(filepath.Join(r.cfg.Fundamentals.BaseDir, "f10.db"), "F10Content", "")
	if err != nil {
		return reconcileObservation{}, err
	}
	covered := 0
	cursorSummary := make([]string, 0, expected)
	for _, instrument := range instruments {
		if instrument.AssetType != AssetTypeStock {
			continue
		}
		cursor, err := r.store.GetCollectCursor("f10", MetadataAssetType, instrument.Code, "")
		if err != nil {
			return reconcileObservation{}, err
		}
		if cursor != nil && cursor.Cursor != "" {
			covered++
			cursorSummary = append(cursorSummary, instrument.Code+"="+cursor.Cursor)
		}
	}
	return reconcileObservation{
		rows:          categories + contents,
		expectedItems: expected,
		coveredItems:  covered,
		cursorSummary: strings.Join(cursorSummary, ", "),
		tables: map[string]int64{
			"F10Category": categories,
			"F10Content":  contents,
		},
	}, nil
}

func countSQLiteTableRows(filename, table, where string, args ...any) (int64, error) {
	if _, err := os.Stat(filename); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	engine, err := openMetadataEngine(filename)
	if err != nil {
		return 0, err
	}
	defer engine.Close()
	session := engine.Table(table)
	if where != "" {
		session = session.Where(where, args...)
	}
	return session.Count()
}

func unixCursorAtOrAfter(cursor string, target int64) bool {
	value, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil {
		return false
	}
	return value >= target
}

func tradeDateAtOrAfter(cursor, target string) bool {
	return cursor == target || tradeDateAfter(cursor, target)
}
