package tdx

import (
	"github.com/injoyai/tdx/internal/appenv"
)

const defaultDatabaseDirFallback = "./data/database"

var DefaultDatabaseDir = resolveDefaultDatabaseDir()

func resolveDefaultDatabaseDir() string {
	return appenv.ResolveTDXDataDir(defaultDatabaseDirFallback)
}
