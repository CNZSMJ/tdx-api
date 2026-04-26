//go:build windows

package main

import "errors"

func freeBytes(path string) (int64, error) {
	return 0, errors.New("disk free bytes is unsupported on windows")
}
