package xkproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

type collectorUpstream interface {
	Fetch(context.Context) ([]Proxy, time.Time, error)
	Close()
}

// Collector fetches and validates proxies from upstream on a schedule
type Collector struct {
	upstream       collectorUpstream
	validator      *Validator
	pool           *Pool
	logger         *slog.Logger
	interval       time.Duration
	proxyTTL       time.Duration
	expectedQty    int
	concurrency    int
	ejectionPolicy EjectionPolicy

	mu         sync.Mutex
	fetchMu    sync.Mutex
	closed     bool
	started    bool
	done       chan struct{}
	closeDone  chan struct{}
	// closeCalls is a test-only observation hook (production never sets it; the
	// nil-channel guard keeps Close a no-op for it). It lets the lifecycle test
	// observe concurrent Close entry without a public callback.
	closeCalls chan struct{}
	wg         sync.WaitGroup
	lastErr    error

	// lastFetchAt/lastSuccessAt track collector health for the admin status view:
	// when the pool is empty the operator needs to see whether the upstream is
	// unreachable or simply returned nothing (audit H10).
	lastFetchAt   time.Time
	lastSuccessAt time.Time

	// backoffLevel grows on consecutive upstream fetch failures and shrinks the
	// fetch rate so a rate-limited or down upstream is not hammered on a fixed
	// interval. Only touched by the run goroutine.
	backoffLevel int
}

// collectorMaxBackoffLevel caps the fetch-interval stretch to 2^level times the
// base interval (8x at the cap). A dead upstream must not stop collection
// forever: the pool drains via TTL and the first successful fetch resets it.
const collectorMaxBackoffLevel = 3

type CollectorConfig struct {
	UpstreamURL       string
	UpstreamTimeout   time.Duration
	ValidationURL     string
	ValidationStatus  int
	ValidationTimeout time.Duration
	// MaxLatency rejects a fetched proxy whose validation round-trip exceeds
	// this window, keeping slow exits out of the pool even when every candidate
	// is slow (audit H1). Zero disables the gate.
	MaxLatency     time.Duration
	Interval       time.Duration
	ProxyTTL       time.Duration
	ExpectedQty    int
	Concurrency    int
	EjectionPolicy EjectionPolicy
}

func NewCollector(cfg CollectorConfig, pool *Pool, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.Default()
	}
	// A zero interval would make nextInterval produce a zero timer and busy-loop
	// the fetch loop; the config layer always provides a positive value, but
	// guard here so a zero CollectorConfig cannot spin a goroutine.
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	// A zero/negative concurrency would block every validation goroutine on the
	// empty semaphore forever and hang Close(); normalize to one worker.
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	return &Collector{
		upstream:       NewUpstreamClient(cfg.UpstreamURL, cfg.UpstreamTimeout, cfg.ExpectedQty),
		validator:      NewValidatorWithMaxLatency(cfg.ValidationURL, cfg.ValidationStatus, cfg.ValidationTimeout, cfg.MaxLatency),
		pool:           pool,
		logger:         logger,
		interval:       cfg.Interval,
		proxyTTL:       cfg.ProxyTTL,
		expectedQty:    cfg.ExpectedQty,
		concurrency:    cfg.Concurrency,
		ejectionPolicy: cfg.EjectionPolicy,
		done:           make(chan struct{}),
		closeDone:      make(chan struct{}),
	}
}

func (c *Collector) Start(ctx context.Context) {
	c.mu.Lock()
	if c.closed || c.started {
		c.mu.Unlock()
		return
	}
	// Mark started and register the goroutine under the lock so a concurrent
	// Start cannot launch a second run loop (both would mutate backoffLevel)
	// and Close cannot reach Wait() before the goroutine is registered.
	c.started = true
	c.wg.Add(1)
	c.mu.Unlock()

	go c.run(ctx)
}

func (c *Collector) run(ctx context.Context) {
	defer c.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			c.logger.Error("proxy_collector_run_panic", "panic", fmt.Sprint(recovered))
		}
	}()

	// Immediate first fetch; its outcome seeds the backoff state so a startup
	// against a not-yet-ready upstream does not hammer it on the base interval.
	success := c.fetchSingleFlight(ctx)

	timer := time.NewTimer(c.nextInterval(success))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-timer.C:
			success := c.fetchSingleFlight(ctx)
			timer.Reset(c.nextInterval(success))
		}
	}
}

// nextInterval returns the delay before the next fetch, stretching the base
// interval after consecutive upstream failures and resetting on any success.
// A rate-limited or down upstream must be polled less aggressively, but a
// success proves it recovered, so the schedule snaps back to the base interval.
func (c *Collector) nextInterval(success bool) time.Duration {
	if success {
		c.backoffLevel = 0
		return c.interval
	}
	if c.backoffLevel < collectorMaxBackoffLevel {
		c.backoffLevel++
	}
	base := c.interval << c.backoffLevel
	// Jitter up to half the base so a fleet of routers sharing one upstream does
	// not re-poll in lockstep after a provider outage (a synchronized thundering
	// herd would look exactly like the outage continuing).
	return base + time.Duration(rand.Int64N(int64(base)/2+1))
}

