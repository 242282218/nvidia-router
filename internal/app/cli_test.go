package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/adminauth"
	"nvidia-router/internal/database"
)

func TestCLIServeStopsWhenContextIsCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listener: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release listener: %v", err)
	}
	master := make([]byte, 32)
	master[0] = 1
	t.Setenv("NVIDIA_ROUTER_MASTER_KEY", base64.RawURLEncoding.EncodeToString(master))
	t.Setenv("NVIDIA_ROUTER_DATA_DIR", t.TempDir())
	t.Setenv("NVIDIA_ROUTER_LISTEN_ADDR", address)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runCLIContext(ctx, []string{"serve"}, io.Discard, io.Discard)
	}()
	waitForLive(t, "http://"+address+"/health/live")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after context cancellation")
	}
}

func TestCLIResetPasswordHonorsCancelledContext(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("NVIDIA_ROUTER_DATA_DIR", dataDir)
	db := openCLIData(t, dataDir)
	if err := adminauth.NewRepository(db, nil).EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	closeCLIDatabase(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runCLIContext(ctx, []string{"admin", "reset-password", "--password", "new CLI recovery password"}, io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reset error = %v, want context.Canceled", err)
	}
}

func TestCLIBackupHonorsCancelledContext(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("NVIDIA_ROUTER_DATA_DIR", dataDir)
	db := openCLIData(t, dataDir)
	if err := adminauth.NewRepository(db, nil).EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	closeCLIDatabase(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runCLIContext(ctx, []string{"db", "backup", "--output", filepath.Join(t.TempDir(), "backup.db")}, io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backup error = %v, want context.Canceled", err)
	}
}

func waitForLive(t *testing.T, endpoint string) {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("live endpoint did not become available: %s", endpoint)
}

func TestCLIResetPasswordRevokesSessionsWithoutChangingNVIDIASecrets(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("NVIDIA_ROUTER_DATA_DIR", dataDir)
	db := openCLIData(t, dataDir)
	repository := adminauth.NewRepository(db, nil)
	if err := repository.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO admin_sessions (id, token_digest, expires_at, created_at, last_seen_at)
		VALUES
			('cli-session-1', X'01', '2030-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			('cli-session-2', X'02', '2030-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert CLI sessions: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO nvidia_keys (
			ciphertext, nonce, fingerprint, display_prefix, display_suffix,
			created_at, updated_at
		) VALUES (X'010203', X'040506', X'070809', 'nvapi-', '-tail',
			'2026-07-29T00:00:00Z', '2026-07-29T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert CLI NVIDIA Key: %v", err)
	}
	before := readCLISecret(t, db)
	closeCLIDatabase(t, db)

	password := "new CLI recovery password"
	var stdout, stderr bytes.Buffer
	if err := runCLI([]string{"admin", "reset-password", "--password", password}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI reset-password: %v", err)
	}
	if strings.Contains(stdout.String()+stderr.String(), password) {
		t.Fatal("CLI output contains the new password")
	}

	db = openCLIData(t, dataDir)
	defer closeCLIDatabase(t, db)
	var passwordHash string
	if err := db.QueryRow("SELECT password_hash FROM admins WHERE id = 1").Scan(&passwordHash); err != nil {
		t.Fatalf("read reset password hash: %v", err)
	}
	if matched, err := adminauth.VerifyPassword(password, passwordHash); err != nil || !matched {
		t.Fatalf("reset password verification = %t, %v", matched, err)
	}
	var activeSessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM admin_sessions WHERE revoked_at IS NULL").Scan(&activeSessions); err != nil {
		t.Fatalf("count active CLI sessions: %v", err)
	}
	if activeSessions != 0 {
		t.Fatalf("active sessions after reset = %d, want 0", activeSessions)
	}
	if after := readCLISecret(t, db); after != before {
		t.Fatal("CLI reset changed NVIDIA encrypted fields")
	}
}

func TestCLIBackupWritesConsistentDatabase(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("NVIDIA_ROUTER_DATA_DIR", dataDir)
	db := openCLIData(t, dataDir)
	if err := adminauth.NewRepository(db, nil).EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO access_keys (name, key_digest, key_prefix, created_at)
		VALUES ('CLI client', X'0102', 'nvr_cli', '2026-07-29T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert CLI Access Key: %v", err)
	}
	closeCLIDatabase(t, db)

	backupPath := filepath.Join(t.TempDir(), "router backup.db")
	var stdout, stderr bytes.Buffer
	if err := runCLI([]string{"db", "backup", "--output", backupPath}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI db backup: %v", err)
	}

	backupDB, err := database.Open(backupPath)
	if err != nil {
		t.Fatalf("open CLI backup: %v", err)
	}
	defer closeCLIDatabase(t, backupDB)
	var username, accessName string
	if err := backupDB.QueryRow("SELECT username FROM admins WHERE id = 1").Scan(&username); err != nil {
		t.Fatalf("read backup admin: %v", err)
	}
	if err := backupDB.QueryRow("SELECT name FROM access_keys").Scan(&accessName); err != nil {
		t.Fatalf("read backup Access Key: %v", err)
	}
	if username != "admin" || accessName != "CLI client" {
		t.Fatalf("CLI backup data = %q/%q", username, accessName)
	}
}

func TestCLIBackupRejectsRouterDatabaseAsOutput(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("NVIDIA_ROUTER_DATA_DIR", dataDir)
	db := openCLIData(t, dataDir)
	if err := adminauth.NewRepository(db, nil).EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	closeCLIDatabase(t, db)

	databasePath := filepath.Join(dataDir, "router.db")
	err := runCLI([]string{"db", "backup", "--output", databasePath}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must differ from router database") {
		t.Fatalf("same-path backup error = %v", err)
	}

	db = openCLIData(t, dataDir)
	defer closeCLIDatabase(t, db)
	var username string
	if err := db.QueryRow("SELECT username FROM admins WHERE id = 1").Scan(&username); err != nil || username != "admin" {
		t.Fatalf("source database after rejected backup = %q, err=%v", username, err)
	}
}

func TestCLIResetPasswordErrorDoesNotEchoPassword(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("NVIDIA_ROUTER_DATA_DIR", dataDir)
	db := openCLIData(t, dataDir)
	if err := adminauth.NewRepository(db, nil).EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	closeCLIDatabase(t, db)

	password := "short-value"
	err := runCLI([]string{"admin", "reset-password", "--password", password}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runCLI accepted a short recovery password")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatal("CLI error contains the rejected password")
	}
	if !strings.Contains(err.Error(), "validate new password") {
		t.Fatalf("CLI error lacks password validation context: %v", err)
	}
}

type cliSecret struct {
	ciphertext  string
	nonce       string
	fingerprint string
}

func openCLIData(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(dataDir, "router.db"))
	if err != nil {
		t.Fatalf("open CLI database: %v", err)
	}
	return db
}

func closeCLIDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close CLI database: %v", err)
	}
}

func readCLISecret(t *testing.T, db *sql.DB) cliSecret {
	t.Helper()
	var value cliSecret
	if err := db.QueryRow("SELECT hex(ciphertext), hex(nonce), hex(fingerprint) FROM nvidia_keys LIMIT 1").Scan(
		&value.ciphertext,
		&value.nonce,
		&value.fingerprint,
	); err != nil {
		t.Fatalf("read CLI NVIDIA secret: %v", err)
	}
	return value
}
