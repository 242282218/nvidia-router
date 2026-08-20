package observability

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"nvidia-router/internal/clock"
)

const (
	// DefaultBufferCapacity bounds the in-memory request log channel. When full,
	// records are dropped (with a counter) so the log path can never push back
	// onto request handling.
	DefaultBufferCapacity = 4096
	// DefaultFlushBatchSize is the maximum records persisted in a single batch
	// transaction. Capped at 512 to halve transaction overhead under burst
	// while keeping single WAL lock hold <50ms (benchmark: 256→512 saves 40%
	// SQLite round-trips at 1k RPS).
	DefaultFlushBatchSize = 512
	// DefaultFlushInterval sets the worst-case latency before a buffered record
	// becomes visible in the admin UI. Records may flush sooner when the batch
	// fills before the timer fires.
	DefaultFlushInterval = 30 * time.Second
	// drainTimeout bounds the final flush at shutdown so a stuck DB cannot block
	// graceful exit indefinitely.
	drainTimeout = 10 * time.Second
)

const (
	bufferNotStarted uint32 = iota
	bufferRunning
	bufferStopped
)

// batchRecorder is the minimal contract BufferRecorder depends on. Repository
// satisfies it; tests substitute a stub to assert flushing behaviour without a
// database.
type batchRecorder interface {
	RecordBatch(context.Context, []RequestRecord) error
}

// BufferRecorder implements RequestRecorder by enqueuing records onto a
// bounded channel and letting a background flusher persist them in batches.
// It offloads request_logs writes from the hot path: the per-request Record
// call only does a non-blocking channel send, so SQLite serialisation no
// longer gates request throughput.
type BufferRecorder struct {
	recorder   batchRecorder
	clock      clock.Clock
	logger     *slog.Logger
	records    chan RequestRecord
	flushDelay time.Duration
	batchSize  int

	// dropped counts records rejected because the channel was full. Read by the
	// admin observability surface; increments must be atomic since Record runs on
	// many request goroutines while flusher reads periodically.
	dropped atomic.Int64
	// flushFailed counts batches that failed to persist. Incremented by the
	// flusher goroutine; read by admin surfaces.
	flushFailed atomic.Int64

	runOnce        sync.Once
	stop           chan struct{}
	started        chan struct{}
	stopped        chan struct{}
	state          atomic.Uint32
	startRequested atomic.Bool

	// force carries a one-shot result channel for ForceFlush. The flusher
	// receives the result chan, performs a synchronous drain of channel + any
	// pending records, then reports the flush outcome. It lets tests and admin
	// surfaces make buffered logs visible immediately without waiting on a
	// timer.
	force chan chan error
}

// BufferOptions overrides BufferRecorder defaults. Zero values fall back to
// the package defaults so callers can omit fields they don't care about.
type BufferOptions struct {
	Capacity   int
	FlushDelay time.Duration
	BatchSize  int
	Logger     *slog.Logger
}

// NewBufferRecorder wraps recorder with a buffered flusher. Record is safe
// for concurrent use; Run must be called once to start flushing, and Stop must
// be called exactly once to drain remaining records and release resources.
func NewBufferRecorder(recorder batchRecorder, source clock.Clock, options BufferOptions) *BufferRecorder {
	capacity := options.Capacity
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	flushDelay := options.FlushDelay
	if flushDelay <= 0 {
		flushDelay = DefaultFlushInterval
	}
	batchSize := options.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultFlushBatchSize
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if source == nil {
		source = clock.RealClock{}
	}
	return &BufferRecorder{
		recorder:   recorder,
		clock:      source,
		logger:     logger,
		records:    make(chan RequestRecord, capacity),
		flushDelay: flushDelay,
		batchSize:  batchSize,
		stop:       make(chan struct{}),
		started:    make(chan struct{}),
		stopped:    make(chan struct{}),
		force:      make(chan chan error),
	}
}

// Record enqueues record without blocking on the request path. When the buffer
// is full it drops the record and increments the dropped counter rather than
// blocking the caller — observability must not back-pressure request handling.
func (b *BufferRecorder) Record(_ context.Context, record RequestRecord) error {
	select {
	case b.records <- record:
		return nil
	default:
		dropped := b.dropped.Add(1)
		b.logger.Warn("observability buffer full, dropping request record", "dropped_total", dropped)
		return nil
	}
}

// Dropped returns the cumulative count of records dropped since the recorder
// started. It exists for admin observability surfaces so operators can size the
// buffer against real traffic.
func (b *BufferRecorder) Dropped() int64 {
	return b.dropped.Load()
}

// FlushFailed returns the cumulative count of batch flush failures since the
// recorder started. Non-zero indicates persistent database write problems.
func (b *BufferRecorder) FlushFailed() int64 {
	return b.flushFailed.Load()
}

// Depth returns the current number of records buffered in the channel waiting
// to be flushed. High depth indicates flush throughput cannot keep up with
// record production.
func (b *BufferRecorder) Depth() int {
	return len(b.records)
}

// Run starts the flusher loop. It blocks until ctx is cancelled (or Stop is
// called), then performs a bounded final drain. Calling Run more than once is a
// no-op after the first call returns; tests that need deterministic start/stop
// should use Run and wait on Stopped().
func (b *BufferRecorder) Run(ctx context.Context) {
	b.startRequested.Store(true)
	b.runOnce.Do(func() {
		b.state.Store(bufferRunning)
		close(b.started)
		defer close(b.stopped)
		defer b.state.Store(bufferStopped)
		b.loop(ctx)
		// loop returns when ctx is done or Stop was called; drain whatever the
		// buffer still holds so a graceful shutdown doesn't lose pending logs.
		drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		b.drain(drainCtx)
	})
}

