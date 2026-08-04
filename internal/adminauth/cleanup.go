package adminauth

import (
	"context"
	"log/slog"
	"time"

	"nvidia-router/internal/clock"
)

type sessionCleanupRepository interface {
	DeleteExpiredOrRevoked(context.Context, time.Time) (int64, error)
}

type SessionCleanupWorker struct {
	repository sessionCleanupRepository
	now        func() time.Time
	wait       func(context.Context, time.Duration) bool
	logger     *slog.Logger
}

const sessionCleanupRetryInterval = time.Minute

func NewSessionCleanupWorker(repository sessionCleanupRepository, source clock.Clock, logger *slog.Logger) *SessionCleanupWorker {
	if source == nil {
		source = clock.RealClock{}
	}
	return newSessionCleanupWorker(repository, source.Now, func(ctx context.Context, duration time.Duration) bool {
		timer := source.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}, logger)
}

func newSessionCleanupWorker(
	repository sessionCleanupRepository,
	now func() time.Time,
	wait func(context.Context, time.Duration) bool,
	logger *slog.Logger,
) *SessionCleanupWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionCleanupWorker{repository: repository, now: now, wait: wait, logger: logger}
}

func (w *SessionCleanupWorker) Run(ctx context.Context) {
	for {
		if !w.cleanup(ctx) {
			if !w.wait(ctx, sessionCleanupRetryInterval) {
				return
			}
			continue
		}
		now := w.now().UTC()
		if !w.wait(ctx, nextSessionCleanupAt(now).Sub(now)) {
			return
		}
	}
}

func (w *SessionCleanupWorker) cleanup(ctx context.Context) bool {
	deleted, err := w.repository.DeleteExpiredOrRevoked(ctx, w.now().UTC())
	if err != nil {
		w.logger.Error("admin session cleanup failed", "error", err)
		return false
	}
	w.logger.Info("admin session cleanup completed", "deleted", deleted)
	return true
}

// nextSessionCleanupAt schedules the session sweep 30 minutes after the
// request_logs sweep (observability.CleanupWorker runs at 03:00 UTC). Both
// cleanups issue large DELETEs on the single shared SQLite connection, so the
// stagger keeps the two retention windows from stalling business traffic at
// the same instant.
func nextSessionCleanupAt(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 30, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
