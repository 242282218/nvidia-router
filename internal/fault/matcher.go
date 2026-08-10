package fault

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DefaultFailoverStatusCodes is the spec used when runtime config has no
// failover status code matcher. NVIDIA may use 529 for temporary overload;
// keep it in the default key-switch set alongside the common gateway codes.
const DefaultFailoverStatusCodes = "429,500,502,503,504,529"

// FailoverMatcher decides whether a given upstream HTTP status code should
// trigger a switch to a different key for retry. It replaces the previous
// hardcoded Retryable check at the routing layer so operators can adjust the
// set without a code change (audit item B4 of the gpt-load comparison plan).
//
// The matcher is constructed from a spec string like "429,500-599" (single
// codes and inclusive ranges, comma- or newline-separated) and answers Match
// in O(log n) via a sorted, merged list of inclusive endpoints.
type FailoverMatcher struct {
	ranges []inclusiveRange
	// spec is the canonical form the matcher was built from, retained for
	// diagnostics and so IsConfigured can tell "no config" apart from "empty
	// config" (which intentionally means "never fail over").
	spec string
}

type inclusiveRange struct {
	low  int
	high int
}

// NewFailoverMatcher parses spec into a matcher. An empty spec always matches
// nothing — operators who really want "never switch keys" need a deliberate
// escape hatch, and the empty string is a safe sentinel for that. Parse
// failures return an error so the caller can fall back to the default spec
// rather than silently treating invalid config as "match nothing".
func NewFailoverMatcher(spec string) (FailoverMatcher, error) {
	matcher := FailoverMatcher{spec: spec}
	if strings.TrimSpace(spec) == "" {
		// Empty configured spec means: no automatic failover. This is a
		// legitimate operator choice (e.g. exhaustive 4xx debugging),
		// distinct from "config missing" which the runtime layer maps to the
		// default. We return an empty matcher and nil.
		return matcher, nil
	}
	ranges, err := parseFailoverSpec(spec)
	if err != nil {
		return FailoverMatcher{}, err
	}
	matcher.ranges = mergeRanges(ranges)
	return matcher, nil
}

// MustFailoverMatcher is a convenience for constants/defaults; panics on an
// invalid spec. Used only for DefaultFailoverStatusCodes, which is itself
// validated by a unit test.
func MustFailoverMatcher(spec string) FailoverMatcher {
	matcher, err := NewFailoverMatcher(spec)
	if err != nil {
		panic(fmt.Sprintf("fault: invalid failover spec %q: %v", spec, err))
	}
	return matcher
}

// Spec returns the canonical string the matcher was constructed from. Useful
// for admin "current effective settings" surfaces to round-trip the value.
func (m FailoverMatcher) Spec() string { return m.spec }

// IsEmpty reports whether the matcher was constructed from an empty spec.
// Callers use it to decide whether to fall back to a default rather than
// treat "match nothing" as the active policy.
func (m FailoverMatcher) IsEmpty() bool { return len(m.ranges) == 0 }

// Match reports whether status lies inside any configured range. Range lookups
// are a binary search over the merged range list, so Match stays cheap even
// on a busy request path.
func (m FailoverMatcher) Match(status int) bool {
	if status <= 0 || len(m.ranges) == 0 {
		return false
	}
	index := sort.Search(len(m.ranges), func(i int) bool {
		return m.ranges[i].high >= status
	})
	if index >= len(m.ranges) {
		return false
	}
	return m.ranges[index].low <= status && status <= m.ranges[index].high
}

func parseFailoverSpec(spec string) ([]inclusiveRange, error) {
	var ranges []inclusiveRange
	tokens := strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		low, high, err := parseRangeToken(token)
		if err != nil {
			return nil, fmt.Errorf("parse failover token %q: %w", token, err)
		}
		if low < 100 || high > 599 {
			return nil, fmt.Errorf("parse failover token %q: codes must be within [100,599]", token)
		}
		// Reject success-class codes from triggering failover: putting 200-299
		// in the spec would re-roll the request after every success, burning
		// key quota. Validation rejects it up front so the runtime layer can
		// trust the matcher.
		if low < 400 {
			return nil, fmt.Errorf("parse failover token %q: success codes (<400) cannot trigger failover", token)
		}
		ranges = append(ranges, inclusiveRange{low: low, high: high})
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("parse failover spec: no codes in %q", spec)
	}
	return ranges, nil
}

func parseRangeToken(token string) (int, int, error) {
	if strings.Contains(token, "-") {
		parts := strings.SplitN(token, "-", 2)
		low, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, 0, fmt.Errorf("non-numeric range start: %w", err)
		}
		high, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, fmt.Errorf("non-numeric range end: %w", err)
		}
		if low > high {
			return 0, 0, fmt.Errorf("range start %d > end %d", low, high)
		}
		return low, high, nil
	}
	code, err := strconv.Atoi(strings.TrimSpace(token))
	if err != nil {
		return 0, 0, fmt.Errorf("non-numeric code: %w", err)
	}
	return code, code, nil
}

// mergeRanges sorts and collapses overlapping/adjacent inclusive ranges so the
// binary search in Match stays O(log n) on a minimal list.
func mergeRanges(ranges []inclusiveRange) []inclusiveRange {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].low != ranges[j].low {
			return ranges[i].low < ranges[j].low
		}
		return ranges[i].high < ranges[j].high
	})
	merged := []inclusiveRange{ranges[0]}
	for _, current := range ranges[1:] {
		last := &merged[len(merged)-1]
		if current.low <= last.high+1 {
			if current.high > last.high {
				last.high = current.high
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged
}
