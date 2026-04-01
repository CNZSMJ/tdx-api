package collector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type StateFile struct {
	Project string `yaml:"project"`
	Goal    string `yaml:"goal"`

	CurrentPhase NamedPhase `yaml:"current_phase"`
	NextPhase    NamedPhase `yaml:"next_phase"`
	CurrentTask  NamedTask  `yaml:"current_task"`

	RequiredReadOrder  []string `yaml:"required_read_order"`
	AllowedToCommit    bool     `yaml:"allowed_to_commit"`
	AllowedToAdvance   bool     `yaml:"allowed_to_advance"`
	PhaseExitCriteria  []string `yaml:"phase_exit_criteria"`
	LastVerifiedCommit *string  `yaml:"last_verified_commit"`
	LastVerifiedAt     *string  `yaml:"last_verified_at"`
	LastTestSuite      []string `yaml:"last_test_suite"`
	BlockingIssues     []string `yaml:"blocking_issues"`
	RequiredEvidence   []string `yaml:"required_evidence"`
	AntiFabrication    []string `yaml:"anti_fabrication_rules"`
}

type NamedPhase struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Status string `yaml:"status"`
}

type NamedTask struct {
	ID      string `yaml:"id"`
	Summary string `yaml:"summary"`
}

func LoadStateFile(filename string) (*StateFile, error) {
	bs, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	state := new(StateFile)
	if err := yaml.Unmarshal(bs, state); err != nil {
		return nil, err
	}
	return state, state.Validate()
}

func SaveStateFile(filename string, state *StateFile) error {
	if err := state.Validate(); err != nil {
		return err
	}
	bs, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	tmp := filename + ".tmp"
	if err := os.WriteFile(tmp, bs, 0o666); err != nil {
		return err
	}
	return os.Rename(tmp, filename)
}

func (s *StateFile) Validate() error {
	if s == nil {
		return errors.New("state is nil")
	}
	switch {
	case strings.TrimSpace(s.Project) == "":
		return errors.New("state.project is required")
	case strings.TrimSpace(s.Goal) == "":
		return errors.New("state.goal is required")
	case strings.TrimSpace(s.CurrentPhase.ID) == "":
		return errors.New("state.current_phase.id is required")
	case strings.TrimSpace(s.CurrentPhase.Name) == "":
		return errors.New("state.current_phase.name is required")
	case strings.TrimSpace(s.CurrentPhase.Status) == "":
		return errors.New("state.current_phase.status is required")
	case strings.TrimSpace(s.CurrentTask.ID) == "":
		return errors.New("state.current_task.id is required")
	case strings.TrimSpace(s.CurrentTask.Summary) == "":
		return errors.New("state.current_task.summary is required")
	case len(s.RequiredReadOrder) == 0:
		return errors.New("state.required_read_order must not be empty")
	case len(s.PhaseExitCriteria) == 0:
		return errors.New("state.phase_exit_criteria must not be empty")
	case len(s.RequiredEvidence) == 0:
		return errors.New("state.required_evidence must not be empty")
	case len(s.AntiFabrication) == 0:
		return errors.New("state.anti_fabrication_rules must not be empty")
	}
	return nil
}
