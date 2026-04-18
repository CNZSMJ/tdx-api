package profinance

import "testing"

func TestDefaultRegistryCoversFullBaseline(t *testing.T) {
	registry := DefaultRegistry()

	fields := registry.All()
	if len(fields) != 403 {
		t.Fatalf("field count = %d, want 403", len(fields))
	}

	categories := registry.Categories()
	if len(categories) != 17 {
		t.Fatalf("category count = %d, want 17", len(categories))
	}

	seen := make(map[int]struct{}, len(fields))
	for _, field := range fields {
		if _, ok := seen[field.SourceFieldID]; ok {
			t.Fatalf("duplicate source_field_id = %d", field.SourceFieldID)
		}
		seen[field.SourceFieldID] = struct{}{}
	}

	for _, sourceFieldID := range []int{4, 40, 74, 107, 238, 242, 276, 281, 283, 304, 319, 501, 580} {
		field, ok := registry.BySourceFieldID(sourceFieldID)
		if !ok {
			t.Fatalf("source_field_id %d missing", sourceFieldID)
		}
		if field.FieldCode == "" || field.FieldNameCN == "" || field.FieldNameEN == "" {
			t.Fatalf("field %d metadata incomplete: %#v", sourceFieldID, field)
		}
		if field.Statement == "" || field.PeriodSemantics == "" || field.Unit == "" || field.ValueType == "" {
			t.Fatalf("field %d semantic metadata incomplete: %#v", sourceFieldID, field)
		}
		if field.RoundingMode == "" {
			t.Fatalf("field %d rounding_mode missing", sourceFieldID)
		}
		if field.Source != "tdx_professional_finance" {
			t.Fatalf("field %d source = %s, want tdx_professional_finance", sourceFieldID, field.Source)
		}
	}
}

func TestDefaultRegistryProvidesExpectedKeyMetadata(t *testing.T) {
	registry := DefaultRegistry()

	bookValue, ok := registry.ByFieldCode("book_value_per_share")
	if !ok {
		t.Fatalf("book_value_per_share missing")
	}
	if bookValue.SourceFieldID != 4 {
		t.Fatalf("book_value_per_share source_field_id = %d, want 4", bookValue.SourceFieldID)
	}
	if bookValue.Category != "per_share" {
		t.Fatalf("book_value_per_share category = %s, want per_share", bookValue.Category)
	}
	if bookValue.Statement != "per_share" {
		t.Fatalf("book_value_per_share statement = %s, want per_share", bookValue.Statement)
	}
	if bookValue.PeriodSemantics != "instant" {
		t.Fatalf("book_value_per_share period_semantics = %s, want instant", bookValue.PeriodSemantics)
	}
	if bookValue.Unit != "yuan" {
		t.Fatalf("book_value_per_share unit = %s, want yuan", bookValue.Unit)
	}
	if bookValue.ValueType != "number" {
		t.Fatalf("book_value_per_share value_type = %s, want number", bookValue.ValueType)
	}
	if bookValue.DisplayPrecision == nil || *bookValue.DisplayPrecision != 2 {
		t.Fatalf("book_value_per_share display_precision = %#v, want 2", bookValue.DisplayPrecision)
	}

	revenueTTM, ok := registry.ByFieldCode("operating_revenue_ttm")
	if !ok {
		t.Fatalf("operating_revenue_ttm missing")
	}
	if revenueTTM.SourceFieldID != 283 {
		t.Fatalf("operating_revenue_ttm source_field_id = %d, want 283", revenueTTM.SourceFieldID)
	}
	if revenueTTM.Category != "income_statement" {
		t.Fatalf("operating_revenue_ttm category = %s, want income_statement", revenueTTM.Category)
	}
	if revenueTTM.Statement != "income_statement" {
		t.Fatalf("operating_revenue_ttm statement = %s, want income_statement", revenueTTM.Statement)
	}
	if revenueTTM.PeriodSemantics != "ttm" {
		t.Fatalf("operating_revenue_ttm period_semantics = %s, want ttm", revenueTTM.PeriodSemantics)
	}
	if revenueTTM.Unit != "ten_thousand_yuan" {
		t.Fatalf("operating_revenue_ttm unit = %s, want ten_thousand_yuan", revenueTTM.Unit)
	}
	if !revenueTTM.Supported {
		t.Fatalf("operating_revenue_ttm supported = false, want true")
	}
}
