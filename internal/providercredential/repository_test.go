package providercredential

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"nvidia-router/internal/crypto"
	"nvidia-router/internal/database"
)

func openCredentialRepo(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keys, err := crypto.New([32]byte{9})
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return NewRepository(db, nil, keys), db
}

func TestCreateResolveRoundTrip(t *testing.T) {
	repo, db := openCredentialRepo(t)
	ctx := context.Background()
	const token = "sk-test-provider-token-1234567890"

	created, err := repo.Create(ctx, "siliconflow", "https://api.siliconflow.cn/v1", token)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "siliconflow" {
		t.Fatalf("created name = %q", created.Name)
	}
	if created.Enabled {
		t.Fatal("new provider credential is enabled; want disabled by default")
	}
	// Plaintext never persists.
	var ciphertext string
	if err := db.QueryRow("SELECT hex(ciphertext) FROM provider_credentials WHERE id = ?", created.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("query ciphertext: %v", err)
	}
	if strings.Contains(ciphertext, token) {
		t.Fatal("database contains provider token plaintext")
	}

	if err := repo.SetEnabled(ctx, created.ID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	resolved, gotToken, err := repo.Resolve(ctx, "siliconflow")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotToken != token {
		t.Fatalf("resolved token = %q", gotToken)
	}
	if resolved.BaseURL != "https://api.siliconflow.cn/v1" {
		t.Fatalf("resolved base URL = %q", resolved.BaseURL)
	}
}

func TestCreateUpsertsByNameAndPreservesEnabled(t *testing.T) {
	repo, _ := openCredentialRepo(t)
	ctx := context.Background()
	const token = "sk-upsert-token-1234567890"

	first, err := repo.Create(ctx, "siliconflow", "https://api.siliconflow.cn/v1", token)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := repo.Create(ctx, "siliconflow", "https://api.siliconflow.cn/v2", token)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("upsert created new row: %d vs %d", first.ID, second.ID)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list length = %d, want 1", len(list))
	}
	if list[0].BaseURL != "https://api.siliconflow.cn/v2" {
		t.Fatalf("upsert base URL = %q, want v2", list[0].BaseURL)
	}
}

func TestProviderUpsertReturnsConflictedRowID(t *testing.T) {
	repo, _ := openCredentialRepo(t)
	ctx := context.Background()
	first, err := repo.Create(ctx, "fixture-provider", "https://api.example.test/v1", "fixture-provider-key-1")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := repo.Create(ctx, "other-provider", "https://other.example.test/v1", "fixture-provider-key-2"); err != nil {
		t.Fatalf("other Create: %v", err)
	}
	updated, err := repo.Create(ctx, "fixture-provider", "https://api.example.test/v2", "fixture-provider-key-3")
	if err != nil {
		t.Fatalf("conflicting Create: %v", err)
	}
	if updated.ID != first.ID {
		t.Fatalf("upsert returned row ID %d, want conflicted row ID %d", updated.ID, first.ID)
	}
}

func TestSetEnabledGatesResolve(t *testing.T) {
	repo, _ := openCredentialRepo(t)
	ctx := context.Background()
	const token = "sk-disable-token-1234567890"

	created, err := repo.Create(ctx, "siliconflow", "https://api.siliconflow.cn/v1", token)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SetEnabled(ctx, created.ID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if _, _, err := repo.Resolve(ctx, "siliconflow"); err == nil {
		t.Fatal("Resolve of disabled credential succeeded")
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list[0].Enabled {
		t.Fatal("disabled credential reported enabled")
	}
}

func TestResolveUnknownReturnsErrNotFound(t *testing.T) {
	repo, _ := openCredentialRepo(t)
	if _, _, err := repo.Resolve(context.Background(), "missing"); err == nil {
		t.Fatal("Resolve of unknown credential succeeded")
	}
}

func TestMaskNeverExposesMoreThanAQuarter(t *testing.T) {
	tests := []struct {
		token      string
		wantPrefix string
		wantSuffix string
	}{
		// Short tokens must not leak most of their characters.
		{token: "abcdefgh", wantPrefix: "ab"},
		{token: "abcde", wantPrefix: "ab"},
		// A medium token exposes a small prefix (quarter budget) and no tail.
		{token: "nvapi-1234567890abcdef", wantPrefix: "nvapi"},
		// A long token exposes up to 8 prefix chars plus a short tail, bounded by
		// the quarter budget.
		{token: "nvapi-1234567890abcdef1234567890abcdef1234567890", wantPrefix: "nvapi-12", wantSuffix: "7890"},
	}
	for _, tt := range tests {
		prefix, suffix := mask(tt.token)
		if tt.wantPrefix != "" && prefix != tt.wantPrefix {
			t.Fatalf("mask(%q) prefix = %q, want %q", tt.token, prefix, tt.wantPrefix)
		}
		if tt.wantSuffix != "" && suffix != tt.wantSuffix {
			t.Fatalf("mask(%q) suffix = %q, want %q", tt.token, suffix, tt.wantSuffix)
		}
		revealed := len(prefix) + len(suffix)
		if revealed > len(tt.token)/4+1 {
			t.Fatalf("mask(%q) revealed %d/%d chars, exceeds quarter budget", tt.token, revealed, len(tt.token))
		}
	}
}
