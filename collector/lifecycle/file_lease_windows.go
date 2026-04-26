//go:build windows

package lifecycle

import (
	"errors"
	"os"
)

var errFileLeaseUnsupported = errors.New("file lease is unsupported on windows")

func lockFileLease(file *os.File) error {
	return errFileLeaseUnsupported
}

func unlockFileLease(file *os.File) error {
	return nil
}

func isFileLeaseBusy(err error) bool {
	return false
}
