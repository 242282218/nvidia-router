package nvidiakey

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
)

func TestFirstEnabledIDSkipsCoolingDownKeys(t *testing.T) {
	service, db, _ := newNVIDIAKeyTestService(t, newFakeValidator())
	first := insertKeyForDiscoveryTest(t, db)
	second := insertKeyForDiscoveryTest(t, db)
	if _, err := db.Exec(`UPDATE nvidia_keys SET cooldown_until = ? WHERE id = ?`, "2026-07-30T04:00:30Z", first); err != nil {
		t.Fatalf("set cooldown: %v", err)
	}
	id, err := service.FirstEnabledID(context.Background())
	if err != nil {
		t.Fatalf("FirstEnabledID: %v", err)
	}
	if id != second {
		t.Fatalf("FirstEnabledID = %d, want %d", id, second)
	}
}

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

func TestProxyUnavailableImportDoesNotPersistKey(t *testing.T) {
	token := "proxy-unavailable-token-123"
	validator := newFakeValidator()
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationProxyUnavailable}
	service, db, _ := newNVIDIAKeyTestService(t, validator)

	result, err := service.Import(context.Background(), token)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Status != ImportStatusTemporarilyUnavailable || result.Reason != "proxy_temporarily_unavailable" {
		t.Fatalf("result = %+v", result)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM nvidia_keys").Scan(&count); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if count != 0 {
		t.Fatalf("persisted keys = %d, want 0", count)
	}
}

func TestProxyUnavailableKeyTestDoesNotWriteState(t *testing.T) {
	validator := newFakeValidator()
	service, _, _ := newNVIDIAKeyTestService(t, validator)
	token := "proxy-existing-token-123"
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationValid}
	imported, err := service.Import(context.Background(), token)
	if err != nil || imported.Key == nil {
		t.Fatalf("Import = %+v, %v", imported, err)
	}
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationProxyUnavailable}

	result, err := service.Test(context.Background(), imported.Key.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if result.Status != "temporarily_unavailable" || result.Reason != "proxy_temporarily_unavailable" || result.Snapshot.ID != 0 {
		t.Fatalf("result = %+v", result)
	}
	keys, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0].CooldownUntil != nil || keys[0].AuthInvalid {
		t.Fatalf("key state changed = %+v", keys)
	}
}

