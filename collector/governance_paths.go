package collector

import (
	"path/filepath"
	"strings"

	"github.com/injoyai/tdx/internal/appenv"
)

const (
	governanceDirName    = "governance"
	governanceDBName     = "system_governance.db"
	governanceLockName   = "system_governance.lock"
	governanceReportsDir = "reports"
)

type GovernancePaths struct {
	BaseDataDir string `json:"base_data_dir"`
	RootDir     string `json:"root_dir"`
	DBPath      string `json:"db_path"`
	LockPath    string `json:"lock_path"`
	ReportsDir  string `json:"reports_dir"`
}

func ResolveGovernancePaths(baseDataDir string) GovernancePaths {
	baseDataDir = strings.TrimSpace(baseDataDir)
	if baseDataDir == "" {
		baseDataDir = appenv.ResolveTDXDataDir("./data/database")
	}
	rootDir := filepath.Join(baseDataDir, governanceDirName)
	return GovernancePaths{
		BaseDataDir: baseDataDir,
		RootDir:     rootDir,
		DBPath:      filepath.Join(rootDir, governanceDBName),
		LockPath:    filepath.Join(rootDir, governanceLockName),
		ReportsDir:  filepath.Join(rootDir, governanceReportsDir),
	}
}
