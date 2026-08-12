package xkproxy

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Collector fetches and validates proxies from upstream on a schedule
type Collector struct {
	upstream       *UpstreamClient
	validator      *Validator
	pool           *Pool
	logger         *slog.Logger
	interval       time.Duration
	proxyTTL       time.Duration
	expectedQty    int
	concurrency    int
	ejectionPolicy EjectionPolicy

	mu      sync.Mutex
	closed  bool
	done    chan struct{}
	wg      sync.WaitGroup
	lastErr error

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
	MaxLatency       time.Duration
	Interval         time.Duration
	ProxyTTL         time.Duration
	ExpectedQty      int
	Concurrency      int
	EjectionPolicy   EjectionPolicy
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
	return &Collector{
		upstream:       NewUpstreamClient(cfg.UpstreamURL, cfg.UpstreamTimeout),
		validator:      NewValidatorWithMaxLatency(cfg.ValidationURL, cfg.ValidationStatus, cfg.ValidationTimeout, cfg.MaxLatency),
		pool:           pool,
		logger:         logger,
		interval:       cfg.Interval,
		proxyTTL:       cfg.ProxyTTL,
		expectedQty:    cfg.ExpectedQty,
		concurrency:    cfg.Concurrency,
		ejectionPolicy: cfg.EjectionPolicy,
		done:           make(chan struct{}),
	}
}

func (c *Collector) Start(ctx context.Context) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	c.wg.Add(1)
	go c.run(ctx)
}

func (c *Collector) run(ctx context.Context) {
	defer c.wg.Done()

	// Immediate first fetch; its outcome seeds the backoff state so a startup
	// against a not-yet-ready upstream does not hammer it on the base interval.
	success := c.fetch(ctx)

	timer := time.NewTimer(c.nextInterval(success))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-timer.C:
			success := c.fetch(ctx)
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
	return c.interval << c.backoffLevel
}

func (c *Collector) fetch(ctx context.Context) bool {
	started := time.Now()
	c.logger.Info("upstream_fetch_start")

	proxies, fetchedAt, err := c.upstream.Fetch(ctx)
	if err != nil {
		c.mu.Lock()
		c.lastErr = err
		c.mu.Unlock()

		code := ErrorCode(err)
		c.logger.Warn("upstream_fetch_failed",
			"error", err,
			"provider_code", code,
			"duration_ms", time.Since(started).Milliseconds(),
		)
		return false
	}

	if len(proxies) == 0 {
		c.logger.Warn("upstream_fetch_empty",
			"duration_ms", time.Since(started).Milliseconds(),
		)
		return false
	}

	c.logger.Info("upstream_fetch_success",
		"count", len(proxies),
		"duration_ms", time.Since(started).Milliseconds(),
	)

	// Validate proxies concurrently
	validated := c.validateBatch(ctx, proxies, fetchedAt)

	if len(validated) > 0 {
		c.pool.Merge(time.Now(), validated, c.ejectionPolicy)
		c.logger.Info("pool_updated",
			"validated_count", len(validated),
			"pool_size", c.pool.LiveSize(time.Now()),
		)
	} else {
		c.logger.Warn("validation_all_failed", "fetched_count", len(proxies))
	}

	c.mu.Lock()
	c.lastErr = nil
	c.mu.Unlock()
	return true
}

func (c *Collector) validateBatch(ctx context.Context, proxies []Proxy, fetchedAt time.Time) []Proxy {
	sem := make(chan struct{}, c.concurrency)
	results := make(chan Proxy, len(proxies))
	var wg sync.WaitGroup

	for _, proxy := range proxies {
		wg.Add(1)
		go func(p Proxy) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			latency, err := c.validator.ValidateWithLatency(ctx, p)
			if err != nil {
				c.logger.Debug("proxy_validation_failed",
					"address", p.Address,
					"error", err,
					"latency_ms", latency.Milliseconds(),
				)
				return
			}

			p.FetchedAt = fetchedAt
			p.ValidatedAt = time.Now()
			p.ExpiresAt = fetchedAt.Add(c.proxyTTL)
			p.LatencyEWMA = latency
			results <- p
		}(proxy)
	}

	go func() {
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
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	close(c.done)
	c.wg.Wait()

	c.upstream.Close()
	c.validator.Close()
	return nil
}

func (c *Collector) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}
