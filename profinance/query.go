package profinance

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/injoyai/tdx/protocol"
)

type storedReportRecord struct {
	ReportVersionID       int64
	FullCode              string
	ReportDate            string
	AnnounceDateRaw       string
	EffectiveAnnounceDate string
	AnnounceDateSource    string
	SourceReportFile      string
	FieldValues           map[string]interface{}
	MissingFieldCodes     []string
	IsLatestCorrected     bool
}

func (s *Service) Snapshot(ctx context.Context, query SnapshotQuery) (*SnapshotResult, error) {
	fieldCodes, err := validateFieldCodes(DefaultRegistry(), query.FieldCodes)
	if err != nil {
		return nil, err
	}
	fullCode, err := validateFullCode(query.FullCode)
	if err != nil {
		return nil, err
	}
	db, err := s.openDB()
	if err != nil {
		return nil, err
	}
	knowledgeCutoff, err := resolveKnowledgeCutoff(ctx, db, query.AsOfDate)
	if err != nil {
		return nil, err
	}

	records, err := loadStoredReportRecords(ctx, db, fullCode)
	if err != nil {
		return nil, err
	}
	record, err := selectSnapshotRecord(records, query, knowledgeCutoff)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, &QueryError{
			ErrorCode:  "NOT_FOUND",
			HTTPStatus: 404,
			Retryable:  false,
			Message:    "professional finance snapshot not found",
		}
	}
	fieldValues, missingFields := selectRequestedFieldValues(record, fieldCodes)
	return &SnapshotResult{
		FullCode:        record.FullCode,
		ReportDate:      record.ReportDate,
		AnnounceDate:    record.EffectiveAnnounceDate,
		AsOfDate:        normalizeOptionalDate(query.AsOfDate),
		KnowledgeCutoff: knowledgeCutoff,
		Source:          profFinanceSource,
		FieldValues:     fieldValues,
		MissingFields:   missingFields,
		Coverage: CoverageInfo{
			Available:             true,
			AnnounceDateSource:    record.AnnounceDateSource,
			EffectiveAnnounceDate: record.EffectiveAnnounceDate,
			SourceReportFile:      record.SourceReportFile,
		},
	}, nil
}

func (s *Service) History(ctx context.Context, query HistoryQuery) (*HistoryResult, error) {
	fieldCodes, err := validateFieldCodes(DefaultRegistry(), query.FieldCodes)
	if err != nil {
		return nil, err
	}
	fullCode, err := validateFullCode(query.FullCode)
	if err != nil {
		return nil, err
	}
	if query.Limit <= 0 {
		query.Limit = 40
	}
	if query.Limit > 40 {
		query.Limit = 40
	}
	if strings.TrimSpace(query.Period) == "" {
		query.Period = "all"
	}
	if query.Period != "all" && query.Period != "quarterly" && query.Period != "annual" {
		return nil, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "invalid history period",
			Details: map[string]any{
				"field":  "period",
				"reason": "period must be quarterly|annual|all",
			},
		}
	}

	db, err := s.openDB()
	if err != nil {
		return nil, err
	}
	knowledgeCutoff, err := resolveKnowledgeCutoff(ctx, db, query.AsOfDate)
	if err != nil {
		return nil, err
	}
	records, err := loadStoredReportRecords(ctx, db, fullCode)
	if err != nil {
		return nil, err
	}

	selected := make([]storedReportRecord, 0)
	for _, group := range groupRecordsByReportDate(records) {
		record := selectRecordForReportDate(group, knowledgeCutoff, query.AsOfDate != "")
		if record == nil {
			continue
		}
		if query.StartReportDate != "" && record.ReportDate < query.StartReportDate {
			continue
		}
		if query.EndReportDate != "" && record.ReportDate > query.EndReportDate {
			continue
		}
		if !matchesHistoryPeriod(record.ReportDate, query.Period) {
			continue
		}
		selected = append(selected, *record)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ReportDate > selected[j].ReportDate })
	if len(selected) > query.Limit {
		selected = selected[:query.Limit]
	}

	items := make([]HistoryItem, 0, len(selected))
	for _, record := range selected {
		fieldValues, missingFields := selectRequestedFieldValues(&record, fieldCodes)
		items = append(items, HistoryItem{
			ReportDate:       record.ReportDate,
			AnnounceDate:     record.EffectiveAnnounceDate,
			FieldValues:      fieldValues,
			MissingFields:    missingFields,
			SourceReportFile: record.SourceReportFile,
		})
	}

	return &HistoryResult{
		FullCode:        fullCode,
		AsOfDate:        normalizeOptionalDate(query.AsOfDate),
		KnowledgeCutoff: knowledgeCutoff,
		FieldCodes:      fieldCodes,
		List:            items,
	}, nil
}

