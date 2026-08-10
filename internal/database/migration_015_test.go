package database

import (
	"io/fs"
	"net/url"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigration015SeedsDeepSeekV4FlashAlias(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/015_deepseek_v4_flash_alias.sql")
	if err != nil {
		t.Fatalf("read migration 015: %v", err)
	}
	with015 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with015[name] = file
	}
	with015["migrations/015_deepseek_v4_flash_alias.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-015: %v", err)
	}
	if err := migrateFS(db, with015); err != nil {
		t.Fatalf("migrate 015: %v", err)
	}

	var publicID, upstreamID, displayName, kind string
	var enabled, reasoning int
	var reasoningFormat string
	if err := db.QueryRow(`
		SELECT public_id, upstream_id, display_name, kind, enabled, supports_reasoning, reasoning_wire_format
		FROM models WHERE public_id = 'deepseek-v4-flash'`).Scan(&publicID, &upstreamID, &displayName, &kind, &enabled, &reasoning, &reasoningFormat); err != nil {
		t.Fatalf("read seeded deepseek alias: %v", err)
	}
	if upstreamID != "deepseek-ai/deepseek-v4-flash-0731" {
		t.Fatalf("seeded upstream_id = %q, want deepseek-ai/deepseek-v4-flash-0731", upstreamID)
	}
	if kind != "chat" || enabled != 1 || reasoning != 1 || reasoningFormat != "openai" {
		t.Fatalf("seeded alias capabilities = kind:%s enabled:%d reasoning:%d format:%s", kind, enabled, reasoning, reasoningFormat)
	}
	if displayName == "" {
		t.Fatal("seeded alias has empty display name")
	}
	if err := migrateFS(db, with015); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}

func TestMigration015DoesNotOverwriteExistingDeepSeekAlias(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/015_deepseek_v4_flash_alias.sql")
	if err != nil {
		t.Fatalf("read migration 015: %v", err)
	}
	with015 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with015[name] = file
	}
	with015["migrations/015_deepseek_v4_flash_alias.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-015: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO models (
			public_id, upstream_id, display_name, kind, enabled,
			supports_vision, supports_tools, supports_reasoning, reasoning_wire_format,
			capability_verified_at, created_at, updated_at
		) VALUES (
			'deepseek-v4-flash', 'custom/upstream-target', 'Operator Custom', 'chat', 0,
			0, 0, 0, 'none', NULL, '2026-08-08T00:00:00Z', '2026-08-08T00:00:00Z'
		)
	`); err != nil {
		t.Fatalf("insert operator alias before migration: %v", err)
	}
	if err := migrateFS(db, with015); err != nil {
		t.Fatalf("migrate 015 over existing alias: %v", err)
	}

	var upstreamID, displayName string
	var enabled int
	if err := db.QueryRow(`SELECT upstream_id, display_name, enabled FROM models WHERE public_id = 'deepseek-v4-flash'`).Scan(&upstreamID, &displayName, &enabled); err != nil {
		t.Fatalf("read preserved alias: %v", err)
	}
	if upstreamID != "custom/upstream-target" || displayName != "Operator Custom" || enabled != 0 {
		t.Fatalf("operator alias was overwritten: upstream=%s name=%s enabled=%d", upstreamID, displayName, enabled)
	}
}
