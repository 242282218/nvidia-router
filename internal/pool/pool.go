package pool

import (
	mathrand "math/rand"
	"sort"
	"sync"
	"time"

	"nvidia-router/internal/clock"
	"nvidia-router/internal/fault"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/runtimeconfig"
)

type StateSync interface {
	LoadSnapshot(keys []keystate.KeySnapshot, blocks []keystate.ModelBlock)
	UpsertKey(key keystate.KeySnapshot)
	SetKeyEnabled(keyID int64, enabled bool)
	RemoveKey(keyID int64)
	ApplySuccess(keyID int64)
	ApplyFailure(keyID, modelID int64, f fault.Fault, persisted keystate.KeySnapshot)
	SetModelBlock(keyID, modelID int64, blocked bool)
}

// KeyStatusCounts contains non-secret scheduling state counts for the admin summary.
type KeyStatusCounts struct {
	Total       int `json:"total"`
	Enabled     int `json:"enabled"`
	Disabled    int `json:"disabled"`
	AuthInvalid int `json:"auth_invalid"`
	CoolingDown int `json:"cooling_down"`
	Ready       int `json:"ready"`
}

// QueueSummary contains only queue sizing and occupancy metadata.
type QueueSummary struct {
	Length   int `json:"length"`
	Capacity int `json:"capacity"`
}

// Summary contains safe operational metadata for the admin runtime view.
type Summary struct {
	Keys             KeyStatusCounts `json:"keys"`
	Active           int             `json:"active"`
	Queue            QueueSummary    `json:"queue"`
	EarliestCooldown *time.Time      `json:"earliest_cooldown,omitempty"`
	ShuttingDown     bool            `json:"shutting_down"`
}

type Pool struct {
	mu       sync.Mutex
	settings runtimeconfig.Provider
	clock    clock.Clock
	keys     map[int64]*keyState
	models   map[int64]bool
	order    []int64
	cursor   int
	waiters  waitQueue
	closed   bool
	// latencyRNG returns a float in [0,1) for the weighted latency selection.
	// It is injectable so tests can force a deterministic winner.
	latencyRNG func() float64
}

func New(settings runtimeconfig.Provider, source clock.Clock) *Pool {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Pool{
		settings: settings,
		clock:    source,
		keys:     make(map[int64]*keyState),
		models:   make(map[int64]bool),
	}
}

// Summary returns a lock-consistent view without exposing credential state.
func (p *Pool) Summary() Summary {
	p.mu.Lock()
	defer p.mu.Unlock()

	summary := Summary{Queue: QueueSummary{Length: p.waiters.Len(), Capacity: resolveQueueSettings(p.currentSnapshot()).capacity}, ShuttingDown: p.closed}
	now := p.clock.Now()
	for _, state := range p.keys {
		summary.Keys.Total++
		if state.snapshot.Enabled {
			summary.Keys.Enabled++
		} else {
			summary.Keys.Disabled++
		}
		if state.snapshot.AuthInvalid {
			summary.Keys.AuthInvalid++
		}
		if state.busy {
			summary.Active++
		}
		summary.Active += state.streamingBusy
		if state.snapshot.CooldownUntil != nil && now.Before(*state.snapshot.CooldownUntil) {
			summary.Keys.CoolingDown++
			if summary.EarliestCooldown == nil || state.snapshot.CooldownUntil.Before(*summary.EarliestCooldown) {
				cooldown := *state.snapshot.CooldownUntil
				summary.EarliestCooldown = &cooldown
			}
		}
		if state.snapshot.Enabled && !state.snapshot.AuthInvalid && !state.busy && !isCoolingDown(state.snapshot.CooldownUntil, now) {
			summary.Keys.Ready++
		}
	}
	return summary
}

func (p *Pool) currentSnapshot() runtimeconfig.Snapshot {
	if p.settings == nil {
		return runtimeconfig.Snapshot{}
	}
	return p.settings.Snapshot()
}

func isCoolingDown(until *time.Time, now time.Time) bool {
	return until != nil && now.Before(*until)
}

func (p *Pool) SetModelEnabled(modelID int64, enabled bool) {
	p.mu.Lock()
	p.models[modelID] = enabled
	p.unlockAndDispatch()
}

func (p *Pool) ClearModelBlocks(modelID int64) {
	p.mu.Lock()
	for _, state := range p.keys {
		delete(state.blocks, modelID)
	}
	p.unlockAndDispatch()
}

func (p *Pool) LoadSnapshot(keys []keystate.KeySnapshot, blocks []keystate.ModelBlock) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	p.keys = make(map[int64]*keyState, len(keys))
	p.order = make([]int64, 0, len(keys))
	for _, key := range keys {
		p.keys[key.ID] = newKeyState(key)
		p.order = append(p.order, key.ID)
	}
	p.sortOrder()
	p.cursor = 0
	for _, block := range blocks {
		if state, ok := p.keys[block.KeyID]; ok {
			state.blocks[block.ModelID] = struct{}{}
		}
	}
}

