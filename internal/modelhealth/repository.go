package modelhealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Repository struct {
	db     *sql.DB
	reader *sql.DB
}

type SummarySnapshot struct {
	Events   []ProbeEvent
	Latest   map[int64]Latest
	Settings Settings
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) WithReader(reader *sql.DB) *Repository {
	r.reader = reader
	return r
}

func (r *Repository) read() *sql.DB {
	if r.reader != nil {
		return r.reader
	}
	return r.db
}

func (r *Repository) LoadSettings(ctx context.Context) (Settings, error) {
	return loadSettings(ctx, r.read())
}

func loadSettings(ctx context.Context, query queryer) (Settings, error) {
	var settings Settings
	var enabled int
	var updatedAt string
	err := query.QueryRowContext(ctx, `
		SELECT enabled, interval_seconds, concurrency, updated_at
		FROM model_health_settings WHERE id = 1
	`).Scan(&enabled, &settings.IntervalSeconds, &settings.Concurrency, &updatedAt)
	if err != nil {
		return Settings{}, fmt.Errorf("load model health settings: %w", err)
	}
	settings.Enabled = enabled != 0
	settings.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return Settings{}, fmt.Errorf("parse model health settings timestamp: %w", err)
	}
	return settings, nil
}

func (r *Repository) PatchSettings(ctx context.Context, patch SettingsPatch, now time.Time) (Settings, error) {
	if now.IsZero() {
		return Settings{}, errors.New("model health settings time is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, fmt.Errorf("begin model health settings transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	if patch.Enabled != nil {
		settings.Enabled = *patch.Enabled
	}
	if patch.IntervalSeconds != nil {
		settings.IntervalSeconds = *patch.IntervalSeconds
	}
	if patch.Concurrency != nil {
		settings.Concurrency = *patch.Concurrency
	}
	if err := ValidateSettings(settings); err != nil {
		return Settings{}, err
	}
	now = now.UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE model_health_settings
		SET enabled = ?, interval_seconds = ?, concurrency = ?, updated_at = ?
		WHERE id = 1
	`, boolInt(settings.Enabled), settings.IntervalSeconds, settings.Concurrency, formatTimestamp(now)); err != nil {
		return Settings{}, fmt.Errorf("save model health settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, fmt.Errorf("commit model health settings: %w", err)
	}
	committed = true
	settings.UpdatedAt = now
	return settings, nil
}

func (r *Repository) SaveSettings(ctx context.Context, settings Settings, now time.Time) (Settings, error) {
	if err := ValidateSettings(settings); err != nil {
		return Settings{}, err
	}
	if now.IsZero() {
		return Settings{}, errors.New("model health settings time is required")
	}
	now = now.UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE model_health_settings
		SET enabled = ?, interval_seconds = ?, concurrency = ?, updated_at = ?
		WHERE id = 1
	`, boolInt(settings.Enabled), settings.IntervalSeconds, settings.Concurrency, formatTimestamp(now))
	if err != nil {
		return Settings{}, fmt.Errorf("save model health settings: %w", err)
	}
	settings.UpdatedAt = now
	return settings, nil
}

func (r *Repository) Record(ctx context.Context, event ProbeEvent) error {
	if event.ModelID <= 0 {
		return errors.New("model health event model_id must be positive")
	}
	if !validOutcome(event.Outcome) {
		return fmt.Errorf("model health event outcome %q is invalid", event.Outcome)
	}
	if event.DurationMS < 0 {
		return errors.New("model health event duration_ms must be non-negative")
	}
	if event.CreatedAt.IsZero() {
		return errors.New("model health event created_at is required")
	}

	createdAt := formatTimestamp(event.CreatedAt)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin model health event transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO model_health_probes (model_id, outcome, duration_ms, error_code, created_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?)
	`, event.ModelID, event.Outcome, event.DurationMS, event.ErrorCode, createdAt)
	if err != nil {
		return fmt.Errorf("insert model health event: %w", err)
	}
	if id, err := result.LastInsertId(); err == nil {
		event.ID = id
	}

	consecutiveFailures, err := r.nextConsecutiveFailures(ctx, tx, event.ModelID, event.Outcome)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO model_health_latest (
			model_id, outcome, duration_ms, error_code, last_probe_at, consecutive_failures
		) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?)
		ON CONFLICT(model_id) DO UPDATE SET
			outcome = excluded.outcome,
			duration_ms = excluded.duration_ms,
			error_code = excluded.error_code,
			last_probe_at = excluded.last_probe_at,
			consecutive_failures = excluded.consecutive_failures
	`, event.ModelID, event.Outcome, event.DurationMS, event.ErrorCode, createdAt, consecutiveFailures)
	if err != nil {
		return fmt.Errorf("update latest model health state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit model health event: %w", err)
	}
	committed = true
	return nil
}

