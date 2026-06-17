package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/injoyai/ios"
	tdxclient "github.com/injoyai/ios/client"
	"github.com/injoyai/tdx"
)

func dialTDX(op ...tdxclient.Option) (*tdx.Client, error) {
	hosts := rotatedTDXHosts(tdxHostsForWeb(), tdxDialOffset.Add(1))
	var cli *tdx.Client
	var err error
	if bindInterface := tdxBindInterface(); bindInterface != "" {
		log.Printf("tdx: 使用网卡 %s 直连 TDX", bindInterface)
		cli, err = tdx.DialWith(newBoundTDXRangeDial(hosts, bindInterface), op...)
	} else {
		cli, err = tdx.DialHostsRange(hosts, op...)
	}
	if err != nil {
		return nil, err
	}
	cli.SetRedial()
	return cli, nil
}

func tdxBindInterface() string {
	return strings.TrimSpace(os.Getenv("TDX_BIND_INTERFACE"))
}

func newBoundTDXRangeDial(hosts []string, bindInterface string) ios.DialFunc {
	if len(hosts) == 0 {
		hosts = tdx.Hosts
	}
	return func(ctx context.Context) (ios.ReadWriteCloser, string, error) {
		control, err := tdxBoundDialControl(bindInterface)
		if err != nil {
			return nil, "", err
		}
		dialer := net.Dialer{
			Timeout: 3 * time.Second,
			Control: control,
		}
		var lastErr error
		for _, host := range hosts {
			addr := host
			if !strings.Contains(addr, ":") {
				addr += ":7709"
			}
			conn, err := dialer.DialContext(ctx, "tcp4", addr)
			if err == nil {
				return conn, addr, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("TDX host 列表为空")
		}
		return nil, "", lastErr
	}
}
