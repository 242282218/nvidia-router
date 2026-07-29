package adminauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Memory      uint32 = 65536
	argon2Iterations  uint32 = 3
	argon2Parallelism uint8  = 2
	argon2SaltLength         = 16
	argon2KeyLength   uint32 = 32
)

var errInvalidPasswordHash = errors.New("invalid password hash")

// HashPassword derives an Argon2id password hash in the required PHC format.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	passwordBytes := []byte(password)
	defer clear(passwordBytes)
	hash := argon2.IDKey(passwordBytes, salt, argon2Iterations, argon2Memory, argon2Parallelism, argon2KeyLength)
	defer clear(hash)

	return fmt.Sprintf(
		"$argon2id$v=19$m=65536,t=3,p=2$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword checks a password against a strictly parsed Argon2id PHC hash.
func VerifyPassword(password, encodedHash string) (bool, error) {
	salt, expected, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false, fmt.Errorf("parse password hash: %w", err)
	}
	defer clear(salt)
	defer clear(expected)

	passwordBytes := []byte(password)
	defer clear(passwordBytes)
	actual := argon2.IDKey(passwordBytes, salt, argon2Iterations, argon2Memory, argon2Parallelism, argon2KeyLength)
	defer clear(actual)

	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parsePasswordHash(encodedHash string) ([]byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=2" {
		return nil, nil, errInvalidPasswordHash
	}

	salt, err := decodePHCValue(parts[4], argon2SaltLength)
	if err != nil {
		return nil, nil, fmt.Errorf("decode password salt: %w", err)
	}
	hash, err := decodePHCValue(parts[5], int(argon2KeyLength))
	if err != nil {
		return nil, nil, fmt.Errorf("decode password digest: %w", err)
	}
	return salt, hash, nil
}

func decodePHCValue(value string, expectedLength int) ([]byte, error) {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return nil, errInvalidPasswordHash
	}
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != expectedLength {
		clear(decoded)
		return nil, errInvalidPasswordHash
	}
	return decoded, nil
}
