package collector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
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
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
