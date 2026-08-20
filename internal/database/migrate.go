package database

import (
	"context"
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
	"sync"
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

// verifyMigrationsCache memoizes the embedded migration ledger: the embed FS is
// immutable for the process lifetime, but /health/ready re-verifies migrations
// on every probe, so re-reading and re-hashing the migration files per probe is
// wasted work.
var (
	verifyMigrationsCacheOnce sync.Once
	verifyMigrationsCacheList []migration
	verifyMigrationsCacheErr  error
)

func embeddedMigrationList() ([]migration, error) {
	verifyMigrationsCacheOnce.Do(func() {
		verifyMigrationsCacheList, verifyMigrationsCacheErr = loadMigrations(embeddedMigrations)
	})
	return verifyMigrationsCacheList, verifyMigrationsCacheErr
}

func Migrate(db *sql.DB) error {
	if err := migrateFS(db, embeddedMigrations); err != nil {
		return fmt.Errorf("apply embedded SQLite migrations: %w", err)
	}
	return nil
}

// VerifyMigrations checks that the database exactly matches the embedded migration ledger.
func VerifyMigrations(ctx context.Context, db *sql.DB) error {
	migrations, err := embeddedMigrationList()
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	recorded, err := readMigrationLedger(ctx, db)
	if err != nil {
		return fmt.Errorf("read migration ledger: %w", err)
	}
	if err := validateMigrationVersions(recorded, migrations); err != nil {
		return fmt.Errorf("verify migration ledger: %w", err)
	}
	if len(recorded) != len(migrations) {
		return fmt.Errorf("verify migration ledger: expected %d migrations, found %d", len(migrations), len(recorded))
	}
	for _, item := range migrations {
		if recorded[item.version] != item.checksum {
			return fmt.Errorf("verify migration %d checksum: mismatch", item.version)
		}
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
	recorded, err := readMigrationLedger(context.Background(), db)
	if err != nil {
		return fmt.Errorf("read migration ledger before apply: %w", err)
	}
	if err := validateMigrationVersions(recorded, migrations); err != nil {
		return fmt.Errorf("validate migration ledger before apply: %w", err)
	}
	for _, item := range migrations {
		if err := applyMigration(db, item); err != nil {
			return fmt.Errorf("apply migration version %d (%s): %w", item.version, item.name, err)
		}
	}
	return nil
}

func readMigrationLedger(ctx context.Context, db *sql.DB) (map[int]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	recorded := make(map[int]string)
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan migration ledger: %w", err)
		}
		recorded[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration ledger: %w", err)
	}
	return recorded, nil
}

func validateMigrationVersions(recorded map[int]string, migrations []migration) error {
	known := make(map[int]struct{}, len(migrations))
	for _, item := range migrations {
		known[item.version] = struct{}{}
	}
	unknown := make([]int, 0)
	for version := range recorded {
		if _, ok := known[version]; !ok {
			unknown = append(unknown, version)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Ints(unknown)
	return fmt.Errorf("unknown migration version %d: database records a migration not present in embedded migrations", unknown[0])
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
