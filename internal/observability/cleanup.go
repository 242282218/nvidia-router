package observability

import (
	"context"
	"log/slog"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/runtimeconfig"
)

// DefaultRequestLogRetentionDays is the fallback when the runtime settings
// snapshot does not carry an operator-tuned value (audit B5: previously this
// was a magic 30 baked straight into cleanup). Operators who want a different
// value set request_log_retention_days through the admin settings surface.
const DefaultRequestLogRetentionDays = 30

type cleanupRepository interface {
	DeleteRequestLogsBefore(context.Context, time.Time) (int64, error)
}

type cleanupSettingsProvider interface {
	Snapshot() runtimeconfig.Snapshot
}

type CleanupWorker struct {
	repository cleanupRepository
	settings   cleanupSettingsProvider
	now        func() time.Time
	wait       func(context.Context, time.Duration) bool
	logger     *slog.Logger
}

func NewCleanupWorker(repository cleanupRepository, source clock.Clock, logger *slog.Logger, settings cleanupSettingsProvider) *CleanupWorker {
	if source == nil {
		source = clock.RealClock{}
	}
	if settings == nil {
		// Tests that still pass nil keep the legacy constant-only behaviour so
		// they do not need to wire a settings provider to assert the cutoff.
		settings = noopCleanupSettings{}
	}
	return newCleanupWorker(repository, settings, source.Now, func(ctx context.Context, duration time.Duration) bool {
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
	settings cleanupSettingsProvider,
	now func() time.Time,
	wait func(context.Context, time.Duration) bool,
	logger *slog.Logger,
) *CleanupWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &CleanupWorker{repository: repository, settings: settings, now: now, wait: wait, logger: logger}
}

// Run schedules cleanups at the daily UTC 03:00 boundary. It never cleans up
// at startup: a full retention-window DELETE on the single shared connection
// would stall every request path, and stale logs below the cutoff are harmless
// until the first scheduled pass. Each cycle re-reads the snapshot so a runtime
// operator tuning (B5) takes effect on the next pass without a restart.
func (w *CleanupWorker) Run(ctx context.Context) {
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
	retentionDays := w.retentionDays()
	cutoff := now.AddDate(0, 0, -retentionDays)
	deleted, err := w.repository.DeleteRequestLogsBefore(ctx, cutoff)
	if err != nil {
		w.logger.Error("request metadata cleanup failed", "error", err)
		return
	}
	w.logger.Info("request metadata cleanup completed", "deleted", deleted, "retention_days", retentionDays)
}

// retentionDays reads the live runtime settings and falls back to the
// operational default for malformed/incomplete rows. The admin handler
// validates the persistence boundary (30..365), but a snapshot cloned before
// the migration landed — or a future database downgrade — could surface a
// below-floor value that would delete newer metadata if passed to AddDate.
func (w *CleanupWorker) retentionDays() int {
	if w.settings == nil {
		return DefaultRequestLogRetentionDays
	}
	value := w.settings.Snapshot().RequestLogRetentionDays
	if value < DefaultRequestLogRetentionDays || value > 365 {
		return DefaultRequestLogRetentionDays
	}
	return value
}

// noopCleanupSettings returns an empty snapshot so callers that opt out of
// wiring a real provider stay on the documented default retention.
type noopCleanupSettings struct{}

func (noopCleanupSettings) Snapshot() runtimeconfig.Snapshot { return runtimeconfig.Snapshot{} }

func nextCleanupAt(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
