package lifecycle

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func TestLocalColdStoragePutGetListRenameAndDelete(t *testing.T) {
	storage := NewLocalColdStorage(t.TempDir())
	uri := MustFormatColdURI(ColdURI{
		Dataset: "a-stock-market-tdx",
		Path:    "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-abcd-000.parquet",
	})
	if err := storage.Put(uri, []byte("cold-data")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if ok, err := storage.Exists(uri); err != nil || !ok {
		t.Fatalf("exists=%v err=%v", ok, err)
	}
	body, err := storage.Get(uri)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != "cold-data" {
		t.Fatalf("data = %q", data)
	}

	list, err := storage.List("tdx-cold://a-stock-market-tdx/cold/domain=trade")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0] != uri {
		t.Fatalf("list = %#v, want [%s]", list, uri)
	}

	dst := MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-abcd-001.parquet"})
	if err := storage.Rename(uri, dst); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if ok, _ := storage.Exists(uri); ok {
		t.Fatalf("source still exists after rename")
	}
	stat, err := storage.Stat(dst)
	if err != nil {
		t.Fatalf("stat renamed: %v", err)
	}
	if stat.Size != int64(len("cold-data")) {
		t.Fatalf("stat size = %d", stat.Size)
	}
	if err := storage.Delete(dst); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestLocalColdStorageRenameFailsFastAcrossDevices(t *testing.T) {
	storage := NewLocalColdStorage(t.TempDir())
	storage.deviceID = func(path string) (uint64, error) {
		if filepath.Base(path) == "part-a-000.parquet" {
			return 1, nil
		}
		return 2, nil
	}
	src := MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-a-000.parquet"})
	dst := MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-b-000.parquet"})
	if err := storage.Put(src, []byte("x")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := storage.Rename(src, dst); !errors.Is(err, ErrCrossDeviceRename) {
		t.Fatalf("rename error = %v, want ErrCrossDeviceRename", err)
	}
}
