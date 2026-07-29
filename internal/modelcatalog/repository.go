package modelcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nvidia-router/internal/keystate"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SaveSelections(ctx context.Context, selections []Selection, now time.Time) (returnErr error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin model selection transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			returnErr = fmt.Errorf("rollback model selection transaction: %w", errors.Join(returnErr, rollbackErr))
		}
	}()

	for _, selection := range selections {
		if err := saveSelection(ctx, tx, selection, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit model selection transaction: %w", err)
	}
	committed = true
	return nil
}

func saveSelection(ctx context.Context, tx *sql.Tx, selection Selection, now time.Time) error {
	verifiedAt := optionalTimestamp(selection.CapabilityVerifiedAt)
	timestamp := formatTimestamp(now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO models (
			public_id, upstream_id, display_name, kind, enabled,
			supports_vision, supports_tools, supports_reasoning,
			reasoning_wire_format, capability_verified_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(public_id) DO UPDATE SET
			upstream_id = excluded.upstream_id,
			display_name = excluded.display_name,
			kind = excluded.kind,
			enabled = excluded.enabled,
			supports_vision = excluded.supports_vision,
			supports_tools = excluded.supports_tools,
			supports_reasoning = excluded.supports_reasoning,
			reasoning_wire_format = excluded.reasoning_wire_format,
			capability_verified_at = excluded.capability_verified_at,
			updated_at = excluded.updated_at
	`, selection.PublicID, selection.UpstreamID, selection.DisplayName, selection.Kind, boolInt(selection.Enabled),
		boolInt(selection.SupportsVision), boolInt(selection.SupportsTools), boolInt(selection.SupportsReasoning),
		selection.ReasoningWireFormat, verifiedAt, timestamp, timestamp); err != nil {
		return fmt.Errorf("save model %q: %w", selection.PublicID, err)
	}
	return nil
}

func (r *Repository) ListEnabled(ctx context.Context) ([]Model, error) {
	rows, err := r.db.QueryContext(ctx, modelColumns+" WHERE enabled = 1 ORDER BY public_id")
	if err != nil {
		return nil, fmt.Errorf("list enabled models: %w", err)
	}
	defer rows.Close()
	models := make([]Model, 0)
	for rows.Next() {
		model, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled models: %w", err)
	}
	return models, nil
}

func (r *Repository) ResolveEnabled(ctx context.Context, publicID string) (Model, error) {
	model, err := scanModel(r.db.QueryRowContext(ctx, modelColumns+" WHERE public_id = ? AND enabled = 1", publicID))
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, ErrModelNotFound
	}
	if err != nil {
		return Model{}, fmt.Errorf("resolve enabled model: %w", err)
	}
	return model, nil
}

func (r *Repository) Get(ctx context.Context, id int64) (Model, error) {
	model, err := scanModel(r.db.QueryRowContext(ctx, modelColumns+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, ErrModelNotFound
	}
	if err != nil {
		return Model{}, fmt.Errorf("get model: %w", err)
	}
	return model, nil
}

func (r *Repository) SetEnabled(ctx context.Context, id int64, enabled bool, now time.Time) error {
	if _, err := r.db.ExecContext(ctx, "UPDATE models SET enabled = ?, updated_at = ? WHERE id = ?", boolInt(enabled), formatTimestamp(now), id); err != nil {
		return fmt.Errorf("set model enabled state: %w", err)
	}
	return nil
}

func (r *Repository) SetCapabilityVerified(ctx context.Context, id int64, verifiedAt *time.Time, disable bool, now time.Time) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE models
		SET capability_verified_at = ?,
		    enabled = CASE WHEN ? = 1 THEN 0 ELSE enabled END,
		    updated_at = ?
		WHERE id = ?
	`, optionalTimestamp(verifiedAt), boolInt(disable), formatTimestamp(now), id); err != nil {
		return fmt.Errorf("set model capability verification: %w", err)
	}
	return nil
}

func (r *Repository) DeleteModel(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, "DELETE FROM models WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	return nil
}

func (r *Repository) BlockKeyModel(ctx context.Context, keyID, modelID int64, reason string, upstreamStatus *int, now time.Time) error {
	timestamp := formatTimestamp(now)
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO nvidia_key_model_blocks (
			nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(nvidia_key_id, model_id) DO UPDATE SET
			reason_code = excluded.reason_code,
			upstream_status = excluded.upstream_status,
			last_seen_at = excluded.last_seen_at
	`, keyID, modelID, reason, upstreamStatus, timestamp, timestamp); err != nil {
		return fmt.Errorf("block NVIDIA key for model: %w", err)
	}
	return nil
}

func (r *Repository) UnblockKeyModel(ctx context.Context, keyID, modelID int64) error {
	if _, err := r.db.ExecContext(ctx, "DELETE FROM nvidia_key_model_blocks WHERE nvidia_key_id = ? AND model_id = ?", keyID, modelID); err != nil {
		return fmt.Errorf("unblock NVIDIA key for model: %w", err)
	}
	return nil
}

func (r *Repository) ListBlocks(ctx context.Context) ([]keystate.ModelBlock, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT nvidia_key_id, model_id FROM nvidia_key_model_blocks ORDER BY nvidia_key_id, model_id")
	if err != nil {
		return nil, fmt.Errorf("list NVIDIA key model blocks: %w", err)
	}
	defer rows.Close()
	blocks := make([]keystate.ModelBlock, 0)
	for rows.Next() {
		var block keystate.ModelBlock
		if err := rows.Scan(&block.KeyID, &block.ModelID); err != nil {
			return nil, fmt.Errorf("scan NVIDIA key model block: %w", err)
		}
		blocks = append(blocks, block)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate NVIDIA key model blocks: %w", err)
	}
	return blocks, nil
}

const modelColumns = `SELECT id, public_id, upstream_id, display_name, kind, enabled,
	supports_vision, supports_tools, supports_reasoning, reasoning_wire_format,
	capability_verified_at FROM models`

type rowScanner interface{ Scan(dest ...any) error }

func scanModel(row rowScanner) (Model, error) {
	var model Model
	var enabled, vision, tools, reasoning int
	var verifiedAt sql.NullString
	if err := row.Scan(&model.ID, &model.PublicID, &model.UpstreamID, &model.DisplayName, &model.Kind,
		&enabled, &vision, &tools, &reasoning, &model.ReasoningWireFormat, &verifiedAt); err != nil {
		return Model{}, err
	}
	model.Enabled = enabled == 1
	model.SupportsVision = vision == 1
	model.SupportsTools = tools == 1
	model.SupportsReasoning = reasoning == 1
	if verifiedAt.Valid {
		parsed, err := time.Parse(time.RFC3339, verifiedAt.String)
		if err != nil {
			return Model{}, fmt.Errorf("parse model capability verification time: %w", err)
		}
		model.CapabilityVerifiedAt = &parsed
	}
	return model, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func optionalTimestamp(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTimestamp(*value)
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}