func (s *Service) Coverage(ctx context.Context, query CoverageQuery) (*CoverageResult, error) {
	fieldCodes, err := validateFieldCodes(DefaultRegistry(), query.FieldCodes)
	if err != nil {
		return nil, err
	}
	fullCode, err := validateFullCode(query.FullCode)
	if err != nil {
		return nil, err
	}

	db, err := s.openDB()
	if err != nil {
		return nil, err
	}
	knowledgeCutoff, err := resolveKnowledgeCutoff(ctx, db, query.AsOfDate)
	if err != nil {
		return nil, err
	}
	records, err := loadStoredReportRecords(ctx, db, fullCode)
	if err != nil {
		return nil, err
	}

	result := &CoverageResult{
		FullCode:            fullCode,
		RequestedFieldCodes: fieldCodes,
		EvaluatedFieldCodes: fieldCodes,
		KnowledgeCutoff:     knowledgeCutoff,
	}
	if len(records) == 0 {
		result.MissingFields = append([]string(nil), fieldCodes...)
		result.Status = "no_coverage"
		result.StatusReason = "no_professional_finance_reports"
		return result, nil
	}

	result.LatestReportDate = maxReportDate(records)
	for reportDate, group := range groupRecordsByReportDate(records) {
		if record := selectRecordForReportDate(group, knowledgeCutoff, query.AsOfDate != ""); record != nil {
			result.AvailableReports = append(result.AvailableReports, reportDate)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result.AvailableReports)))

	record := (*storedReportRecord)(nil)
	if query.ReportDate != "" {
		group := groupRecordsByReportDate(records)[query.ReportDate]
		record = selectRecordForReportDate(group, knowledgeCutoff, query.AsOfDate != "")
	} else {
		record, err = selectSnapshotRecord(records, SnapshotQuery{
			FullCode:   fullCode,
			AsOfDate:   query.AsOfDate,
			FieldCodes: fieldCodes,
			PeriodMode: defaultCoveragePeriodMode(query.AsOfDate),
		}, knowledgeCutoff)
		if err != nil {
			return nil, err
		}
	}

	if record == nil {
		result.MissingFields = append([]string(nil), fieldCodes...)
		result.Status = "report_unavailable"
		result.StatusReason = "requested_report_not_visible"
		return result, nil
	}

	fieldValues, missingFields := selectRequestedFieldValues(record, fieldCodes)
	for fieldCode := range fieldValues {
		result.AvailableFieldCodes = append(result.AvailableFieldCodes, fieldCode)
	}
	sort.Strings(result.AvailableFieldCodes)
	result.MissingFields = missingFields
	if len(missingFields) == 0 {
		result.Status = "available"
		result.StatusReason = "all_requested_fields_available"
	} else {
		result.Status = "field_missing"
		result.StatusReason = "requested_fields_missing"
	}
	return result, nil
}

