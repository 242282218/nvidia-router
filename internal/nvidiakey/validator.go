package nvidiakey

import (
	"context"
	"time"
	"unicode"

	"nvidia-router/internal/upstream/nvidia"
)

const (
	minimumTokenLength = 20
	maximumTokenLength = 512
)

type CredentialValidator interface {
	ValidateCredential(context.Context, string, time.Time) nvidia.ValidationResult
}

func validToken(token string) bool {
	if len(token) < minimumTokenLength || len(token) > maximumTokenLength {
		return false
	}
	for _, character := range token {
		if !unicode.IsPrint(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func maskToken(token string) (string, string, string) {
	characters := []rune(token)
	// Two-phase reveal budget: never expose more than a quarter of the token,
	// and never show a suffix unless the prefix already leaves room for one.
	// The earlier 8+4 default leaked 60% of a minimum-length (20-char) token;
	// this caps short tokens to prefix-only so brute-forcing the tail stays
	// infeasible while long tokens still show the `nvapi-` prefix and a tail.
	totalRevealBudget := max(2, len(characters)/4)
	prefixLength := min(8, totalRevealBudget)
	if prefixLength > len(characters) {
		prefixLength = len(characters)
	}
	prefix := string(characters[:prefixLength])

	suffixLength := 0
	if len(characters)-prefixLength > 4 {
		// Only show a tail when there is room inside the budget.
		suffixLength = min(4, totalRevealBudget-prefixLength)
	}
	suffix := ""
	if suffixLength > 0 {
		suffix = string(characters[len(characters)-suffixLength:])
	}

	masked := prefix + "..."
	if suffixLength > 0 {
		masked = prefix + "..." + suffix
	}
	return masked, prefix, suffix
}
