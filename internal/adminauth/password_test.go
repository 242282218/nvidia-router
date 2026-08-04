package adminauth

import (
	"strings"
	"testing"
)

func TestPasswordHashUsesRequiredPHCParameters(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		t.Fatalf("PHC parts = %d, want 6", len(parts))
	}
	if got, want := strings.Join(parts[:4], "$"), "$argon2id$v=19$m=65536,t=3,p=2"; got != want {
		t.Fatalf("PHC parameters = %q, want %q", got, want)
	}
	if len(parts[4]) != 22 {
		t.Fatalf("encoded salt length = %d, want 22", len(parts[4]))
	}
	if len(parts[5]) != 43 {
		t.Fatalf("encoded hash length = %d, want 43", len(parts[5]))
	}
}

func TestPasswordVerificationAcceptsOnlyTheCorrectPassword(t *testing.T) {
	const password = "correct horse battery staple"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	matched, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword correct password: %v", err)
	}
	if !matched {
		t.Fatal("VerifyPassword correct password = false, want true")
	}

	matched, err = VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword wrong password: %v", err)
	}
	if matched {
		t.Fatal("VerifyPassword wrong password = true, want false")
	}
}

func TestPasswordVerificationRejectsEqualLengthWrongPassword(t *testing.T) {
	const password = "correct horse battery staple"
	const wrongPassword = "wrongxx horse battery staple"
	if len(password) != len(wrongPassword) {
		t.Fatal("test passwords must have equal length")
	}
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	matched, err := VerifyPassword(wrongPassword, hash)
	if err != nil {
		t.Fatalf("VerifyPassword equal-length wrong password: %v", err)
	}
	if matched {
		t.Fatal("VerifyPassword equal-length wrong password = true, want false")
	}
}

func TestPasswordVerificationRejectsMalformedPHC(t *testing.T) {
	valid, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(valid, "$")
	if len(parts) != 6 {
		t.Fatalf("PHC parts = %d, want 6", len(parts))
	}
	if matched, err := VerifyPassword("correct horse battery staple", valid); err != nil || !matched {
		t.Fatalf("VerifyPassword valid PHC = %t, %v", matched, err)
	}

	// A different but syntactically valid parameter value (e.g. t=2 instead of
	// t=3) is parsed and used for verification. The digest was produced with the
	// original parameters, so the constant-time comparison fails: this proves
	// the verifier honours the parameters carried in the PHC string rather than
	// blindly trusting process-local constants (which would silently verify a
	// hash an attacker downgraded). Mismatch yields (false, nil), not an error.
	weakerParams := phc(parts[1], parts[2], "m=65536,t=2,p=2", parts[4], parts[5])
	if matched, err := VerifyPassword("correct horse battery staple", weakerParams); err != nil || matched {
		t.Fatalf("downgraded-parameter PHC verified = %t, %v; want mismatch without error", matched, err)
	}

	invalid := []struct {
		name  string
		value string
	}{
		{name: "missing leading delimiter", value: strings.TrimPrefix(valid, "$")},
		{name: "algorithm", value: phc("argon2i", parts[2], parts[3], parts[4], parts[5])},
		{name: "version", value: phc(parts[1], "v=18", parts[3], parts[4], parts[5])},
		{name: "repeated parameter", value: phc(parts[1], parts[2], "m=65536,t=3,p=2,m=65536", parts[4], parts[5])},
		{name: "parameter overflow", value: phc(parts[1], parts[2], "m=4294967296,t=3,p=2", parts[4], parts[5])},
		{name: "invalid salt base64", value: phc(parts[1], parts[2], parts[3], "!"+parts[4][1:], parts[5])},
		{name: "salt trailing carriage return", value: phc(parts[1], parts[2], parts[3], parts[4]+"\r", parts[5])},
		{name: "salt embedded line feed", value: phc(parts[1], parts[2], parts[3], parts[4][:10]+"\n"+parts[4][10:], parts[5])},
		{name: "salt non-zero tail bits", value: phc(parts[1], parts[2], parts[3], strings.Repeat("A", 21)+"B", parts[5])},
		{name: "hash trailing line feed", value: phc(parts[1], parts[2], parts[3], parts[4], parts[5]+"\n")},
		{name: "hash embedded CRLF", value: phc(parts[1], parts[2], parts[3], parts[4], parts[5][:20]+"\r\n"+parts[5][20:])},
		{name: "hash non-zero tail bits", value: phc(parts[1], parts[2], parts[3], parts[4], strings.Repeat("A", 42)+"B")},
		{name: "trailing hash data", value: phc(parts[1], parts[2], parts[3], parts[4], parts[5]+"A")},
		{name: "short salt", value: phc(parts[1], parts[2], parts[3], parts[4][1:], parts[5])},
		{name: "short hash", value: phc(parts[1], parts[2], parts[3], parts[4], parts[5][1:])},
		{name: "zero memory", value: phc(parts[1], parts[2], "m=0,t=3,p=2", parts[4], parts[5])},
		{name: "missing parameter", value: phc(parts[1], parts[2], "m=65536,t=3", parts[4], parts[5])},
		{name: "unknown parameter key", value: phc(parts[1], parts[2], "m=65536,t=3,p=2,s=16", parts[4], parts[5])},
	}
	for _, item := range invalid {
		t.Run(item.name, func(t *testing.T) {
			assertPasswordVerificationRejects(t, item.value)
		})
	}
}

