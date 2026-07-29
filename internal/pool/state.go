package pool

import "nvidia-router/internal/keystate"

type keyState struct {
	snapshot keystate.KeySnapshot
	busy     bool
	blocks   map[int64]struct{}
}

func newKeyState(snapshot keystate.KeySnapshot) *keyState {
	return &keyState{
		snapshot: cloneSnapshot(snapshot),
		blocks:   make(map[int64]struct{}),
	}
}

func cloneSnapshot(snapshot keystate.KeySnapshot) keystate.KeySnapshot {
	if snapshot.CooldownUntil == nil {
		return snapshot
	}
	cooldownUntil := *snapshot.CooldownUntil
	snapshot.CooldownUntil = &cooldownUntil
	return snapshot
}
