package governance

import (
	"context"
	"fmt"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type WindowDispatcherConfig struct {
	Store         *collectorpkg.GovernanceStore
	Now           func() time.Time
	Owner         string
	LeaseDuration time.Duration
	Execute       func(context.Context, collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error)
}

type WindowExecutionResult struct {
	Status  collectorpkg.GovernanceWindowStatus
	Summary string
	RunID   string
}

type WindowDispatcher struct {
	cfg WindowDispatcherConfig
}

func NewWindowDispatcher(cfg WindowDispatcherConfig) *WindowDispatcher {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		cfg.Owner = "governance-dispatcher"
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 30 * time.Minute
	}
	return &WindowDispatcher{cfg: cfg}
}

func (d *WindowDispatcher) RunNext(ctx context.Context) (bool, error) {
	if d == nil || d.cfg.Store == nil {
		return false, fmt.Errorf("window dispatcher requires governance store")
	}
	if d.cfg.Execute == nil {
		return false, fmt.Errorf("window dispatcher requires executor")
	}

	now := d.cfg.Now()
	windows, err := d.cfg.Store.ListWindowsByStatus(
		collectorpkg.GovernanceWindowStatusQueued,
		collectorpkg.GovernanceWindowStatusContinued,
		collectorpkg.GovernanceWindowStatusWaitingDependency,
	)
	if err != nil {
		return false, err
	}

	for _, window := range windows {
		if !window.DueAt.IsZero() && window.DueAt.After(now) {
			continue
		}
		if !window.NextRunAt.IsZero() && window.NextRunAt.After(now) {
			continue
		}
		dependencyState, err := d.dependencyState(window)
		if err != nil {
			return false, err
		}
		if dependencyState.missing {
			window.Status = collectorpkg.GovernanceWindowStatusTerminalFailed
			window.LastError = "missing dependency " + window.DependencyKey
			window.ResultSummary = "dependency missing; terminally deferred " + window.DependencyKey
			if err := d.cfg.Store.UpdateWindow(&window); err != nil {
				return false, err
			}
			return false, nil
		}
		if !dependencyState.ready {
			if window.Status != collectorpkg.GovernanceWindowStatusWaitingDependency {
				window.Status = collectorpkg.GovernanceWindowStatusWaitingDependency
				window.ResultSummary = "waiting for dependency " + window.DependencyKey
				if err := d.cfg.Store.UpdateWindow(&window); err != nil {
					return false, err
				}
			}
			return false, nil
		}

		window.Status = collectorpkg.GovernanceWindowStatusRunning
		window.Attempts++
		window.LeaseOwner = d.cfg.Owner
		window.LeaseUntil = now.Add(d.cfg.LeaseDuration)
		if window.StartedAt.IsZero() {
			window.StartedAt = now
		}
		if err := d.cfg.Store.UpdateWindow(&window); err != nil {
			return false, err
		}

		result, execErr := d.cfg.Execute(ctx, window)
		finishedAt := d.cfg.Now()
		window.EndedAt = finishedAt
		window.LeaseOwner = ""
		window.LeaseUntil = time.Time{}
		window.ResultSummary = result.Summary
		window.RunID = result.RunID
		if execErr != nil {
			window.Status = collectorpkg.GovernanceWindowStatusQueued
			window.LastError = execErr.Error()
			window.NextRunAt = finishedAt.Add(5 * time.Minute)
			if err := d.cfg.Store.UpdateWindow(&window); err != nil {
				return true, err
			}
			return true, execErr
		}
		if result.Status == "" {
			result.Status = collectorpkg.GovernanceWindowStatusPassed
		}
		window.Status = result.Status
		window.LastError = ""
		window.NextRunAt = time.Time{}
		if err := d.cfg.Store.UpdateWindow(&window); err != nil {
			return true, err
		}
		return true, nil
	}

	return false, nil
}

type windowDependencyState struct {
	ready   bool
	missing bool
}

func (d *WindowDispatcher) dependencyState(window collectorpkg.GovernanceWindowRecord) (windowDependencyState, error) {
	if strings.TrimSpace(window.DependencyKey) == "" {
		return windowDependencyState{ready: true}, nil
	}
	dependency, err := d.cfg.Store.GetWindowByKey(window.DependencyKey)
	if err != nil {
		return windowDependencyState{}, err
	}
	if dependency == nil {
		return windowDependencyState{missing: true}, nil
	}
	return windowDependencyState{ready: collectorpkg.GovernanceWindowStatusIsTerminal(dependency.Status)}, nil
}
