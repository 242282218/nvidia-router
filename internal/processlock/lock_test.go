package processlock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockIsMutuallyExclusiveAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.lock")
	first, err := TryLock(path)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("second TryLock error = %v, want ErrAlreadyLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	second, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file should remain addressable: %v", err)
	}
}
