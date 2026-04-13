package appenv

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/joho/godotenv"
)

var loadOnce sync.Once

func init() {
	EnsureLoaded()
}

func EnsureLoaded() {
	loadOnce.Do(func() {
		if envPath, ok := findDotEnv(); ok {
			_ = godotenv.Load(envPath)
			normalizeRelativeTDXDataDir(envPath)
		}
	})
}

func findDotEnv() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for {
		candidate := filepath.Join(dir, ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