func TestProxyUnavailableTestLeavesKeyRowAndModelBlocksUntouched(t *testing.T) {
	validator := newFakeValidator()
	service, db, _ := newNVIDIAKeyTestService(t, validator)
	token := "proxy-mutation-token-123"
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
	imported, err := service.Import(context.Background(), token)
	if err != nil || imported.Key == nil {
		t.Fatalf("Import = %+v, %v", imported, err)
	}
	keyID := imported.Key.ID
	// Seed non-default values on every key field the failure path could touch,
	// plus one model block, so any accidental state write during the proxy
	// error path is detected as a before/after diff.
	if _, err := db.Exec(`
		UPDATE nvidia_keys SET
			auth_invalid = 1,
			cooldown_until = '2026-07-29T03:00:00Z',
			cooldown_reason = 'seed_reason',
			cooldown_level = 2,
			consecutive_failures = 3,
			last_error_at = '2026-07-29T04:00:00Z',
			last_error_code = 'seed_error',
			updated_at = '2026-07-29T05:00:00Z'
		WHERE id = ?
	`, keyID); err != nil {
		t.Fatalf("seed key state: %v", err)
	}
	modelID := createStateTestModel(t, db)
	if _, err := db.Exec(`
		INSERT INTO nvidia_key_model_blocks (
			nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at
		) VALUES (?, ?, 'seed_block', 403, '2026-07-29T03:00:00Z', '2026-07-29T03:00:00Z')
	`, keyID, modelID); err != nil {
		t.Fatalf("seed model block: %v", err)
	}

	readKeyRow := func() []string {
		t.Helper()
		var (
			authInvalid, cooldownLevel, consecutiveFailures           int
			cooldownUntil, cooldownReason, lastErrorAt, lastErrorCode sql.NullString
			updatedAt                                                 string
		)
		if err := db.QueryRow(`
			SELECT auth_invalid, cooldown_until, cooldown_reason, cooldown_level,
			       consecutive_failures, last_error_at, last_error_code, updated_at
			FROM nvidia_keys WHERE id = ?
		`, keyID).Scan(
			&authInvalid, &cooldownUntil, &cooldownReason, &cooldownLevel,
			&consecutiveFailures, &lastErrorAt, &lastErrorCode, &updatedAt,
		); err != nil {
			t.Fatalf("read key row: %v", err)
		}
		return []string{
			strconv.Itoa(authInvalid), cooldownUntil.String, cooldownReason.String,
			strconv.Itoa(cooldownLevel), strconv.Itoa(consecutiveFailures),
			lastErrorAt.String, lastErrorCode.String, updatedAt,
		}
	}
	readBlocks := func() []string {
		t.Helper()
		rows, err := db.Query(`
			SELECT reason_code, upstream_status, first_seen_at, last_seen_at
			FROM nvidia_key_model_blocks WHERE nvidia_key_id = ?
		`, keyID)
		if err != nil {
			t.Fatalf("read model blocks: %v", err)
		}
		defer rows.Close()
		blocks := []string{}
		for rows.Next() {
			var reason string
			var status sql.NullInt64
			var firstSeen, lastSeen string
			if err := rows.Scan(&reason, &status, &firstSeen, &lastSeen); err != nil {
				t.Fatalf("scan model block: %v", err)
			}
			blocks = append(blocks, fmt.Sprintf("%s/%d/%s/%s", reason, status.Int64, firstSeen, lastSeen))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate model blocks: %v", err)
		}
		return blocks
	}

	beforeRow, beforeBlocks := readKeyRow(), readBlocks()
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationProxyUnavailable}

	result, err := service.Test(context.Background(), keyID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if result.Status != "temporarily_unavailable" || result.Reason != "proxy_temporarily_unavailable" || result.Snapshot.ID != 0 {
		t.Fatalf("result = %+v", result)
	}

	afterRow, afterBlocks := readKeyRow(), readBlocks()
	if !reflect.DeepEqual(beforeRow, afterRow) {
		t.Fatalf("key row changed across proxy error\nbefore: %v\nafter:  %v", beforeRow, afterRow)
	}
	if !reflect.DeepEqual(beforeBlocks, afterBlocks) {
		t.Fatalf("model blocks changed across proxy error\nbefore: %v\nafter:  %v", beforeBlocks, afterBlocks)
	}
	if len(afterBlocks) != 1 {
		t.Fatalf("block count = %d, want 1", len(afterBlocks))
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

func TestMarkFailurePersistsCooldownAcrossReopen(t *testing.T) {
	service, db, dbPath, keyID := newImportedKeyForStateTest(t)
	snapshot, err := service.MarkFailure(context.Background(), keyID, 0, fault.Fault{
		HTTPStatus: 429, Scope: fault.ScopeTransientCredential, Retryable: true,
		RetryAfter: 10 * time.Second, PublicCode: "rate_limit_exceeded",
	})
	if err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}
	if snapshot.CooldownUntil == nil || snapshot.CooldownLevel != 1 || snapshot.ConsecutiveFailures != 1 {
		t.Fatalf("failure snapshot = %+v", snapshot)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	reopened, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	persisted, err := NewRepository(reopened).ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots after reopen: %v", err)
	}
	if len(persisted) != 1 || persisted[0].CooldownUntil == nil || persisted[0].CooldownLevel != 1 || persisted[0].ConsecutiveFailures != 1 {
		t.Fatalf("persisted snapshots = %+v", persisted)
	}
}

func TestMarkFailureRecordsPastRetryAfterWithoutFallback(t *testing.T) {
	service, _, _, keyID := newImportedKeyForStateTest(t)
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	response := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{now.Add(-time.Minute).Format(http.TimeFormat)}},
	}
	classified := fault.Classify(response, nil, false, now)

	snapshot, err := service.MarkFailure(context.Background(), keyID, 0, classified)
	if err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}
	if snapshot.CooldownUntil == nil || !snapshot.CooldownUntil.Equal(now) || snapshot.CooldownLevel != 1 || snapshot.ConsecutiveFailures != 1 {
		t.Fatalf("past Retry-After snapshot = %+v", snapshot)
	}
}

