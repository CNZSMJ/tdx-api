package lifecycle

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestFileLeaseRequiresGovernanceFirst(t *testing.T) {
	manager := NewLockOrderManager()
	if _, err := manager.AcquireFileLease(filepath.Join(t.TempDir(), "sh600000.db")); !errors.Is(err, ErrGovernanceLeaseRequired) {
		t.Fatalf("file lease without governance error = %v", err)
	}
	governance := manager.MarkGovernanceLeaseAcquired("run-1")
	lease, err := manager.AcquireFileLeaseWithGovernance(governance, filepath.Join(t.TempDir(), "sh600000.db"))
	if err != nil {
		t.Fatalf("file lease with governance: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release lease: %v", err)
	}
}

func TestFileLeasePreventsConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sh600000.db")
	manager := NewLockOrderManager()
	governance := manager.MarkGovernanceLeaseAcquired("run-1")
	first, err := manager.AcquireFileLeaseWithGovernance(governance, path)
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}
	defer first.Release()
	if _, err := manager.AcquireFileLeaseWithGovernance(governance, path); !errors.Is(err, ErrFileLeaseHeld) {
		t.Fatalf("second lease error = %v, want ErrFileLeaseHeld", err)
	}
}
