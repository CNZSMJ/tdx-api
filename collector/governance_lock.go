package collector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrGovernanceLockHeld = errors.New("system governance lock already held")

type GovernanceLock struct {
	path string
	file *os.File
}

func AcquireGovernanceLock(path string) (*GovernanceLock, error) {
	if path == "" {
		path = ResolveGovernancePaths("").LockPath
	}
	dir, _ := filepath.Split(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}
	if err := lockGovernanceFile(file); err != nil {
		_ = file.Close()
		if isGovernanceLockBusy(err) {
			return nil, fmt.Errorf("%w: %s", ErrGovernanceLockHeld, path)
		}
		return nil, err
	}
	return &GovernanceLock{path: path, file: file}, nil
}

func IsGovernanceLockHeld(err error) bool {
	return errors.Is(err, ErrGovernanceLockHeld)
}

func (l *GovernanceLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *GovernanceLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockGovernanceFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
