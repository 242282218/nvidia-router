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

	invalid := []struct {
		name  string
		value string
	}{
		{name: "missing leading delimiter", value: strings.TrimPrefix(valid, "$")},
		{name: "algorithm", value: phc("argon2i", parts[2], parts[3], parts[4], parts[5])},
		{name: "version", value: phc(parts[1], "v=18", parts[3], parts[4], parts[5])},
		{name: "parameter value", value: phc(parts[1], parts[2], "m=65536,t=2,p=2", parts[4], parts[5])},
		{name: "parameter order", value: phc(parts[1], parts[2], "t=3,m=65536,p=2", parts[4], parts[5])},
		{name: "repeated parameter", value: phc(parts[1], parts[2], "m=65536,t=3,p=2,m=65536", parts[4], parts[5])},
		{name: "parameter overflow", value: phc(parts[1], parts[2], "m=4294967296,t=3,p=2", parts[4], parts[5])},
		{name: "invalid salt base64", value: phc(parts[1], parts[2], parts[3], "!"+parts[4][1:], parts[5])},
		{name: "salt non-zero tail bits", value: phc(parts[1], parts[2], parts[3], strings.Repeat("A", 21)+"B", parts[5])},
		{name: "hash non-zero tail bits", value: phc(parts[1], parts[2], parts[3], parts[4], strings.Repeat("A", 42)+"B")},
		{name: "trailing hash data", value: phc(parts[1], parts[2], parts[3], parts[4], parts[5]+"A")},
		{name: "short salt", value: phc(parts[1], parts[2], parts[3], parts[4][1:], parts[5])},
		{name: "short hash", value: phc(parts[1], parts[2], parts[3], parts[4], parts[5][1:])},
	}
	for _, item := range invalid {
		t.Run(item.name, func(t *testing.T) {
			assertPasswordVerificationRejects(t, item.value)
		})
	}
}

func phc(algorithm, version, parameters, salt, hash string) string {
	return "$" + algorithm + "$" + version + "$" + parameters + "$" + salt + "$" + hash
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