func (s *Service) CrossSection(ctx context.Context, query CrossSectionQuery) (*CrossSectionResult, error) {
	fieldCodes, err := validateFieldCodes(DefaultRegistry(), query.FieldCodes)
	if err != nil {
		return nil, err
	}
	if len(query.FullCodes) == 0 {
		return nil, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "full_codes is required",
			Details: map[string]any{
				"field":  "full_codes",
				"reason": "at least one full_code is required",
			},
		}
	}
	if len(query.FullCodes) > 500 {
		return nil, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "too many full_codes",
			Details: map[string]any{
				"field":  "full_codes",
				"reason": "maximum 500 full_codes per request",
			},
		}
	}
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 500 {
		query.Limit = 500
	}
	normalizedCodes := make([]string, 0, len(query.FullCodes))
	seen := make(map[string]struct{}, len(query.FullCodes))
	for _, fullCode := range query.FullCodes {
		value, err := validateFullCode(fullCode)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalizedCodes = append(normalizedCodes, value)
	}
	sort.Strings(normalizedCodes)
	offset, err := decodeCursor(query.Cursor)
	if err != nil {
		return nil, err
	}
	if offset > len(normalizedCodes) {
		offset = len(normalizedCodes)
	}
	end := offset + query.Limit
	if end > len(normalizedCodes) {
		end = len(normalizedCodes)
	}

	result := &CrossSectionResult{
		AsOfDate:   normalizeOptionalDate(query.AsOfDate),
		FieldCodes: fieldCodes,
		Items:      make([]CrossSectionItem, 0, end-offset),
	}
	for _, fullCode := range normalizedCodes[offset:end] {
		snapshot, err := s.Snapshot(ctx, SnapshotQuery{
			FullCode:   fullCode,
			ReportDate: query.ReportDate,
			AsOfDate:   query.AsOfDate,
			FieldCodes: fieldCodes,
			PeriodMode: query.PeriodMode,
		})
		if err != nil {
			if queryErr, ok := err.(*QueryError); ok && queryErr.ErrorCode == "NOT_FOUND" {
				result.Items = append(result.Items, CrossSectionItem{
					FullCode:      fullCode,
					FieldValues:   map[string]interface{}{},
					MissingFields: append([]string(nil), fieldCodes...),
					Coverage: CoverageInfo{
						Available: false,
					},
				})
				continue
			}
			return nil, err
		}
		result.ReportDate = snapshot.ReportDate
		result.KnowledgeCutoff = snapshot.KnowledgeCutoff
		result.Items = append(result.Items, CrossSectionItem{
			FullCode:      fullCode,
			ReportDate:    snapshot.ReportDate,
			AnnounceDate:  snapshot.AnnounceDate,
			FieldValues:   snapshot.FieldValues,
			MissingFields: snapshot.MissingFields,
			Coverage:      snapshot.Coverage,
		})
	}
	if end < len(normalizedCodes) {
		next := encodeCursor(end)
		result.NextCursor = &next
	}
	if result.KnowledgeCutoff == "" {
		db, err := s.openDB()
		if err != nil {
			return nil, err
		}
		knowledgeCutoff, err := resolveKnowledgeCutoff(ctx, db, query.AsOfDate)
		if err != nil {
			return nil, err
		}
		result.KnowledgeCutoff = knowledgeCutoff
	}
	return result, nil
}

func (s *Service) LatestForCode(ctx context.Context, code string) (*Snapshot, error) {
	fullCode := normalizeLegacyFullCode(code)
	if fullCode == "" {
		return nil, fmt.Errorf("professional finance requires code")
	}
	result, err := s.Snapshot(ctx, SnapshotQuery{
		FullCode:   fullCode,
		FieldCodes: legacyFieldCodes(),
		AsOfDate:   s.now().Format("20060102"),
		PeriodMode: "latest_available",
	})
	if queryErr, ok := err.(*QueryError); ok && queryErr.ErrorCode == "SOURCE_NOT_READY" {
		if syncErr := s.Sync(ctx); syncErr != nil {
			return nil, syncErr
		}
		result, err = s.Snapshot(ctx, SnapshotQuery{
			FullCode:   fullCode,
			FieldCodes: legacyFieldCodes(),
			AsOfDate:   s.now().Format("20060102"),
			PeriodMode: "latest_available",
		})
	}
	if err != nil {
		return nil, err
	}
	return legacySnapshotFromResult(fullCode, result), nil
}

func (s *Service) ListForCode(ctx context.Context, code string, limit int, startDate, endDate string) ([]Snapshot, error) {
	fullCode := normalizeLegacyFullCode(code)
	if fullCode == "" {
		return nil, fmt.Errorf("professional finance requires code")
	}
	result, err := s.History(ctx, HistoryQuery{
		FullCode:        fullCode,
		FieldCodes:      legacyFieldCodes(),
		AsOfDate:        s.now().Format("20060102"),
		StartReportDate: startDate,
		EndReportDate:   endDate,
		Limit:           limit,
		Period:          "all",
	})
	if queryErr, ok := err.(*QueryError); ok && queryErr.ErrorCode == "SOURCE_NOT_READY" {
		if syncErr := s.Sync(ctx); syncErr != nil {
			return nil, syncErr
		}
		result, err = s.History(ctx, HistoryQuery{
			FullCode:        fullCode,
			FieldCodes:      legacyFieldCodes(),
			AsOfDate:        s.now().Format("20060102"),
			StartReportDate: startDate,
			EndReportDate:   endDate,
			Limit:           limit,
			Period:          "all",
		})
	}
	if err != nil {
		return nil, err
	}
	items := make([]Snapshot, 0, len(result.List))
	for _, item := range result.List {
		items = append(items, legacySnapshotFromHistoryItem(fullCode, item))
	}
	return items, nil
}

