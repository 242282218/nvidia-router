package observability

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// redactedBearerText is the placeholder substituted in place of any captured
// bearer token. Keeping it lowercase and bracketed matches common redaction
// conventions and is easy to grep for in logs.
const redactedBearerText = "<redacted>"

// bearerTokenPattern matches a `Bearer <token>` pair (case-insensitive scheme)
// where the token is a 20-or-more-character run of the URL/credential safe
// alphabet. The 20-char floor avoids matching short words that happen to follow
// "bearer" in prose, while the safe-alphabet restriction matches the shape of
// real nvapi-/sk-/jwt-style credentials.
//
// Examples that match:
//
//	"Authorization: Bearer nvapi_xxxxxxxxxxxx" -> "Authorization: bearer <redacted>"
//	"bearer abcdefghij1234567890XYZ"          -> "bearer <redacted>"
//
// Examples that do not match (token too short):
//
//	"bearer short"
var bearerTokenPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+\-]{20,}`)

// RedactBearerToken returns s with every `bearer <token>` pair replaced by
// `bearer <redacted>`. It is intended as a final safety filter on content that
// may have captured upstream echoes of Authorization headers; it never touches
// the actual request headers sent upstream (those must remain intact).
func RedactBearerToken(s string) string {
	if s == "" || !strings.Contains(strings.ToLower(s), "bearer") {
		// Fast path: without the literal word "bearer" no match is possible,
		// so skip the regex scan entirely. This keeps negligible-cost content
		// (typical chat completions) off the hot path.
		return s
	}
	return bearerTokenPattern.ReplaceAllString(s, "bearer "+redactedBearerText)
}

// RedactAuthorizationHeader returns a copy of header with the Authorization
// value replaced by <redacted>. It is the structured form of RedactBearerToken
// for code paths that hold an http.Header rather than a raw string.
func RedactAuthorizationHeader(header http.Header) http.Header {
	if header == nil {
		return nil
	}
	if _, ok := header["Authorization"]; !ok {
		return header
	}
	clone := header.Clone()
	clone.Set("Authorization", redactedBearerText)
	return clone
}

// RedactURLQueryToken strips token-shaped query parameters from rawURL while
// preserving every other key. It guards future integrations (e.g. Gemini) where
// the credential travels in the URL string and could leak into logged
// redirection/error bodies. NVIDIA keys today never enter the query string —
// this function exists so the redaction surface is consistent before it's
// needed.
var tokenQueryKeys = map[string]struct{}{
	"key":    {},
	"apikey": {},
	"api_key": {},
}

// RedactURLQueryToken returns rawURL with any `?key=`/`?apikey=`/`?api_key=`
// values replaced by <redacted>. On parse failure the original URL is
// returned unchanged so a malformed value can never wedge a caller.
func RedactURLQueryToken(rawURL string) string {
	if !strings.Contains(rawURL, "?") {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if _, hit := tokenQueryKeys[strings.ToLower(key)]; hit {
			query.Set(key, redactedBearerText)
			changed = true
		}
	}
	if !changed {
		return rawURL
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
