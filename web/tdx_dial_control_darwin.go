//go:build darwin

package main

import (
	"net"
	"syscall"
)

func tdxBoundDialControl(interfaceName string) (func(string, string, syscall.RawConn) error, error) {
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	return func(network, address string, conn syscall.RawConn) error {
		var sockErr error
		err := conn.Control(func(fd uintptr) {
			sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_BOUND_IF, iface.Index)
		})
		if err != nil {
			return err
		}
		return sockErr
	}, nil
}
