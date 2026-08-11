package xkproxy

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Outcome reports what a failure report did to the pool
type Outcome uint8

const (
	OutcomeCounted Outcome = iota
	OutcomeEjected
	OutcomeRemoved
)

// EjectionPolicy controls how failures translate into isolation and removal
type EjectionPolicy struct {
	FailureLimit int
	BaseDuration time.Duration
	MaxDuration  time.Duration
	MaxEjections int
	LatencyAlpha float64
}

const slowLatencyFactor = 3

func (e EjectionPolicy) normalized() EjectionPolicy {
	if e.FailureLimit <= 0 {
		e.FailureLimit = 3
	}
	if e.BaseDuration <= 0 {
		e.BaseDuration = 10 * time.Second
	}
	if e.MaxDuration < e.BaseDuration {
		e.MaxDuration = e.BaseDuration
	}
	if e.MaxEjections <= 0 {
		e.MaxEjections = 3
	}
	if e.LatencyAlpha <= 0 || e.LatencyAlpha > 1 {
		e.LatencyAlpha = 0.3
	}
	return e
}

type Pool struct {
	mu              sync.RWMutex
	proxies         []Proxy
	selectionCursor atomic.Uint64

	stickyMu       sync.Mutex
	stickyBindings map[string]stickyEntry
}

type stickyEntry struct {
	proxyKey  string
	expiresAt time.Time
}

func NewPool() *Pool {
	return &Pool{stickyBindings: make(map[string]stickyEntry)}
}

func (p *Pool) Replace(proxies []Proxy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxies = append([]Proxy(nil), proxies...)
}

func (p *Pool) Clear() {
	p.Replace(nil)
}

func (p *Pool) Peek(now time.Time) (Proxy, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ordered, _ := p.orderedLocked(now, 0)
	if len(ordered) == 0 {
		return Proxy{}, false
	}
	return ordered[0], true
}

func (p *Pool) Get(now time.Time) (Proxy, bool) {
	candidates, _ := p.Candidates(now, 0)
	if len(candidates) == 0 {
		return Proxy{}, false
	}
	return candidates[0], true
}

func (p *Pool) Candidates(now time.Time, minRemainingLife time.Duration) (candidates []Proxy, panicMode bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ordered, panicMode := p.orderedLocked(now, minRemainingLife)
	if len(ordered) == 0 {
		return nil, false
	}
	p.selectionCursor.Add(1)
	return ordered, panicMode
}

func (p *Pool) orderedLocked(now time.Time, minRemainingLife time.Duration) (ordered []Proxy, panicMode bool) {
	live := make([]Proxy, 0, len(p.proxies))
	available := make([]Proxy, 0, len(p.proxies))
	preferred := make([]Proxy, 0, len(p.proxies))
	for _, proxy := range p.proxies {
		if !proxy.LiveAt(now) {
			continue
		}
		live = append(live, proxy)
		if !proxy.AvailableAt(now) {
			continue
		}
		available = append(available, proxy)
		if proxy.RemainingLife(now) >= minRemainingLife {
			preferred = append(preferred, proxy)
		}
	}

	if len(preferred) > 0 {
		return p.rotate(preferred), false
	}
	if minRemainingLife > 0 {
		sufficientLive := make([]Proxy, 0, len(live))
		for _, proxy := range live {
			if proxy.RemainingLife(now) >= minRemainingLife {
				sufficientLive = append(sufficientLive, proxy)
			}
		}
		if len(sufficientLive) > 0 {
			return p.rotate(sufficientLive), true
		}
		return nil, false
	}
	switch {
	case len(available) > 0:
		return p.rotate(available), false
	case len(live) > 0:
		return p.rotate(live), true
	default:
		return nil, false
	}
}

func (p *Pool) rotate(source []Proxy) []Proxy {
	length := len(source)
	cursor := p.selectionCursor.Load()
	start := length - 1 - int(cursor%uint64(length))
	result := make([]Proxy, length)
	for offset := range result {
		index := (start - offset + length) % length
		result[offset] = source[index]
	}
	return demoteSlow(result)
}

