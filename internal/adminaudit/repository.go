package adminaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type Repository struct {
	db     *sql.DB
	reader *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// WithReader routes audit queries to a separate connection pool so long
// history scans never contend with the single SQLite writer.
func (r *Repository) WithReader(reader *sql.DB) *Repository {
	clone := *r
	clone.reader = reader
	return &clone
}

func (r *Repository) read() *sql.DB {
	if r.reader != nil {
		return r.reader
	}
	return r.db
}

// Insert appends an audit entry. Writes go through the writer connection; audit
// is append-only and low-volume, so a direct synchronous insert is fine.
func (r *Repository) Insert(ctx context.Context, entry Entry) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO admin_audit_logs (action, target_type, target_id, detail, session_id, client_ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, entry.Action, entry.TargetType, entry.TargetID, entry.Detail, entry.SessionID, entry.ClientIP, entry.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("insert admin audit entry: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read admin audit entry id: %w", err)
	}
	return id, nil
}

// ListQuery describes a paginated read of the audit trail.
type ListQuery struct {
	Limit  int
	Offset int
	Action string // optional exact-match filter
}

type Page struct {
	Items   []Entry
	Total   int
	HasMore bool
	Next    *int
}

// List returns audit entries newest-first within an optional action filter.
func (r *Repository) List(ctx context.Context, query ListQuery) (Page, error) {
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 1000 {
		query.Limit = 1000
	}
	where := ""
	args := []any{}
	if query.Action != "" {
		where = "WHERE action = ?"
		args = append(args, query.Action)
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM admin_audit_logs " + where
	if err := r.read().QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("count admin audit entries: %w", err)
	}

	selectQuery := `
		SELECT id, action, target_type, target_id, detail, session_id, client_ip, created_at
		FROM admin_audit_logs ` + where + `
		ORDER BY id DESC
		LIMIT ? OFFSET ?`
	selectArgs := append(append([]any{}, args...), query.Limit+1, query.Offset)
	rows, err := r.read().QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return Page{}, fmt.Errorf("list admin audit entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]Entry, 0, query.Limit)
	for rows.Next() {
		var entry Entry
		var created string
		var sessionID sql.NullString
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.TargetType, &entry.TargetID, &entry.Detail, &sessionID, &entry.ClientIP, &created); err != nil {
			return Page{}, fmt.Errorf("scan admin audit entry: %w", err)
		}
		if sessionID.Valid {
			id := sessionID.String // copy out of the reused scan slot
			entry.SessionID = &id
		}
		parsed, err := time.Parse(time.RFC3339, created)
		if err != nil {
			return Page{}, fmt.Errorf("parse admin audit created_at: %w", err)
		}
		entry.CreatedAt = parsed
		items = append(items, entry)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate admin audit entries: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	page := Page{Items: items, Total: total, HasMore: hasMore}
	if hasMore {
		next := query.Offset + len(items)
		page.Next = &next
	}
	return page, nil
}

// MarshalDetail encodes a secrets-free detail payload into the Entry.Detail
// field; a nil/empty payload encodes to an empty string (not "null").
func MarshalDetail(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}
