package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const klineGapDowngradeTimeLayout = "2006-01-02 15:04:05 -07:00"

func (r *Runtime) DowngradeKlineGaps(ctx context.Context, opts KlineGapDowngradeOptions) (*KlineGapDowngradeReport, error) {
	if r == nil || r.kline == nil {
		return nil, errors.New("collector runtime kline service not initialized")
	}
	report, err := r.kline.DowngradeCollectGaps(ctx, opts)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, errors.New("nil kline gap downgrade report")
	}

	reportPath, err := r.writeKlineGapDowngradeReport(report)
	if err != nil {
		return nil, err
	}
	report.ReportPath = reportPath

	taskListPath, err := r.writeKlineGapDowngradeTaskList(report)
	if err != nil {
		return nil, err
	}
	report.TaskListPath = taskListPath
	if _, err := r.writeKlineGapDowngradeReport(report); err != nil {
		return nil, err
	}
	return report, nil
}

func (r *Runtime) writeKlineGapDowngradeReport(report *KlineGapDowngradeReport) (string, error) {
	if report == nil {
		return "", errors.New("nil kline gap downgrade report")
	}
	if r.cfg.ReportDir == "" {
		return "", errors.New("runtime report directory is empty")
	}
	if err := os.MkdirAll(r.cfg.ReportDir, 0o777); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("kline-gap-downgrade-%s.json", report.StartedAt.Format("20060102T150405"))
	path := filepath.Join(r.cfg.ReportDir, filename)
	report.ReportPath = path
	bs, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, bs, 0o666); err != nil {
		return "", err
	}
	return path, nil
}

func (r *Runtime) writeKlineGapDowngradeTaskList(report *KlineGapDowngradeReport) (string, error) {
	if report == nil {
		return "", errors.New("nil kline gap downgrade report")
	}
	if strings.TrimSpace(report.ReportPath) == "" {
		return "", errors.New("kline gap downgrade report path is empty")
	}

	var b strings.Builder
	b.WriteString("# Kline Gap Downgrade Task List\n\n")
	b.WriteString(fmt.Sprintf("- generated_at: %s\n", report.StartedAt.Format(klineGapDowngradeTimeLayout)))
	b.WriteString(fmt.Sprintf("- dry_run: %t\n", report.DryRun))
	b.WriteString(fmt.Sprintf("- reason: %s\n", report.Reason))
	b.WriteString(fmt.Sprintf("- scanned: %d\n", report.Scanned))
	b.WriteString(fmt.Sprintf("- matched: %d\n", report.Matched))
	b.WriteString(fmt.Sprintf("- downgraded: %d\n", report.Downgraded))
	b.WriteString(fmt.Sprintf("- remaining_open_gaps: %d\n", report.RemainingOpenGaps))
	b.WriteString(fmt.Sprintf("- degraded_gap_count: %d\n\n", report.DegradedGapCount))
	b.WriteString("| GapID | Instrument | Period | Window | Task Dates | Governance Task |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, entry := range report.Entries {
		window := entry.StartDate
		if entry.EndDate != "" && entry.EndDate != entry.StartDate {
			window += ".." + entry.EndDate
		}
		b.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %s |\n",
			entry.GapID,
			entry.Instrument,
			entry.Period,
			window,
			strings.Join(entry.TaskDates, ", "),
			entry.GovernanceTask,
		))
	}

	path := klineGapDowngradeTaskListPath(report.ReportPath)
	if err := os.WriteFile(path, []byte(b.String()), 0o666); err != nil {
		return "", err
	}
	return path, nil
}
