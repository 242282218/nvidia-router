package fault

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterParsesSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value string
		want  time.Duration
		valid bool
	}{
		{name: "seconds", value: "12", want: 12 * time.Second, valid: true},
		{name: "date", value: now.Add(45 * time.Second).Format(http.TimeFormat), want: 45 * time.Second, valid: true},
		{name: "past", value: now.Add(-time.Minute).Format(http.TimeFormat), valid: true},
		{name: "invalid", value: "later", valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := ParseRetryAfter(tt.value, now)
			if got != tt.want || valid != tt.valid {
				t.Fatalf("ParseRetryAfter() = %s, %t; want %s, %t", got, valid, tt.want, tt.valid)
			}
		})
	}
}

func TestCooldownRateLimitBackoffAndCap(t *testing.T) {
	f := Fault{HTTPStatus: 429, Retryable: true}
	for level, want := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
		got, nextLevel := CalculateCooldown(f, level, RandomFunc(func() float64 { return 0.5 }))
		if got != want || nextLevel != level+1 {
			t.Fatalf("level %d cooldown = %s, next %d; want %s, %d", level, got, nextLevel, want, level+1)
		}
	}
	got, nextLevel := CalculateCooldown(f, 9, RandomFunc(func() float64 { return 0.5 }))
	if got != 5*time.Minute || nextLevel != 9 {
		t.Fatalf("capped cooldown = %s, next %d", got, nextLevel)
	}
}

func TestCooldownJitterAndFixedTransientDelay(t *testing.T) {
	rateLimit := Fault{HTTPStatus: 429, Retryable: true}
	low, _ := CalculateCooldown(rateLimit, 0, RandomFunc(func() float64 { return 0 }))
	high, _ := CalculateCooldown(rateLimit, 0, RandomFunc(func() float64 { return 1 }))
	if low != 4*time.Second || high != 6*time.Second {
		t.Fatalf("jitter bounds = %s..%s, want 4s..6s", low, high)
	}

	transient := Fault{HTTPStatus: 503, Retryable: true}
	got, nextLevel := CalculateCooldown(transient, 3, RandomFunc(func() float64 { return 0 }))
	if got != 15*time.Second || nextLevel != 3 {
		t.Fatalf("transient cooldown = %s, next %d", got, nextLevel)
	}

	explicit := Fault{HTTPStatus: 429, Retryable: true, RetryAfter: 37 * time.Second}
	got, _ = CalculateCooldown(explicit, 2, RandomFunc(func() float64 { return 0 }))
	if got != 37*time.Second {
		t.Fatalf("explicit Retry-After cooldown = %s", got)
	}
}

func TestCooldownCapsExplicitRetryAfter(t *testing.T) {
	oversized := Fault{HTTPStatus: 429, Retryable: true, RetryAfter: 24 * time.Hour, retryAfterValid: true}
	got, nextLevel := CalculateCooldown(oversized, 1, RandomFunc(func() float64 { return 0 }))
	if got != maximumRateLimitCooldown || nextLevel != 2 {
		t.Fatalf("oversized Retry-After cooldown = %s, next %d; want cap %s, 2", got, nextLevel, maximumRateLimitCooldown)
	}

	exact := Fault{HTTPStatus: 429, Retryable: true, RetryAfter: maximumRateLimitCooldown, retryAfterValid: true}
	got, _ = CalculateCooldown(exact, 0, nil)
	if got != maximumRateLimitCooldown {
		t.Fatalf("exact-cap Retry-After cooldown = %s", got)
	}

	zero := Fault{HTTPStatus: 429, Retryable: true, RetryAfter: 0, retryAfterValid: true}
	got, _ = CalculateCooldown(zero, 0, nil)
	if got != 0 {
		t.Fatalf("zero Retry-After cooldown = %s, want 0 (already past)", got)
	}
}

func TestClassifierPreservesPastRetryAfterAsZeroCooldown(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	response := responseForClassifier(429, "", now.Add(-time.Minute).Format(http.TimeFormat))
	classified := Classify(response, nil, false, now)

	got, nextLevel := CalculateCooldown(classified, 0, RandomFunc(func() float64 { return 0.5 }))
	if got != 0 || nextLevel != 1 {
		t.Fatalf("past Retry-After cooldown = %s, next %d; want 0s, 1", got, nextLevel)
	}
}

func TestClassifierInvalidRetryAfterUsesExponentialFallback(t *testing.T) {
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	response := responseForClassifier(429, "", "invalid")
	classified := Classify(response, nil, false, now)

	got, nextLevel := CalculateCooldown(classified, 2, RandomFunc(func() float64 { return 0.5 }))
	if got != 20*time.Second || nextLevel != 3 {
		t.Fatalf("invalid Retry-After cooldown = %s, next %d; want 20s, 3", got, nextLevel)
	}
}

func TestCooldownServerFaultHonoursUpstreamRetryAfter(t *testing.T) {
	// A 503 with an explicit Retry-After must cool the key down for that window
	// instead of the fixed 15s transient (audit P2-3). Without Retry-After the
	// transient default stays.
	withRetryAfter := Fault{HTTPStatus: 503, Retryable: true, RetryAfter: 37 * time.Second}
	got, nextLevel := CalculateCooldown(withRetryAfter, 3, nil)
	if got != 37*time.Second || nextLevel != 3 {
		t.Fatalf("503+RetryAfter cooldown = %s, next %d; want 37s, 3", got, nextLevel)
	}

	without := Fault{HTTPStatus: 503, Retryable: true}
	got, _ = CalculateCooldown(without, 3, nil)
	if got != transientCooldown {
		t.Fatalf("503 without RetryAfter cooldown = %s, want %s", got, transientCooldown)
	}

	capped := Fault{HTTPStatus: 503, Retryable: true, RetryAfter: 24 * time.Hour}
	got, _ = CalculateCooldown(capped, 1, nil)
	if got != maximumRateLimitCooldown {
		t.Fatalf("oversized 503 RetryAfter cooldown = %s, want cap %s", got, maximumRateLimitCooldown)
	}
}

func TestClassifyCarriesRetryAfterIntoServerFault(t *testing.T) {
	response := responseForClassifier(503, "", "42")
	got := Classify(response, nil, false, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if got.HTTPStatus != 503 || !got.Retryable {
		t.Fatalf("503 fault = %+v", got)
	}
	if got.RetryAfter != 42*time.Second {
		t.Fatalf("503 RetryAfter = %s, want 42s", got.RetryAfter)
	}
}
