//go:build !darwin

package main

import (
	"fmt"
	"syscall"
)

func tdxBoundDialControl(interfaceName string) (func(string, string, syscall.RawConn) error, error) {
	return nil, fmt.Errorf("TDX_BIND_INTERFACE=%s 仅支持 darwin", interfaceName)
}
