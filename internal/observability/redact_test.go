package observability

import (
	"net/http"
	"testing"
)

func TestRedactBearerTokenReplacesLongCredentials(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "nvapi", in: "Authorization: Bearer nvapi_abcdefghij1234567890XYZ", want: "Authorization: bearer <redacted>"},
		{name: "sk-style", in: "header bearer sk-1234567890abcdefghijkl", want: "header bearer <redacted>"},
		{name: "case-insensitive scheme", in: "BEARER 12345678901234567890ABCD", want: "bearer <redacted>"},
		{name: "jwt-like", in: "token: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhYmMifQ.signature", want: "token: bearer <redacted>"},
		{name: "multiple matches", in: "Bearer 12345678901234567890AA Bearer 98765432109876543210BB", want: "bearer <redacted> bearer <redacted>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactBearerToken(c.in); got != c.want {
				t.Fatalf("RedactBearerToken(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactBearerTokenPreservesShortBearerInProse(t *testing.T) {
	// Body text containing the word "bearer" followed by a short token must not
	// be mutilated — the floor prevents false positives on natural language.
	in := "He was the bearer of bad news."
	if got := RedactBearerToken(in); got != in {
		t.Fatalf("RedactBearerToken mutated prose: %q", got)
	}
}

func TestRedactBearerTokenFastPathSkipsWithoutKeyword(t *testing.T) {
	in := "nothing here but chat content"
	if got := RedactBearerToken(in); got != in {
		t.Fatalf("fast path mutated content: %q", got)
	}
}

func TestRedactAuthorizationHeaderReplacesValue(t *testing.T) {
	header := http.Header{}
	header.Set("Authorization", "Bearer nvapi_abcdefghijklmnopqrst")
	header.Set("Content-Type", "application/json")
	out := RedactAuthorizationHeader(header)
	if got := out.Get("Authorization"); got != redactedBearerText {
		t.Fatalf("Authorization = %q, want %q", got, redactedBearerText)
	}
	if got := out.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type lost: %q", got)
	}
	// Ensure the original header is not mutated.
	if header.Get("Authorization") == redactedBearerText {
		t.Fatal("RedactAuthorizationHeader mutated the input header")
	}
}

func TestRedactAuthorizationHeaderNoOpWhenAbsent(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	out := RedactAuthorizationHeader(header)
	if out.Get("Content-Type") != "application/json" {
		t.Fatalf("header altered: %v", out)
	}
	if out.Get("Authorization") != "" {
		t.Fatalf("Authorization unexpectedly added: %q", out.Get("Authorization"))
	}
}

func TestRedactURLQueryTokenReplacesKnownKeys(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "key", in: "https://api.example.com/v1?model=x&key=secret-value", want: "https://api.example.com/v1?key=%3Credacted%3E&model=x"},
		{name: "apikey", in: "https://api.example.com/?apikey=longapikeyvalue123", want: "https://api.example.com/?apikey=%3Credacted%3E"},
		{name: "api_key", in: "https://api.example.com/?api_key=longapikeyvalue123", want: "https://api.example.com/?api_key=%3Credacted%3E"},
		{name: "no query", in: "https://api.example.com/v1", want: "https://api.example.com/v1"},
		{name: "other key only", in: "https://api.example.com/?model=x", want: "https://api.example.com/?model=x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactURLQueryToken(c.in); got != c.want {
				t.Fatalf("RedactURLQueryToken(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
