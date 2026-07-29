package fault

import (
	"math/rand"
	"time"
)

const (
	initialRateLimitCooldown = 5 * time.Second
	maximumRateLimitCooldown = 5 * time.Minute
	transientCooldown        = 15 * time.Second
	maximumCooldownLevel     = 6
)

type RandomSource interface {
	Float64() float64
}

type RandomFunc func() float64

func (f RandomFunc) Float64() float64 {
	return f()
}

func CalculateCooldown(f Fault, level int, random RandomSource) (time.Duration, int) {
	if f.HTTPStatus == 429 {
		nextLevel := nextCooldownLevel(level)
		if f.retryAfterValid || f.RetryAfter > 0 {
			return f.RetryAfter, nextLevel
		}
		base := rateLimitCooldown(level)
		return applyJitter(base, random), nextLevel
	}
	if f.Retryable && f.HTTPStatus >= 500 && f.HTTPStatus <= 599 {
		return transientCooldown, level
	}
	return 0, level
}

func rateLimitCooldown(level int) time.Duration {
	if level < 0 {
		level = 0
	}
	if level >= maximumCooldownLevel {
		return maximumRateLimitCooldown
	}
	return initialRateLimitCooldown * time.Duration(1<<level)
}

func nextCooldownLevel(level int) int {
	if level < 0 {
		return 1
	}
	if level < maximumCooldownLevel {
		return level + 1
	}
	return level
}

func applyJitter(base time.Duration, random RandomSource) time.Duration {
	value := rand.Float64()
	if random != nil {
		value = random.Float64()
	}
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	duration := time.Duration(float64(base) * (0.8 + 0.4*value))
	if duration > maximumRateLimitCooldown {
		return maximumRateLimitCooldown
	}
	return duration
}
