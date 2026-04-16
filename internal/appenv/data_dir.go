package appenv

import (
	"os"
	"path/filepath"
	"strings"
)

const TDXDataDirEnv = "TDX_DATA_DIR"

// ResolveTDXDataDir resolves the TDX data directory with this priority:
// 1. explicit TDX_DATA_DIR
// 2. provided fallback
func ResolveTDXDataDir(fallback string) string {
	if dir := strings.TrimSpace(os.Getenv(TDXDataDirEnv)); dir != "" {
		return dir
	}
	return fallback
}

func normalizeRelativeTDXDataDir(envPath string) {
	dir := strings.TrimSpace(os.Getenv(TDXDataDirEnv))
	if dir == "" || filepath.IsAbs(dir) {
		return
	}
	abs := filepath.Clean(filepath.Join(filepath.Dir(envPath), dir))
	_ = os.Setenv(TDXDataDirEnv, abs)
}
