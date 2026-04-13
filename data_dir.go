package tdx

import (
	"os"
	"strings"
)

const defaultDatabaseDirFallback = "./data/database"

var DefaultDatabaseDir = resolveDefaultDatabaseDir()

func resolveDefaultDatabaseDir() string {
	dir := strings.TrimSpace(os.Getenv("TDX_DATA_DIR"))
	if dir == "" {
		return defaultDatabaseDirFallback
	}
	return dir
}
