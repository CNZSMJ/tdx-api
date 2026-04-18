package profinance

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const profFinanceSource = "tdx_professional_finance"

type fieldCatalogCore struct {
	SourceFieldID int
	FieldCode     string
	FieldNameCN   string
	FieldNameEN   string
	Category      string
}

type StoragePrecision struct {
	MaxDigits     int `json:"max_digits"`
	DecimalPlaces int `json:"decimal_places"`
}

type FieldDefinition struct {
	FieldCode        string            `json:"field_code"`
	SourceFieldID    int               `json:"source_field_id"`
	ConceptCode      string            `json:"concept_code"`
	FieldNameCN      string            `json:"field_name_cn"`
	FieldNameEN      string            `json:"field_name_en"`
	Category         string            `json:"category"`
	Statement        string            `json:"statement"`
	PeriodSemantics  string            `json:"period_semantics"`
	Unit             string            `json:"unit"`
	ValueType        string            `json:"value_type"`
	StoragePrecision *StoragePrecision `json:"storage_precision"`
	DisplayPrecision *int              `json:"display_precision"`
	RoundingMode     string            `json:"rounding_mode"`
	Nullable         bool              `json:"nullable"`
	Source           string            `json:"source"`
	Supported        bool              `json:"supported"`
}

type Registry struct {
	fields     []FieldDefinition
	byField    map[string]FieldDefinition
	bySourceID map[int]FieldDefinition
	categories []string
}

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *Registry
)

func DefaultRegistry() *Registry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = buildRegistry()
	})
	return defaultRegistry
}

func (r *Registry) All() []FieldDefinition {
	if r == nil {
		return nil
	}
	out := make([]FieldDefinition, len(r.fields))
	copy(out, r.fields)
	return out
}

func (r *Registry) Categories() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.categories))
	copy(out, r.categories)
	return out
}

func (r *Registry) ByFieldCode(fieldCode string) (FieldDefinition, bool) {
	if r == nil {
		return FieldDefinition{}, false
	}
	field, ok := r.byField[strings.TrimSpace(fieldCode)]
	return field, ok
}

func (r *Registry) BySourceFieldID(sourceFieldID int) (FieldDefinition, bool) {
	if r == nil {
		return FieldDefinition{}, false
	}
	field, ok := r.bySourceID[sourceFieldID]
	return field, ok
}

func (r *Registry) Filter(category, query string) ([]FieldDefinition, error) {
	if r == nil {
		return nil, nil
	}

	category = strings.TrimSpace(category)
	if category != "" && category != "all" {
		if _, ok := r.categorySet()[category]; !ok {
			return nil, fmt.Errorf("unsupported category: %s", category)
		}
	}

	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]FieldDefinition, 0, len(r.fields))
	for _, field := range r.fields {
		if category != "" && category != "all" && field.Category != category {
			continue
		}
		if query != "" && !matchesFieldQuery(field, query) {
			continue
		}
		out = append(out, field)
	}
	return out, nil
}

func (r *Registry) categorySet() map[string]struct{} {
	set := make(map[string]struct{}, len(r.categories))
	for _, category := range r.categories {
		set[category] = struct{}{}
	}
	return set
}

func buildRegistry() *Registry {
	fields := make([]FieldDefinition, 0, len(fieldCatalogCoreData))
	byField := make(map[string]FieldDefinition, len(fieldCatalogCoreData))
	bySourceID := make(map[int]FieldDefinition, len(fieldCatalogCoreData))
	categorySet := make(map[string]struct{})

	for _, core := range fieldCatalogCoreData {
		field := enrichField(core)
		fields = append(fields, field)
		byField[field.FieldCode] = field
		bySourceID[field.SourceFieldID] = field
		categorySet[field.Category] = struct{}{}
	}

	sort.Slice(fields, func(i, j int) bool {
		return fields[i].SourceFieldID < fields[j].SourceFieldID
	})

	categories := make([]string, 0, len(categorySet))
	for category := range categorySet {
		categories = append(categories, category)
	}
	sort.Strings(categories)

	return &Registry{
		fields:     fields,
		byField:    byField,
		bySourceID: bySourceID,
		categories: categories,
	}
}

func enrichField(core fieldCatalogCore) FieldDefinition {
	valueType := valueTypeForField(core)
	unit := unitForField(core, valueType)
	displayPrecision := displayPrecisionForUnit(unit, valueType)

	return FieldDefinition{
		FieldCode:        core.FieldCode,
		SourceFieldID:    core.SourceFieldID,
		ConceptCode:      conceptCodeForField(core.FieldCode),
		FieldNameCN:      core.FieldNameCN,
		FieldNameEN:      core.FieldNameEN,
		Category:         core.Category,
		Statement:        statementForCategory(core.Category),
		PeriodSemantics:  periodSemanticsForField(core),
		Unit:             unit,
		ValueType:        valueType,
		StoragePrecision: storagePrecisionForUnit(unit, valueType),
		DisplayPrecision: displayPrecision,
		RoundingMode:     roundingModeForValueType(valueType),
		Nullable:         core.SourceFieldID != 0,
		Source:           profFinanceSource,
		Supported:        true,
	}
}

func matchesFieldQuery(field FieldDefinition, query string) bool {
	for _, candidate := range []string{
		field.FieldCode,
		field.FieldNameCN,
		field.FieldNameEN,
		field.ConceptCode,
		strconv.Itoa(field.SourceFieldID),
	} {
		if strings.Contains(strings.ToLower(candidate), query) {
			return true
		}
	}
	return false
}