func loadStoredReportRecords(ctx context.Context, db *sql.DB, fullCode string) ([]storedReportRecord, error) {
	rows, err := db.QueryContext(ctx, `
SELECT rv.report_version_id, rv.full_code, rv.report_date, COALESCE(rv.announce_date_raw, ''),
	rv.effective_announce_date, rv.announce_date_source, COALESCE(sf.filename, ''),
	rp.field_values, rp.missing_field_codes, rv.is_latest_corrected
FROM prof_finance_report_version rv
JOIN prof_finance_report_payload rp ON rp.report_version_id = rv.report_version_id
JOIN prof_finance_source_file sf ON sf.source_file_id = rv.source_file_id
WHERE rv.full_code = ?
ORDER BY rv.report_date DESC, rv.effective_announce_date DESC, rv.report_version_id DESC`, fullCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]storedReportRecord, 0)
	for rows.Next() {
		var record storedReportRecord
		var fieldValuesJSON string
		var missingJSON string
		var isLatestCorrected int
		if err := rows.Scan(
			&record.ReportVersionID,
			&record.FullCode,
			&record.ReportDate,
			&record.AnnounceDateRaw,
			&record.EffectiveAnnounceDate,
			&record.AnnounceDateSource,
			&record.SourceReportFile,
			&fieldValuesJSON,
			&missingJSON,
			&isLatestCorrected,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(fieldValuesJSON), &record.FieldValues); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(missingJSON), &record.MissingFieldCodes); err != nil {
			return nil, err
		}
		record.IsLatestCorrected = isLatestCorrected == 1
		out = append(out, record)
	}
	return out, rows.Err()
}

func resolveKnowledgeCutoff(ctx context.Context, db *sql.DB, asOfDate string) (string, error) {
	var watermarkDate string
	err := db.QueryRowContext(ctx, `SELECT watermark_date FROM prof_finance_source_watermark WHERE source_name = ?`, "gpcw").Scan(&watermarkDate)
	if err == sql.ErrNoRows {
		return "", &QueryError{
			ErrorCode:  "SOURCE_NOT_READY",
			HTTPStatus: 503,
			Retryable:  true,
			Message:    "professional finance source is not ready",
		}
	}
	if err != nil {
		return "", err
	}
	if asOfDate == "" {
		return watermarkDate, nil
	}
	asOfDate = normalizeOptionalDate(asOfDate)
	if asOfDate == "" {
		return "", &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "invalid as_of_date",
			Details: map[string]any{
				"field":  "as_of_date",
				"reason": "expected YYYYMMDD or YYYY-MM-DD",
			},
		}
	}
	if asOfDate < watermarkDate {
		return asOfDate, nil
	}
	return watermarkDate, nil
}

