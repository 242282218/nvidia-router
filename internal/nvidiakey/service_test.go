package nvidiakey

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/upstream/nvidia"
)

func TestImportAcceptsGenericPrintableTokensAndDeduplicates(t *testing.T) {
	validator := newFakeValidator()
	validToken := "generic-build-token-123456"
	validator.results[validToken] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
	service, db, _ := newNVIDIAKeyTestService(t, validator)

	imported, err := service.Import(context.Background(), validToken)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imported.Status != ImportStatusImported || imported.Key == nil || strings.Contains(imported.Masked, validToken) {
		t.Fatalf("import result = %+v", imported)
	}
	duplicate, err := service.Import(context.Background(), validToken)
	if err != nil {
		t.Fatalf("Import duplicate: %v", err)
	}
	if duplicate.Status != ImportStatusDuplicate || validator.CallCount(validToken) != 1 {
		t.Fatalf("duplicate/calls = %+v/%d", duplicate, validator.CallCount(validToken))
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM nvidia_keys").Scan(&count); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("key count = %d", count)
	}
}

func TestImportRejectsInvalidFormatsBeforeValidation(t *testing.T) {
	validator := newFakeValidator()
	service, _, _ := newNVIDIAKeyTestService(t, validator)
	tests := []string{
		strings.Repeat("a", 19),
		strings.Repeat("a", 513),
		"token with whitespace 123",
		"token-with-newline-123\n",
		"token-with-control-\x00123",
	}
	for _, token := range tests {
		result, err := service.Import(context.Background(), token)
		if err != nil {
			t.Fatalf("Import(%q): %v", token, err)
		}
		if result.Status != ImportStatusInvalid || validator.CallCount(token) != 0 {
			t.Fatalf("result/calls for %q = %+v/%d", token, result, validator.CallCount(token))
		}
	}

	for _, token := range []string{strings.Repeat("a", 20), strings.Repeat("b", 512)} {
		validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
		result, err := service.Import(context.Background(), token)
		if err != nil || result.Status != ImportStatusImported {
			t.Fatalf("boundary import len %d = %+v, %v", len(token), result, err)
		}
	}
}

func TestImportMasksPrintableUnicodeWithoutCorruptingUTF8(t *testing.T) {
	token := strings.Repeat("密", 7)
	validator := newFakeValidator()
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
	service, _, _ := newNVIDIAKeyTestService(t, validator)

	result, err := service.Import(context.Background(), token)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Status != ImportStatusImported || result.Key == nil {
		t.Fatalf("result = %+v", result)
	}
	for name, value := range map[string]string{
		"masked": result.Masked,
		"prefix": result.Key.DisplayPrefix,
		"suffix": result.Key.DisplaySuffix,
	} {
		if !utf8.ValidString(value) || value == token {
			t.Fatalf("%s = %q", name, value)
		}
	}
}

func TestBatchImportPreservesLineNumbersAndOnlyPersistsValidKeys(t *testing.T) {
	valid := "valid-generic-token-123456"
	invalid := "invalid-generic-token-1234"
	temporary := "temporary-generic-token-12"
	indeterminate := "unknown-generic-token-123"
	validator := newFakeValidator()
	validator.results[valid] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
	validator.results[invalid] = nvidia.ValidationResult{State: nvidia.ValidationInvalidCredential}
	validator.results[temporary] = nvidia.ValidationResult{State: nvidia.ValidationTemporarilyUnavailable}
	validator.results[indeterminate] = nvidia.ValidationResult{State: nvidia.ValidationIndeterminate}
	service, db, _ := newNVIDIAKeyTestService(t, validator)

	results := service.ImportBatch(context.Background(), strings.Join([]string{
		"  " + valid + "  ",
		"",
		invalid,
		temporary,
		indeterminate,
	}, "\n"))

	wantLines := []int{1, 3, 4, 5}
	wantStatuses := []ImportStatus{ImportStatusImported, ImportStatusInvalid, ImportStatusTemporarilyUnavailable, ImportStatusIndeterminate}
	if len(results) != len(wantLines) {
		t.Fatalf("result count = %d, want %d", len(results), len(wantLines))
	}
	for index, result := range results {
		if result.Line != wantLines[index] || result.Status != wantStatuses[index] {
			t.Fatalf("result %d = %+v", index, result)
		}
		for _, secret := range []string{valid, invalid, temporary, indeterminate} {
			if result.Masked == secret || strings.Contains(result.Reason, secret) {
				t.Fatalf("result leaks secret: %+v", result)
			}
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM nvidia_keys").Scan(&count); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted keys = %d, want 1", count)
	}
}

