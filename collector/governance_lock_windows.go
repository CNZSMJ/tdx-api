//go:build windows

package collector

import (
	"errors"
	"os"
)

var errGovernanceFileLockUnsupported = errors.New("governance file lock is unsupported on windows")

func lockGovernanceFile(file *os.File) error {
	return errGovernanceFileLockUnsupported
}

func unlockGovernanceFile(file *os.File) error {
	return nil
}

func isGovernanceLockBusy(err error) bool {
	return false
}
