package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/internal/appenv"
)

type cliConfig struct {
	DataDir      string
	GovernanceDB string
	BackupDir    string
	Apply        bool
}

func main() {
	appenv.EnsureLoaded()
	if err := runCLI(os.Args[1:], os.Stdout); err != nil {
		log.Printf("governance repair failed: %v", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout io.Writer) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	mode := collectorpkg.GovernanceRepairModeDryRun
	if cfg.Apply {
		mode = collectorpkg.GovernanceRepairModeApply
	}
	result, err := collectorpkg.RunGovernanceRepairBatch(collectorpkg.GovernanceRepairBatchOptions{
		DBPath:    cfg.GovernanceDB,
		BackupDir: cfg.BackupDir,
		Mode:      mode,
	}, nil)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func parseConfig(args []string) (cliConfig, error) {
	appenv.EnsureLoaded()
	defaultDataDir := appenv.ResolveTDXDataDir(filepath.Join(".", "data", "database"))
	cfg := cliConfig{DataDir: defaultDataDir}
	fs := flag.NewFlagSet("governance-repair", flag.ContinueOnError)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "TDX data directory")
	fs.StringVar(&cfg.GovernanceDB, "governance-db", "", "governance DB path, defaults to <data-dir>/governance/system_governance.db")
	fs.StringVar(&cfg.BackupDir, "backup-dir", "", "backup directory, defaults to the governance DB directory")
	fs.BoolVar(&cfg.Apply, "apply", false, "apply planned governance repairs; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return cliConfig{}, err
	}
	if fs.NArg() > 0 {
		return cliConfig{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	paths := collectorpkg.ResolveGovernancePaths(cfg.DataDir)
	if cfg.GovernanceDB == "" {
		cfg.GovernanceDB = paths.DBPath
	}
	return cfg, nil
}
