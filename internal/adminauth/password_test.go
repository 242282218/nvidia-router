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
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	matched, err := VerifyPassword("correct horse battery staple", hash)
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

func TestPasswordVerificationRejectsMalformedPHC(t *testing.T) {
	valid, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	malformed := []string{
		"argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		strings.Replace(valid, "argon2id", "argon2i", 1),
		strings.Replace(valid, "v=19", "v=18", 1),
		strings.Replace(valid, "m=65536,t=3,p=2", "m=65536,t=2,p=2", 1),
		strings.Replace(valid, "$", "$=", 1),
		strings.Replace(valid, "$", "$$", 1),
		strings.Replace(valid, "$argon2id$v=19$m=65536,t=3,p=2$", "$argon2id$v=19$m=65536,t=3,p=2$!", 1),
		"$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	for _, encoded := range malformed {
		t.Run("rejects invalid PHC", func(t *testing.T) {
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
		})
	}
}
