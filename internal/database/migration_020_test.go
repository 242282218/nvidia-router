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

func TestMigration020AddsLatencyAndCacheSettings(t *testing.T) {
	priorFiles := []string{
		"001_initial.sql", "002_indexes.sql", "003_xk_proxy_settings.sql",
		"004_observability_indexes.sql", "005_runtime_failover_and_retention.sql",
		"006_proxy_pool_settings.sql", "007_monitoring_retention.sql", "008_retry_budget.sql",
		"009_access_key_limits.sql", "010_master_key_versions.sql",
		"011_streaming_quota.sql", "012_drop_xk_proxy_settings.sql", "013_first_token_ms.sql",
		"014_stream_timeouts.sql", "015_deepseek_v4_flash_alias.sql", "016_model_stream_timeouts.sql",
		"017_admin_audit_logs.sql", "018_access_key_token_budget.sql", "019_model_pricing.sql",
	}
	prior := make(fstest.MapFS, len(priorFiles))
	for _, name := range priorFiles {
		contents, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		prior["migrations/"+name] = &fstest.MapFile{Data: contents}
	}
	contents, err := fs.ReadFile(embeddedMigrations, "migrations/020_latency_routing_and_embedding_cache.sql")
	if err != nil {
		t.Fatalf("read migration 020: %v", err)
	}
	with020 := make(fstest.MapFS, len(prior)+1)
	for name, file := range prior {
		with020[name] = file
	}
	with020["migrations/020_latency_routing_and_embedding_cache.sql"] = &fstest.MapFile{Data: contents}

	dsn := "file:" + url.PathEscape(filepath.Join(t.TempDir(), "router.db")) + "?_timefmt=rfc3339&_txlock=immediate"
	db, err := driver.Open(dsn, configureConnection)
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateFS(db, prior); err != nil {
		t.Fatalf("migrate pre-020: %v", err)
	}
	if err := migrateFS(db, with020); err != nil {
		t.Fatalf("migrate 020: %v", err)
	}

	// Defaults: latency routing on, embedding cache on with a bounded entry cap.
	var latency, cacheEnabled, cacheEntries int
	if err := db.QueryRow(`SELECT latency_routing_enabled, embedding_cache_enabled, embedding_cache_max_entries FROM runtime_settings WHERE id = 1`).Scan(&latency, &cacheEnabled, &cacheEntries); err != nil {
		t.Fatalf("read migrated settings: %v", err)
	}
	if latency != 1 || cacheEnabled != 1 || cacheEntries != 256 {
		t.Fatalf("migrated defaults = %d/%d/%d, want 1/1/256", latency, cacheEnabled, cacheEntries)
	}

	if _, err := db.Exec(`UPDATE runtime_settings SET latency_routing_enabled = 0, embedding_cache_max_entries = 512 WHERE id = 1`); err != nil {
		t.Fatalf("update migrated settings: %v", err)
	}
	if err := migrateFS(db, with020); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
