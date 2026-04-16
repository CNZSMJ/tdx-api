package collector

import (
	"path/filepath"
	"testing"
)

func TestDocsConsistency(t *testing.T) {
	progress := repoPath("docs", "collector", "PROGRESS.md")
	state := repoPath("docs", "collector", "STATE.yaml")
	if err := ValidateDocsConsistency(progress, state); err != nil {
		t.Fatalf("docs consistency: %v", err)
	}
}

func repoPath(parts ...string) string {
	pathParts := append([]string{".."}, parts...)
	return filepath.Join(pathParts...)
}
