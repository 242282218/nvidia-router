package responses

import "testing"

func TestParseAcceptsCodexMetadataAndCacheHints(t *testing.T) {
	request, err := Parse([]byte(`{
		"model":"opencode-free/deepseek-v4-flash-free",
		"input":"Reply with exactly OK.",
		"client_metadata":{"cwd":"/workspace","approval_policy":"never"},
		"prompt_cache_key":"codex-smoke",
		"include":["reasoning.encrypted_content"]
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	for _, field := range []string{"client_metadata", "prompt_cache_key"} {
		if _, forwarded := request.chatFields[field]; forwarded {
			t.Fatalf("%s must not be forwarded to Chat Completions", field)
		}
	}
}