func statementForCategory(category string) string {
	switch category {
	case "meta":
		return "meta"
	case "disclosure", "shareholder", "institutional_holding":
		return "disclosure"
	case "per_share":
		return "per_share"
	case "balance_sheet":
		return "balance_sheet"
	case "income_statement":
		return "income_statement"
	case "cash_flow_statement":
		return "cash_flow_statement"
	case "earnings_preview":
		return "preview"
	case "earnings_flash_report":
		return "flash_report"
	default:
		return "analysis"
	}
}

func periodSemanticsForField(core fieldCatalogCore) string {
	switch core.Category {
	case "earnings_preview":
		return "preview"
	case "earnings_flash_report":
		return "flash_report"
	case "single_quarter":
		return "single_quarter"
	case "balance_sheet", "shareholder", "institutional_holding", "capital_structure":
		return "instant"
	case "meta", "disclosure":
		return "report_period"
	}

	if strings.HasSuffix(core.FieldCode, "_ttm") || strings.Contains(core.FieldNameCN, "近一年") || strings.Contains(strings.ToLower(core.FieldNameEN), "ttm") {
		return "ttm"
	}
	if strings.Contains(core.FieldCode, "book_value_per_share") || strings.Contains(core.FieldCode, "capital_reserve_per_share") || strings.Contains(core.FieldCode, "undistributed_profit_per_share") {
		return "instant"
	}
	return "report_period"
}

func valueTypeForField(core fieldCatalogCore) string {
	if isDateField(core) {
		return "date"
	}
	return "number"
}

func unitForField(core fieldCatalogCore, valueType string) string {
	if valueType == "date" {
		return "date_yyyymmdd"
	}
	lowerCode := core.FieldCode
	if strings.Contains(lowerCode, "_count") || strings.Contains(core.FieldNameCN, "机构数") || strings.Contains(core.FieldNameCN, "股东人数") {
		return "count"
	}
	if strings.Contains(lowerCode, "shares") || strings.Contains(lowerCode, "shareholder") || strings.Contains(core.FieldNameCN, "持股量") || strings.Contains(core.FieldNameCN, "股本") || strings.Contains(core.FieldNameCN, "股份") || strings.Contains(core.FieldNameCN, "股)") {
		return "shares"
	}
	if strings.Contains(core.FieldNameCN, "天数") || strings.Contains(lowerCode, "_days") {
		return "days"
	}
	if strings.Contains(core.FieldNameCN, "倍") || strings.Contains(lowerCode, "_times") || strings.Contains(lowerCode, "multiple") || strings.Contains(lowerCode, "times_") {
		return "times"
	}
	if strings.Contains(core.FieldNameCN, "%") || strings.Contains(core.FieldNameCN, "率") || strings.Contains(lowerCode, "_ratio") || strings.Contains(lowerCode, "_margin") || strings.Contains(lowerCode, "_roe") || strings.Contains(lowerCode, "_growth") || lowerCode == "roe" || lowerCode == "weighted_roe" {
		return "percent"
	}
	if strings.Contains(core.FieldNameCN, "每股") {
		return "yuan"
	}
	if strings.Contains(core.FieldNameCN, "万元") {
		return "ten_thousand_yuan"
	}
	switch core.Category {
	case "balance_sheet", "income_statement", "cash_flow_statement":
		return "ten_thousand_yuan"
	case "profitability", "growth", "solvency", "operating_efficiency", "capital_structure", "cash_flow_analysis":
		return "ratio"
	default:
		return "number"
	}
}

func storagePrecisionForUnit(unit, valueType string) *StoragePrecision {
	if valueType != "number" {
		return nil
	}
	switch unit {
	case "count":
		return &StoragePrecision{MaxDigits: 12, DecimalPlaces: 0}
	case "shares":
		return &StoragePrecision{MaxDigits: 20, DecimalPlaces: 0}
	case "percent", "ratio", "times":
		return &StoragePrecision{MaxDigits: 12, DecimalPlaces: 6}
	case "days":
		return &StoragePrecision{MaxDigits: 10, DecimalPlaces: 2}
	case "yuan", "ten_thousand_yuan":
		return &StoragePrecision{MaxDigits: 20, DecimalPlaces: 4}
	default:
		return &StoragePrecision{MaxDigits: 20, DecimalPlaces: 4}
	}
}

func displayPrecisionForUnit(unit, valueType string) *int {
	if valueType != "number" {
		return nil
	}
	switch unit {
	case "count", "shares":
		return intPtr(0)
	case "ratio", "times":
		return intPtr(4)
	case "days":
		return intPtr(2)
	default:
		return intPtr(2)
	}
}

func roundingModeForValueType(valueType string) string {
	if valueType != "number" {
		return "none"
	}
	return "round_half_up"
}

func conceptCodeForField(fieldCode string) string {
	concept := fieldCode
	for _, prefix := range []string{"flash_report_", "earnings_preview_"} {
		concept = strings.TrimPrefix(concept, prefix)
	}
	for _, suffix := range []string{
		"_single_quarter",
		"_ttm",
		"_profitability",
		"_cash_flow_analysis",
		"_extended_balance_sheet",
		"_lower_bound",
		"_upper_bound",
	} {
		concept = strings.TrimSuffix(concept, suffix)
	}
	switch fieldCode {
	case "financial_report_announcement_date", "earnings_preview_announcement_date", "flash_report_announcement_date":
		return "announcement_date"
	}
	return concept
}

func isDateField(core fieldCatalogCore) bool {
	return strings.Contains(core.FieldNameCN, "日期") || strings.HasSuffix(core.FieldCode, "_date")
}

func intPtr(v int) *int {
	return &v
}
