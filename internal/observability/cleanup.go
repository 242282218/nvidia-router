package observability

import (
	"context"
	"log/slog"
	"time"

	"nvidia-router/internal/clock"
)

const requestLogRetentionDays = 30

type cleanupRepository interface {
	DeleteRequestLogsBefore(context.Context, time.Time) (int64, error)
}

type CleanupWorker struct {
	repository cleanupRepository
	now        func() time.Time
	wait       func(context.Context, time.Duration) bool
	logger     *slog.Logger
}

func NewCleanupWorker(repository cleanupRepository, source clock.Clock, logger *slog.Logger) *CleanupWorker {
	if source == nil {
		source = clock.RealClock{}
	}
	return newCleanupWorker(repository, source.Now, func(ctx context.Context, duration time.Duration) bool {
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

func newCleanupWorker(
	repository cleanupRepository,
	now func() time.Time,
	wait func(context.Context, time.Duration) bool,
	logger *slog.Logger,
) *CleanupWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &CleanupWorker{repository: repository, now: now, wait: wait, logger: logger}
}

func (w *CleanupWorker) Run(ctx context.Context) {
	w.cleanup(ctx)
	for {
		now := w.now().UTC()
		if !w.wait(ctx, nextCleanupAt(now).Sub(now)) {
			return
		}
		w.cleanup(ctx)
	}
}

func (w *CleanupWorker) cleanup(ctx context.Context) {
	now := w.now().UTC()
	cutoff := now.AddDate(0, 0, -requestLogRetentionDays)
	deleted, err := w.repository.DeleteRequestLogsBefore(ctx, cutoff)
	if err != nil {
		w.logger.Error("request metadata cleanup failed", "error", err)
		return
	}
	w.logger.Info("request metadata cleanup completed", "deleted", deleted)
}

func nextCleanupAt(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
