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

func TestMigration019AddsModelPricingColumns(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql", "015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql",
		"017_admin_audit_logs.sql", "018_access_key_token_budget.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/019_model_pricing.sql")
	if err != nil {
		t.Fatalf("read migration 019: %v", err)
	}
	with019 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with019[name] = file
	}
	with019["migrations/019_model_pricing.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-019: %v", err)
	}

	// Seed a model before the migration; its pricing must default to NULL.
	if _, err := db.Exec(`
		INSERT INTO models (public_id, upstream_id, display_name, kind, enabled, created_at, updated_at)
		VALUES ('test-model', 'upstream/test', 'Test', 'chat', 1, '2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert model: %v", err)
	}
	if err := migrateFS(db, with019); err != nil {
		t.Fatalf("migrate 019: %v", err)
	}

	var input, output any
	if err := db.QueryRow(`SELECT input_usd_per_mtok, output_usd_per_mtok FROM models WHERE public_id = 'test-model'`).Scan(&input, &output); err != nil {
		t.Fatalf("read model pricing: %v", err)
	}
	if input != nil || output != nil {
		t.Fatalf("legacy model pricing = %v/%v, want nil/nil", input, output)
	}

	if _, err := db.Exec(`UPDATE models SET input_usd_per_mtok = 0.14, output_usd_per_mtok = 0.28 WHERE public_id = 'test-model'`); err != nil {
		t.Fatalf("update pricing: %v", err)
	}
	var storedInput, storedOutput float64
	if err := db.QueryRow(`SELECT input_usd_per_mtok, output_usd_per_mtok FROM models WHERE public_id = 'test-model'`).Scan(&storedInput, &storedOutput); err != nil {
		t.Fatalf("read updated pricing: %v", err)
	}
	if storedInput != 0.14 || storedOutput != 0.28 {
		t.Fatalf("updated pricing = %v/%v, want 0.14/0.28", storedInput, storedOutput)
	}

	if err := migrateFS(db, with019); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
