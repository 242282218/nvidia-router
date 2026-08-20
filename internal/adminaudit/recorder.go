package adminaudit

import (
	"context"
	"log/slog"
	"time"

	"nvidia-router/internal/adminauth"
)

// Recorder appends admin-audit entries and hides the repository behind a thin
// interface. Missing principal (unauthenticated requests) is recorded with a
// nil session ID so auth-failure attempts still leave a trail.
type Recorder struct {
	repository interface {
		Insert(context.Context, Entry) (int64, error)
	}
	logger *slog.Logger
}

func NewRecorder(repository interface {
	Insert(context.Context, Entry) (int64, error)
}, logger *slog.Logger) *Recorder {
	return &Recorder{repository: repository, logger: logger}
}

// Record writes an audit entry. detail is a secrets-free map; it is optional.
// Insert failures are logged and swallowed — the admin action has already
// happened and must not be rolled back because its audit write failed.
func (r *Recorder) Record(ctx context.Context, action, targetType, targetID string, detail map[string]any) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	entry := Entry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     MarshalDetail(detail),
		// Insert writes this column explicitly, so leaving it zero stamps every
		// row with the zero time and the audit trail loses its ordering.
		CreatedAt: time.Now().UTC(),
	}
	if principal, ok := adminauth.PrincipalFromContext(ctx); ok {
		entry.SessionID = &principal.SessionID
		entry.ClientIP = principal.ClientIP
	}
	if _, err := r.repository.Insert(writeCtx, entry); err != nil {
		r.logger.Error("admin audit insert failed", "action", action, "target_type", targetType, "error", err)
	}
}
