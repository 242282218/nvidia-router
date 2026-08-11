package accesskey

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
)

func TestCreateReturnsPlaintextOnceAndPersistsOnlyDigest(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	dbPath := filepath.Join(t.TempDir(), "router.db")
	service, db := newTestService(t, dbPath)

	created, err := service.Create(context.Background(), "laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !validPlaintext(created.Plaintext) {
		t.Fatalf("plaintext format = %q", created.Plaintext)
	}
	if created.Key.Name != "laptop" || !strings.HasPrefix(created.Key.Prefix, "nvr_") {
		t.Fatalf("created key = %+v", created.Key)
	}

	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	if len(listed) != 1 || strings.Contains(string(encoded), created.Plaintext) || strings.Contains(string(encoded), "digest") {
		t.Fatalf("unsafe list response = %s", encoded)
	}

	var digest []byte
	if err := db.QueryRow("SELECT key_digest FROM access_keys WHERE id = ?", created.Key.ID).Scan(&digest); err != nil {
		t.Fatalf("query digest: %v", err)
	}
	if len(digest) != 32 || strings.Contains(string(digest), created.Plaintext) {
		t.Fatalf("stored digest length/content = %d/%q", len(digest), digest)
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint WAL: %v", err)
	}
	databaseBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read database: %v", err)
	}
	if strings.Contains(string(databaseBytes), created.Plaintext) {
		t.Fatal("database contains access key plaintext")
	}
	if strings.Contains(logs.String(), created.Plaintext) {
		t.Fatal("logs contain access key plaintext")
	}
}

func TestAuthenticateRejectsExpiredKeyAndPolicyUpdatesInvalidateCache(t *testing.T) {
	source := newManualClock(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	service, db := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)
	created, err := service.Create(context.Background(), "expiring")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), created.Plaintext); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	expires := source.Now().Add(time.Minute)
	zero := int64(0)
	if err := service.UpdatePolicy(context.Background(), created.Key.ID, &expires, 7, 11, 2, &zero); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	identity, err := service.Authenticate(context.Background(), created.Plaintext)
	if err != nil {
		t.Fatalf("Authenticate after policy update: %v", err)
	}
	if identity.RPMLimit != 7 || identity.TPMLimit != 11 || identity.MaxConcurrent != 2 {
		t.Fatalf("identity policy = %+v", identity)
	}
	source.Advance(time.Minute)
	if _, err := service.Authenticate(context.Background(), created.Plaintext); !errors.Is(err, ErrInvalidAccessKey) {
		t.Fatalf("expired Authenticate error = %v, want ErrInvalidAccessKey", err)
	}
	var stored string
	if err := db.QueryRow("SELECT expires_at FROM access_keys WHERE id = ?", created.Key.ID).Scan(&stored); err != nil {
		t.Fatalf("query expiration: %v", err)
	}
	if stored == "" {
		t.Fatal("expiration was not persisted")
	}
}

func TestAuthenticateRejectsMalformedAndRevokedKeys(t *testing.T) {
	service, _ := newTestService(t, filepath.Join(t.TempDir(), "router.db"))
	created, err := service.Create(context.Background(), "phone")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	identity, err := service.Authenticate(context.Background(), created.Plaintext)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if identity.ID != created.Key.ID || identity.Prefix != created.Key.Prefix {
		t.Fatalf("identity = %+v", identity)
	}
	for _, value := range []string{"", "nvr_short", created.Plaintext + "x", strings.Replace(created.Plaintext, "_", "+", 1)} {
		if _, err := service.Authenticate(context.Background(), value); !errors.Is(err, ErrInvalidAccessKey) {
			t.Fatalf("Authenticate(%q) error = %v", value, err)
		}
	}

	if err := service.Revoke(context.Background(), created.Key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), created.Plaintext); !errors.Is(err, ErrInvalidAccessKey) {
		t.Fatalf("Authenticate revoked error = %v", err)
	}
}

func TestRecordUseUpdatesAsynchronouslyAtMostOncePerMinute(t *testing.T) {
	source := newManualClock(time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC))
	service, db := newTestServiceWithClock(t, filepath.Join(t.TempDir(), "router.db"), source)
	created, err := service.Create(context.Background(), "desktop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	service.RecordUse(context.Background(), created.Key.ID)
	waitLastUsed(t, db, created.Key.ID, source.Now())

	source.Advance(30 * time.Second)
	service.RecordUse(context.Background(), created.Key.ID)
	time.Sleep(20 * time.Millisecond)
	assertLastUsed(t, db, created.Key.ID, time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC))

	source.Advance(31 * time.Second)
	service.RecordUse(context.Background(), created.Key.ID)
	waitLastUsed(t, db, created.Key.ID, source.Now())
}

func TestRevokeDropsUsageTrackingEntries(t *testing.T) {
	service, _ := newTestService(t, filepath.Join(t.TempDir(), "router.db"))
	created, err := service.Create(context.Background(), "desktop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	service.usageMu.Lock()
	service.lastRecorded[created.Key.ID] = time.Now()
	service.pending[created.Key.ID] = struct{}{}
	service.usageMu.Unlock()
	// Seed a rate-limit bucket so revocation must clean it up too.
	service.limiter.mu.Lock()
	service.limiter.buckets[created.Key.ID] = &limitBucket{windowStart: time.Now(), rpmCount: 3}
	service.limiter.mu.Unlock()

	if err := service.Revoke(context.Background(), created.Key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	service.usageMu.Lock()
	defer service.usageMu.Unlock()
	if _, ok := service.lastRecorded[created.Key.ID]; ok {
		t.Fatal("Revoke left a lastRecorded entry")
	}
	if _, ok := service.pending[created.Key.ID]; ok {
		t.Fatal("Revoke left a pending entry")
	}
	service.limiter.mu.Lock()
	defer service.limiter.mu.Unlock()
	if _, ok := service.limiter.buckets[created.Key.ID]; ok {
		t.Fatal("Revoke left a rate-limit bucket")
	}
}

func newTestService(t *testing.T, path string) (*Service, *sql.DB) {
	t.Helper()
	return newTestServiceWithClock(t, path, newManualClock(time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)))
}

func newTestServiceWithClock(t *testing.T, path string, source *manualClock) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	keys, err := crypto.New([32]byte{1})
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return NewService(NewRepository(db), keys, source), db
}

func validPlaintext(value string) bool {
	if len(value) != 47 || !strings.HasPrefix(value, "nvr_") {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(value, "nvr_"))
	return err == nil && len(decoded) == 32
}

func waitLastUsed(t *testing.T, db *sql.DB, id int64, want time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var value sql.NullString
		if err := db.QueryRow("SELECT last_used_at FROM access_keys WHERE id = ?", id).Scan(&value); err != nil {
			t.Fatalf("query last used: %v", err)
		}
		if value.Valid && value.String == want.UTC().Format(time.RFC3339) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	assertLastUsed(t, db, id, want)
}

func assertLastUsed(t *testing.T, db *sql.DB, id int64, want time.Time) {
	t.Helper()
	var value sql.NullString
	if err := db.QueryRow("SELECT last_used_at FROM access_keys WHERE id = ?", id).Scan(&value); err != nil {
		t.Fatalf("query last used: %v", err)
	}
	if !value.Valid || value.String != want.UTC().Format(time.RFC3339) {
		t.Fatalf("last_used_at = %q, want %q", value.String, want.UTC().Format(time.RFC3339))
	}
}
