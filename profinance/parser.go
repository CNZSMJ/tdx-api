package profinance

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/tdx/protocol"
)

type rawFieldValue struct {
	Numeric   float64
	Text      string
	ValueType string
}

type parsedFinanceRow struct {
	FullCode   string
	ReportDate string
	Fields     map[int]rawFieldValue
}

type parsedRawReport struct {
	ReportDate string
	FieldCount int
	RowCount   int
	Rows       []parsedFinanceRow
}

func parseZipReportRaw(bs []byte, report ReportFile, registry *Registry) (*parsedRawReport, error) {
	reader, err := zip.NewReader(bytes.NewReader(bs), int64(len(bs)))
	if err != nil {
		return nil, err
	}
	for _, file := range reader.File {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".dat") {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		return parseDATReportRaw(raw, report, registry)
	}
	return nil, fmt.Errorf("zip %s has no dat file", report.Filename)
}

func parseDATReportRaw(data []byte, report ReportFile, registry *Registry) (*parsedRawReport, error) {
	if len(data) < 20 {
		return nil, errors.New("dat report too short")
	}

	var header struct {
		_          int16
		ReportDate uint32
		Count      uint16
		_          uint32
		ReportSize uint32
		_          uint32
	}
	if err := binary.Read(bytes.NewReader(data[:20]), binary.LittleEndian, &header); err != nil {
		return nil, err
	}

	reportDate := deriveReportDate(report.Filename)
	if reportDate == "" && header.ReportDate != 0 {
		reportDate = fmt.Sprintf("%08d", header.ReportDate)
	}

	const headerSize = 20
	const stockItemSize = 11
	fieldCount := int(header.ReportSize / 4)
	rows := make([]parsedFinanceRow, 0, int(header.Count))
	rowIndexByCode := make(map[string]int, int(header.Count))
	orderedFields := registry.All()

	for i := 0; i < int(header.Count); i++ {
		offset := headerSize + i*stockItemSize
		if offset+stockItemSize > len(data) {
			return nil, fmt.Errorf("stock header out of range at index %d", i)
		}
		code := strings.TrimSpace(string(data[offset : offset+6]))
		if code == "" {
			continue
		}
		foa := int(binary.LittleEndian.Uint32(data[offset+7 : offset+11]))
		if foa <= 0 || foa+int(header.ReportSize) > len(data) {
			continue
		}

		fullCode := protocol.AddPrefix(code)
		fields := map[int]rawFieldValue{
			0: {
				Text:      reportDate,
				ValueType: "date",
			},
		}

		for _, field := range orderedFields {
			if field.SourceFieldID == 0 || field.SourceFieldID > fieldCount {
				continue
			}
			value, ok := readSourceFieldValue(data, foa, field)
			if !ok {
				continue
			}
			fields[field.SourceFieldID] = value
		}

		parsedRow := parsedFinanceRow{
			FullCode:   fullCode,
			ReportDate: reportDate,
			Fields:     fields,
		}
		if existingIndex, ok := rowIndexByCode[fullCode]; ok {
			if equal, differingFieldIDs := equalParsedFinanceRows(rows[existingIndex], parsedRow); equal {
				continue
			} else {
				return nil, fmt.Errorf("duplicate full_code %s has conflicting tracked fields %v", fullCode, differingFieldIDs)
			}
		}

		rowIndexByCode[fullCode] = len(rows)
		rows = append(rows, parsedRow)
	}

	return &parsedRawReport{
		ReportDate: reportDate,
		FieldCount: fieldCount,
		RowCount:   len(rows),
		Rows:       rows,
	}, nil
}

func equalParsedFinanceRows(left, right parsedFinanceRow) (bool, []int) {
	if left.FullCode != right.FullCode || left.ReportDate != right.ReportDate {
		return false, []int{0}
	}

	seen := make(map[int]struct{}, len(left.Fields)+len(right.Fields))
	for sourceFieldID := range left.Fields {
		seen[sourceFieldID] = struct{}{}
	}
	for sourceFieldID := range right.Fields {
		seen[sourceFieldID] = struct{}{}
	}

	differingFieldIDs := make([]int, 0)
	for sourceFieldID := range seen {
		leftValue, leftOK := left.Fields[sourceFieldID]
		rightValue, rightOK := right.Fields[sourceFieldID]
		if !leftOK || !rightOK || leftValue != rightValue {
			differingFieldIDs = append(differingFieldIDs, sourceFieldID)
		}
	}
	sort.Ints(differingFieldIDs)
	return len(differingFieldIDs) == 0, differingFieldIDs
}

func readSourceFieldValue(data []byte, rowOffset int, field FieldDefinition) (rawFieldValue, bool) {
	if field.SourceFieldID <= 0 {
		return rawFieldValue{}, false
	}
	fieldOffset := rowOffset + (field.SourceFieldID-1)*4
	if fieldOffset+4 > len(data) {
		return rawFieldValue{}, false
	}
	bits := binary.LittleEndian.Uint32(data[fieldOffset : fieldOffset+4])
	raw := float64(math.Float32frombits(bits))
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return rawFieldValue{}, false
	}
	if field.ValueType == "date" {
		date := normalizeTDXDate(raw)
		if date == "" {
			return rawFieldValue{}, false
		}
		return rawFieldValue{Numeric: raw, Text: date, ValueType: field.ValueType}, true
	}
	return rawFieldValue{Numeric: raw, ValueType: field.ValueType}, true
}

func normalizeTDXDate(raw float64) string {
	if raw == 0 {
		return ""
	}
	value := int64(math.Round(raw))
	if value <= 0 {
		return ""
	}
	text := fmt.Sprintf("%d", value)
	switch len(text) {
	case 8:
		if _, err := time.Parse("20060102", text); err != nil {
			return ""
		}
		return text
	case 6:
		year := int(value / 10000)
		monthDay := int(value % 10000)
		if year >= 80 {
			text = fmt.Sprintf("19%02d%04d", year, monthDay)
		} else {
			text = fmt.Sprintf("20%02d%04d", year, monthDay)
		}
		if _, err := time.Parse("20060102", text); err != nil {
			return ""
		}
		return text
	default:
		return ""
	}
}

func buildPayloadForRow(registry *Registry, row parsedFinanceRow) (map[string]interface{}, []string) {
	fields := registry.All()
	payload := make(map[string]interface{}, len(row.Fields))
	missing := make([]string, 0, len(fields))

	for _, field := range fields {
		raw, ok := row.Fields[field.SourceFieldID]
		if !ok {
			missing = append(missing, field.FieldCode)
			continue
		}
		switch raw.ValueType {
		case "date":
			if raw.Text == "" {
				missing = append(missing, field.FieldCode)
				continue
			}
			payload[field.FieldCode] = raw.Text
		default:
			payload[field.FieldCode] = normalizeNumericValue(field, raw.Numeric)
		}
	}

	sort.Strings(missing)
	return payload, missing
}

func normalizeNumericValue(field FieldDefinition, raw float64) interface{} {
	switch field.Unit {
	case "count", "shares":
		return int64(math.Round(raw))
	default:
		return raw
	}
}
