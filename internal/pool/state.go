package pool

import (
	"math"

	"nvidia-router/internal/keystate"
)

// ewmaAlpha is the smoothing factor for per-key latency EWMA. A small alpha
// weights recent attempts heavily while a floor of samples prevents a single
// cold-start outlier from dominating.
const ewmaAlpha = 0.2

// latencyWarmupSamples is how many recorded attempts a key needs before the
// latency-aware scheduler trusts its EWMA; below it the key is treated as
// unmeasured and shares the default round-robin path.
const latencyWarmupSamples = 3

type keyState struct {
	snapshot      keystate.KeySnapshot
	busy          bool
	streamingBusy int
	blocks        map[int64]struct{}

	// latencyEWMA is an exponentially weighted moving average of successful
	// attempt durations (ms). It drives latency-aware scheduling: keys with
	// faster historical responses are preferred while their EWMA is fresher.
	latencyEWMA float64
	latencySamples int
}

func newKeyState(snapshot keystate.KeySnapshot) *keyState {
	return &keyState{
		snapshot: cloneSnapshot(snapshot),
		blocks:   make(map[int64]struct{}),
	}
}

// recordLatency folds one successful attempt duration into the key's EWMA.
// Zero or negative durations (clock jumps) are ignored rather than polluting
// the estimate.
func (s *keyState) recordLatency(durationMS float64) {
	if durationMS <= 0 || math.IsNaN(durationMS) || math.IsInf(durationMS, 0) {
		return
	}
	if s.latencySamples == 0 {
		s.latencyEWMA = durationMS
	} else {
		s.latencyEWMA = ewmaAlpha*durationMS + (1-ewmaAlpha)*s.latencyEWMA
	}
	if s.latencySamples < math.MaxInt {
		s.latencySamples++
	}
}

// latencyScore returns the key's measurable latency or a zero sentinel when it
// has not yet accumulated enough samples to be trusted.
func (s *keyState) latencyScore() (float64, bool) {
	if s.latencySamples < latencyWarmupSamples {
		return 0, false
	}
	return s.latencyEWMA, true
}

// latencyWeight returns the selection weight for latency-aware scheduling. A
// measured key is weighted inversely to its EWMA (scaled to seconds so typical
// sub-second latencies map to weights near 1) — faster keys win more often. An
// unmeasured key receives a small constant exploration weight so it can
// accumulate samples instead of being starved out of rotation forever.
func (s *keyState) latencyWeight(unmeasuredWeight float64) float64 {
	score, measured := s.latencyScore()
	if !measured {
		return unmeasuredWeight
	}
	// Scale ms to seconds and add 1 to keep the weight finite: a 100ms key gets
	// 1/1.1 ≈ 0.91, a 3s key gets 1/4 = 0.25.
	return 1.0 / (1.0 + score/1000.0)
}

// unmeasuredLatencyWeight is the exploration weight for keys still warming up.
// 0.5 means an unmeasured key stays competitive with a ~1s key but is clearly
// out-weighted by a fast (<500ms) key, and it is never fully starved.
const unmeasuredLatencyWeight = 0.5

func cloneSnapshot(snapshot keystate.KeySnapshot) keystate.KeySnapshot {
	if snapshot.CooldownUntil == nil {
		return snapshot
	}
	cooldownUntil := *snapshot.CooldownUntil
	snapshot.CooldownUntil = &cooldownUntil
	return snapshot
}
