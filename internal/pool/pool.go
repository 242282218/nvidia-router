package pool

import (
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
	RemoveKey(keyID int64)
	ApplySuccess(keyID int64)
	ApplyFailure(keyID, modelID int64, f fault.Fault, persisted keystate.KeySnapshot)
	SetModelBlock(keyID, modelID int64, blocked bool)
}

type Pool struct {
	mu       sync.Mutex
	settings runtimeconfig.Provider
	clock    clock.Clock
	keys     map[int64]*keyState
	order    []int64
	cursor   int
	waiters  waitQueue
	closed   bool
}

func New(settings runtimeconfig.Provider, source clock.Clock) *Pool {
	if source == nil {
		source = clock.RealClock{}
	}
	return &Pool{
		settings: settings,
		clock:    source,
		keys:     make(map[int64]*keyState),
	}
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
	state.snapshot.CooldownUntil = nil
	state.snapshot.CooldownLevel = 0
	state.snapshot.ConsecutiveFailures = 0
}

func (p *Pool) ApplyFailure(keyID, modelID int64, f fault.Fault, persisted keystate.KeySnapshot) {
	p.mu.Lock()
	defer p.unlockAndDispatch()

	state, ok := p.keys[keyID]
	if !ok {
		return
	}
	state.snapshot = cloneSnapshot(persisted)
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
	p.mu.Lock()
	defer p.mu.Unlock()
	lease, _ := p.tryAcquireLocked(modelID, attempted)
	return lease, lease != nil
}

func (p *Pool) tryAcquireLocked(modelID int64, attempted map[int64]struct{}) (Lease, unavailableState) {
	now := p.clock.Now()
	hasEnabled := false
	hasUnblocked := false
	hasReady := false
	var earliestCooldown time.Time
	for offset := range p.order {
		index := (p.cursor + offset) % len(p.order)
		state := p.keys[p.order[index]]
		if _, alreadyAttempted := attempted[state.snapshot.ID]; alreadyAttempted {
			continue
		}
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
		if state.busy {
			continue
		}
		state.busy = true
		p.cursor = (index + 1) % len(p.order)
		return &lease{
			keyID:   state.snapshot.ID,
			release: func() { p.release(state) },
		}, unavailableState{}
	}
	if !hasEnabled {
		return nil, unavailableState{reason: UnavailableDisabled}
	}
	if !hasUnblocked {
		return nil, unavailableState{reason: UnavailableModelBlocked}
	}
	if !hasReady {
		return nil, unavailableState{reason: UnavailableCooling, retryAfter: earliestCooldown.Sub(now)}
	}
	return nil, unavailableState{reason: UnavailableBusy}
}

func (p *Pool) release(state *keyState) {
	p.mu.Lock()
	state.busy = false
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
