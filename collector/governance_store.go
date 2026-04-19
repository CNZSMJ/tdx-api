package collector

import (
	"os"
	"path/filepath"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"xorm.io/core"
	"xorm.io/xorm"
)

type GovernanceStore struct {
	engine *xorm.Engine
}

func OpenGovernanceStore(filename string) (*GovernanceStore, error) {
	if filename == "" {
		filename = ResolveGovernancePaths("").DBPath
	}
	dir, _ := filepath.Split(filename)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, err
		}
	}

	engine, err := xorm.NewEngine("sqlite", filename)
	if err != nil {
		return nil, err
	}
	engine.SetMapper(core.SameMapper{})
	engine.DB().SetMaxOpenConns(1)

	store := &GovernanceStore{engine: engine}
	if err := store.EnsureSchema(); err != nil {
		_ = engine.Close()
		return nil, err
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

func (s *GovernanceStore) UpsertTask(record *GovernanceTaskRecord) error {
	existing := new(GovernanceTaskRecord)
	has, err := s.engine.Where("TaskKey = ?", record.TaskKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
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

func (s *GovernanceStore) AddEvidence(record *GovernanceEvidenceRecord) error {
	_, err := s.engine.Insert(record)
	return err
}

func (s *GovernanceStore) ListEvidenceForRun(runID string) ([]GovernanceEvidenceRecord, error) {
	records := make([]GovernanceEvidenceRecord, 0, 8)
	err := s.engine.Where("RunID = ?", runID).Asc("CreatedAt").Find(&records)
	return records, err
}