// TestVerifyPasswordAcceptsPermutedParameterOrder documents that the verifier
// honours the PHC spec rather than a fixed canonical ordering: parameters may
// appear in any order as long as each of m=, t=, p= is present exactly once. The
// digest still verifies because the same costs are applied for derivation.
func TestVerifyPasswordAcceptsPermutedParameterOrder(t *testing.T) {
	const password = "correct horse battery staple"
	stored, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 6 {
		t.Fatalf("PHC parts = %d, want 6", len(parts))
	}
	permuted := phc(parts[1], parts[2], "p=2,m=65536,t=3", parts[4], parts[5])
	matched, _, err := VerifyPasswordWithRehash(password, permuted)
	if err != nil {
		t.Fatalf("permuted-order verify: %v", err)
	}
	if !matched {
		t.Fatal("permuted-parameter PHC did not verify")
	}
}

func phc(algorithm, version, parameters, salt, hash string) string {
	return "$" + algorithm + "$" + version + "$" + parameters + "$" + salt + "$" + hash
}

// TestVerifyPasswordWithRehashSignalsUpgradeOnWeakerParameters pins the
// upgrade contract: a digest produced under weaker parameters still verifies
// with the correct password, but VerifyPasswordWithRehash reports a pending
// upgrade so Repository.VerifyCredentials can re-hash and persist it.
func TestVerifyPasswordWithRehashSignalsUpgradeOnWeakerParameters(t *testing.T) {
	const password = "correct horse battery staple"
	stored, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 6 {
		t.Fatalf("PHC parts = %d, want 6", len(parts))
	}

	// current PHC: no upgrade needed
	matched, needsRehash, err := VerifyPasswordWithRehash(password, stored)
	if err != nil || !matched || needsRehash {
		t.Fatalf("current params: matched=%t needsRehash=%t err=%v", matched, needsRehash, err)
	}

	// weaker PHC: same digest (parts[5]) reused with t=2. The hash was computed
	// with t=3, so the comparison must fail; this proves the verifier uses the
	// PHC-carried cost, not process constants. A wrong password also yields no
	// rehash signal even on a weaker hash.
	weaker := phc(parts[1], parts[2], "m=65536,t=2,p=2", parts[4], parts[5])
	if matched, needsRehash, err := VerifyPasswordWithRehash(password, weaker); err != nil || matched {
		t.Fatalf("weaker params with t-mismatch digest: matched=%t needsRehash=%t err=%v", matched, needsRehash, err)
	}

	// To assert the rehash path itself we need a digest computed under weaker
	// parameters and verified with those same weaker parameters. We synthesize
	// such a hash using the argon2id layout directly so it actually verifies.
	weakerMatched, weakerNeedsRehash, err := VerifyPasswordWithRehash(password, hashWithParameters(password, 65536, 2, 2))
	if err != nil || !weakerMatched || !weakerNeedsRehash {
		t.Fatalf("weaker hash verify: matched=%t needsRehash=%t err=%v", weakerMatched, weakerNeedsRehash, err)
	}

	// A wrong password never triggers the rehash signal, even on a weaker hash.
	wrongMatched, wrongNeedsRehash, err := VerifyPasswordWithRehash("definitely not the password", hashWithParameters(password, 65536, 2, 2))
	if err != nil || wrongMatched || wrongNeedsRehash {
		t.Fatalf("wrong password on weaker hash: matched=%t needsRehash=%t err=%v", wrongMatched, wrongNeedsRehash, err)
	}
}

func assertPasswordVerificationRejects(t *testing.T, encoded string) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("VerifyPassword panicked for malformed PHC: %v", recovered)
		}
	}()
	matched, err := VerifyPassword("correct horse battery staple", encoded)
	if err == nil {
		t.Fatal("VerifyPassword malformed PHC succeeded")
	}
	if matched {
		t.Fatal("VerifyPassword malformed PHC matched")
	}
	if strings.Contains(err.Error(), encoded) {
		t.Fatal("VerifyPassword error includes PHC hash")
	}
}
