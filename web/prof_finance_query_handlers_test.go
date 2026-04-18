package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/tdx/profinance"
)

func TestHandleProfFinanceHistorySnapshotCoverageAndCrossSection(t *testing.T) {
	service := newProfFinanceTestService(t)
	originalService := proFinanceService
	proFinanceService = service
	defer func() { proFinanceService = originalService }()

	originalLookup := profFinanceLookupName
	profFinanceLookupName = func(fullCode string) string {
		switch fullCode {
		case "sh600000":
			return "浦发银行"
		case "sz000001":
			return "平安银行"
		default:
			return ""
		}
	}
	defer func() { profFinanceLookupName = originalLookup }()

	t.Run("history", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/history?full_code=sh600000&field_codes=book_value_per_share,weighted_roe&as_of_date=20260417&period=all", nil)
		rec := httptest.NewRecorder()

		handleProfFinanceHistory(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var payload struct {
			Code      int    `json:"code"`
			RequestID string `json:"request_id"`
			Data      struct {
				FullCode        string `json:"full_code"`
				Name            string `json:"name"`
				KnowledgeCutoff string `json:"knowledge_cutoff"`
				List            []struct {
					ReportDate       string                 `json:"report_date"`
					AnnounceDate     string                 `json:"announce_date"`
					FieldValues      map[string]interface{} `json:"field_values"`
					MissingFields    []string               `json:"missing_fields"`
					SourceReportFile string                 `json:"source_report_file"`
				} `json:"list"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.Code != 0 || payload.RequestID == "" {
			t.Fatalf("unexpected envelope: %#v", payload)
		}
		if payload.Data.FullCode != "sh600000" || payload.Data.Name != "浦发银行" {
			t.Fatalf("unexpected identity: %#v", payload.Data)
		}
		if payload.Data.KnowledgeCutoff != "20260417" {
			t.Fatalf("knowledge_cutoff = %s, want 20260417", payload.Data.KnowledgeCutoff)
		}
		if len(payload.Data.List) != 1 {
			t.Fatalf("history list = %#v, want single visible record", payload.Data.List)
		}
		if payload.Data.List[0].ReportDate != "20251231" || payload.Data.List[0].AnnounceDate != "20260328" {
			t.Fatalf("unexpected history item %#v", payload.Data.List[0])
		}
		if _, ok := payload.Data.List[0].FieldValues["weighted_roe"]; ok {
			t.Fatalf("weighted_roe should be absent when missing")
		}
		if len(payload.Data.List[0].MissingFields) != 1 || payload.Data.List[0].MissingFields[0] != "weighted_roe" {
			t.Fatalf("missing_fields = %#v, want [weighted_roe]", payload.Data.List[0].MissingFields)
		}
		if payload.Data.List[0].SourceReportFile != "gpcw20251231.zip" {
			t.Fatalf("source_report_file = %s, want gpcw20251231.zip", payload.Data.List[0].SourceReportFile)
		}
	})

	t.Run("snapshot", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/snapshot?full_code=sh600000&field_codes=book_value_per_share,operating_revenue_ttm&period_mode=latest_available&as_of_date=20260417", nil)
		rec := httptest.NewRecorder()

		handleProfFinanceSnapshot(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var payload struct {
			Code      int    `json:"code"`
			RequestID string `json:"request_id"`
			Data      struct {
				FullCode        string                 `json:"full_code"`
				Name            string                 `json:"name"`
				ReportDate      string                 `json:"report_date"`
				AnnounceDate    string                 `json:"announce_date"`
				KnowledgeCutoff string                 `json:"knowledge_cutoff"`
				FieldValues     map[string]interface{} `json:"field_values"`
				MissingFields   []string               `json:"missing_fields"`
				Coverage        struct {
					Available             bool   `json:"available"`
					AnnounceDateSource    string `json:"announce_date_source"`
					EffectiveAnnounceDate string `json:"effective_announce_date"`
				} `json:"coverage"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.Code != 0 || payload.RequestID == "" {
			t.Fatalf("unexpected envelope: %#v", payload)
		}
		if payload.Data.FullCode != "sh600000" || payload.Data.Name != "浦发银行" {
			t.Fatalf("unexpected identity %#v", payload.Data)
		}
		if payload.Data.ReportDate != "20251231" || payload.Data.AnnounceDate != "20260328" {
			t.Fatalf("unexpected snapshot timing %#v", payload.Data)
		}
		if payload.Data.KnowledgeCutoff != "20260417" {
			t.Fatalf("knowledge_cutoff = %s, want 20260417", payload.Data.KnowledgeCutoff)
		}
		if len(payload.Data.MissingFields) != 0 {
			t.Fatalf("missing_fields = %#v, want empty", payload.Data.MissingFields)
		}
		if !payload.Data.Coverage.Available || payload.Data.Coverage.AnnounceDateSource != "gpcw_314" {
			t.Fatalf("unexpected coverage %#v", payload.Data.Coverage)
		}
	})

	t.Run("coverage", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/coverage?full_code=sh600000&field_codes=book_value_per_share,weighted_roe&as_of_date=20260417", nil)
		rec := httptest.NewRecorder()

		handleProfFinanceCoverage(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var payload struct {
			Code      int    `json:"code"`
			RequestID string `json:"request_id"`
			Data      struct {
				FullCode         string   `json:"full_code"`
				Name             string   `json:"name"`
				Status           string   `json:"status"`
				StatusReason     string   `json:"status_reason"`
				KnowledgeCutoff  string   `json:"knowledge_cutoff"`
				AvailableReports []string `json:"available_reports"`
				AvailableFields  []string `json:"available_field_codes"`
				MissingFields    []string `json:"missing_fields"`
				RequestedFields  []string `json:"requested_field_codes"`
				EvaluatedFields  []string `json:"evaluated_field_codes"`
				LatestReportDate string   `json:"latest_report_date"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.Code != 0 || payload.RequestID == "" {
			t.Fatalf("unexpected envelope: %#v", payload)
		}
		if payload.Data.Status != "field_missing" || payload.Data.StatusReason == "" {
			t.Fatalf("unexpected coverage status %#v", payload.Data)
		}
		if payload.Data.LatestReportDate != "20260331" {
			t.Fatalf("latest_report_date = %s, want 20260331", payload.Data.LatestReportDate)
		}
		if len(payload.Data.AvailableReports) != 1 || payload.Data.AvailableReports[0] != "20251231" {
			t.Fatalf("available_reports = %#v, want [20251231]", payload.Data.AvailableReports)
		}
		if len(payload.Data.MissingFields) != 1 || payload.Data.MissingFields[0] != "weighted_roe" {
			t.Fatalf("missing_fields = %#v, want [weighted_roe]", payload.Data.MissingFields)
		}
	})

	t.Run("cross-section", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/cross-section?full_codes=sh600000,sz000001&field_codes=book_value_per_share&period_mode=latest_available&as_of_date=20260417&limit=1", nil)
		rec := httptest.NewRecorder()

		handleProfFinanceCrossSection(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var payload struct {
			Code      int    `json:"code"`
			RequestID string `json:"request_id"`
			Data      struct {
				KnowledgeCutoff string  `json:"knowledge_cutoff"`
				NextCursor      *string `json:"next_cursor"`
				Items           []struct {
					FullCode string `json:"full_code"`
					Name     string `json:"name"`
					Coverage struct {
						Available bool `json:"available"`
					} `json:"coverage"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.Code != 0 || payload.RequestID == "" {
			t.Fatalf("unexpected envelope: %#v", payload)
		}
		if payload.Data.KnowledgeCutoff != "20260417" {
			t.Fatalf("knowledge_cutoff = %s, want 20260417", payload.Data.KnowledgeCutoff)
		}
		if len(payload.Data.Items) != 1 {
			t.Fatalf("items len = %d, want 1", len(payload.Data.Items))
		}
		if payload.Data.Items[0].Name == "" || !payload.Data.Items[0].Coverage.Available {
			t.Fatalf("unexpected item %#v", payload.Data.Items[0])
		}
		if payload.Data.NextCursor == nil || *payload.Data.NextCursor == "" {
			t.Fatalf("next_cursor should be present for first page")
		}

		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/cross-section?full_codes=sh600000,sz000001&field_codes=book_value_per_share&period_mode=latest_available&as_of_date=20260417&limit=1&cursor="+*payload.Data.NextCursor, nil)
		rec2 := httptest.NewRecorder()
		handleProfFinanceCrossSection(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("page 2 status = %d, want 200", rec2.Code)
		}
	})
}

func TestHandleProfFinanceCrossSectionRejectsTooManyCodes(t *testing.T) {
	tooMany := make([]string, 501)
	for i := range tooMany {
		tooMany[i] = "sh600000"
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/cross-section?full_codes="+strings.Join(tooMany, ",")+"&field_codes=book_value_per_share&period_mode=latest_available&as_of_date=20260417", nil)
	rec := httptest.NewRecorder()

	handleProfFinanceCrossSection(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var payload struct {
		RequestID string `json:"request_id"`
		Error     struct {
			ErrorCode string `json:"error_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.RequestID == "" || payload.Error.ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("unexpected error payload %#v", payload)
	}
}

func TestHandleProfFinanceSnapshotRejectsBareCode(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/snapshot?full_code=600000&field_codes=book_value_per_share&period_mode=latest_available&as_of_date=20260417", nil)
	rec := httptest.NewRecorder()

	handleProfFinanceSnapshot(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var payload struct {
		Error struct {
			ErrorCode string `json:"error_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("error_code = %s, want INVALID_ARGUMENT", payload.Error.ErrorCode)
	}
}

func TestHandleProfFinanceHistoryRejectsInvalidDateFilter(t *testing.T) {
	service := newProfFinanceTestService(t)
	originalService := proFinanceService
	proFinanceService = service
	defer func() { proFinanceService = originalService }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/history?full_code=sh600000&field_codes=book_value_per_share&as_of_date=2026/04/17", nil)
	rec := httptest.NewRecorder()

	handleProfFinanceHistory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var payload struct {
		Error struct {
			ErrorCode string `json:"error_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("error_code = %s, want INVALID_ARGUMENT", payload.Error.ErrorCode)
	}
}

func TestHandleProfFinanceSnapshotRejectsNonDigitFullCode(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/snapshot?full_code=shABCDEF&field_codes=book_value_per_share&period_mode=latest_available&as_of_date=20260417", nil)
	rec := httptest.NewRecorder()

	handleProfFinanceSnapshot(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var payload struct {
		Error struct {
			ErrorCode string `json:"error_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("error_code = %s, want INVALID_ARGUMENT", payload.Error.ErrorCode)
	}
}

func newProfFinanceTestService(t *testing.T) *profinance.Service {
	t.Helper()

	listBody := "gpcw20260331.zip,newhash,100\n" +
		"gpcw20251231.zip,oldhash,100\n"

	q12026 := buildProfFinanceZIPFixture(t, "gpcw20260331.dat", buildProfFinanceDATReportFixture(t, 20260331, []profFinanceRowFixture{
		{
			code: "600000",
			fields: map[int]float32{
				4:   14.12,
				238: 274006894,
				239: 168084707,
				276: 7123456789,
				283: 2754321,
				281: 13.1,
				314: 260430,
			},
		},
		{
			code: "000001",
			fields: map[int]float32{
				4:   11.24,
				238: 19405918198,
				239: 19405918198,
				276: 39512345678,
				283: 12004567,
				281: 9.15,
				314: 260321,
			},
		},
	}))
	fy2025 := buildProfFinanceZIPFixture(t, "gpcw20251231.dat", buildProfFinanceDATReportFixture(t, 20251231, []profFinanceRowFixture{
		{
			code: "600000",
			fields: map[int]float32{
				4:   13.88,
				238: 274006894,
				239: 168084707,
				276: 6123456789,
				283: 2456789,
				281: float32(math.NaN()),
				314: 260328,
			},
		},
		{
			code: "000001",
			fields: map[int]float32{
				4:   10.88,
				238: 19405918198,
				239: 19405918198,
				276: 38654321098,
				283: 11876543,
				281: 8.91,
				314: 260321,
			},
		},
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gpcw.txt":
			_, _ = w.Write([]byte(listBody))
		case "/gpcw20260331.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(q12026)
		case "/gpcw20251231.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(fy2025)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	service := profinance.NewService(t.TempDir(), profinance.Config{
		BaseURL:             server.URL,
		HTTPClient:          server.Client(),
		DisableAutoPrefetch: true,
		Now: func() time.Time {
			return time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
		},
	})
	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return service
}

func buildProfFinanceZIPFixture(t *testing.T, filename string, datParts ...[]byte) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	file, err := zw.Create(filename)
	if err != nil {
		t.Fatalf("Create zip entry: %v", err)
	}
	for _, part := range datParts {
		if _, err := file.Write(part); err != nil {
			t.Fatalf("Write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip writer: %v", err)
	}
	return buf.Bytes()
}

type profFinanceRowFixture struct {
	code   string
	fields map[int]float32
}

func buildProfFinanceDATReportFixture(t *testing.T, reportDate uint32, rows []profFinanceRowFixture) []byte {
	t.Helper()
	const headerSize = 20
	const stockItemSize = 11

	reportFieldCount := 314
	for _, row := range rows {
		for fieldID := range row.fields {
			if fieldID > reportFieldCount {
				reportFieldCount = fieldID
			}
		}
	}
	reportSize := reportFieldCount * 4
	rowOffset := headerSize + len(rows)*stockItemSize
	buf := make([]byte, rowOffset+len(rows)*reportSize)

	binary.LittleEndian.PutUint16(buf[0:2], 1)
	binary.LittleEndian.PutUint32(buf[2:6], reportDate)
	binary.LittleEndian.PutUint16(buf[6:8], uint16(len(rows)))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(reportSize))

	for i, row := range rows {
		headerOffset := headerSize + i*stockItemSize
		dataOffset := rowOffset + i*reportSize

		copy(buf[headerOffset:headerOffset+6], []byte(row.code))
		buf[headerOffset+6] = '1'
		binary.LittleEndian.PutUint32(buf[headerOffset+7:headerOffset+11], uint32(dataOffset))

		for fieldID := 1; fieldID <= reportFieldCount; fieldID++ {
			fieldOffset := dataOffset + (fieldID-1)*4
			binary.LittleEndian.PutUint32(buf[fieldOffset:fieldOffset+4], math.Float32bits(float32(math.NaN())))
		}
		for fieldID, value := range row.fields {
			fieldOffset := dataOffset + (fieldID-1)*4
			binary.LittleEndian.PutUint32(buf[fieldOffset:fieldOffset+4], math.Float32bits(value))
		}
	}
	return buf
}
