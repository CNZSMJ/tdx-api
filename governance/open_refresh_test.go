package governance

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type fakeClock struct {
	current time.Time
	sleeps  []time.Duration
}

func (c *fakeClock) Now() time.Time {
	return c.current
}

func (c *fakeClock) Sleep(d time.Duration) {
	c.sleeps = append(c.sleeps, d)
	c.current = c.current.Add(d)
}

func TestDailyOpenRefreshSkipsNonTradingDayWindow(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	calls := 0
	runner, err := NewDailyOpenRefreshRunner(DailyOpenRefreshConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 19, 9, 5, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return false, nil
		},
		CodesRefresh: func(ctx context.Context) error {
			calls++
			return nil
		},
		WorkdayRefresh: func(ctx context.Context) error {
			calls++
			return nil
		},
		BlockRefresh: func(ctx context.Context) error {
			calls++
			return nil
		},
		ProFinanceRefresh: func(ctx context.Context) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-09:00")
	if err != nil {
		t.Fatalf("run daily open refresh: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected non-trading-day skip to avoid refresh calls, got %d", calls)
	}
	if run.Status != collectorpkg.GovernanceRunStatusSkipped {
		t.Fatalf("run status = %s, want skipped", run.Status)
	}
	if run.Reason != "non_trading_day_window" {
		t.Fatalf("run reason = %s, want non_trading_day_window", run.Reason)
	}
}

func TestDailyOpenRefreshFastRetriesOpenCriticalDomainsBefore0930(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	clock := &fakeClock{current: time.Date(2026, 4, 20, 9, 5, 0, 0, time.Local)}
	codesAttempts := 0
	workdayAttempts := 0
	blockAttempts := 0
	profAttempts := 0

	runner, err := NewDailyOpenRefreshRunner(DailyOpenRefreshConfig{
		Store:         store,
		Paths:         paths,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		RetryInterval: 5 * time.Minute,
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		CodesRefresh: func(ctx context.Context) error {
			codesAttempts++
			if codesAttempts < 3 {
				return errors.New("temporary network jitter")
			}
			return nil
		},
		WorkdayRefresh: func(ctx context.Context) error {
			workdayAttempts++
			return nil
		},
		BlockRefresh: func(ctx context.Context) error {
			blockAttempts++
			return nil
		},
		ProFinanceRefresh: func(ctx context.Context) error {
			profAttempts++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-09:00")
	if err != nil {
		t.Fatalf("run daily open refresh: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPassed {
		t.Fatalf("run status = %s, want passed", run.Status)
	}
	if codesAttempts != 3 {
		t.Fatalf("codes attempts = %d, want 3", codesAttempts)
	}
	if workdayAttempts != 1 || blockAttempts != 1 || profAttempts != 1 {
		t.Fatalf("unexpected non-critical attempts: workday=%d block=%d prof=%d", workdayAttempts, blockAttempts, profAttempts)
	}
	if len(clock.sleeps) != 2 || clock.sleeps[0] != 5*time.Minute || clock.sleeps[1] != 5*time.Minute {
		t.Fatalf("unexpected retry sleeps: %+v", clock.sleeps)
	}
	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no backlog tasks after recovered run, got %+v", tasks)
	}
}

func TestDailyOpenRefreshExhaustedFastRetryCreatesBacklogTask(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	clock := &fakeClock{current: time.Date(2026, 4, 20, 9, 25, 0, 0, time.Local)}
	codesAttempts := 0

	runner, err := NewDailyOpenRefreshRunner(DailyOpenRefreshConfig{
		Store:         store,
		Paths:         paths,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		RetryInterval: 3 * time.Minute,
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		CodesRefresh: func(ctx context.Context) error {
			codesAttempts++
			return errors.New("upstream unavailable")
		},
		WorkdayRefresh: func(ctx context.Context) error { return nil },
		BlockRefresh: func(ctx context.Context) error { return nil },
		ProFinanceRefresh: func(ctx context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-09:00")
	if err != nil {
		t.Fatalf("run daily open refresh: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPartial {
		t.Fatalf("run status = %s, want partial", run.Status)
	}
	if codesAttempts < 2 {
		t.Fatalf("expected fast retry attempts before cutoff, got %d", codesAttempts)
	}

	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("open tasks = %d, want 1", len(tasks))
	}
	if tasks[0].Domain != "codes" {
		t.Fatalf("task domain = %s, want codes", tasks[0].Domain)
	}
}

func TestDailyOpenRefreshFansOutThroughSingleSystemEntry(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(filepath.Join(paths.RootDir, "system_governance.db"))
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	calls := make(map[string]int)
	runner, err := NewDailyOpenRefreshRunner(DailyOpenRefreshConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 20, 9, 1, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		CodesRefresh: func(ctx context.Context) error {
			calls["codes"]++
			return nil
		},
		WorkdayRefresh: func(ctx context.Context) error {
			calls["workday"]++
			return nil
		},
		BlockRefresh: func(ctx context.Context) error {
			calls["block"]++
			return errors.New("block artifact unavailable")
		},
		ProFinanceRefresh: func(ctx context.Context) error {
			calls["professional_finance"]++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-09:00")
	if err != nil {
		t.Fatalf("run daily open refresh: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPartial {
		t.Fatalf("run status = %s, want partial", run.Status)
	}
	for _, domain := range []string{"codes", "workday", "block", "professional_finance"} {
		if calls[domain] != 1 {
			t.Fatalf("domain %s calls = %d, want 1", domain, calls[domain])
		}
	}

	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Domain != "block" {
		t.Fatalf("unexpected backlog tasks: %+v", tasks)
	}
}
