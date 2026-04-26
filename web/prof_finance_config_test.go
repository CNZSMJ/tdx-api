package main

import (
	"errors"
	"testing"
)

func TestBuildProFinanceConfigHonorsExplicitAutoPrefetchDisable(t *testing.T) {
	t.Setenv("PROFINANCE_DISABLE_BACKGROUND_REFRESH", "1")
	t.Setenv("PROFINANCE_MIN_FREE_BYTES", "0")

	cfg := buildProFinanceConfig(t.TempDir())
	if !cfg.DisableAutoPrefetch {
		t.Fatalf("DisableAutoPrefetch = false, want true")
	}
}

func TestDecideProFinanceAutoPrefetchDisablesWhenDiskBelowWatermark(t *testing.T) {
	t.Setenv("PROFINANCE_DISABLE_BACKGROUND_REFRESH", "0")
	t.Setenv("PROFINANCE_MIN_FREE_BYTES", "1000")
	restore := stubProFinanceDiskFreeBytes(999, nil)
	defer restore()

	decision := decideProFinanceAutoPrefetch(t.TempDir())
	if !decision.Disable {
		t.Fatalf("Disable = false, want true")
	}
	if decision.Reason != "free_disk_below_watermark" {
		t.Fatalf("Reason = %s, want free_disk_below_watermark", decision.Reason)
	}
	if decision.FreeBytes != 999 || decision.MinFreeBytes != 1000 {
		t.Fatalf("decision = %+v, want free=999 min=1000", decision)
	}
}

func TestDecideProFinanceAutoPrefetchAllowsWhenDiskHealthy(t *testing.T) {
	t.Setenv("PROFINANCE_DISABLE_BACKGROUND_REFRESH", "0")
	t.Setenv("PROFINANCE_MIN_FREE_BYTES", "1000")
	restore := stubProFinanceDiskFreeBytes(1000, nil)
	defer restore()

	decision := decideProFinanceAutoPrefetch(t.TempDir())
	if decision.Disable {
		t.Fatalf("Disable = true, want false: %+v", decision)
	}
}

func TestDecideProFinanceAutoPrefetchKeepsExistingBehaviorWhenDiskCheckFails(t *testing.T) {
	t.Setenv("PROFINANCE_DISABLE_BACKGROUND_REFRESH", "0")
	t.Setenv("PROFINANCE_MIN_FREE_BYTES", "1000")
	restore := stubProFinanceDiskFreeBytes(0, errors.New("stat failed"))
	defer restore()

	decision := decideProFinanceAutoPrefetch(t.TempDir())
	if decision.Disable {
		t.Fatalf("Disable = true, want false when disk check is inconclusive")
	}
}

func stubProFinanceDiskFreeBytes(freeBytes int64, err error) func() {
	original := proFinanceDiskFreeBytes
	proFinanceDiskFreeBytes = func(string) (int64, error) {
		return freeBytes, err
	}
	return func() {
		proFinanceDiskFreeBytes = original
	}
}
