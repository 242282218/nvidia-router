package modelcatalog

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Discovery result grouping: the free OpenCodeFree models lead the list, then
// the NVIDIA catalog, and finally any remaining (non-free) OpenCodeFree models.
const (
	candidateGroupOpenCodeFreeFree  = 0
	candidateGroupNVIDIA            = 1
	candidateGroupOpenCodeFreeOther = 2
)

// modelSizePattern matches a parameter count suffix like "550b"/"120b"/"31b"
// anywhere in a model ID. The leftmost match wins, so a moe-style ID such as
// "nemotron-3-ultra-550b-a55b" reports 550, not the later 55.
var modelSizePattern = regexp.MustCompile(`(?i)([0-9]+)b`)

// sortCandidates orders discovery candidates in place: OpenCodeFree free
// models first (alphabetical), then NVIDIA models by parameter size (largest
// first) with models whose size is not encoded in the ID falling back to a
// vendor ordering, and finally any non-free OpenCodeFree models.
func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		groupLeft, groupRight := candidateGroup(left), candidateGroup(right)
		if groupLeft != groupRight {
			return groupLeft < groupRight
		}
		if groupLeft == candidateGroupNVIDIA {
			return nvidiaCandidateBefore(left, right)
		}
		return strings.ToLower(left.UpstreamID) < strings.ToLower(right.UpstreamID)
	})
}

func candidateGroup(candidate Candidate) int {
	if candidate.Provider == ProviderOpenCodeFree {
		if isFreeModelID(candidate.UpstreamID) {
			return candidateGroupOpenCodeFreeFree
		}
		return candidateGroupOpenCodeFreeOther
	}
	return candidateGroupNVIDIA
}

func isFreeModelID(id string) bool {
	return strings.HasSuffix(strings.ToLower(id), "-free")
}

// nvidiaCandidateBefore compares two NVIDIA candidates: sized models first,
// ordered largest to smallest; models without a detectable size fall back to a
// vendor (ID prefix) ordering so the same family stays grouped.
func nvidiaCandidateBefore(left, right Candidate) bool {
	sizeLeft, sizedLeft := modelSizeParams(left.UpstreamID)
	sizeRight, sizedRight := modelSizeParams(right.UpstreamID)
	if sizedLeft && sizedRight && sizeLeft != sizeRight {
		return sizeLeft > sizeRight
	}
	if sizedLeft != sizedRight {
		return sizedLeft
	}
	vendorLeft, vendorRight := vendorKey(left.UpstreamID), vendorKey(right.UpstreamID)
	if vendorLeft != vendorRight {
		return vendorLeft < vendorRight
	}
	lowerLeft, lowerRight := strings.ToLower(left.UpstreamID), strings.ToLower(right.UpstreamID)
	if lowerLeft != lowerRight {
		return lowerLeft < lowerRight
	}
	return strings.ToLower(left.PublicID) < strings.ToLower(right.PublicID)
}

// modelSizeParams returns the model parameter count in billions when the ID
// encodes one (e.g. "550b", "120b", "31b"); ok is false otherwise.
func modelSizeParams(id string) (size int, ok bool) {
	match := modelSizePattern.FindStringSubmatch(id)
	if match == nil {
		return 0, false
	}
	size, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return size, true
}

// vendorKey returns the vendor prefix of a model ID (the part before the
// first "/"), lowercased, so "z-ai/glm-5.2" and "nvidia/nemotron-..." sort by
// their vendor.
func vendorKey(id string) string {
	if index := strings.IndexByte(id, '/'); index > 0 {
		return strings.ToLower(id[:index])
	}
	return strings.ToLower(id)
}
