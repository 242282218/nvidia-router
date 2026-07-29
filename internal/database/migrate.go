package database

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

const migrationBootstrap = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL
);`

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

type migration struct {
	version  int
	name     string
	contents string
	checksum string
}

func Migrate(db *sql.DB) error {
	if err := migrateFS(db, embeddedMigrations); err != nil {
		return fmt.Errorf("apply embedded SQLite migrations: %w", err)
	}
	return nil
}

func migrateFS(db *sql.DB, migrationFS fs.FS) error {
	if _, err := db.Exec(migrationBootstrap); err != nil {
		return fmt.Errorf("bootstrap migration ledger: %w", err)
	}

	migrations, err := loadMigrations(migrationFS)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	for _, item := range migrations {
		if err := applyMigration(db, item); err != nil {
			return fmt.Errorf("apply migration version %d (%s): %w", item.version, item.name, err)
		}
	}
	return nil
}

func loadMigrations(migrationFS fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	versions := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}

		version, err := migrationVersion(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("parse migration %q: %w", entry.Name(), err)
		}
		if existing, exists := versions[version]; exists {
			return nil, fmt.Errorf("validate migration %q: %w", entry.Name(), fmt.Errorf("version %d duplicates %q", version, existing))
		}
		versions[version] = entry.Name()

		contents, err := fs.ReadFile(migrationFS, path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		migrations = append(migrations, migration{
			version:  version,
			name:     entry.Name(),
			contents: string(contents),
			checksum: hex.EncodeToString(digest[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	return migrations, nil
}

func migrationVersion(name string) (int, error) {
	prefix, _, found := strings.Cut(name, "_")
	if !found {
		return 0, errors.New("filename must start with a numeric version followed by an underscore")
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("parse version prefix: %w", err)
	}
	if version <= 0 {
		return 0, errors.New("version must be positive")
	}
	return version, nil
}

func applyMigration(db *sql.DB, item migration) (returnErr error) {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			returnErr = fmt.Errorf("rollback migration transaction: %w", errors.Join(returnErr, rollbackErr))
		}
	}()

	var recordedChecksum string
	err = tx.QueryRow("SELECT checksum FROM schema_migrations WHERE version = ?", item.version).Scan(&recordedChecksum)
	switch {
	case err == nil:
		if recordedChecksum != item.checksum {
			return fmt.Errorf("verify migration checksum: %w", fmt.Errorf("recorded %s, current %s", recordedChecksum, item.checksum))
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit checksum verification: %w", err)
		}
		committed = true
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read migration ledger: %w", err)
	}

	if _, err := tx.Exec(item.contents); err != nil {
		return fmt.Errorf("execute migration SQL: %w", err)
	}
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, checksum, applied_at) VALUES (?, ?, CURRENT_TIMESTAMP)",
		item.version,
		item.checksum,
	); err != nil {
		return fmt.Errorf("record migration checksum: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	committed = true
	return nil
}
