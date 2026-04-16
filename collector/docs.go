package collector

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	currentPhaseRe = regexp.MustCompile("(?m)^- Current phase: `([^`]+)`$")
	nextPhaseRe    = regexp.MustCompile("(?m)^- Next phase after current completion: `([^`]+)`$")
)

type ProgressSnapshot struct {
	CurrentPhase string
	NextPhase    string
}

func LoadProgressSnapshot(filename string) (*ProgressSnapshot, error) {
	bs, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	text := string(bs)
	currentMatch := currentPhaseRe.FindStringSubmatch(text)
	if len(currentMatch) != 2 {
		return nil, fmt.Errorf("failed to parse current phase from %s", filename)
	}
	nextMatch := nextPhaseRe.FindStringSubmatch(text)
	if len(nextMatch) != 2 {
		return nil, fmt.Errorf("failed to parse next phase from %s", filename)
	}
	return &ProgressSnapshot{
		CurrentPhase: strings.TrimSpace(currentMatch[1]),
		NextPhase:    strings.TrimSpace(nextMatch[1]),
	}, nil
}

func ValidateDocsConsistency(progressPath, statePath string) error {
	progress, err := LoadProgressSnapshot(progressPath)
	if err != nil {
		return err
	}
	state, err := LoadStateFile(statePath)
	if err != nil {
		return err
	}

	expectedCurrent := fmt.Sprintf("%s - %s", normalizePhaseID(state.CurrentPhase.ID), state.CurrentPhase.Name)
	if progress.CurrentPhase != expectedCurrent {
		return fmt.Errorf("progress current phase mismatch: got %q want %q", progress.CurrentPhase, expectedCurrent)
	}

	expectedNext := fmt.Sprintf("%s - %s", normalizePhaseID(state.NextPhase.ID), state.NextPhase.Name)
	if strings.TrimSpace(state.NextPhase.ID) != "" && progress.NextPhase != expectedNext {
		return fmt.Errorf("progress next phase mismatch: got %q want %q", progress.NextPhase, expectedNext)
	}

	return nil
}

func normalizePhaseID(id string) string {
	return strings.TrimPrefix(id, "phase_")
}
