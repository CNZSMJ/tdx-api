package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/injoyai/tdx/profinance"
)

const defaultProFinanceMinFreeBytes int64 = 10 * 1024 * 1024 * 1024

var proFinanceDiskFreeBytes = diskFreeBytesForPath

type proFinanceAutoPrefetchDecision struct {
	Disable      bool
	Reason       string
	FreeBytes    int64
	MinFreeBytes int64
}

func buildProFinanceConfig(cacheDir string) profinance.Config {
	decision := decideProFinanceAutoPrefetch(cacheDir)
	if decision.Disable {
		if decision.FreeBytes > 0 && decision.MinFreeBytes > 0 {
			log.Printf(
				"profinance: disable auto prefetch: reason=%s free_bytes=%d min_free_bytes=%d",
				decision.Reason,
				decision.FreeBytes,
				decision.MinFreeBytes,
			)
		} else {
			log.Printf("profinance: disable auto prefetch: reason=%s", decision.Reason)
		}
	}
	return profinance.Config{DisableAutoPrefetch: decision.Disable}
}

func decideProFinanceAutoPrefetch(cacheDir string) proFinanceAutoPrefetchDecision {
	if envBool("PROFINANCE_DISABLE_BACKGROUND_REFRESH") {
		return proFinanceAutoPrefetchDecision{Disable: true, Reason: "env_disabled"}
	}

	minFreeBytes := envInt64("PROFINANCE_MIN_FREE_BYTES", envInt64("TDX_LIFECYCLE_MIN_FREE_BYTES", defaultProFinanceMinFreeBytes))
	if minFreeBytes <= 0 {
		return proFinanceAutoPrefetchDecision{MinFreeBytes: minFreeBytes}
	}

	freeBytes, err := proFinanceDiskFreeBytes(cacheDir)
	if err != nil {
		log.Printf("profinance: cannot inspect free disk for %s: %v", cacheDir, err)
		return proFinanceAutoPrefetchDecision{MinFreeBytes: minFreeBytes}
	}
	if freeBytes < minFreeBytes {
		return proFinanceAutoPrefetchDecision{
			Disable:      true,
			Reason:       "free_disk_below_watermark",
			FreeBytes:    freeBytes,
			MinFreeBytes: minFreeBytes,
		}
	}
	return proFinanceAutoPrefetchDecision{
		Reason:       "enabled",
		FreeBytes:    freeBytes,
		MinFreeBytes: minFreeBytes,
	}
}

func envBool(name string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func envInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		log.Printf("env: ignore invalid %s=%q, using default %d", name, raw, fallback)
		return fallback
	}
	return value
}

func existingPathForStat(path string) string {
	target := strings.TrimSpace(path)
	if target == "" {
		target = "."
	}
	target = filepath.Clean(target)
	for {
		if _, err := os.Stat(target); err == nil {
			return target
		}
		parent := filepath.Dir(target)
		if parent == target {
			return "."
		}
		target = parent
	}
}