// validationRetryLimit bounds how many times fetch re-fetches after a batch of
// proxies all fail validation. The upstream's proxy quality is random (a single
// dead IP is common), so one immediate re-fetch usually lands a usable exit
// without waiting out the next interval. The limit keeps a persistently-dead
// upstream from being hammered inside a single fetch.
const validationRetryLimit = 3

// validationRetryBackoff spaces re-fetch attempts after a validation-all-failed
// batch so a failed lease is not hammered back-to-back; the provider needs a
// beat to rotate its exit pool.
const validationRetryBackoff = 500 * time.Millisecond

// Refresh performs one bounded collection cycle. It is intentionally single-flight:
// the scheduler and an operator-triggered refresh must never overlap and consume two
// provider leases at once.
func (c *Collector) Refresh(ctx context.Context) error {
	if c == nil {
		return errors.New("proxy collector is nil")
	}
	if !c.beginLifecycle() {
		return errors.New("proxy collector is closed")
	}
	defer c.wg.Done()
	if !c.fetchSingleFlight(ctx) {
		if err := c.LastError(); err != nil {
			return err
		}
		return errors.New("proxy collection did not produce a healthy batch")
	}
	return nil
}

func (c *Collector) beginLifecycle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.wg.Add(1)
	return true
}

func (c *Collector) fetchSingleFlight(ctx context.Context) bool {
	if !c.beginLifecycle() {
		return false
	}
	defer c.wg.Done()

	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()
	return c.fetch(ctx)
}

func (c *Collector) fetch(ctx context.Context) bool {
	started := time.Now()
	// Per-poll noise stays at Debug; the operator-facing health is the status
	// projection (LastError/LastFetchResult), not one Info line every 5s.
	c.logger.Debug("upstream_fetch_start")

	for attempt := 0; attempt < validationRetryLimit; attempt++ {
		proxies, fetchedAt, err := c.upstream.Fetch(ctx)
		if err != nil {
			code := ErrorCode(err)
			// A rate-limit (403/406) or account-level code (204/205/208/211/302/
			// 430/432) means the provider will keep refusing for a while. Jump to
			// the deepest backoff immediately instead of grinding up one level per
			// poll, so a throttled upstream is not hammered at base rate.
			if ShouldBackOff(code) {
				c.mu.Lock()
				c.backoffLevel = collectorMaxBackoffLevel
				c.mu.Unlock()
			}
			c.mu.Lock()
			c.lastErr = err
			c.lastFetchAt = time.Now()
			c.mu.Unlock()

			c.logger.Warn("upstream_fetch_failed",
				"error_class", upstreamErrorClass(err),
				"provider_code", code,
				"duration_ms", time.Since(started).Milliseconds(),
			)
			c.gracePool()
			return false
		}

		if len(proxies) == 0 {
			c.logger.Warn("upstream_fetch_empty",
				"duration_ms", time.Since(started).Milliseconds(),
			)
			c.mu.Lock()
			c.lastFetchAt = time.Now()
			c.mu.Unlock()
			c.gracePool()
			return false
		}

		// Validate proxies concurrently
		validated := c.validateBatch(ctx, proxies, fetchedAt)
		if len(validated) > 0 {
			c.pool.Merge(time.Now(), validated, c.ejectionPolicy)
			c.logger.Debug("pool_updated",
				"validated_count", len(validated),
				"pool_size", c.pool.LiveSize(time.Now()),
			)
			c.mu.Lock()
			c.lastErr = nil
			c.lastFetchAt = time.Now()
			c.lastSuccessAt = time.Now()
			c.mu.Unlock()
			return true
		}

		// The upstream answered but every fetched exit failed validation. The
		// provider is returning unusable exits rather than being down, so retain
		// last-known-good exits the same way a fetch error does — otherwise a
		// bad-exit patch drains the pool and turns a provider quality dip into a
		// request outage. One immediate re-fetch is worth it because proxy quality
		// is random per lease; a rate-limit or account code is not, so no further
		// attempts are made in those cases.
		c.logger.Warn("validation_all_failed",
			"fetched_count", len(proxies),
			"attempt", attempt+1,
		)
		if !c.retryWorthwhile(ctx, attempt) {
			break
		}
		// Brief pause before re-fetching so a failed lease is not hammered
		// back-to-back; proxy quality is random per lease but the provider needs
		// a beat to rotate its exits.
		select {
		case <-time.After(validationRetryBackoff):
		case <-ctx.Done():
			return false
		}
	}

	c.mu.Lock()
	c.lastErr = nil
	c.lastFetchAt = time.Now()
	c.mu.Unlock()
	c.gracePool()
	// A batch that failed validation is not a "success": returning false keeps
	// the backoff schedule from snapping to the base interval and re-hammering a
	// degraded upstream every interval.
	return false
}

