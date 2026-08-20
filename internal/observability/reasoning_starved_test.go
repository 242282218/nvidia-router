package observability

import "testing"

func TestReasoningStarvedDetectsEmptyAnswer(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"","reasoning_content":"thinking a lot"}}]}`)
	if !ReasoningStarvedFromBody(body) {
		t.Fatal("want starved response detected")
	}
}

func TestReasoningStarvedIgnoresAnsweredResponse(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"length","message":{"content":"here is the answer","reasoning_content":"thinking"}}]}`)
	if ReasoningStarvedFromBody(body) {
		t.Fatal("a response with content is not starved")
	}
}

func TestReasoningStarvedIgnoresNaturalStop(t *testing.T) {
	// finish_reason=stop with empty content is a model choice, not budget starvation.
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"content":"","reasoning_content":"thinking"}}]}`)
	if ReasoningStarvedFromBody(body) {
		t.Fatal("stop is not starvation")
	}
}

func TestReasoningStarvedIgnoresPlainTruncation(t *testing.T) {
	// Hitting the limit without any reasoning is ordinary truncation.
	body := []byte(`{"choices":[{"finish_reason":"length","message":{"content":""}}]}`)
	if ReasoningStarvedFromBody(body) {
		t.Fatal("truncation without reasoning is not starvation")
	}
}

func TestReasoningStarvedIgnoresGarbage(t *testing.T) {
	if ReasoningStarvedFromBody([]byte("not json")) {
		t.Fatal("unparseable body must not be reported as starved")
	}
}