func (p *Pool) UpsertKey(key keystate.KeySnapshot) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	if state, ok := p.keys[key.ID]; ok {
		state.snapshot = cloneSnapshot(key)
		return
	}
	nextKeyID, hasNextKey := p.nextKeyID()
	p.keys[key.ID] = newKeyState(key)
	p.order = append(p.order, key.ID)
	p.sortOrder()
	if !hasNextKey {
		p.cursor = 0
		return
	}
	for index, id := range p.order {
		if id == nextKeyID {
			p.cursor = index
			return
		}
	}
}

// SetKeyEnabled applies only the admin-owned Enabled flag. UpsertKey replaces
// the whole snapshot, which loses in-memory cooldown when an admin toggle and a
// concurrent failure race: both write the DB in order, but the memory-apply
// order is not guaranteed, so a full overwrite arriving second reverts the
// cooldown in memory while the DB keeps it, and the key gets scheduled during
// its own cooldown. ApplyFailure already merges for the same reason.
func (p *Pool) SetKeyEnabled(keyID int64, enabled bool) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	state, ok := p.keys[keyID]
	if !ok {
		return
	}
	state.snapshot.Enabled = enabled
}

func (p *Pool) RemoveKey(keyID int64) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	if _, ok := p.keys[keyID]; !ok {
		return
	}
	delete(p.keys, keyID)
	for index, id := range p.order {
		if id != keyID {
			continue
		}
		p.order = append(p.order[:index], p.order[index+1:]...)
		if index < p.cursor {
			p.cursor--
		}
		break
	}
	if len(p.order) == 0 {
		p.cursor = 0
		return
	}
	if p.cursor >= len(p.order) {
		p.cursor = 0
	}
}

func (p *Pool) ApplySuccess(keyID int64) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	state, ok := p.keys[keyID]
	if !ok {
		return
	}
	state.snapshot.AuthInvalid = false
	state.snapshot.CooldownUntil = nil
	state.snapshot.CooldownLevel = 0
	state.snapshot.ConsecutiveFailures = 0
}

// RecordLatency feeds a successful attempt duration (in milliseconds) into the
// key's EWMA so the latency-aware scheduler can prefer faster keys. It is
// called by the router on success and is a no-op for unknown keys.
func (p *Pool) RecordLatency(keyID int64, durationMS int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state, ok := p.keys[keyID]; ok {
		state.recordLatency(float64(durationMS))
	}
}

func (p *Pool) ApplyFailure(keyID, modelID int64, f fault.Fault, persisted keystate.KeySnapshot) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	state, ok := p.keys[keyID]
	if !ok {
		return
	}
	// Merge only the fields owned by the failure path. The persisted snapshot
	// was read inside the markFailure transaction and can predate a concurrent
	// admin update, so a full overwrite would roll back e.g. SetEnabled.
	state.snapshot.AuthInvalid = persisted.AuthInvalid
	state.snapshot.CooldownUntil = persisted.CooldownUntil
	state.snapshot.CooldownLevel = persisted.CooldownLevel
	state.snapshot.ConsecutiveFailures = persisted.ConsecutiveFailures
	if f.BlockModel {
		state.blocks[modelID] = struct{}{}
	}
}

func (p *Pool) SetModelBlock(keyID, modelID int64, blocked bool) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	state, ok := p.keys[keyID]
	if !ok {
		return
	}
	if blocked {
		state.blocks[modelID] = struct{}{}
		return
	}
	delete(state.blocks, modelID)
}

func (p *Pool) tryAcquire(modelID int64, attempted map[int64]struct{}) (Lease, bool) {
	return p.tryAcquireMode(modelID, attempted, false)
}

// tryAcquireStream acquires a streaming lease slot for tests, mirroring the
// non-streaming tryAcquire helper.
func (p *Pool) tryAcquireStream(modelID int64, attempted map[int64]struct{}) (Lease, bool) {
	return p.tryAcquireMode(modelID, attempted, true)
}

func (p *Pool) tryAcquireMode(modelID int64, attempted map[int64]struct{}, stream bool) (Lease, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	maxStreaming := resolveQueueSettings(p.currentSnapshot()).maxStreamingPerKey
	lease, _ := p.tryAcquireLocked(modelID, attempted, stream, maxStreaming, false, false)
	return lease, lease != nil
}

