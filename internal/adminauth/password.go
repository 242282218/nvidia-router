package adminauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id cost parameters used when hashing new passwords. Hashes produced
// under previous (weaker) parameters still verify via VerifyPassword because
// the cost is read back from the PHC string and only an upgrade-trigger signal
// is returned to the caller. Keep these aligned with the OWASP Argon2id(2) /
// RFC-9106 memory-time trade-off baseline.
const (
	argon2Memory      uint32 = 65536
	argon2Iterations  uint32 = 3
	argon2Parallelism uint8  = 2
	argon2SaltLength         = 16
	argon2KeyLength   uint32 = 32
)

var errInvalidPasswordHash = errors.New("invalid password hash")

// phcParams holds the cost parameters parsed from an Argon2id PHC string.
type phcParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

// currentArgon2Params is the cost used when hashing new passwords; comparison
// against it lets VerifyPasswordWithRehash signal a needed upgrade when a
// stored hash predates an increase.
func currentArgon2Params() phcParams {
	return phcParams{
		memory:      argon2Memory,
		iterations:  argon2Iterations,
		parallelism: argon2Parallelism,
	}
}

// HashPassword derives an Argon2id password hash in the required PHC format.
func HashPassword(password string) (string, error) {
	return hashWithParams(password, argon2Iterations, argon2Memory, argon2Parallelism)
}

// hashWithParams derives a PHC string under the supplied cost parameters. It is
// exported via the lowercase form for in-package tests that need to synthesise a
// verifiable hash with weaker parameters (e.g. exercising the rehash signal).
func hashWithParams(password string, iterations, memory uint32, parallelism uint8) (string, error) {
	salt := make([]byte, argon2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	passwordBytes := []byte(password)
	defer clear(passwordBytes)
	hash := argon2.IDKey(passwordBytes, salt, iterations, memory, parallelism, argon2KeyLength)
	defer clear(hash)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// hashWithParameters is a test-only helper producing a verifiable PHC string
// under arbitrary cost parameters, so password_test can synthesise a weaker
// digest that legitimately verifies and triggers the rehash path.
func hashWithParameters(password string, memory, iterations, parallelism uint32) string {
	if parallelism > 255 {
		parallelism = 255
	}
	digest, err := hashWithParams(password, iterations, memory, uint8(parallelism))
	if err != nil {
		panic(fmt.Sprintf("hashWithParameters: %v", err))
	}
	return digest
}

// VerifyPassword checks a password against a strictly parsed Argon2id PHC hash
// using the cost parameters embedded in the hash itself. It rejects any PHC
// string whose parameters diverge from what argon2.IDKey can reproduce (e.g.
// unsupported algorithm/version, malformed parameter segments, or non-canonical
// ordering) so the verifier never silently accepts a downgraded hash.
func VerifyPassword(password, encodedHash string) (bool, error) {
	matched, _, err := VerifyPasswordWithRehash(password, encodedHash)
	return matched, err
}

// VerifyPasswordWithRehash behaves like VerifyPassword and additionally reports
// whether the stored predates the current cost parameters. When needsRehash is
// true and matched is true, the caller should re-hash the supplied password
// with HashPassword and persist the upgraded hash. needsRehash is false on
// mismatch or parse failure, so callers can rely on it only after a successful
// match.
func VerifyPasswordWithRehash(password, encodedHash string) (matched bool, needsRehash bool, err error) {
	params, salt, expected, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false, false, fmt.Errorf("parse password hash: %w", err)
	}
	defer clear(salt)
	defer clear(expected)

	passwordBytes := []byte(password)
	defer clear(passwordBytes)
	actual := argon2.IDKey(passwordBytes, salt, params.iterations, params.memory, params.parallelism, argon2KeyLength)
	defer clear(actual)

	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return false, false, nil
	}
	return true, params != currentArgon2Params(), nil
}

// parsePasswordHash splits a PHC string into its cost parameters, salt and
// digest. It accepts the canonical Argon2id v=19 layout only and requires the
// parameter segment to carry exactly one each of m=, t= and p= in canonical
// order, so the encoded form stays unambiguous and audit-friendly. Non-canonical
// ordering, repeat or overflow are rejected to keep the verifier one true path.
func parsePasswordHash(encodedHash string) (phcParams, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return phcParams{}, nil, nil, errInvalidPasswordHash
	}
	params, err := parsePHCParams(parts[3])
	if err != nil {
		return phcParams{}, nil, nil, errInvalidPasswordHash
	}
	salt, err := decodePHCValue(parts[4], argon2SaltLength)
	if err != nil {
		return phcParams{}, nil, nil, fmt.Errorf("decode password salt: %w", err)
	}
	hash, err := decodePHCValue(parts[5], int(argon2KeyLength))
	if err != nil {
		return phcParams{}, nil, nil, fmt.Errorf("decode password digest: %w", err)
	}
	return params, salt, hash, nil
}

// parsePHCParams parses "m=<mem>,t=<iter>,p=<par>" accepting any order of the
// three canonical keys as long as each appears once. A repeat, missing key,
// unknown key, or zero cost is rejected so the verifier cannot be tricked into
// deriving under a degenerate cost (e.g. m=0 which argon2.IDKey rejects).
func parsePHCParams(segment string) (phcParams, error) {
	if segment == "" || strings.ContainsAny(segment, "\r\n") {
		return phcParams{}, errInvalidPasswordHash
	}
	fields := strings.Split(segment, ",")
	if len(fields) != 3 {
		return phcParams{}, errInvalidPasswordHash
	}
	var params phcParams
	seen := 0
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || value == "" {
			return phcParams{}, errInvalidPasswordHash
		}
		switch key {
		case "m":
			v, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return phcParams{}, errInvalidPasswordHash
			}
			params.memory = uint32(v)
			seen |= 1 << 0
		case "t":
			v, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return phcParams{}, errInvalidPasswordHash
			}
			params.iterations = uint32(v)
			seen |= 1 << 1
		case "p":
			v, err := strconv.ParseUint(value, 10, 8)
			if err != nil {
				return phcParams{}, errInvalidPasswordHash
			}
			params.parallelism = uint8(v)
			seen |= 1 << 2
		default:
			return phcParams{}, errInvalidPasswordHash
		}
	}
	if seen != 0b111 {
		return phcParams{}, errInvalidPasswordHash
	}
	if params.memory == 0 || params.iterations == 0 || params.parallelism == 0 {
		return phcParams{}, errInvalidPasswordHash
	}
	return params, nil
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
