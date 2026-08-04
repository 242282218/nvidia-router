package accesskey

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthenticateServesRepeatLookupsFromCache(t *testing.T) {
	source := newManualClock(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	service, db := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)

	created, err := service.Create(context.Background(), "laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := service.Authenticate(context.Background(), created.Plaintext)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	// Removing the row proves the second lookup never reaches SQLite.
	if _, err := db.Exec("DELETE FROM access_keys WHERE id = ?", created.Key.ID); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	second, err := service.Authenticate(context.Background(), created.Plaintext)
	if err != nil {
		t.Fatalf("cached Authenticate: %v", err)
	}
	if second != first {
		t.Fatalf("cached identity = %+v, want %+v", second, first)
	}
}

func TestAuthenticateRereadsAfterCacheTTL(t *testing.T) {
	source := newManualClock(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	service, db := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)

	created, err := service.Create(context.Background(), "laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), created.Plaintext); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := db.Exec("DELETE FROM access_keys WHERE id = ?", created.Key.ID); err != nil {
		t.Fatalf("delete row: %v", err)
	}

	source.Advance(defaultCacheTTL + time.Second)
	if _, err := service.Authenticate(context.Background(), created.Plaintext); !errors.Is(err, ErrInvalidAccessKey) {
		t.Fatalf("expired Authenticate error = %v, want ErrInvalidAccessKey", err)
	}
}

func TestRevokeInvalidatesCacheImmediately(t *testing.T) {
	source := newManualClock(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	service, _ := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)

	created, err := service.Create(context.Background(), "laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), created.Plaintext); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := service.Revoke(context.Background(), created.Key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), created.Plaintext); !errors.Is(err, ErrInvalidAccessKey) {
		t.Fatalf("revoked Authenticate error = %v, want ErrInvalidAccessKey", err)
	}
}

func TestCacheDoesNotStoreFailedLookups(t *testing.T) {
	source := newManualClock(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	service, _ := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)

	unknown := "nvr_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := service.Authenticate(context.Background(), unknown); !errors.Is(err, ErrInvalidAccessKey) {
		t.Fatalf("unknown Authenticate error = %v, want ErrInvalidAccessKey", err)
	}
	if got := service.cache.size(); got != 0 {
		t.Fatalf("cache size after failed lookup = %d, want 0", got)
	}
}

func TestCacheEvictsExpiredBeforeLiveEntries(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	c := newCache(time.Minute, 2)

	c.store([]byte("a"), AccessKeyIdentity{ID: 1}, now)
	c.store([]byte("b"), AccessKeyIdentity{ID: 2}, now)

	later := now.Add(2 * time.Minute)
	c.store([]byte("c"), AccessKeyIdentity{ID: 3}, later)

	if _, ok := c.lookup([]byte("a"), later); ok {
		t.Fatal("expired entry a still present")
	}
	if _, ok := c.lookup([]byte("b"), later); ok {
		t.Fatal("expired entry b still present")
	}
	identity, ok := c.lookup([]byte("c"), later)
	if !ok || identity.ID != 3 {
		t.Fatalf("live entry c = %+v, ok=%v", identity, ok)
	}
}

func TestCacheRespectsMaxEntriesWithAllLive(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	c := newCache(time.Hour, 2)

	c.store([]byte("a"), AccessKeyIdentity{ID: 1}, now)
	c.store([]byte("b"), AccessKeyIdentity{ID: 2}, now)
	c.store([]byte("c"), AccessKeyIdentity{ID: 3}, now)

	if got := c.size(); got > 2 {
		t.Fatalf("cache size = %d, want <= 2", got)
	}
	if _, ok := c.lookup([]byte("c"), now); !ok {
		t.Fatal("newest entry c was not admitted")
	}
}
