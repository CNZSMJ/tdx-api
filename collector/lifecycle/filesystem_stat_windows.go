//go:build windows

package lifecycle

import "errors"

func diskFreeBytes(path string) (int64, error) {
	return 0, errors.New("disk free bytes is unsupported on windows")
}

func filesystemDeviceID(path string) (uint64, error) {
	return 0, nil
}