func (r *Repository) nextConsecutiveFailures(ctx context.Context, tx *sql.Tx, modelID int64, outcome string) (int, error) {
	if outcome == OutcomeSuccess || outcome == OutcomeSkipped || outcome == OutcomeCanceled {
		return 0, nil
	}
	var previous int
	err := tx.QueryRowContext(ctx, `
		SELECT consecutive_failures FROM model_health_latest WHERE model_id = ?
	`, modelID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load consecutive model health failures: %w", err)
	}
	return previous + 1, nil
}

// probeRetention is how long individual probe rows are kept. The widest view
// the summary can ask for is 7 days, so anything older is unreadable weight on
// the same SQLite file the request path writes through. Double the window
// leaves room for a report that straddles the boundary.
const probeRetention = 14 * 24 * time.Hour

// DeleteProbesBefore prunes probe history. Without it the table grows without
// bound: at the 10s minimum interval and a catalog of 50 models it adds more
// than 400k rows a day, none of which can ever be read back.
func (r *Repository) DeleteProbesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM model_health_probes WHERE created_at < ?`, formatTimestamp(cutoff))
	if err != nil {
		return 0, fmt.Errorf("delete model health probes: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted model health probes: %w", err)
	}
	return deleted, nil
}

func (r *Repository) ListEvents(ctx context.Context, from, to time.Time) ([]ProbeEvent, error) {
	return listEvents(ctx, r.read(), from, to)
}

func listEvents(ctx context.Context, query queryer, from, to time.Time) ([]ProbeEvent, error) {
	if !to.After(from) {
		return []ProbeEvent{}, nil
	}
	rows, err := query.QueryContext(ctx, `
		SELECT id, model_id, outcome, duration_ms, COALESCE(error_code, ''), created_at
		FROM model_health_probes
		WHERE created_at >= ? AND created_at < ?
		ORDER BY created_at, id
	`, formatTimestamp(from), formatTimestamp(to))
	if err != nil {
		return nil, fmt.Errorf("list model health events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events := make([]ProbeEvent, 0)
	for rows.Next() {
		var event ProbeEvent
		var createdAt string
		if err := rows.Scan(&event.ID, &event.ModelID, &event.Outcome, &event.DurationMS, &event.ErrorCode, &createdAt); err != nil {
			return nil, fmt.Errorf("scan model health event: %w", err)
		}
		event.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse model health event timestamp: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model health events: %w", err)
	}
	return events, nil
}

func (r *Repository) ListLatest(ctx context.Context) (map[int64]Latest, error) {
	return listLatest(ctx, r.read())
}

func listLatest(ctx context.Context, query queryer) (map[int64]Latest, error) {
	rows, err := query.QueryContext(ctx, `
		SELECT model_id, outcome, duration_ms, COALESCE(error_code, ''), last_probe_at, consecutive_failures
		FROM model_health_latest
	`)
	if err != nil {
		return nil, fmt.Errorf("list latest model health states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	latest := make(map[int64]Latest)
	for rows.Next() {
		var modelID int64
		var item Latest
		var lastProbeAt string
		if err := rows.Scan(&modelID, &item.Outcome, &item.DurationMS, &item.ErrorCode, &lastProbeAt, &item.ConsecutiveFailures); err != nil {
			return nil, fmt.Errorf("scan latest model health state: %w", err)
		}
		item.LastProbeAt, err = parseTimestamp(lastProbeAt)
		if err != nil {
			return nil, fmt.Errorf("parse latest model health timestamp: %w", err)
		}
		latest[modelID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest model health states: %w", err)
	}
	return latest, nil
}

func (r *Repository) SummarySnapshot(ctx context.Context, from, to time.Time) (SummarySnapshot, error) {
	tx, err := r.read().BeginTx(ctx, nil)
	if err != nil {
		return SummarySnapshot{}, fmt.Errorf("begin model health summary snapshot: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	events, err := listEvents(ctx, tx, from, to)
	if err != nil {
		return SummarySnapshot{}, err
	}
	latest, err := listLatest(ctx, tx)
	if err != nil {
		return SummarySnapshot{}, err
	}
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return SummarySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return SummarySnapshot{}, fmt.Errorf("commit model health summary snapshot: %w", err)
	}
	committed = true
	return SummarySnapshot{Events: events, Latest: latest, Settings: settings}, nil
}

func validOutcome(outcome string) bool {
	switch outcome {
	case OutcomeSuccess, OutcomeFailure, OutcomeTimeout, OutcomeSkipped, OutcomeCanceled:
		return true
	default:
		return false
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
