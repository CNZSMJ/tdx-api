package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/injoyai/tdx/profinance"
)

type profFinanceErrorBody struct {
	ErrorCode  string         `json:"error_code"`
	ErrorType  string         `json:"error_type"`
	HTTPStatus int            `json:"http_status"`
	Retryable  bool           `json:"retryable"`
	Details    map[string]any `json:"details,omitempty"`
}

type profFinanceEnvelope struct {
	Code      int                   `json:"code"`
	Message   string                `json:"message"`
	RequestID string                `json:"request_id"`
	Data      any                   `json:"data,omitempty"`
	Error     *profFinanceErrorBody `json:"error,omitempty"`
}

var profFinanceLookupName = defaultProfFinanceLookupName

func handleProfFinanceFields(w http.ResponseWriter, r *http.Request) {
	requestID := newProfFinanceRequestID()
	startedAt := time.Now()
	resultStatus := "success"
	errorCode := ""
	defer func() {
		logProfFinanceRequest("/api/v1/prof-finance/fields", requestID, startedAt, resultStatus, errorCode, 0, 0)
	}()
	if r.Method != http.MethodGet {
		resultStatus = "error"
		errorCode = "INVALID_ARGUMENT"
		writeProfFinanceError(w, requestID, http.StatusMethodNotAllowed, 4051000, "method not allowed", "INVALID_ARGUMENT", false, map[string]any{
			"field":  "method",
			"reason": "only GET is supported",
		})
		return
	}

	registry := profinance.DefaultRegistry()
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	query := strings.TrimSpace(r.URL.Query().Get("query"))

	items, err := registry.Filter(category, query)
	if err != nil {
		resultStatus = "error"
		errorCode = "INVALID_ARGUMENT"
		writeProfFinanceError(w, requestID, http.StatusBadRequest, 4001001, "invalid argument", "INVALID_ARGUMENT", false, map[string]any{
			"field":  "category",
			"reason": err.Error(),
		})
		return
	}

	writeProfFinanceSuccess(w, requestID, map[string]any{
		"count":    len(items),
		"category": normalizeEmpty(category, "all"),
		"query":    query,
		"items":    items,
	})
}

func handleProfFinanceHistory(w http.ResponseWriter, r *http.Request) {
	requestID := newProfFinanceRequestID()
	startedAt := time.Now()
	resultStatus := "success"
	errorCode := ""
	fullCode := strings.TrimSpace(r.URL.Query().Get("full_code"))
	fieldCodes := parseProfFinanceCSV(r.URL.Query().Get("field_codes"))
	defer func() {
		logProfFinanceRequest("/api/v1/prof-finance/history", requestID, startedAt, resultStatus, errorCode, len(fieldCodes), 1)
	}()
	if err := validateProfFinanceSingleCodeRequest(fullCode, fieldCodes); err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	if proFinanceService == nil {
		resultStatus = "error"
		errorCode = "SOURCE_NOT_READY"
		writeProfFinanceError(w, requestID, http.StatusServiceUnavailable, profFinanceNumericCode("SOURCE_NOT_READY"), "professional finance source is not ready", "SOURCE_NOT_READY", true, nil)
		return
	}
	asOfDate, err := parseProfFinanceDateParam(r.URL.Query().Get("as_of_date"), "as_of_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	startReportDate, err := parseProfFinanceDateParam(r.URL.Query().Get("start_report_date"), "start_report_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	endReportDate, err := parseProfFinanceDateParam(r.URL.Query().Get("end_report_date"), "end_report_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	result, err := proFinanceService.History(r.Context(), profinance.HistoryQuery{
		FullCode:        fullCode,
		FieldCodes:      fieldCodes,
		AsOfDate:        asOfDate,
		StartReportDate: startReportDate,
		EndReportDate:   endReportDate,
		Limit:           parseProfFinanceLimit(r.URL.Query().Get("limit"), 40),
		Period:          strings.TrimSpace(r.URL.Query().Get("period")),
	})
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	result.Name = profFinanceLookupName(result.FullCode)
	writeProfFinanceSuccess(w, requestID, result)
}