func selectSnapshotRecord(records []storedReportRecord, query SnapshotQuery, knowledgeCutoff string) (*storedReportRecord, error) {
	periodMode := strings.TrimSpace(query.PeriodMode)
	if periodMode == "" {
		periodMode = "latest_available"
	}
	if periodMode != "latest_available" && periodMode != "latest_report" && periodMode != "exact" {
		return nil, &QueryError{
			ErrorCode:  "UNSUPPORTED_PERIOD_MODE",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "unsupported period_mode",
			Details: map[string]any{
				"field":  "period_mode",
				"reason": "period_mode must be latest_available|latest_report|exact",
			},
		}
	}
	grouped := groupRecordsByReportDate(records)
	switch periodMode {
	case "exact":
		if strings.TrimSpace(query.ReportDate) == "" {
			return nil, &QueryError{
				ErrorCode:  "INVALID_ARGUMENT",
				HTTPStatus: 400,
				Retryable:  false,
				Message:    "report_date is required for exact",
				Details: map[string]any{
					"field":  "report_date",
					"reason": "period_mode=exact requires report_date",
				},
			}
		}
		return selectRecordForReportDate(grouped[query.ReportDate], knowledgeCutoff, query.AsOfDate != ""), nil
	case "latest_report":
		reportDates := sortedReportDates(grouped)
		if len(reportDates) == 0 {
			return nil, nil
		}
		return latestCorrectedRecord(grouped[reportDates[0]]), nil
	default:
		if strings.TrimSpace(query.AsOfDate) == "" {
			return nil, &QueryError{
				ErrorCode:  "INVALID_ARGUMENT",
				HTTPStatus: 400,
				Retryable:  false,
				Message:    "as_of_date is required for latest_available",
				Details: map[string]any{
					"field":  "as_of_date",
					"reason": "period_mode=latest_available requires as_of_date",
				},
			}
		}
		for _, reportDate := range sortedReportDates(grouped) {
			if record := selectRecordForReportDate(grouped[reportDate], knowledgeCutoff, true); record != nil {
				return record, nil
			}
		}
		return nil, nil
	}
}

func selectRecordForReportDate(records []storedReportRecord, knowledgeCutoff string, requireVisible bool) *storedReportRecord {
	if len(records) == 0 {
		return nil
	}
	if !requireVisible {
		return latestCorrectedRecord(records)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].EffectiveAnnounceDate == records[j].EffectiveAnnounceDate {
			return records[i].ReportVersionID > records[j].ReportVersionID
		}
		return records[i].EffectiveAnnounceDate > records[j].EffectiveAnnounceDate
	})
	for _, record := range records {
		if record.EffectiveAnnounceDate != "" && record.EffectiveAnnounceDate <= knowledgeCutoff {
			copy := record
			return &copy
		}
	}
	return nil
}

func latestCorrectedRecord(records []storedReportRecord) *storedReportRecord {
	for _, record := range records {
		if record.IsLatestCorrected {
			copy := record
			return &copy
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ReportVersionID > records[j].ReportVersionID })
	copy := records[0]
	return &copy
}

func groupRecordsByReportDate(records []storedReportRecord) map[string][]storedReportRecord {
	grouped := make(map[string][]storedReportRecord)
	for _, record := range records {
		grouped[record.ReportDate] = append(grouped[record.ReportDate], record)
	}
	return grouped
}

func sortedReportDates(grouped map[string][]storedReportRecord) []string {
	reportDates := make([]string, 0, len(grouped))
	for reportDate := range grouped {
		reportDates = append(reportDates, reportDate)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(reportDates)))
	return reportDates
}

func selectRequestedFieldValues(record *storedReportRecord, fieldCodes []string) (map[string]interface{}, []string) {
	fieldValues := make(map[string]interface{}, len(fieldCodes))
	missingFields := make([]string, 0)
	for _, fieldCode := range fieldCodes {
		value, ok := record.FieldValues[fieldCode]
		if !ok {
			missingFields = append(missingFields, fieldCode)
			continue
		}
		fieldValues[fieldCode] = value
	}
	return fieldValues, missingFields
}

func matchesHistoryPeriod(reportDate, period string) bool {
	if period == "all" {
		return true
	}
	if len(reportDate) != 8 {
		return false
	}
	switch period {
	case "annual":
		return strings.HasSuffix(reportDate, "1231")
	case "quarterly":
		for _, suffix := range []string{"0331", "0630", "0930", "1231"} {
			if strings.HasSuffix(reportDate, suffix) {
				return true
			}
		}
	}
	return false
}

func validateFieldCodes(registry *Registry, fieldCodes []string) ([]string, error) {
	if len(fieldCodes) == 0 {
		return nil, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "field_codes is required",
			Details: map[string]any{
				"field":  "field_codes",
				"reason": "at least one field_code is required",
			},
		}
	}
	out := make([]string, 0, len(fieldCodes))
	seen := make(map[string]struct{}, len(fieldCodes))
	for _, raw := range fieldCodes {
		fieldCode := strings.TrimSpace(raw)
		if fieldCode == "" {
			continue
		}
		field, ok := registry.ByFieldCode(fieldCode)
		if !ok || !field.Supported {
			return nil, &QueryError{
				ErrorCode:  "UNSUPPORTED_FIELD",
				HTTPStatus: 400,
				Retryable:  false,
				Message:    "unsupported field_code",
				Details: map[string]any{
					"field":  "field_codes",
					"reason": fieldCode,
				},
			}
		}
		if _, ok := seen[fieldCode]; ok {
			continue
		}
		seen[fieldCode] = struct{}{}
		out = append(out, fieldCode)
	}
	if len(out) == 0 {
		return nil, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "field_codes is required",
		}
	}
	return out, nil
}

