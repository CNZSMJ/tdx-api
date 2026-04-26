package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrGovernanceLeaseRequired = errors.New("governance lease must be acquired before file lease")
var ErrFileLeaseHeld = errors.New("file lease already held")

type GovernanceLeaseToken struct {
	RunID string
}

type LockOrderManager struct{}

func NewLockOrderManager() *LockOrderManager {
	return &LockOrderManager{}
}

func (m *LockOrderManager) MarkGovernanceLeaseAcquired(runID string) GovernanceLeaseToken {
	return GovernanceLeaseToken{RunID: runID}
}

func (m *LockOrderManager) AcquireFileLease(path string) (*FileLease, error) {
	return nil, ErrGovernanceLeaseRequired
}

func (m *LockOrderManager) AcquireFileLeaseWithGovernance(token GovernanceLeaseToken, path string) (*FileLease, error) {
	if token.RunID == "" {
		return nil, ErrGovernanceLeaseRequired
	}
	return AcquireFileLease(path)
}

type FileLease struct {
	path string
	file *os.File
}

func AcquireFileLease(path string) (*FileLease, error) {
	if path == "" {
		return nil, errors.New("file lease path is required")
	}
	leasePath := path + ".lifecycle.lock"
	if err := os.MkdirAll(filepath.Dir(leasePath), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(leasePath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFileLease(file); err != nil {
		_ = file.Close()
		if isFileLeaseBusy(err) {
			return nil, fmt.Errorf("%w: %s", ErrFileLeaseHeld, path)
		}
		return nil, err
	}
	return &FileLease{path: leasePath, file: file}, nil
}

func (l *FileLease) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockFileLease(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
