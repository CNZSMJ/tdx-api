package lifecycle

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestLogicalChecksumIsDeterministicByStableKey(t *testing.T) {
	rowsA := []map[string]any{
		{"code": "sh600000", "trade_date": "20260424", "seq": 2, "price": int64(12100)},
		{"code": "sh600000", "trade_date": "20260424", "seq": 1, "price": int64(12000)},
	}
	rowsB := []map[string]any{
		{"code": "sh600000", "trade_date": "20260424", "seq": 1, "price": int64(12000)},
		{"code": "sh600000", "trade_date": "20260424", "seq": 2, "price": int64(12100)},
	}
	keys := []string{"code", "trade_date", "seq"}
	fields := []string{"code", "trade_date", "seq", "price"}
	a, err := LogicalChecksum(rowsA, keys, fields)
	if err != nil {
		t.Fatalf("checksum A: %v", err)
	}
	b, err := LogicalChecksum(rowsB, keys, fields)
	if err != nil {
		t.Fatalf("checksum B: %v", err)
	}
	if a != b || len(a) != 64 {
		t.Fatalf("checksums = %q and %q", a, b)
	}
}

func TestLogicalChecksumRejectsNaNAndInf(t *testing.T) {
	if _, err := LogicalChecksum([]map[string]any{{"id": 1, "value": math.NaN()}}, []string{"id"}, []string{"id", "value"}); err == nil {
		t.Fatalf("NaN should be rejected")
	}
}

func TestFileChecksumSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "segment.parquet")
	if err := os.WriteFile(path, []byte("cold-data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := FileChecksum(path)
	if err != nil {
		t.Fatalf("file checksum: %v", err)
	}
	if got != "c10bd4675e40e19e1f31b91f25f7b5086d5c147a338b1bd15868cea90f992038" {
		t.Fatalf("checksum = %s", got)
	}
}
