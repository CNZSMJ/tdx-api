package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"xorm.io/core"
	"xorm.io/xorm"
)

type GovernanceStore struct {
	engine *xorm.Engine
}

func OpenGovernanceStore(filename string) (*GovernanceStore, error) {
	return openGovernanceStore(filename, false)
}

func OpenGovernanceStoreReadOnly(filename string) (*GovernanceStore, error) {
	return openGovernanceStore(filename, true)
}

func openGovernanceStore(filename string, readOnly bool) (*GovernanceStore, error) {
	if filename == "" {
		filename = ResolveGovernancePaths("").DBPath
	}
	dir, _ := filepath.Split(filename)
	if readOnly {
		if _, err := os.Stat(filename); err != nil {
			return nil, err
		}
	} else if dir != "" {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, err
		}
	}

	dsn := filename
	if readOnly {
		abs, err := filepath.Abs(filename)
		if err != nil {
			return nil, err
		}
		dsn = "file:" + filepath.ToSlash(abs) + "?mode=ro"
	}
	engine, err := xorm.NewEngine("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	engine.SetMapper(core.SameMapper{})
	engine.DB().SetMaxOpenConns(1)

	store := &GovernanceStore{engine: engine}
	if !readOnly {
		if err := store.EnsureSchema(); err != nil {
			_ = engine.Close()
			return nil, err
		}
	}
	return store, nil
}

func (s *GovernanceStore) Close() error {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.Close()
}

func (s *GovernanceStore) HasTable(bean interface{}) (bool, error) {
	return s.engine.IsTableExist(bean)
}

func (s *GovernanceStore) EnsureSchema() error {
	if err := s.engine.Sync2(
		new(GovernanceSchemaVersion),
		new(GovernanceRunRecord),
		new(GovernanceTaskRecord),
		new(GovernanceWindowRecord),
		new(DomainHealthSnapshotRecord),
		new(GovernanceLockMetadataRecord),
		new(GovernanceEvidenceRecord),
	); err != nil {
		return err
	}

	has, err := s.engine.Where("Version = ?", GovernanceSchemaVersionCurrent).Exist(new(GovernanceSchemaVersion))
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = s.engine.Insert(&GovernanceSchemaVersion{
		Version:   GovernanceSchemaVersionCurrent,
		AppliedAt: time.Now(),
	})
	return err
}

func (s *GovernanceStore) AddRun(record *GovernanceRunRecord) error {
	_, err := s.engine.Insert(record)
	return err
}

func (s *GovernanceStore) UpdateRun(record *GovernanceRunRecord) error {
	if record.ID > 0 {
		_, err := s.engine.ID(record.ID).AllCols().Update(record)
		return err
	}
	_, err := s.engine.Where("RunID = ?", record.RunID).AllCols().Update(record)
	return err
}

func (s *GovernanceStore) InterruptRunningRuns(reason string, endedAt time.Time) (int64, error) {
	if endedAt.IsZero() {
		endedAt = time.Now()
	}

	var updated int64
	_, err := s.engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		var txUpdated int64
		runs := make([]GovernanceRunRecord, 0, 8)
		if err := session.Where("Status = ?", GovernanceRunStatusRunning).Find(&runs); err != nil {
			return nil, err
		}
		for _, run := range runs {
			run.Status = GovernanceRunStatusInterrupted
			run.Reason = reason
			run.EndedAt = endedAt
			if _, err := session.ID(run.ID).AllCols().Update(&run); err != nil {
				return nil, err
			}
			txUpdated++
		}
		updated = txUpdated
		return nil, nil
	})
	return updated, err
}