func TestWithSecretZerosPlaintextAndPropagatesCallbackError(t *testing.T) {
	token := "secret-generic-token-12345"
	validator := newFakeValidator()
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
	service, _, _ := newNVIDIAKeyTestService(t, validator)
	result, err := service.Import(context.Background(), token)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	callbackErr := errors.New("callback failed")
	var borrowed []byte
	err = service.WithSecret(context.Background(), result.Key.ID, func(secret []byte) error {
		if string(secret) != token {
			t.Fatalf("secret = %q", secret)
		}
		borrowed = secret
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("WithSecret error = %v", err)
	}
	if !allZero(borrowed) {
		t.Fatalf("borrowed secret was not zeroed: %v", borrowed)
	}
}

func TestImportDoesNotLeakPlaintextToDatabaseWALOrLogs(t *testing.T) {
	token := "nvapi-fixture-not-a-real-key-123456789"
	validator := newFakeValidator()
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
	service, _, dbPath := newNVIDIAKeyTestService(t, validator)

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	if _, err := service.Import(context.Background(), token); err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, path := range []string{dbPath, dbPath + "-wal"} {
		contents, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(contents, []byte(token)) {
			t.Fatalf("%s contains plaintext key", filepath.Base(path))
		}
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("logs contain plaintext key")
	}
}

func TestRepositoryListsPersistedSchedulingSnapshots(t *testing.T) {
	_, db, _ := newNVIDIAKeyTestService(t, newFakeValidator())
	if _, err := db.Exec(`
		INSERT INTO nvidia_keys (
			ciphertext, nonce, fingerprint, display_prefix, display_suffix,
			enabled, auth_invalid, cooldown_until, cooldown_level,
			consecutive_failures, created_at, updated_at
		) VALUES
			(x'01', x'02', x'03', 'first', 'one', 1, 0, NULL, 0, 0, ?, ?),
			(x'04', x'05', x'06', 'second', 'two', 0, 1, ?, 3, 4, ?, ?)
	`,
		"2026-07-30T03:00:00Z", "2026-07-30T03:00:00Z",
		"2026-07-30T03:05:00Z", "2026-07-30T03:00:00Z", "2026-07-30T03:00:00Z",
	); err != nil {
		t.Fatalf("insert scheduling states: %v", err)
	}

	snapshots, err := NewRepository(db).ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count = %d", len(snapshots))
	}
	want := keystate.KeySnapshot{ID: snapshots[1].ID, Enabled: false, AuthInvalid: true, CooldownLevel: 3, ConsecutiveFailures: 4}
	if snapshots[1].Enabled != want.Enabled || snapshots[1].AuthInvalid != want.AuthInvalid ||
		snapshots[1].CooldownLevel != want.CooldownLevel || snapshots[1].ConsecutiveFailures != want.ConsecutiveFailures ||
		snapshots[1].CooldownUntil == nil || snapshots[1].CooldownUntil.Format(time.RFC3339) != "2026-07-30T03:05:00Z" {
		t.Fatalf("snapshot = %+v", snapshots[1])
	}
}

func newNVIDIAKeyTestService(t *testing.T, validator *fakeValidator) (*Service, *sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "router.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	keys, err := crypto.New([32]byte{2})
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return NewService(NewRepository(db), keys, validator, fixedClock{now: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)}), db, dbPath
}

type fakeValidator struct {
	mu      sync.Mutex
	results map[string]nvidia.ValidationResult
	calls   map[string]int
}

func newFakeValidator() *fakeValidator {
	return &fakeValidator{results: make(map[string]nvidia.ValidationResult), calls: make(map[string]int)}
}

func (v *fakeValidator) ValidateCredential(_ context.Context, token string) nvidia.ValidationResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls[token]++
	if result, ok := v.results[token]; ok {
		return result
	}
	return nvidia.ValidationResult{State: nvidia.ValidationInvalidCredential}
}

func (v *fakeValidator) CallCount(token string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls[token]
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time                            { return c.now }
func (fixedClock) NewTimer(duration time.Duration) *time.Timer { return time.NewTimer(duration) }
func (fixedClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
