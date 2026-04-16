package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/joho/godotenv"
)

const (
	dotEnvTDXDataDirEnv   = "TDX_DATA_DIR"
	dotEnvTDXInconPathEnv = "TDX_INCON_PATH"
)

var envLoadOnce sync.Once

func ensureDotEnvLoaded() {
	envLoadOnce.Do(func() {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		envPath, ok := findDotEnvFrom(cwd)
		if !ok {
			return
		}
		_ = godotenv.Load(envPath)
		normalizeRelativeEnvPath(dotEnvTDXDataDirEnv, envPath)
		normalizeRelativeEnvPath(dotEnvTDXInconPathEnv, envPath)
	})
}

func findDotEnvFrom(dir string) (string, bool) {
	current := dir
	for {
		candidate := filepath.Join(current, ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

func normalizeRelativeEnvPath(envKey, envPath string) {
	value := strings.TrimSpace(os.Getenv(envKey))
	if value == "" || filepath.IsAbs(value) {
		return
	}
	absolute := filepath.Clean(filepath.Join(filepath.Dir(envPath), value))
	_ = os.Setenv(envKey, absolute)
}
