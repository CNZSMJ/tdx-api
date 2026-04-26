package lifecycle

import (
	"path/filepath"
	"testing"
)

func TestParquetLibraryProofWritesAndReadsMillionRows(t *testing.T) {
	result, err := RunParquetLibraryProof(filepath.Join(t.TempDir(), "proof.parquet"), 1_000_000)
	if err != nil {
		t.Fatalf("parquet proof: %v", err)
	}
	if result.Library != "github.com/parquet-go/parquet-go" {
		t.Fatalf("library = %s", result.Library)
	}
	if result.GoModVersion == "" || result.GoCompatibility != "go1.20-compatible" {
		t.Fatalf("unexpected library compatibility: %+v", result)
	}
	if result.RowsWritten != 1_000_000 || result.RowsRead != 1_000_000 {
		t.Fatalf("row count mismatch: %+v", result)
	}
	if result.FileBytes <= 0 {
		t.Fatalf("file size not reported: %+v", result)
	}
	for _, field := range []string{"schema_version", "code", "trade_date", "seq", "price_milli"} {
		if !result.SchemaFields[field] {
			t.Fatalf("schema field %s missing: %+v", field, result.SchemaFields)
		}
	}
}
