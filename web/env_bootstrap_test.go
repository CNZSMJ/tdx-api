package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFindDotEnvFromWalksUpParents(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("TDX_DATA_DIR=./state\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, ok := findDotEnvFrom(nested)
	if !ok {
		t.Fatal("expected to find .env")
	}
	if got != envPath {
		t.Fatalf("findDotEnvFrom() = %q, want %q", got, envPath)
	}
}

func TestNormalizeRelativeEnvPath(t *testing.T) {
	t.Setenv("TDX_DATA_DIR", "./state/a-stock-market-tdx")
	envPath := filepath.Join(t.TempDir(), ".env")

	normalizeRelativeEnvPath("TDX_DATA_DIR", envPath)

	want := filepath.Join(filepath.Dir(envPath), "state", "a-stock-market-tdx")
	if got := os.Getenv("TDX_DATA_DIR"); got != want {
		t.Fatalf("TDX_DATA_DIR = %q, want %q", got, want)
	}
}

func TestEnsureDotEnvLoadedSkipsDuringGoTestByDefault(t *testing.T) {
	oldOnce := envLoadOnce
	envLoadOnce = sync.Once{}
	defer func() { envLoadOnce = oldOnce }()

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

	ensureDotEnvLoaded()

	if got := os.Getenv("TDX_DATA_DIR"); got != "" {
		t.Fatalf("TDX_DATA_DIR = %q, want empty", got)
	}
}
