package nvidiakey

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/keystate"
)

const (
	// DefaultHealthCheckInterval bounds how often the health checker sweeps
	// unhealthy (non-auth-invalid) keys. 10 minutes keeps per-key probe volume
	// around 144 calls/day — well within NVIDIA's free /v1/models quota budget.
	DefaultHealthCheckInterval = 10 * time.Minute
	// DefaultHealthCheckConcurrency caps the parallel probe fan-out. SQLite's
	// single writer prefers modest concurrency so background probes don't
	// contend with markSuccess/markFailure on the request path.
	DefaultHealthCheckConcurrency = 4
)

// ProbeResult is the outcome of a single health probe. It deliberately exposes
// only a boolean and a category so the checker never re-implements validation
// policy — the live Service owns that decision.
type ProbeResult struct {
	// Recovered is true when the probe found the key healthy and any cooldown
	// recorded on the key should be cleared via MarkSuccess.
	Recovered bool
	// Category is a stable string for logs/metrics: "valid", "invalid",
	// "temporarily_unavailable", "indeterminate", "skipped".
	Category string
	// Reason carries extra detail for logs (e.g. proxy unavailable). Not
	// persisted to avoid leaking upstream error text.
	Reason string
}

// HealthChecker periodically probes NVIDIA keys whose scheduling state shows
// recent failure history and recovers any that the validator now reports
// healthy. It moves the "is this key usable?" decision off user requests —
// without it, a key that recovered while in cooldown stays in the
// recover→first request hits 401→cooldown cycle, burning user traffic.
//
// It does NOT probe auth_invalid keys. The design's divergence from gpt-load's
// CronChecker is intentional: those keys are an operator intervention point
// and must not auto-recover without a human Test, otherwise a permanently
// revoked key would spin forever against the free probe budget.
type HealthChecker struct {
	repository healthRepository
	probe      func(ctx context.Context, keyID int64) ProbeResult
	writer     probeStateWriter
	// sync mirrors a recovered key into in-memory scheduling state
	// (pool.ApplySuccess). Without it the DB cooldown is cleared but the pool
	// keeps treating the key as cooling down until the next restart.
	sync   func(keyID int64)
	clock  clock.Clock
	logger *slog.Logger

	interval    time.Duration
	concurrency int
	// waitFn overrides the default RealClock-based wait. Tests inject it to
	// drive deterministic sweep scheduling; production leaves it nil and Run
	// falls back to c.wait (clock.NewTimer based).
	waitFn func(context.Context, time.Duration) bool
}

type healthRepository interface {
	ListKeysForHealthCheck(context.Context) ([]keystate.KeySnapshot, error)
}

type probeStateWriter interface {
	MarkSuccess(ctx context.Context, keyID int64) (keystate.KeySnapshot, error)
}

// HealthCheckerOptions configures the checker. Probe wires the live Service
// probe (Service.ProbeHealth); tests substitute a closure. MarkSuccess wires
// the same Service; it's another field rather than a direct call so tests can
// assert which keys got recovered independently of probe results.
type HealthCheckerOptions struct {
	Interval    time.Duration
	Concurrency int
	// Wait overrides the default clock-based wait between sweeps. Tests inject
	// it for deterministic scheduling; production leaves it nil.
	Wait   func(context.Context, time.Duration) bool
	Logger *slog.Logger
}

// NewHealthChecker builds a checker. Probe and MarkSuccess are wired later via
// WireProbe/WireWriter so the constructor stays decoupled from the concrete
// Service (avoiding an import cycle where Service already references the same
// validator).
func NewHealthChecker(repository healthRepository, source clock.Clock, options HealthCheckerOptions) *HealthChecker {
	if source == nil {
		source = clock.RealClock{}
	}
	interval := options.Interval
	if interval <= 0 {
		interval = DefaultHealthCheckInterval
	}
	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultHealthCheckConcurrency
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthChecker{
		repository:  repository,
		clock:       source,
		logger:      logger,
		interval:    interval,
		concurrency: concurrency,
		waitFn:      options.Wait,
	}
}

// WireProbe injects the live probe callback. Splitting setter from constructor
// keeps NewHealthChecker usable in tests that wire a stub instead.
func (c *HealthChecker) WireProbe(probe func(ctx context.Context, keyID int64) ProbeResult) {
	c.probe = probe
}

// WireWriter injects the writer used to clear cooldowns on recovery.
func (c *HealthChecker) WireWriter(writer probeStateWriter) {
	c.writer = writer
}

// WireSync injects a callback that mirrors a recovered key into in-memory
// scheduling state. Production wires pool.ApplySuccess; tests substitute a
// capturing closure. Optional: a nil callback means DB-only recovery.
func (c *HealthChecker) WireSync(sync func(keyID int64)) {
	c.sync = sync
}

// Run sweeps unhealthy keys on the configured interval until ctx is cancelled.
// Wait is honoured for test instrumentation; production uses clock.NewTimer.
func (c *HealthChecker) Run(ctx context.Context) {
	wait := c.wait
	if c.waitFn != nil {
		wait = c.waitFn
	}
	for {
		if !wait(ctx, c.interval) {
			return
		}
		_ = c.Sweep(ctx)
	}
}

func (c *HealthChecker) wait(ctx context.Context, duration time.Duration) bool {
	timer := c.clock.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Sweep performs one probe pass over unhealthy non-auth-invalid keys. It is
// exported so tests can trigger a sweep deterministically without waiting on
// the timer. Returns any error from listing candidates; probe-level errors are
// logged but do not abort the pass (one stuck key shouldn't gate the others).
func (c *HealthChecker) Sweep(ctx context.Context) error {
	if c.probe == nil || c.writer == nil {
		// Nothing wired (test misconfiguration or production forgot Wire*).
		// Fall through to no-op rather than panic.
		c.logger.Warn("health checker sweep skipped: probe or writer not wired")
		return nil
	}
	candidates, err := c.repository.ListKeysForHealthCheck(ctx)
	if err != nil {
		c.logger.Error("health check list candidates failed", "error", err)
		return fmt.Errorf("list health-check candidates: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}

	// Bounded fan-out: workers pull the next snapshot off the channel and
	// probe it. Sizing work to len(candidates) and closing up front means
	// workers exit naturally once they drain the channel.
	work := make(chan keystate.KeySnapshot, len(candidates))
	for _, candidate := range candidates {
		work <- candidate
	}
	close(work)

	workers := c.concurrency
	if workers > len(candidates) {
		workers = len(candidates)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for candidate := range work {
				c.probeOne(ctx, candidate)
			}
		}()
	}
	wg.Wait()
	return nil
}

func (c *HealthChecker) probeOne(ctx context.Context, candidate keystate.KeySnapshot) {
	result := c.probe(ctx, candidate.ID)
	if !result.Recovered {
		if c.logger.Enabled(ctx, slog.LevelDebug) {
			c.logger.Debug("health check probe not recovered", "key_id", candidate.ID, "category", result.Category, "reason", result.Reason)
		}
		return
	}
	if _, err := c.writer.MarkSuccess(ctx, candidate.ID); err != nil {
		c.logger.Error("health check mark success failed", "key_id", candidate.ID, "error", err)
		return
	}
	if c.sync != nil {
		c.sync(candidate.ID)
	}
	c.logger.Info("health check recovered key", "key_id", candidate.ID, "previous_cooldown_level", candidate.CooldownLevel)
}
