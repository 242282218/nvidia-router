package nvidiakey

import (
	"context"
	"unicode"

	"nvidia-router/internal/upstream/nvidia"
)

const (
	minimumTokenLength = 20
	maximumTokenLength = 512
)

type CredentialValidator interface {
	ValidateCredential(context.Context, string) nvidia.ValidationResult
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

func validationStatus(state nvidia.ValidationState) (ImportStatus, string) {
	switch state {
	case nvidia.ValidationInvalidCredential:
		return ImportStatusInvalid, "invalid_credential"
	case nvidia.ValidationTemporarilyUnavailable:
		return ImportStatusTemporarilyUnavailable, "validation_temporarily_unavailable"
	case nvidia.ValidationIndeterminate:
		return ImportStatusIndeterminate, "validation_indeterminate"
	default:
		return ImportStatusIndeterminate, "validation_indeterminate"
	}
}

func maskToken(token string) (string, string, string) {
	characters := []rune(token)
	prefixLength := min(8, len(characters)/2)
	suffixLength := min(4, len(characters)-prefixLength-1)
	prefix := string(characters[:prefixLength])
	suffix := string(characters[len(characters)-suffixLength:])
	return prefix + "..." + suffix, prefix, suffix
}