func (s *GovernanceStore) ExpireRunningWindow(record *GovernanceWindowRecord, reason string, endedAt time.Time) (bool, error) {
	if record == nil {
		return false, nil
	}
	if endedAt.IsZero() {
		endedAt = time.Now()
	}

	var expired bool
	_, err := s.engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		current := new(GovernanceWindowRecord)
		var (
			has bool
			err error
		)
		if record.ID > 0 {
			has, err = session.ID(record.ID).Get(current)
		} else {
			has, err = session.Where("WindowKey = ?", record.WindowKey).Get(current)
		}
		if err != nil {
			return nil, err
		}
		if !has ||
			current.Status != GovernanceWindowStatusRunning ||
			current.LeaseUntil.IsZero() ||
			current.LeaseUntil.After(endedAt) {
			return nil, nil
		}

		latestRun := new(GovernanceRunRecord)
		hasRun, err := session.
			Where("JobName = ? AND TargetWindow = ?", current.JobName, current.TargetWindow).
			Desc("StartedAt").
			Desc("ID").
			Get(latestRun)
		if err != nil {
			return nil, err
		}
		if hasRun && latestRun.Status == GovernanceRunStatusRunning {
			return nil, nil
		}

		current.Status = GovernanceWindowStatusTerminalFailed
		current.LastError = reason
		current.ResultSummary = reason
		current.EndedAt = endedAt
		if hasRun {
			current.Status = governanceWindowStatusFromRunStatus(latestRun.Status)
			current.RunID = latestRun.RunID
			current.ResultSummary = fmt.Sprintf("run_id=%s status=%s target=%s", latestRun.RunID, latestRun.Status, latestRun.TargetWindow)
			if !latestRun.EndedAt.IsZero() {
				current.EndedAt = latestRun.EndedAt
			}
			if current.Status == GovernanceWindowStatusTerminalFailed {
				current.LastError = latestRun.Reason
				if current.LastError == "" {
					current.LastError = reason
				}
			} else {
				current.LastError = ""
			}
		}
		current.LeaseOwner = ""
		current.LeaseUntil = time.Time{}

		affected, err := session.
			Where("ID = ? AND Status = ? AND LeaseUntil <= ?", current.ID, GovernanceWindowStatusRunning, endedAt).
			AllCols().
			Update(current)
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			return nil, nil
		}
		expired = true
		return nil, nil
	})
	return expired, err
}

func (s *GovernanceStore) RecoverRunningWindowsFromEndedRuns(reason string, endedAt time.Time) (int64, error) {
	if endedAt.IsZero() {
		endedAt = time.Now()
	}

	var recovered int64
	_, err := s.engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		windows := make([]GovernanceWindowRecord, 0, 8)
		if err := session.Where("Status = ?", GovernanceWindowStatusRunning).Find(&windows); err != nil {
			return nil, err
		}
		for _, window := range windows {
			latestRun := new(GovernanceRunRecord)
			hasRun, err := session.
				Where("JobName = ? AND TargetWindow = ?", window.JobName, window.TargetWindow).
				Desc("StartedAt").
				Desc("ID").
				Get(latestRun)
			if err != nil {
				return nil, err
			}
			if !hasRun || latestRun.Status == GovernanceRunStatusRunning {
				continue
			}

			window.Status = governanceWindowStatusFromRunStatus(latestRun.Status)
			window.RunID = latestRun.RunID
			window.ResultSummary = fmt.Sprintf("run_id=%s status=%s target=%s", latestRun.RunID, latestRun.Status, latestRun.TargetWindow)
			if !latestRun.EndedAt.IsZero() {
				window.EndedAt = latestRun.EndedAt
			} else {
				window.EndedAt = endedAt
			}
			if window.Status == GovernanceWindowStatusTerminalFailed {
				window.LastError = latestRun.Reason
				if window.LastError == "" {
					window.LastError = reason
				}
			} else {
				window.LastError = ""
			}
			window.LeaseOwner = ""
			window.LeaseUntil = time.Time{}

			affected, err := session.
				Where("ID = ? AND Status = ?", window.ID, GovernanceWindowStatusRunning).
				AllCols().
				Update(&window)
			if err != nil {
				return nil, err
			}
			if affected > 0 {
				recovered++
			}
		}
		return nil, nil
	})
	return recovered, err
}

func governanceWindowStatusFromRunStatus(status GovernanceRunStatus) GovernanceWindowStatus {
	switch status {
	case GovernanceRunStatusPassed:
		return GovernanceWindowStatusPassed
	case GovernanceRunStatusPartial:
		return GovernanceWindowStatusPartial
	case GovernanceRunStatusSkipped:
		return GovernanceWindowStatusSkipped
	case GovernanceRunStatusFailed, GovernanceRunStatusInterrupted:
		return GovernanceWindowStatusTerminalFailed
	default:
		return GovernanceWindowStatusTerminalFailed
	}
}

