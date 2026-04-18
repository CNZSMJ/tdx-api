package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleProfFinanceFieldsReturnsEnvelopeAndFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/fields?category=per_share&query=book", nil)
	rec := httptest.NewRecorder()

	handleProfFinanceFields(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var payload struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Data      struct {
			Count int `json:"count"`
			Items []struct {
				FieldCode          string `json:"field_code"`
				SourceFieldID      int    `json:"source_field_id"`
				Category           string `json:"category"`
				Statement          string `json:"statement"`
				PeriodSemantics    string `json:"period_semantics"`
				Unit               string `json:"unit"`
				ValueType          string `json:"value_type"`
				RoundingMode       string `json:"rounding_mode"`
				Supported          bool   `json:"supported"`
				StoragePrecision   any    `json:"storage_precision"`
				DisplayPrecision   *int   `json:"display_precision"`
				FieldNameCN        string `json:"field_name_cn"`
				FieldNameEN        string `json:"field_name_en"`
				ConceptCode        string `json:"concept_code"`
				Nullable           bool   `json:"nullable"`
				Source             string `json:"source"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Code != 0 {
		t.Fatalf("code = %d, want 0", payload.Code)
	}
	if payload.Message != "success" {
		t.Fatalf("message = %s, want success", payload.Message)
	}
	if payload.RequestID == "" {
		t.Fatalf("request_id should not be empty")
	}
	if payload.Data.Count == 0 || len(payload.Data.Items) == 0 {
		t.Fatalf("expected filtered field items, got %#v", payload.Data)
	}
	for _, item := range payload.Data.Items {
		if item.Category != "per_share" {
			t.Fatalf("category = %s, want per_share", item.Category)
		}
		if item.FieldCode == "" || item.SourceFieldID <= 0 {
			t.Fatalf("invalid item %#v", item)
		}
		if item.Statement == "" || item.PeriodSemantics == "" || item.Unit == "" || item.ValueType == "" {
			t.Fatalf("semantic metadata missing: %#v", item)
		}
		if item.RoundingMode == "" || item.Source != "tdx_professional_finance" {
			t.Fatalf("rounding/source missing: %#v", item)
		}
	}
}

func TestHandleProfFinanceFieldsRejectsInvalidCategory(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/prof-finance/fields?category=unknown", nil)
	rec := httptest.NewRecorder()

	handleProfFinanceFields(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	var payload struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Error     struct {
			ErrorCode string `json:"error_code"`
			HTTPStatus int   `json:"http_status"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.RequestID == "" {
		t.Fatalf("request_id should not be empty")
	}
	if payload.Error.ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("error_code = %s, want INVALID_ARGUMENT", payload.Error.ErrorCode)
	}
	if payload.Error.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("http_status = %d, want 400", payload.Error.HTTPStatus)
	}
	if payload.Error.Retryable {
		t.Fatalf("retryable = true, want false")
	}
}