func validateFullCode(fullCode string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(fullCode))
	if isStrictFullCodeValue(value) {
		return value, nil
	}
	return "", &QueryError{
		ErrorCode:  "INVALID_ARGUMENT",
		HTTPStatus: 400,
		Retryable:  false,
		Message:    "invalid full_code",
		Details: map[string]any{
			"field":  "full_code",
			"reason": "use full_code with market prefix, for example sh600000",
		},
	}
}

func isStrictFullCodeValue(value string) bool {
	if len(value) != 8 {
		return false
	}
	if !strings.HasPrefix(value, "sh") && !strings.HasPrefix(value, "sz") && !strings.HasPrefix(value, "bj") {
		return false
	}
	for _, ch := range value[2:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func normalizeOptionalDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "-", "")
	if len(value) != 8 {
		return ""
	}
	if _, err := strconv.Atoi(value); err != nil {
		return ""
	}
	return value
}

func maxReportDate(records []storedReportRecord) string {
	maxDate := ""
	for _, record := range records {
		if record.ReportDate > maxDate {
			maxDate = record.ReportDate
		}
	}
	return maxDate
}

func defaultCoveragePeriodMode(asOfDate string) string {
	if strings.TrimSpace(asOfDate) != "" {
		return "latest_available"
	}
	return "latest_report"
}

func encodeCursor(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("offset:%d", offset)))
}

func decodeCursor(cursor string) (int, error) {
	if strings.TrimSpace(cursor) == "" {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "invalid cursor",
			Details: map[string]any{
				"field":  "cursor",
				"reason": err.Error(),
			},
		}
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 || parts[0] != "offset" {
		return 0, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "invalid cursor",
		}
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 {
		return 0, &QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "invalid cursor",
		}
	}
	return offset, nil
}

func legacyFieldCodes() []string {
	return []string{
		"book_value_per_share",
		"total_shares",
		"float_a_shares",
		"net_profit_ttm",
		"operating_revenue_ttm",
		"weighted_roe",
	}
}

func normalizeLegacyFullCode(code string) string {
	clean := strings.ToLower(strings.TrimSpace(code))
	if clean == "" {
		return ""
	}
	return protocol.AddPrefix(clean)
}

func legacySnapshotFromResult(fullCode string, result *SnapshotResult) *Snapshot {
	snapshot := &Snapshot{
		Code:             strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(fullCode, "sh"), "sz"), "bj"),
		ReportDate:       result.ReportDate,
		SourceReportFile: result.Coverage.SourceReportFile,
	}
	assignLegacyFieldValues(snapshot, result.FieldValues)
	return snapshot
}

func legacySnapshotFromHistoryItem(fullCode string, item HistoryItem) Snapshot {
	snapshot := Snapshot{
		Code:             strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(fullCode, "sh"), "sz"), "bj"),
		ReportDate:       item.ReportDate,
		SourceReportFile: item.SourceReportFile,
	}
	assignLegacyFieldValues(&snapshot, item.FieldValues)
	return snapshot
}

func assignLegacyFieldValues(snapshot *Snapshot, fieldValues map[string]interface{}) {
	snapshot.BookValuePerShare = floatFromAny(fieldValues["book_value_per_share"])
	snapshot.TotalShares = floatFromAny(fieldValues["total_shares"])
	snapshot.FloatAShares = floatFromAny(fieldValues["float_a_shares"])
	snapshot.NetProfitTTM = floatFromAny(fieldValues["net_profit_ttm"])
	snapshot.WeightedROE = floatFromAny(fieldValues["weighted_roe"])
	snapshot.RevenueTTMYuan = floatFromAny(fieldValues["operating_revenue_ttm"]) * 10000
}

func floatFromAny(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}