func (p *Pool) tryAcquireLocked(modelID int64, attempted map[int64]struct{}, stream bool, maxStreamingPerKey int, latencyEnabled bool, retryAttempted bool) (Lease, unavailableState) {
	now := p.clock.Now()
	if enabled, known := p.models[modelID]; known && !enabled {
		return nil, unavailableState{reason: UnavailableModelBlocked}
	}
	hasEnabled := false
	hasUnblocked := false
	hasReady := false
	var earliestCooldown time.Time

	// When latency scheduling is off, the round-robin path below stays exactly
	// as before. When on, gather every eligible ready key and run a weighted
	// random draw favouring faster keys (with an exploration weight for keys
	// still accumulating samples so they are not starved).
	var latencyCandidates []*keyState
	measuredLatency := 0

	for offset := range p.order {
		index := (p.cursor + offset) % len(p.order)
		state := p.keys[p.order[index]]
		if !state.snapshot.Enabled || state.snapshot.AuthInvalid {
			continue
		}
		hasEnabled = true
		if _, blocked := state.blocks[modelID]; blocked {
			continue
		}
		hasUnblocked = true
		if state.snapshot.CooldownUntil != nil && now.Before(*state.snapshot.CooldownUntil) {
			if earliestCooldown.IsZero() || state.snapshot.CooldownUntil.Before(earliestCooldown) {
				earliestCooldown = *state.snapshot.CooldownUntil
			}
			continue
		}
		hasReady = true
		// An attempted key counts toward hasEnabled/hasReady above: excluding it
		// must not turn "the only ready key is one I already tried" into
		// UnavailableDisabled, which aborts acquire without queueing. The key is
		// still excluded from this acquire; the busy/streaming checks below let a
		// relaxed pass retry it when it is the only candidate (single-key pool
		// after a failover).
		if !retryAttempted {
			if _, alreadyAttempted := attempted[state.snapshot.ID]; alreadyAttempted {
				continue
			}
		}
		// Streaming and short requests draw from independent per-key quotas: a
		// stream holds a streaming slot for its whole (possibly minute-long)
		// lifetime instead of the single busy slot, so short requests on the
		// same key are not stalled behind a slow generation (audit R4).
		if stream {
			if state.streamingBusy >= maxStreamingPerKey {
				continue
			}
		} else {
			if state.busy {
				continue
			}
		}
		if !latencyEnabled {
			if stream {
				state.streamingBusy++
			} else {
				state.busy = true
			}
			p.cursor = (index + 1) % len(p.order)
			return &lease{
				keyID:   state.snapshot.ID,
				release: func() { p.release(state, stream) },
			}, unavailableState{}
		}
		if _, measured := state.latencyScore(); measured {
			measuredLatency++
		}
		latencyCandidates = append(latencyCandidates, state)
	}

	// No eligible key at all: report the precise reason.
	if !hasEnabled {
		return nil, unavailableState{reason: UnavailableDisabled}
	}
	if !hasUnblocked {
		return nil, unavailableState{reason: UnavailableModelBlocked}
	}
	if !hasReady {
		return nil, unavailableState{reason: UnavailableCooling, retryAfter: earliestCooldown.Sub(now)}
	}

	// Every ready key was busy/streaming-saturated; all were skipped above, so
	// there is nothing to hand out.
	if len(latencyCandidates) == 0 {
		return nil, unavailableState{reason: UnavailableBusy}
	}
	selected := p.weightedSelect(latencyCandidates, measuredLatency)
	// Advance the cursor past the chosen key so the next non-latency request
	// continues the rotation from here rather than re-scanning from zero.
	for index, id := range p.order {
		if id == selected.snapshot.ID {
			p.cursor = (index + 1) % len(p.order)
			break
		}
	}
	if stream {
		selected.streamingBusy++
	} else {
		selected.busy = true
	}
	return &lease{
		keyID:   selected.snapshot.ID,
		release: func() { p.release(selected, stream) },
	}, unavailableState{}
}

// weightedSelect draws one candidate with probability proportional to
// 1/(1+EWMA) for measured keys. When fewer than two keys have usable latency
// scores, it falls back to a uniform pick so a warmup pool still spreads load.
func (p *Pool) weightedSelect(candidates []*keyState, measured int) *keyState {
	if measured < 2 {
		// Uniform fallback: index = floor(rng * len).
		index := int(p.rng() * float64(len(candidates)))
		return candidates[index]
	}
	total := 0.0
	weights := make([]float64, len(candidates))
	for index, candidate := range candidates {
		weight := candidate.latencyWeight(unmeasuredLatencyWeight)
		weights[index] = weight
		total += weight
	}
	draw := p.rng() * total
	for index, weight := range weights {
		draw -= weight
		if draw < 0 {
			return candidates[index]
		}
	}
	return candidates[len(candidates)-1]
}

func (p *Pool) rng() float64 {
	if p.latencyRNG != nil {
		return p.latencyRNG()
	}
	return mathrand.Float64()
}

func (p *Pool) release(state *keyState, stream bool) {
	p.mu.Lock()
	if stream {
		state.streamingBusy--
	} else {
		state.busy = false
	}
	p.dispatchWaitersLocked()
	p.mu.Unlock()
}

func (p *Pool) unlockAndDispatch() {
	p.dispatchWaitersLocked()
	p.mu.Unlock()
}

func (p *Pool) sortOrder() {
	sort.Slice(p.order, func(i, j int) bool { return p.order[i] < p.order[j] })
}

func (p *Pool) nextKeyID() (int64, bool) {
	if len(p.order) == 0 {
		return 0, false
	}
	return p.order[p.cursor], true
}