func TestMarkFailureDisablesCredentialOrBlocksOnlyModel(t *testing.T) {
	t.Run("credential", func(t *testing.T) {
		service, db, _, keyID := newImportedKeyForStateTest(t)
		snapshot, err := service.MarkFailure(context.Background(), keyID, 0, fault.Fault{
			HTTPStatus: 401, Scope: fault.ScopeCredential, Retryable: true,
			DisableKey: true, PublicCode: "invalid_api_key",
		})
		if err != nil {
			t.Fatalf("MarkFailure: %v", err)
		}
		if !snapshot.AuthInvalid {
			t.Fatalf("credential snapshot = %+v", snapshot)
		}
		var enabled int
		if err := db.QueryRow("SELECT enabled FROM nvidia_keys WHERE id = ?", keyID).Scan(&enabled); err != nil || enabled != 1 {
			t.Fatalf("enabled = %d, err = %v", enabled, err)
		}
	})

	t.Run("model", func(t *testing.T) {
		service, db, _, keyID := newImportedKeyForStateTest(t)
		modelID := createStateTestModel(t, db)
		snapshot, err := service.MarkFailure(context.Background(), keyID, modelID, fault.Fault{
			HTTPStatus: 403, Scope: fault.ScopeModelCredential, Retryable: true,
			BlockModel: true, PublicCode: "model_not_available",
		})
		if err != nil {
			t.Fatalf("MarkFailure: %v", err)
		}
		if snapshot.AuthInvalid {
			t.Fatalf("model failure invalidated credential: %+v", snapshot)
		}
		var reason string
		var status int
		if err := db.QueryRow(`
			SELECT reason_code, upstream_status FROM nvidia_key_model_blocks
			WHERE nvidia_key_id = ? AND model_id = ?
		`, keyID, modelID).Scan(&reason, &status); err != nil {
			t.Fatalf("query model block: %v", err)
		}
		if reason != "model_not_available" || status != 403 {
			t.Fatalf("model block = %q/%d", reason, status)
		}
	})
}

func TestMarkSuccessClearsCooldownAndFailureCounters(t *testing.T) {
	service, db, _, keyID := newImportedKeyForStateTest(t)
	if _, err := service.MarkFailure(context.Background(), keyID, 0, fault.Fault{
		HTTPStatus: 429, Retryable: true, RetryAfter: 10 * time.Second, PublicCode: "rate_limit_exceeded",
	}); err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}

	snapshot, err := service.MarkSuccess(context.Background(), keyID)
	if err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	if snapshot.CooldownUntil != nil || snapshot.CooldownLevel != 0 || snapshot.ConsecutiveFailures != 0 {
		t.Fatalf("success snapshot = %+v", snapshot)
	}
	var cooldownReason, lastErrorCode sql.NullString
	var lastSuccessAt sql.NullString
	if err := db.QueryRow(`
		SELECT cooldown_reason, last_error_code, last_success_at FROM nvidia_keys WHERE id = ?
	`, keyID).Scan(&cooldownReason, &lastErrorCode, &lastSuccessAt); err != nil {
		t.Fatalf("query success state: %v", err)
	}
	if cooldownReason.Valid || lastErrorCode.Valid || !lastSuccessAt.Valid {
		t.Fatalf("success state reason/error/success = %+v/%+v/%+v", cooldownReason, lastErrorCode, lastSuccessAt)
	}
}

func TestMarkSuccessClearsAuthInvalid(t *testing.T) {
	service, db, _, keyID := newImportedKeyForStateTest(t)
	if _, err := db.Exec(`
		UPDATE nvidia_keys SET
			auth_invalid = 1,
			cooldown_until = '2026-07-30T03:01:00Z',
			cooldown_level = 2,
			consecutive_failures = 3
		WHERE id = ?
	`, keyID); err != nil {
		t.Fatalf("prepare invalid key state: %v", err)
	}

	if _, err := service.MarkSuccess(context.Background(), keyID); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	snapshots, err := NewRepository(db).ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.AuthInvalid || snapshot.CooldownUntil != nil || snapshot.CooldownLevel != 0 || snapshot.ConsecutiveFailures != 0 {
		t.Fatalf("success snapshot = %+v", snapshot)
	}
}

