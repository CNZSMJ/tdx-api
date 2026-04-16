package collector

import (
	"path/filepath"
	"testing"
)

func TestStateFileRoundTrip(t *testing.T) {
	statePath := repoPath("docs", "collector", "STATE.yaml")
	state, err := LoadStateFile(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	out := filepath.Join(t.TempDir(), "state.yaml")
	if err := SaveStateFile(out, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	loaded, err := LoadStateFile(out)
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}

	if loaded.CurrentPhase.ID != state.CurrentPhase.ID {
		t.Fatalf("current phase mismatch: got %q want %q", loaded.CurrentPhase.ID, state.CurrentPhase.ID)
	}
	if loaded.NextPhase.ID != state.NextPhase.ID {
		t.Fatalf("next phase mismatch: got %q want %q", loaded.NextPhase.ID, state.NextPhase.ID)
	}
}
