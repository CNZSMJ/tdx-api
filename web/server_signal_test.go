package main

import (
	"os"
	"syscall"
	"testing"
)

func TestCollectorShutdownSignalsIncludeHangup(t *testing.T) {
	signals := collectorShutdownSignals()

	assertContainsSignal := func(target os.Signal) {
		t.Helper()
		for _, signal := range signals {
			if signal == target {
				return
			}
		}
		t.Fatalf("collector shutdown signals missing %v", target)
	}

	assertContainsSignal(os.Interrupt)
	assertContainsSignal(syscall.SIGTERM)
	assertContainsSignal(syscall.SIGHUP)
}