func TestMarkFailureRollsBackWithoutSnapshot(t *testing.T) {
	service, db, _, keyID := newImportedKeyForStateTest(t)
	snapshot, err := service.MarkFailure(context.Background(), keyID, 999999, fault.Fault{
		HTTPStatus: 403, Scope: fault.ScopeModelCredential, Retryable: true,
		BlockModel: true, PublicCode: "model_not_available",
	})
	if err == nil {
		t.Fatal("MarkFailure succeeded with missing model")
	}
	if snapshot != (keystate.KeySnapshot{}) {
		t.Fatalf("snapshot after rollback = %+v", snapshot)
	}
	var lastErrorAt sql.NullString
	if err := db.QueryRow("SELECT last_error_at FROM nvidia_keys WHERE id = ?", keyID).Scan(&lastErrorAt); err != nil {
		t.Fatalf("query rolled back state: %v", err)
	}
	if lastErrorAt.Valid {
		t.Fatalf("last_error_at persisted after rollback: %s", lastErrorAt.String)
	}
}

func TestManualTestUsesActualFaultMetadata(t *testing.T) {
	service, _, _, keyID := newImportedKeyForStateTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "37")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(upstream.Close)
	baseURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	descriptor, err := nvidia.DefaultDescriptor().WithBaseURL(baseURL)
	if err != nil {
		t.Fatalf("rewrite descriptor: %v", err)
	}
	client, err := nvidia.NewClient(upstream.Client(), descriptor, testNVIDIASettings{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	service.validator = client

	result, err := service.Test(context.Background(), keyID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if result.Status != "temporarily_unavailable" {
		t.Fatalf("status = %q", result.Status)
	}
	wantCooldown := time.Date(2026, 7, 30, 3, 0, 37, 0, time.UTC)
	if result.Snapshot.CooldownUntil == nil || !result.Snapshot.CooldownUntil.Equal(wantCooldown) {
		t.Fatalf("cooldown = %v, want %v", result.Snapshot.CooldownUntil, wantCooldown)
	}
}

type testNVIDIASettings struct{}

func (testNVIDIASettings) Snapshot() runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{ConnectTimeoutMS: 1000, FirstByteTimeoutMS: 2000}
}

func newImportedKeyForStateTest(t *testing.T) (*Service, *sql.DB, string, int64) {
	t.Helper()
	validator := newFakeValidator()
	token := "state-test-token-123456"
	validator.results[token] = nvidia.ValidationResult{State: nvidia.ValidationValid, Models: []string{"model-a"}}
	service, db, dbPath := newNVIDIAKeyTestService(t, validator)
	result, err := service.Import(context.Background(), token)
	if err != nil || result.Key == nil {
		t.Fatalf("Import state test key: %+v, %v", result, err)
	}
	return service, db, dbPath, result.Key.ID
}

func createStateTestModel(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	repository := modelcatalog.NewRepository(db)
	if err := repository.SaveSelections(context.Background(), []modelcatalog.Selection{{
		PublicID: "state-test-model", UpstreamID: "state-test-model", DisplayName: "State Test Model",
		Kind: modelcatalog.KindChat, Enabled: true, ReasoningWireFormat: "none",
	}}, now); err != nil {
		t.Fatalf("SaveSelections: %v", err)
	}
	model, err := repository.ResolveEnabled(context.Background(), "state-test-model")
	if err != nil {
		t.Fatalf("ResolveEnabled: %v", err)
	}
	return model.ID
}

func insertKeyForDiscoveryTest(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO nvidia_keys (ciphertext, nonce, fingerprint, display_prefix, display_suffix, created_at, updated_at) VALUES (x'01', x'02', randomblob(16), 'p', 's', '2026-07-30T03:00:00Z', '2026-07-30T03:00:00Z')`)
	if err != nil {
		t.Fatalf("insert discovery key: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("discovery key id: %v", err)
	}
	return id
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

func (v *fakeValidator) ValidateCredential(_ context.Context, token string, _ time.Time) nvidia.ValidationResult {
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
