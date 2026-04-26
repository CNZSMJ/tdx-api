package lifecycle

import (
	"path/filepath"
	"testing"
)

func TestColdURIParseFormatAndLocalMapping(t *testing.T) {
	raw := "tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-abcd-000.parquet"
	uri, err := ParseColdURI(raw)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	if uri.Dataset != "a-stock-market-tdx" || uri.Path != "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-abcd-000.parquet" {
		t.Fatalf("unexpected uri: %+v", uri)
	}
	if got := FormatColdURI(uri); got != raw {
		t.Fatalf("format = %s, want %s", got, raw)
	}
	path, err := LocalPathForColdURI("/tmp/cold-root", raw)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	want := filepath.Join("/tmp/cold-root", "a-stock-market-tdx", "cold", "domain=trade", "table=TradeHistory", "instrument=sh600000", "year=2024", "part-abcd-000.parquet")
	if path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
}

func TestColdURIRejectsUnsupportedSchemeAndTraversal(t *testing.T) {
	for _, raw := range []string{
		"file:///tmp/x",
		"tdx-cold://",
		"tdx-cold://dataset/cold/../escape.parquet",
	} {
		if _, err := ParseColdURI(raw); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
}
