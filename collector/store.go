package collector

import (
	"os"
	"path/filepath"
	"time"

	_ "github.com/glebarez/go-sqlite"
	tdx "github.com/injoyai/tdx"
	"xorm.io/core"
	"xorm.io/xorm"
)

const DefaultDBName = "collector.db"

func DefaultDBPath(baseDir string) string {
	if baseDir == "" {
		baseDir = tdx.DefaultDatabaseDir
	}
	return filepath.Join(baseDir, DefaultDBName)
}

type Store struct {
	engine *xorm.Engine
}

func OpenStore(filename string) (*Store, error) {
	if filename == "" {
		filename = DefaultDBPath("")
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

	store := &Store{engine: engine}
	if err := store.EnsureSchema(); err != nil {
		_ = engine.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.Close()
}

func (s *Store) EnsureSchema() error {
	if err := s.engine.Sync2(
		new(SchemaVersion),
		new(PhaseStateRecord),
		new(TaskRunRecord),
		new(ValidationRunRecord),
		new(OperationLogRecord),
		new(CollectCursorRecord),
		new(CollectGapRecord),
		new(ScheduleRunRecord),
	); err != nil {
		return err
	}

	has, err := s.engine.Where("Version = ?", SchemaVersionCurrent).Exist(new(SchemaVersion))
	if err != nil {
		return err
	}
	if has {
		return nil
	}

	_, err = s.engine.Insert(&SchemaVersion{
		Version:   SchemaVersionCurrent,
		AppliedAt: time.Now(),
	})
	return err
}

func (s *Store) HasTable(bean interface{}) (bool, error) {
	return s.engine.IsTableExist(bean)
}

func (s *Store) UpsertPhaseState(record *PhaseStateRecord) error {
	record.UpdatedAt = time.Now()
	has, err := s.engine.Where("PhaseID = ?", record.PhaseID).Exist(new(PhaseStateRecord))
	if err != nil {
		return err
	}
	if has {
		_, err = s.engine.Where("PhaseID = ?", record.PhaseID).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func (s *Store) AddValidationRun(record *ValidationRunRecord) error {
	record.CreatedAt = time.Now()
	_, err := s.engine.Insert(record)
	return err
}

func (s *Store) AddOperationLog(record *OperationLogRecord) error {
	record.CreatedAt = time.Now()
	_, err := s.engine.Insert(record)
	return err
}
