package adminaudit

import (
	"context"
	"log/slog"

	"nvidia-router/internal/adminauth"
)

// Recorder appends admin-audit entries and hides the repository behind a thin
// interface. Missing principal (unauthenticated requests) is recorded with a
// nil session ID so auth-failure attempts still leave a trail.
type Recorder struct {
	repository *Repository
	logger     *slog.Logger
}

func NewRecorder(repository *Repository, logger *slog.Logger) *Recorder {
	return &Recorder{repository: repository, logger: logger}
}

// Record writes an audit entry. detail is a secrets-free map; it is optional.
// Insert failures are logged and swallowed — the admin action has already
// happened and must not be rolled back because its audit write failed.
func (r *Recorder) Record(ctx context.Context, action, targetType, targetID string, detail map[string]any) {
	entry := Entry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     MarshalDetail(detail),
	}
	if principal, ok := adminauth.PrincipalFromContext(ctx); ok {
		entry.SessionID = &principal.SessionID
		entry.ClientIP = principal.ClientIP
	}
	if _, err := r.repository.Insert(ctx, entry); err != nil {
		r.logger.Error("admin audit insert failed", "action", action, "target_type", targetType, "error", err)
	}
}