func handleProfFinanceSnapshot(w http.ResponseWriter, r *http.Request) {
	requestID := newProfFinanceRequestID()
	startedAt := time.Now()
	resultStatus := "success"
	errorCode := ""
	fullCode := strings.TrimSpace(r.URL.Query().Get("full_code"))
	fieldCodes := parseProfFinanceCSV(r.URL.Query().Get("field_codes"))
	defer func() {
		logProfFinanceRequest("/api/v1/prof-finance/snapshot", requestID, startedAt, resultStatus, errorCode, len(fieldCodes), 1)
	}()
	if err := validateProfFinanceSingleCodeRequest(fullCode, fieldCodes); err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	if proFinanceService == nil {
		resultStatus = "error"
		errorCode = "SOURCE_NOT_READY"
		writeProfFinanceError(w, requestID, http.StatusServiceUnavailable, profFinanceNumericCode("SOURCE_NOT_READY"), "professional finance source is not ready", "SOURCE_NOT_READY", true, nil)
		return
	}
	reportDate, err := parseProfFinanceDateParam(r.URL.Query().Get("report_date"), "report_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	asOfDate, err := parseProfFinanceDateParam(r.URL.Query().Get("as_of_date"), "as_of_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	result, err := proFinanceService.Snapshot(r.Context(), profinance.SnapshotQuery{
		FullCode:   fullCode,
		ReportDate: reportDate,
		AsOfDate:   asOfDate,
		FieldCodes: fieldCodes,
		PeriodMode: strings.TrimSpace(r.URL.Query().Get("period_mode")),
	})
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	result.Name = profFinanceLookupName(result.FullCode)
	writeProfFinanceSuccess(w, requestID, result)
}

func handleProfFinanceCoverage(w http.ResponseWriter, r *http.Request) {
	requestID := newProfFinanceRequestID()
	startedAt := time.Now()
	resultStatus := "success"
	errorCode := ""
	fullCode := strings.TrimSpace(r.URL.Query().Get("full_code"))
	fieldCodes := parseProfFinanceCSV(r.URL.Query().Get("field_codes"))
	defer func() {
		logProfFinanceRequest("/api/v1/prof-finance/coverage", requestID, startedAt, resultStatus, errorCode, len(fieldCodes), 1)
	}()
	if err := validateProfFinanceSingleCodeRequest(fullCode, fieldCodes); err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	if proFinanceService == nil {
		resultStatus = "error"
		errorCode = "SOURCE_NOT_READY"
		writeProfFinanceError(w, requestID, http.StatusServiceUnavailable, profFinanceNumericCode("SOURCE_NOT_READY"), "professional finance source is not ready", "SOURCE_NOT_READY", true, nil)
		return
	}
	reportDate, err := parseProfFinanceDateParam(r.URL.Query().Get("report_date"), "report_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	asOfDate, err := parseProfFinanceDateParam(r.URL.Query().Get("as_of_date"), "as_of_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	result, err := proFinanceService.Coverage(r.Context(), profinance.CoverageQuery{
		FullCode:   fullCode,
		ReportDate: reportDate,
		AsOfDate:   asOfDate,
		FieldCodes: fieldCodes,
	})
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	result.Name = profFinanceLookupName(result.FullCode)
	writeProfFinanceSuccess(w, requestID, result)
}

func handleProfFinanceCrossSection(w http.ResponseWriter, r *http.Request) {
	requestID := newProfFinanceRequestID()
	startedAt := time.Now()
	resultStatus := "success"
	errorCode := ""
	fullCodes := parseProfFinanceCSV(r.URL.Query().Get("full_codes"))
	fieldCodes := parseProfFinanceCSV(r.URL.Query().Get("field_codes"))
	defer func() {
		logProfFinanceRequest("/api/v1/prof-finance/cross-section", requestID, startedAt, resultStatus, errorCode, len(fieldCodes), len(fullCodes))
	}()
	if err := validateProfFinanceCrossSectionRequest(fullCodes, fieldCodes); err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	if proFinanceService == nil {
		resultStatus = "error"
		errorCode = "SOURCE_NOT_READY"
		writeProfFinanceError(w, requestID, http.StatusServiceUnavailable, profFinanceNumericCode("SOURCE_NOT_READY"), "professional finance source is not ready", "SOURCE_NOT_READY", true, nil)
		return
	}
	reportDate, err := parseProfFinanceDateParam(r.URL.Query().Get("report_date"), "report_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	asOfDate, err := parseProfFinanceDateParam(r.URL.Query().Get("as_of_date"), "as_of_date")
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	result, err := proFinanceService.CrossSection(r.Context(), profinance.CrossSectionQuery{
		FullCodes:  fullCodes,
		ReportDate: reportDate,
		AsOfDate:   asOfDate,
		FieldCodes: fieldCodes,
		PeriodMode: strings.TrimSpace(r.URL.Query().Get("period_mode")),
		Limit:      parseProfFinanceLimit(r.URL.Query().Get("limit"), 500),
		Cursor:     strings.TrimSpace(r.URL.Query().Get("cursor")),
	})
	if err != nil {
		resultStatus = "error"
		errorCode = resolveProfFinanceErrorCode(err)
		writeProfFinanceQueryError(w, requestID, err)
		return
	}
	for i := range result.Items {
		result.Items[i].Name = profFinanceLookupName(result.Items[i].FullCode)
	}
	writeProfFinanceSuccess(w, requestID, result)
}

func writeProfFinanceSuccess(w http.ResponseWriter, requestID string, data any) {
	writeProfFinanceJSON(w, http.StatusOK, profFinanceEnvelope{
		Code:      0,
		Message:   "success",
		RequestID: requestID,
		Data:      data,
	})
}

func writeProfFinanceError(w http.ResponseWriter, requestID string, httpStatus int, code int, message, errorCode string, retryable bool, details map[string]any) {
	errorType := "server_error"
	if httpStatus >= 400 && httpStatus < 500 {
		errorType = "client_error"
	}
	writeProfFinanceJSON(w, httpStatus, profFinanceEnvelope{
		Code:      code,
		Message:   message,
		RequestID: requestID,
		Error: &profFinanceErrorBody{
			ErrorCode:  errorCode,
			ErrorType:  errorType,
			HTTPStatus: httpStatus,
			Retryable:  retryable,
			Details:    details,
		},
	})
}

func writeProfFinanceJSON(w http.ResponseWriter, status int, payload profFinanceEnvelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func newProfFinanceRequestID() string {
	return "req_pf_" + time.Now().UTC().Format("20060102_150405") + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func normalizeEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func writeProfFinanceQueryError(w http.ResponseWriter, requestID string, err error) {
	if queryErr, ok := err.(*profinance.QueryError); ok {
		writeProfFinanceError(w, requestID, queryErr.HTTPStatus, profFinanceNumericCode(queryErr.ErrorCode), queryErr.Message, queryErr.ErrorCode, queryErr.Retryable, queryErr.Details)
		return
	}
	writeProfFinanceError(w, requestID, http.StatusInternalServerError, profFinanceNumericCode("INTERNAL_ERROR"), "internal error", "INTERNAL_ERROR", false, map[string]any{
		"reason": err.Error(),
	})
}

func profFinanceNumericCode(errorCode string) int {
	switch errorCode {
	case "INVALID_ARGUMENT":
		return 4001001
	case "NOT_FOUND":
		return 4041001
	case "UNSUPPORTED_FIELD":
		return 4001404
	case "UNSUPPORTED_PERIOD_MODE":
		return 4001405
	case "SOURCE_NOT_READY":
		return 5031001
	case "RATE_LIMITED":
		return 4291001
	default:
		return 5001000
	}
}

func parseProfFinanceCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func parseProfFinanceDateParam(raw, field string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	value := normalizeDateString(raw)
	if value != "" {
		return value, nil
	}
	return "", &profinance.QueryError{
		ErrorCode:  "INVALID_ARGUMENT",
		HTTPStatus: 400,
		Retryable:  false,
		Message:    "invalid " + field,
		Details: map[string]any{
			"field":  field,
			"reason": "expected YYYYMMDD or YYYY-MM-DD",
		},
	}
}

func parseProfFinanceLimit(raw string, max int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	if max > 0 && value > max {
		return max
	}
	return value
}

func defaultProfFinanceLookupName(fullCode string) string {
	models, err := getAllCodeModels()
	if err != nil {
		return ""
	}
	fullCode = strings.ToLower(strings.TrimSpace(fullCode))
	for _, model := range models {
		if strings.ToLower(model.FullCode()) == fullCode {
			return model.Name
		}
	}
	return ""
}

func validateProfFinanceSingleCodeRequest(fullCode string, fieldCodes []string) error {
	if !isStrictFullCode(fullCode) {
		return &profinance.QueryError{
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
	if len(fieldCodes) == 0 {
		return &profinance.QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "field_codes is required",
		}
	}
	return nil
}

func validateProfFinanceCrossSectionRequest(fullCodes, fieldCodes []string) error {
	if len(fullCodes) == 0 {
		return &profinance.QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "full_codes is required",
		}
	}
	if len(fullCodes) > 500 {
		return &profinance.QueryError{
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
	for _, fullCode := range fullCodes {
		if !isStrictFullCode(fullCode) {
			return &profinance.QueryError{
				ErrorCode:  "INVALID_ARGUMENT",
				HTTPStatus: 400,
				Retryable:  false,
				Message:    "invalid full_code",
				Details: map[string]any{
					"field":  "full_codes",
					"reason": fullCode,
				},
			}
		}
	}
	if len(fieldCodes) == 0 {
		return &profinance.QueryError{
			ErrorCode:  "INVALID_ARGUMENT",
			HTTPStatus: 400,
			Retryable:  false,
			Message:    "field_codes is required",
		}
	}
	return nil
}

func isStrictFullCode(fullCode string) bool {
	fullCode = strings.ToLower(strings.TrimSpace(fullCode))
	if len(fullCode) != 8 {
		return false
	}
	if !strings.HasPrefix(fullCode, "sh") && !strings.HasPrefix(fullCode, "sz") && !strings.HasPrefix(fullCode, "bj") {
		return false
	}
	for _, ch := range fullCode[2:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func resolveProfFinanceErrorCode(err error) string {
	if queryErr, ok := err.(*profinance.QueryError); ok {
		return queryErr.ErrorCode
	}
	return "INTERNAL_ERROR"
}

func logProfFinanceRequest(route, requestID string, startedAt time.Time, resultStatus, errorCode string, fieldCodeCount, fullCodeCount int) {
	log.Printf("prof_finance request_id=%s route=%s latency_ms=%d result_status=%s error_code=%s field_code_count=%d full_code_count=%d",
		requestID,
		route,
		time.Since(startedAt).Milliseconds(),
		resultStatus,
		errorCode,
		fieldCodeCount,
		fullCodeCount,
	)
}