// retryWorthwhile reports whether another fetch/validate round is worth starting
// after a validation-all-failed batch. Reaching this point already means the
// upstream answered with proxy lines (a rate-limit or account code would have
// surfaced as a fetch error), so the provider is merely handing out dead exits.
// Proxy quality is random per lease, so one more round usually lands a usable
// exit; only the attempt cap and client cancellation stop the loop.
func (c *Collector) retryWorthwhile(ctx context.Context, attempt int) bool {
	if attempt >= validationRetryLimit-1 {
		return false
	}
	return ctx.Err() == nil
}

// gracePool extends live proxies' TTL so a short upstream degradation (fetch
// error, empty response, or a batch that all fail validation) does not drain the
// pool while the collector keeps polling. Without this the whole pool drains
// after one TTL window and the provider's own quality dip becomes a request
// outage (audit H6). Dead exits still fall off via their normal TTL, and the
// maxLifetime cap bounds how stale a retained exit may become.
func (c *Collector) gracePool() {
	if extended := c.pool.Grace(time.Now(), c.proxyTTL/2, c.proxyTTL*2); extended > 0 {
		c.logger.Info("proxy_ttl_grace",
			"extended", extended,
			"grace_ms", (c.proxyTTL / 2).Milliseconds(),
		)
	}
}

// LastError returns the most recent upstream fetch error, or nil when the last
// fetch succeeded.
func (c *Collector) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// LastFetchResult exposes collector health for the admin status view: when the
// last fetch failed, code is the provider error code (e.g. "403" rate-limit,
// "208" quota exhausted) or "transport" for a plain network failure. The raw
// error is never exposed: it embeds the upstream URL, which carries credentials.
func (c *Collector) LastFetchResult() (lastFetchAt time.Time, lastSuccessAt time.Time, code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr != nil {
		code = ErrorCode(c.lastErr)
		if code == "" {
			code = "transport"
		}
	}
	return c.lastFetchAt, c.lastSuccessAt, code
}

func upstreamErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	if _, ok := err.(*ProviderError); ok {
		return "provider"
	}
	return "transport_or_parse"
}

func validationErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrProxyAuth):
		return "auth"
	case errors.Is(err, ErrTimeout):
		return "timeout"
	case errors.Is(err, ErrDial):
		return "dial"
	case errors.Is(err, ErrStatus):
		return "status"
	case errors.Is(err, ErrProxyAddress):
		return "address"
	case errors.Is(err, ErrSlowProxy):
		return "slow"
	default:
		return "other"
	}
}

func (c *Collector) validateBatch(ctx context.Context, proxies []Proxy, fetchedAt time.Time) []Proxy {
	results := make(chan Proxy, len(proxies))
	jobs := make(chan Proxy, len(proxies))
	for _, p := range proxies {
		jobs <- p
	}
	close(jobs)

	workers := c.concurrency
	if workers > len(proxies) {
		workers = len(proxies)
	}
	if workers <= 0 {
		workers = 1
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				// A single validation panic must not take down the whole worker
				// pool (and with it the collector run loop).
				func() {
					defer func() {
						if recovered := recover(); recovered != nil {
							c.logger.Error("proxy_validation_panic", "proxy", p.Address, "panic", fmt.Sprint(recovered))
						}
					}()
					latency, err := c.validator.ValidateWithLatency(ctx, p)
					if err != nil {
						c.logger.Debug("proxy_validation_failed",
							"error_class", validationErrorClass(err),
							"latency_ms", latency.Milliseconds(),
						)
						return
					}
					p.FetchedAt = fetchedAt
					p.ValidatedAt = time.Now()
					p.ExpiresAt = fetchedAt.Add(c.proxyTTL)
					p.LatencyEWMA = latency
					// First validation is one latency sample; Merge then increments it on
					// every re-validation so the UI can tell a fresh EWMA from a noisy one.
					p.LatencySamples = 1
					results <- p
				}()
			}
		}()
	}

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				c.logger.Error("proxy_validation_closer_panic", "panic", fmt.Sprint(recovered))
			}
		}()
		wg.Wait()
		close(results)
	}()

	validated := make([]Proxy, 0, len(proxies))
	for proxy := range results {
		validated = append(validated, proxy)
	}

	return validated
}

func (c *Collector) Close() error {
	c.mu.Lock()
	if c.closeCalls != nil { // test hook: observe concurrent Close entry
		c.closeCalls <- struct{}{}
	}
	if c.closed {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	close(c.done)
	c.wg.Wait()

	c.upstream.Close()
	c.validator.Close()

	c.mu.Lock()
	close(c.closeDone)
	c.mu.Unlock()
	return nil
}
