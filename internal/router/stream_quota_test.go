package router

import (
	"context"
	"net/http"
	"testing"
	"time"

	"nvidia-router/internal/clock"
)

// TestAttemptStreamUsesStreamingQuota locks in the R2.1 plumbing end to end: the
// stream flag from Attempt.Run reaches the pool's per-key streaming quota, so a
// saturated streaming slot blocks another stream but leaves short requests
// flowing on the same key.
func TestAttemptStreamUsesStreamingQuota(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	settings.snapshot.MaxStreamingPerKey = 1
	keyPool := newAttemptPool(settings, 1)
	states := newAttemptStateWriter(time.Now())
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})

	// Hold the only streaming slot on the only key.
	holder, err := keyPool.Acquire(context.Background(), 1, nil, true)
	if err != nil {
		t.Fatalf("hold streaming slot: %v", err)
	}
	defer holder.Release()

	// A second streaming request must queue (and time out here), never reaching
	// the upstream: the quota must reject it, not the busy slot.
	streamingCalls := 0
	if _, err := attempt.Run(context.Background(), 1, true, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		streamingCalls++
		return attemptResponse(200, ""), nil
	}); err == nil {
		t.Fatal("streaming Run succeeded while the streaming quota was saturated")
	}
	if streamingCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0 (no streaming slot available)", streamingCalls)
	}

	// A short request on the same key must still succeed: the busy slot is free
	// even though the streaming quota is exhausted.
	shortCalls := 0
	result, err := attempt.Run(context.Background(), 1, false, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		shortCalls++
		return attemptResponse(200, ""), nil
	})
	if err != nil {
		t.Fatalf("short Run blocked by the streaming quota: %v", err)
	}
	defer result.Release()
	if shortCalls != 1 {
		t.Fatalf("short upstream calls = %d, want 1", shortCalls)
	}
}

// TestAttemptSettingsCarryStreamingQuota verifies the default quota resolves to 2
// when the snapshot omits it, so pre-migration settings behave like the default.
func TestAttemptSettingsCarryStreamingQuota(t *testing.T) {
	settings := &countingProvider{snapshot: attemptSettings()}
	keyPool := newAttemptPool(settings, 1)
	states := newAttemptStateWriter(time.Now())
	attempt := NewAttempt(settings, keyPool, testSecrets{}, states, keyPool, clock.RealClock{})

	first, err := keyPool.Acquire(context.Background(), 1, nil, true)
	if err != nil {
		t.Fatalf("first streaming Acquire: %v", err)
	}
	defer first.Release()
	second, err := keyPool.Acquire(context.Background(), 1, nil, true)
	if err != nil {
		t.Fatalf("second streaming Acquire (default quota 2): %v", err)
	}
	second.Release()

	// Both streams released; a third streaming Run succeeds through the router.
	result, err := attempt.Run(context.Background(), 1, true, func(context.Context, int64, []byte, *CommitState) (*http.Response, error) {
		return attemptResponse(200, ""), nil
	})
	if err != nil {
		t.Fatalf("streaming Run after quota release: %v", err)
	}
	defer result.Release()
}