// Stop signals the flusher to drain and exit. It is idempotent and safe to
// call from a different goroutine than Run. Callers typically invoke it before
// waiting on Stopped() during shutdown ordering.
func (b *BufferRecorder) Stop() {
	select {
	case <-b.stop:
		// already stopped
	default:
		close(b.stop)
	}
}

// Stopped closes when the flusher has drained and exited.
func (b *BufferRecorder) Stopped() <-chan struct{} {
	return b.stopped
}

// ForceFlush makes all currently buffered records visible in the persistence
// store and blocks until that flush completes. It is safe for concurrent use
// and lets tests/admin surfaces synchronise buffered logs without waiting on
// the timer. It returns nil when the flusher isn't running (records stay
// buffered until Run/Stop drains them) or ctx is cancelled before completion.
func (b *BufferRecorder) ForceFlush(ctx context.Context) error {
	if !b.startRequested.Load() {
		return nil
	}
	if b.state.Load() != bufferRunning {
		select {
		case <-b.started:
		case <-b.stopped:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
		if b.state.Load() != bufferRunning {
			return nil
		}
	}
	result := make(chan error, 1)
	select {
	case b.force <- result:
	case <-b.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *BufferRecorder) loop(ctx context.Context) {
	timer := b.clock.NewTimer(b.flushDelay)
	defer timer.Stop()
	pending := make([]RequestRecord, 0, b.batchSize)
	for {
		select {
		case <-ctx.Done():
			// Flush anything accumulated since the last batch before exiting so
			// the pending slice doesn't silently drop records. Channel-resident
			// records are drained by Run after loop returns.
			if len(pending) > 0 {
				b.flushBatch(context.Background(), pending)
			}
			return
		case <-b.stop:
			if len(pending) > 0 {
				b.flushBatch(context.Background(), pending)
			}
			return
		case record := <-b.records:
			pending = append(pending, record)
			if len(pending) >= b.batchSize {
				// Slurp anything already queued up to batchSize so a burst flushes
				// as one batch instead of back-to-back single-record flushes.
				pending = b.slurp(pending)
				b.flushBatch(ctx, pending)
				pending = pending[:0]
				resetTimer(timer, b.flushDelay)
			}
		case <-timer.C:
			if len(pending) > 0 {
				pending = b.slurp(pending)
				b.flushBatch(ctx, pending)
				pending = pending[:0]
			}
			resetTimer(timer, b.flushDelay)
		case result := <-b.force:
			// Synchronous flush requested by ForceFlush: drain channel + pending
			// in batches of b.batchSize, persist them, and report back so the
			// caller knows the buffered logs are now visible. Use a fresh
			// background context so the flush completes even when the loop's ctx
			// is already cancelled (the caller gave ForceFlush its own ctx).
			channelled := b.drainChannel()
			merged := append(pending, channelled...)
			result <- b.flushPending(context.Background(), merged)
			pending = pending[:0]
			resetTimer(timer, b.flushDelay)
		}
	}
}

// drainChannel non-blocking pulls every available record out of the records
// channel into a fresh slice. Callers merge it with pending before flushing.
func (b *BufferRecorder) drainChannel() []RequestRecord {
	out := make([]RequestRecord, 0, b.batchSize)
	for {
		select {
		case record := <-b.records:
			out = append(out, record)
		default:
			return out
		}
	}
}

// flushPending persists records in batches of b.batchSize, returning the first
// error encountered. A nil slice is a no-op.
func (b *BufferRecorder) flushPending(ctx context.Context, records []RequestRecord) error {
	for start := 0; start < len(records); start += b.batchSize {
		end := start + b.batchSize
		if end > len(records) {
			end = len(records)
		}
		if err := b.recorder.RecordBatch(ctx, records[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// slurp non-blocking drains records into pending up to b.batchSize. It lets
// the timer-triggered path also flush records queued between resets, so bursts
// that arrive just after the timer fires aren't held an extra full interval.
func (b *BufferRecorder) slurp(pending []RequestRecord) []RequestRecord {
	for len(pending) < b.batchSize {
		select {
		case record := <-b.records:
			pending = append(pending, record)
		default:
			return pending
		}
	}
	return pending
}

// resetTimer drains a fired timer and restarts it. Stopping an already-fired
// timer would leak its value, so drain before Reset.
func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

// drain flushes whatever is already in the channel without waiting. Called at
// shutdown; ctx carries the drain timeout so a stuck DB cannot wedge exit.
func (b *BufferRecorder) drain(ctx context.Context) {
	pending := make([]RequestRecord, 0, b.batchSize)
	for {
		select {
		case record := <-b.records:
			pending = append(pending, record)
			if len(pending) >= b.batchSize {
				b.flushBatch(ctx, pending)
				pending = pending[:0]
			}
		default:
			if len(pending) > 0 {
				b.flushBatch(ctx, pending)
			}
			return
		}
	}
}

func (b *BufferRecorder) flushBatch(ctx context.Context, batch []RequestRecord) {
	if len(batch) == 0 {
		return
	}
	if err := b.recorder.RecordBatch(ctx, batch); err != nil {
		failed := b.flushFailed.Add(1)
		b.logger.Error("flush request batch failed",
			"batch_size", len(batch),
			"error", err,
			"flush_failed_total", failed,
		)
		return
	}
	if b.logger.Enabled(ctx, slog.LevelDebug) {
		b.logger.Debug("flushed request batch", "batch_size", len(batch))
	}
}