func demoteSlow(candidates []Proxy) []Proxy {
	fastest := time.Duration(0)
	for _, candidate := range candidates {
		if candidate.LatencyEWMA <= 0 {
			continue
		}
		if fastest == 0 || candidate.LatencyEWMA < fastest {
			fastest = candidate.LatencyEWMA
		}
	}
	if fastest == 0 {
		return candidates
	}
	threshold := fastest * slowLatencyFactor
	indexed := make([]int, len(candidates))
	for i := range indexed {
		indexed[i] = i
	}
	sort.SliceStable(indexed, func(a, b int) bool {
		slowA := candidates[indexed[a]].LatencyEWMA > threshold
		slowB := candidates[indexed[b]].LatencyEWMA > threshold
		return !slowA && slowB
	})
	result := make([]Proxy, len(candidates))
	for i, index := range indexed {
		result[i] = candidates[index]
	}
	return result
}

func (p *Pool) Merge(now time.Time, incoming []Proxy, policies ...EjectionPolicy) {
	policy := EjectionPolicy{}
	if len(policies) > 0 {
		policy = policies[0]
	}
	policy = policy.normalized()

	p.mu.Lock()
	defer p.mu.Unlock()

	p.pruneLocked(now)
	index := make(map[string]int, len(p.proxies))
	for i, existing := range p.proxies {
		index[existing.Key()] = i
	}

	for _, candidate := range incoming {
		if !candidate.LiveAt(now) {
			continue
		}
		position, found := index[candidate.Key()]
		if !found {
			position = len(p.proxies)
			index[candidate.Key()] = position
			p.proxies = append(p.proxies, candidate)
			continue
		}

		current := p.proxies[position]
		current.ValidatedAt = candidate.ValidatedAt
		current.Scheme = candidate.Scheme
		current.Username = candidate.Username
		current.Password = candidate.Password
		if candidate.LatencyEWMA > 0 {
			current.LatencyEWMA = blendLatency(current.LatencyEWMA, candidate.LatencyEWMA, policy.LatencyAlpha)
		}
		if !candidate.FetchedAt.IsZero() && (current.FetchedAt.IsZero() || candidate.FetchedAt.Before(current.FetchedAt)) {
			current.FetchedAt = candidate.FetchedAt
		}
		if !candidate.ExpiresAt.IsZero() && (current.ExpiresAt.IsZero() || candidate.ExpiresAt.Before(current.ExpiresAt)) {
			current.ExpiresAt = candidate.ExpiresAt
		}
		current.SuccessCount++
		current.LastSuccessAt = candidate.ValidatedAt
		if current.HealthFails > 0 {
			current.HealthFails--
		}
		current.EjectedUntil = time.Time{}
		p.proxies[position] = current
	}
}

func blendLatency(previous, sample time.Duration, alpha float64) time.Duration {
	if previous <= 0 {
		return sample
	}
	return time.Duration(alpha*float64(sample) + (1-alpha)*float64(previous))
}

func (p *Pool) Remove(identity string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removeMatchingLocked(identity)
}

func (p *Pool) removeMatchingLocked(identity string) bool {
	filtered := p.proxies[:0]
	removed := false
	for _, proxy := range p.proxies {
		if matches(proxy, identity) {
			removed = true
			continue
		}
		filtered = append(filtered, proxy)
	}
	p.truncateLocked(filtered)
	return removed
}

func (p *Pool) ReportFailure(identity string, now time.Time, policy EjectionPolicy) Outcome {
	policy = policy.normalized()

	p.mu.Lock()
	defer p.mu.Unlock()

	p.pruneLocked(now)
	outcome := OutcomeCounted
	survivors := p.proxies[:0]
	for _, proxy := range p.proxies {
		if !matches(proxy, identity) {
			survivors = append(survivors, proxy)
			continue
		}

		proxy.FailureCount++
		proxy.HealthFails++
		if proxy.HealthFails < policy.FailureLimit {
			survivors = append(survivors, proxy)
			continue
		}

		proxy.EjectionCount++
		if proxy.EjectionCount > policy.MaxEjections {
			outcome = OutcomeRemoved
			continue
		}
		proxy.HealthFails = 0
		proxy.EjectedUntil = now.Add(ejectionWindow(policy, proxy.EjectionCount))
		if outcome != OutcomeRemoved {
			outcome = OutcomeEjected
		}
		survivors = append(survivors, proxy)
	}
	p.truncateLocked(survivors)
	return outcome
}

