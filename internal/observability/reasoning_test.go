package observability

import "testing"

func TestReasoningFieldsFromBodyReportsRequestedAndWireNames(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		requested bool
		fields   string
	}{
		{"no reasoning fields", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, false, ""},
		{"single reasoning_effort", `{"reasoning_effort":"high","model":"m"}`, true, "reasoning_effort"},
		{"thinking object", `{"thinking":{"type":"enabled"},"model":"m"}`, true, "thinking"},
		{"reasoning field", `{"reasoning":{"effort":"high"},"model":"m"}`, true, "reasoning"},
		{"fixed deterministic order", `{"thinking":{"type":"disabled"},"reasoning_effort":"low","reasoning":{},"model":"m"}`, true, "reasoning_effort,reasoning,thinking"},
		{"null-valued reasoning still requested", `{"reasoning_effort":null,"model":"m"}`, true, "reasoning_effort"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requested, fields := ReasoningFieldsFromBody([]byte(tc.body))
			if requested != tc.requested || fields != tc.fields {
				t.Fatalf("ReasoningFieldsFromBody = requested %v fields %q, want %v %q", requested, fields, tc.requested, tc.fields)
			}
		})
	}
}

func TestReasoningFieldsFromBodyHandlesInvalidJSON(t *testing.T) {
	requested, fields := ReasoningFieldsFromBody([]byte(`not json`))
	if requested || fields != "" {
		t.Fatalf("invalid body = requested %v fields %q, want false empty", requested, fields)
	}
}

func TestReasoningContentFromBodyCountsCharacters(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		present bool
		chars   int64
	}{
		{"no choices", `{}`, false, 0},
		{"empty reasoning", `{"choices":[{"message":{"reasoning_content":"","content":"ok"}}]}`, false, 0},
		{"null reasoning", `{"choices":[{"message":{"reasoning_content":null}}]}`, false, 0},
		{"ascii reasoning", `{"choices":[{"message":{"reasoning_content":"deep-thought"}}]}`, true, 12},
		{"multi-byte runes counted not bytes", `{"choices":[{"message":{"reasoning_content":"思考abc"}}]}`, true, 5},
		{"multiple choices summed", `{"choices":[{"message":{"reasoning_content":"ab"}},{"message":{"reasoning_content":"cd"}}]}`, true, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			present, chars := ReasoningContentFromBody([]byte(tc.body))
			if present != tc.present || chars != tc.chars {
				t.Fatalf("ReasoningContentFromBody = present %v chars %d, want %v %d", present, chars, tc.present, tc.chars)
			}
		})
	}
}

func TestReasoningDeltaCharsParsesStreamChunk(t *testing.T) {
	cases := []struct {
		name    string
		chunk   string
		present bool
		chars   int64
	}{
		{"no reasoning delta", `{"choices":[{"delta":{"content":"hi"}}]}`, false, 0},
		{"reasoning delta", `{"choices":[{"delta":{"reasoning_content":"chain"}}]}`, true, 5},
		{"null reasoning delta", `{"choices":[{"delta":{"reasoning_content":null}}]}`, false, 0},
		{"multiple choices summed", `{"choices":[{"delta":{"reasoning_content":"a"}},{"delta":{"reasoning_content":"bc"}}]}`, true, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			present, chars := ReasoningDeltaChars([]byte(tc.chunk))
			if present != tc.present || chars != tc.chars {
				t.Fatalf("ReasoningDeltaChars = present %v chars %d, want %v %d", present, chars, tc.present, tc.chars)
			}
		})
	}
}