func (s *GovernanceStore) ListRecentRuns(limit int) ([]GovernanceRunRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	records := make([]GovernanceRunRecord, 0, limit)
	err := s.engine.Desc("StartedAt").Limit(limit).Find(&records)
	return records, err
}

func (s *GovernanceStore) GetRunByRunID(runID string) (*GovernanceRunRecord, error) {
	record := new(GovernanceRunRecord)
	has, err := s.engine.Where("RunID = ?", runID).Get(record)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return record, nil
}

func (s *GovernanceStore) LatestRunForWindow(jobName, targetWindow string) (*GovernanceRunRecord, error) {
	record := new(GovernanceRunRecord)
	has, err := s.engine.
		Where("JobName = ? AND TargetWindow = ?", jobName, targetWindow).
		Desc("StartedAt").
		Desc("ID").
		Get(record)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return record, nil
}

func (s *GovernanceStore) UpsertTask(record *GovernanceTaskRecord) error {
	existing := new(GovernanceTaskRecord)
	has, err := s.engine.Where("TaskKey = ?", record.TaskKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
		if governanceTaskStatusIsTerminal(existing.Status) && !governanceTaskStatusIsTerminal(record.Status) {
			record.Status = existing.Status
			record.Reason = existing.Reason
			if record.Priority < existing.Priority {
				record.Priority = existing.Priority
			}
		}
		if existing.Status == GovernanceTaskStatusDegraded && record.Status == GovernanceTaskStatusOpen {
			record.Status = existing.Status
			record.Reason = existing.Reason
			if record.Priority < existing.Priority {
				record.Priority = existing.Priority
			}
		}
		_, err = s.engine.Where("TaskKey = ?", record.TaskKey).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func governanceTaskStatusIsTerminal(status GovernanceTaskStatus) bool {
	switch status {
	case GovernanceTaskStatusRepaired, GovernanceTaskStatusClosed:
		return true
	default:
		return false
	}
}

func (s *GovernanceStore) UpdateTask(record *GovernanceTaskRecord) error {
	if record.ID > 0 {
		_, err := s.engine.ID(record.ID).AllCols().Update(record)
		return err
	}
	_, err := s.engine.Where("TaskKey = ?", record.TaskKey).AllCols().Update(record)
	return err
}

func (s *GovernanceStore) UpdateTaskIfStatus(record *GovernanceTaskRecord, expected GovernanceTaskStatus) (bool, error) {
	if record.ID > 0 {
		affected, err := s.engine.Where("ID = ? AND Status = ?", record.ID, expected).AllCols().Update(record)
		return affected > 0, err
	}
	affected, err := s.engine.Where("TaskKey = ? AND Status = ?", record.TaskKey, expected).AllCols().Update(record)
	return affected > 0, err
}

func (s *GovernanceStore) ListTasksByStatus(statuses ...GovernanceTaskStatus) ([]GovernanceTaskRecord, error) {
	records := make([]GovernanceTaskRecord, 0, 16)
	session := s.engine.Asc("Priority").Asc("CreatedAt")
	if len(statuses) > 0 {
		values := make([]string, 0, len(statuses))
		for _, status := range statuses {
			values = append(values, string(status))
		}
		session = session.In("Status", values)
	}
	err := session.Find(&records)
	return records, err
}

func (s *GovernanceStore) UpsertWindow(record *GovernanceWindowRecord) error {
	if record.WindowKey == "" {
		record.WindowKey = GovernanceWindowKey(GovernanceJob(record.JobName), record.TargetWindow)
	}
	existing := new(GovernanceWindowRecord)
	has, err := s.engine.Where("WindowKey = ?", record.WindowKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
		record.ID = existing.ID
		if record.CreatedAt.IsZero() {
			record.CreatedAt = existing.CreatedAt
		}
		_, err = s.engine.ID(existing.ID).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func (s *GovernanceStore) CreateWindowIfMissing(record *GovernanceWindowRecord) (bool, error) {
	if record.WindowKey == "" {
		record.WindowKey = GovernanceWindowKey(GovernanceJob(record.JobName), record.TargetWindow)
	}
	existing := new(GovernanceWindowRecord)
	has, err := s.engine.Where("WindowKey = ?", record.WindowKey).Get(existing)
	if err != nil {
		return false, err
	}
	if has {
		return false, nil
	}
	_, err = s.engine.Insert(record)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		has, getErr := s.engine.Where("WindowKey = ?", record.WindowKey).Exist(new(GovernanceWindowRecord))
		if getErr != nil {
			return false, getErr
		}
		if has {
			return false, nil
		}
	}
	return err == nil, err
}

func (s *GovernanceStore) UpdateWindow(record *GovernanceWindowRecord) error {
	if record.ID > 0 {
		_, err := s.engine.ID(record.ID).AllCols().Update(record)
		return err
	}
	_, err := s.engine.Where("WindowKey = ?", record.WindowKey).AllCols().Update(record)
	return err
}

func (s *GovernanceStore) UpdateWindowIfStatus(record *GovernanceWindowRecord, expected GovernanceWindowStatus) (bool, error) {
	if record.ID > 0 {
		affected, err := s.engine.Where("ID = ? AND Status = ?", record.ID, expected).AllCols().Update(record)
		return affected > 0, err
	}
	affected, err := s.engine.Where("WindowKey = ? AND Status = ?", record.WindowKey, expected).AllCols().Update(record)
	return affected > 0, err
}

func (s *GovernanceStore) GetWindowByKey(windowKey string) (*GovernanceWindowRecord, error) {
	record := new(GovernanceWindowRecord)
	has, err := s.engine.Where("WindowKey = ?", windowKey).Get(record)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return record, nil
}

func (s *GovernanceStore) ListWindowsByStatus(statuses ...GovernanceWindowStatus) ([]GovernanceWindowRecord, error) {
	records := make([]GovernanceWindowRecord, 0, 16)
	session := s.engine.Asc("Priority").Asc("DueAt").Asc("ID")
	if len(statuses) > 0 {
		values := make([]string, 0, len(statuses))
		for _, status := range statuses {
			values = append(values, string(status))
		}
		session = session.In("Status", values)
	}
	err := session.Find(&records)
	return records, err
}

func (s *GovernanceStore) UpsertDomainHealthSnapshot(record *DomainHealthSnapshotRecord) error {
	has, err := s.engine.Where("Domain = ?", record.Domain).Exist(new(DomainHealthSnapshotRecord))
	if err != nil {
		return err
	}
	if has {
		_, err = s.engine.Where("Domain = ?", record.Domain).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func (s *GovernanceStore) GetDomainHealthSnapshot(domain string) (*DomainHealthSnapshotRecord, error) {
	record := new(DomainHealthSnapshotRecord)
	has, err := s.engine.Where("Domain = ?", domain).Get(record)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return record, nil
}

func (s *GovernanceStore) ListLatestDomainHealthSnapshots() ([]DomainHealthSnapshotRecord, error) {
	records := make([]DomainHealthSnapshotRecord, 0, 16)
	err := s.engine.Asc("Domain").Find(&records)
	return records, err
}

func (s *GovernanceStore) RecordLockMetadata(record *GovernanceLockMetadataRecord) error {
	has, err := s.engine.Where("LockName = ?", record.LockName).Exist(new(GovernanceLockMetadataRecord))
	if err != nil {
		return err
	}
	if has {
		_, err = s.engine.Where("LockName = ?", record.LockName).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func (s *GovernanceStore) LatestLockMetadata() (*GovernanceLockMetadataRecord, error) {
	record := new(GovernanceLockMetadataRecord)
	has, err := s.engine.Desc("LastHeartbeatAt").Get(record)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return record, nil
}

func (s *GovernanceStore) DeleteLockMetadata(lockName string) (int64, error) {
	return s.engine.Where("LockName = ?", lockName).Delete(new(GovernanceLockMetadataRecord))
}

func (s *GovernanceStore) AddEvidence(record *GovernanceEvidenceRecord) error {
	_, err := s.engine.Insert(record)
	return err
}

func (s *GovernanceStore) ListEvidenceForRun(runID string) ([]GovernanceEvidenceRecord, error) {
	records := make([]GovernanceEvidenceRecord, 0, 8)
	err := s.engine.Where("RunID = ?", runID).Asc("CreatedAt").Find(&records)
	return records, err
}
