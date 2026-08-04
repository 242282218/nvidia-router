package fault

import (
	"testing"
)

func TestFailoverMatcherMatchesSingleCodesAndRanges(t *testing.T) {
	cases := []struct {
		name   string
		spec   string
		status int
		want   bool
	}{
		{name: "single code hit", spec: "429", status: 429, want: true},
		{name: "single code miss", spec: "429", status: 430, want: false},
		{name: "range hit low", spec: "500-599", status: 500, want: true},
		{name: "range hit mid", spec: "500-599", status: 555, want: true},
		{name: "range hit high", spec: "500-599", status: 599, want: true},
		{name: "range miss below", spec: "500-599", status: 499, want: false},
		{name: "range miss above", spec: "500-599", status: 600, want: false},
		{name: "mixed", spec: "429,500-503,510", status: 502, want: true},
		{name: "mixed single hit", spec: "429,500-503,510", status: 429, want: true},
		{name: "mixed last token hit", spec: "429,500-503,510", status: 510, want: true},
		{name: "mixed gap miss", spec: "429,500-503,510", status: 504, want: false},
		{name: "newline separator", spec: "429\n500\n502", status: 502, want: true},
		{name: "whitespace tolerant", spec: " 429 , 500 - 599 ", status: 503, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := NewFailoverMatcher(c.spec)
			if err != nil {
				t.Fatalf("NewFailoverMatcher(%q): %v", c.spec, err)
			}
			if got := m.Match(c.status); got != c.want {
				t.Fatalf("Match(%d) = %v, want %v", c.status, got, c.want)
			}
		})
	}
}

func TestFailoverMatcherEmptySpecMatchesNothing(t *testing.T) {
	m, err := NewFailoverMatcher("")
	if err != nil {
		t.Fatalf("NewFailoverMatcher(\"\"): %v", err)
	}
	if !m.IsEmpty() {
		t.Fatal("IsEmpty = false for empty spec")
	}
	if m.Match(429) || m.Match(500) {
		t.Fatal("empty matcher matched non-empty status")
	}
}

func TestFailoverMatcherRejectsBadInput(t *testing.T) {
	bad := []string{
		"abc",
		"4xx",
		"500-499",     // reversed
		"200",          // success code in spec
		"200-299",      // success range
		"99",           // below HTTP code floor
		"600",          // above HTTP code ceiling
		"500-700",      // range exceeds ceiling
		"429,-502",     // empty token between tokens after split: actually empty handled, but ",-" parses wrongly
		", ,",          // all empty tokens
	}
	for _, spec := range bad {
		t.Run(spec, func(t *testing.T) {
			if _, err := NewFailoverMatcher(spec); err == nil {
				t.Fatalf("NewFailoverMatcher(%q) error = nil, want error", spec)
			}
		})
	}
}

func TestFailoverMatcherMergesOverlappingRanges(t *testing.T) {
	m, err := NewFailoverMatcher("500-503,501-506,510,510")
	if err != nil {
		t.Fatalf("NewFailoverMatcher: %v", err)
	}
	// 500-506 + 510 should be the merged result. Matching against 504-506 must
	// be true after merge and 507-509 false.
	for _, status := range []int{500, 504, 506, 510} {
		if !m.Match(status) {
			t.Fatalf("Match(%d) = false, want true", status)
		}
	}
	for _, status := range []int{507, 508, 509, 499, 511} {
		if m.Match(status) {
			t.Fatalf("Match(%d) = true, want false", status)
		}
	}
}

func TestDefaultFailoverStatusCodesMatchesCanonicalSet(t *testing.T) {
	m := MustFailoverMatcher(DefaultFailoverStatusCodes)
	want := map[int]bool{429: true, 500: true, 502: true, 503: true, 504: true}
	for status, want := range want {
		if got := m.Match(status); got != want {
			t.Fatalf("default Match(%d) = %v, want %v", status, got, want)
		}
	}
	negative := []int{401, 403, 501, 200, 599}
	for _, status := range negative {
		if m.Match(status) {
			t.Fatalf("default Match(%d) = true, want false", status)
		}
	}
}
