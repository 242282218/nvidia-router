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