func ejectionWindow(policy EjectionPolicy, ejections int) time.Duration {
	window := policy.BaseDuration * time.Duration(ejections)
	if window > policy.MaxDuration {
		return policy.MaxDuration
	}
	return window
}

func (p *Pool) ReportSuccess(identity string, now time.Time, latency time.Duration, policy EjectionPolicy) {
	policy = policy.normalized()

	p.mu.Lock()
	defer p.mu.Unlock()

	p.pruneLocked(now)
	for i := range p.proxies {
		if !matches(p.proxies[i], identity) {
			continue
		}
		p.proxies[i].SuccessCount++
		p.proxies[i].LastSuccessAt = now
		if p.proxies[i].HealthFails > 0 {
			p.proxies[i].HealthFails--
		}
		if p.proxies[i].EjectionCount > 0 {
			p.proxies[i].EjectionCount--
		}
		p.proxies[i].EjectedUntil = time.Time{}
		if latency > 0 {
			p.proxies[i].LatencyEWMA = blendLatency(p.proxies[i].LatencyEWMA, latency, policy.LatencyAlpha)
		}
	}
}

func matches(proxy Proxy, identity string) bool {
	return proxy.Address == identity || proxy.Key() == identity
}

func (p *Pool) Size(now time.Time) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, proxy := range p.proxies {
		if proxy.AvailableAt(now) {
			count++
		}
	}
	return count
}

func (p *Pool) LiveSize(now time.Time) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, proxy := range p.proxies {
		if proxy.LiveAt(now) {
			count++
		}
	}
	return count
}

func (p *Pool) List(now time.Time) []Proxy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]Proxy, 0, len(p.proxies))
	for _, proxy := range p.proxies {
		if proxy.LiveAt(now) {
			result = append(result, proxy)
		}
	}
	return result
}

func (p *Pool) Prune(now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	before := len(p.proxies)
	p.pruneLocked(now)
	return before - len(p.proxies)
}

func (p *Pool) pruneLocked(now time.Time) {
	filtered := p.proxies[:0]
	for _, proxy := range p.proxies {
		if proxy.LiveAt(now) {
			filtered = append(filtered, proxy)
		}
	}
	p.truncateLocked(filtered)
}

func (p *Pool) truncateLocked(filtered []Proxy) {
	for i := len(filtered); i < len(p.proxies); i++ {
		p.proxies[i] = Proxy{}
	}
	p.proxies = filtered
}

// StickyGet returns candidates with session affinity when configured
func (p *Pool) StickyGet(sessionKey string, now time.Time, minRemainingLife time.Duration, stickyTTL time.Duration) (candidates []Proxy, panicMode bool, stickyHit bool) {
	p.stickyMu.Lock()
	binding, exists := p.stickyBindings[sessionKey]
	if exists && now.Before(binding.expiresAt) {
		p.stickyMu.Unlock()

		p.mu.RLock()
		defer p.mu.RUnlock()

		for _, proxy := range p.proxies {
			if proxy.Key() == binding.proxyKey && proxy.AvailableAt(now) && proxy.RemainingLife(now) >= minRemainingLife {
				allCandidates, pm := p.orderedLocked(now, minRemainingLife)
				if len(allCandidates) == 0 {
					return nil, false, false
				}
				result := make([]Proxy, 0, len(allCandidates))
				result = append(result, proxy)
				for _, candidate := range allCandidates {
					if candidate.Key() != proxy.Key() {
						result = append(result, candidate)
					}
				}
				p.selectionCursor.Add(1)
				return result, pm, true
			}
		}
	} else if exists {
		delete(p.stickyBindings, sessionKey)
	}
	p.stickyMu.Unlock()

	candidates, panicMode = p.Candidates(now, minRemainingLife)
	return candidates, panicMode, false
}

// StickyRebind binds a session to a proxy after successful selection
func (p *Pool) StickyRebind(sessionKey string, proxyKey string, now time.Time, stickyTTL time.Duration) {
	p.stickyMu.Lock()
	defer p.stickyMu.Unlock()
	p.stickyBindings[sessionKey] = stickyEntry{
		proxyKey:  proxyKey,
		expiresAt: now.Add(stickyTTL),
	}
}
