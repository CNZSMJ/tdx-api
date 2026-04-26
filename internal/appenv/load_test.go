package appenv

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEnsureLoadedSkipsDotEnvDuringGoTestByDefault(t *testing.T) {
	oldOnce := loadOnce
	loadOnce = sync.Once{}
	defer func() { loadOnce = oldOnce }()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TDX_DATA_DIR=/should/not/load\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldCwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Setenv("TDX_DATA_DIR", "")
	t.Setenv("TDX_TEST_LOAD_DOTENV", "")

	EnsureLoaded()

	if got := os.Getenv("TDX_DATA_DIR"); got != "" {
		t.Fatalf("TDX_DATA_DIR = %q, want empty", got)
	}
}
