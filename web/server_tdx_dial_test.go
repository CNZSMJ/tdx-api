package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/ios"
	tdxclient "github.com/injoyai/ios/client"
	"github.com/injoyai/tdx"
)

func TestRotatedTDXHosts(t *testing.T) {
	hosts := []string{"a", "b", "c"}
	got := rotatedTDXHosts(hosts, 1)
	want := []string{"b", "c", "a"}
	if len(got) != len(want) {
		t.Fatalf("rotated host count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rotated hosts = %#v, want %#v", got, want)
		}
	}
	if hosts[0] != "a" {
		t.Fatalf("rotatedTDXHosts must not mutate input: %#v", hosts)
	}
}

func TestTDXBindInterfaceFromEnv(t *testing.T) {
	t.Setenv("TDX_BIND_INTERFACE", " en0 ")
	if got := tdxBindInterface(); got != "en0" {
		t.Fatalf("tdxBindInterface = %q, want en0", got)
	}
}

func TestDialTDXReturnsInitialDialError(t *testing.T) {
	t.Setenv("TDX_BIND_INTERFACE", "")
	t.Setenv("TDX_HOSTS", "127.0.0.1:1")
	done := make(chan error, 1)
	go func() {
		cli, err := dialTDX(tdx.WithDebug(false))
		if cli != nil {
			_ = cli.CloseAll()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected initial dial error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initial dial did not return")
	}
}

func TestEnableTDXRedialAfterInitialDial(t *testing.T) {
	client := &tdx.Client{
		Client: tdxclient.New(func(context.Context) (ios.ReadWriteCloser, string, error) {
			return ios.Null, "test", nil
		}),
	}
	client.SetRedial()
	if !reflect.ValueOf(client.Client).Elem().FieldByName("redial").Bool() {
		t.Fatal("expected client redial to be enabled after initial dial")
	}
}

func TestStartupCodeRefreshUsesCache(t *testing.T) {
	t.Setenv("TDX_STARTUP_CODE_REFRESH", " cache ")
	if !startupCodeRefreshUsesCache() {
		t.Fatal("expected cache startup code refresh")
	}
}

func TestCollectorIntradayWorkerCapUsesTickerClock(t *testing.T) {
	if got := collectorIntradayWorkerCapAt(32, time.Date(2026, 6, 4, 9, 20, 0, 0, time.Local)); got != collectorDefaultWorkers {
		t.Fatalf("intraday worker cap = %d, want %d", got, collectorDefaultWorkers)
	}
	if got := collectorIntradayWorkerCapAt(32, time.Date(2026, 6, 4, 8, 55, 0, 0, time.Local)); got != 32 {
		t.Fatalf("pre-session worker cap = %d, want 32", got)
	}
	if got := collectorIntradayWorkerCapAt(32, time.Date(2026, 6, 6, 9, 20, 0, 0, time.Local)); got != 32 {
		t.Fatalf("weekend worker cap = %d, want 32", got)
	}
}

func TestCollectorConnectionPoolWorkersCapsStartupPool(t *testing.T) {
	if got := collectorConnectionPoolWorkers(32); got != collectorDefaultWorkers {
		t.Fatalf("connection pool workers = %d, want %d", got, collectorDefaultWorkers)
	}
	if got := collectorConnectionPoolWorkers(4); got != 4 {
		t.Fatalf("connection pool workers = %d, want 4", got)
	}
}
