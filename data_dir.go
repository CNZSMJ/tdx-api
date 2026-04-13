package tdx

import (
	"os"
	"strings"

	_ "github.com/injoyai/tdx/internal/appenv"
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
