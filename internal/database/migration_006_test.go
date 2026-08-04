package database

import (
	"path/filepath"
	"testing"
)

func TestMigration006CreatesProxyPoolSettingsWithSingleRowConstraints(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var tableSQL string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'proxy_pool_settings'").Scan(&tableSQL); err != nil {
		t.Fatalf("read proxy_pool_settings schema: %v", err)
	}
	if tableSQL == "" {
		t.Fatal("proxy_pool_settings table has empty schema")
	}

	if _, err := db.Exec(`
		INSERT INTO proxy_pool_settings (id, enabled, proxy_url, auth_key_nonce, auth_key_ciphertext, version, updated_at)
		VALUES (1, 0, '', NULL, NULL, 1, '2026-08-03T00:00:00Z')`); err != nil {
		t.Fatalf("insert disabled proxy setting: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO proxy_pool_settings (id, enabled, proxy_url, version, updated_at)
		VALUES (2, 0, '', 1, '2026-08-03T00:00:00Z')`); err == nil {
		t.Fatal("second proxy setting row was accepted")
	}
	if _, err := db.Exec(`
		UPDATE proxy_pool_settings SET enabled = 1, proxy_url = 'http://proxy-pool:8080', auth_key_nonce = NULL, auth_key_ciphertext = NULL WHERE id = 1`); err == nil {
		t.Fatal("enabled proxy setting without ciphertext was accepted")
	}
}
