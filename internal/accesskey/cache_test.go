package accesskey

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"nvidia-router/internal/crypto"
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

func TestAuthenticateMigratesLegacyDigest(t *testing.T) {
	source := newManualClock(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	legacyService, db := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)
	created, err := legacyService.Create(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var activeMaster [32]byte
	activeMaster[0] = 2
	activeKeys, err := crypto.NewVersioned(2, activeMaster)
	if err != nil {
		t.Fatalf("NewVersioned: %v", err)
	}
	activeKeys, err = activeKeys.WithLegacyMasterKey(1, [32]byte{1})
	if err != nil {
		t.Fatalf("WithLegacyMasterKey: %v", err)
	}
	service := NewService(NewRepository(db), activeKeys, source)
	if _, err := service.Authenticate(context.Background(), created.Plaintext); err != nil {
		t.Fatalf("Authenticate legacy key: %v", err)
	}
	var version int
	if err := db.QueryRow("SELECT digest_key_version FROM access_keys WHERE id = ?", created.Key.ID).Scan(&version); err != nil {
		t.Fatalf("read digest version: %v", err)
	}
	if version != activeKeys.ActiveVersion() {
		t.Fatalf("digest version = %d, want %d", version, activeKeys.ActiveVersion())
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

// An expired identity found in the cache used to call invalidate(), which rebuilds
// the whole map. One client looping with an expired key therefore evicted every
// other key's identity and pushed the entire instance back onto the DB read this
// cache exists to remove.
func TestExpiredCachedKeyDropsOnlyItsOwnEntry(t *testing.T) {
	source := newManualClock(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	service, db := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)

	expiring, err := service.Create(context.Background(), "expiring")
	if err != nil {
		t.Fatalf("Create expiring: %v", err)
	}
	survivor, err := service.Create(context.Background(), "survivor")
	if err != nil {
		t.Fatalf("Create survivor: %v", err)
	}
	// The key must lapse while its cache entry is still live, otherwise the 30s
	// cache TTL — not the branch under test — is what empties the map.
	expires := source.Now().Add(10 * time.Second)
	zero := int64(0)
	if err := service.UpdatePolicy(context.Background(), expiring.Key.ID, &expires, 0, 0, 0, &zero); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	for _, plaintext := range []string{expiring.Plaintext, survivor.Plaintext} {
		if _, err := service.Authenticate(context.Background(), plaintext); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
	}
	source.Advance(15 * time.Second)
	// Cache hit whose identity has lapsed: this is the branch that used to flush
	// the entire map.
	if _, err := service.Authenticate(context.Background(), expiring.Plaintext); !errors.Is(err, ErrInvalidAccessKey) {
		t.Fatalf("expired Authenticate error = %v, want ErrInvalidAccessKey", err)
	}

	// Deleting the row proves the survivor is still answered from the cache.
	if _, err := db.Exec("DELETE FROM access_keys WHERE id = ?", survivor.Key.ID); err != nil {
		t.Fatalf("delete survivor row: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), survivor.Plaintext); err != nil {
		t.Fatalf("survivor lost its cache entry: %v", err)
	}
}
