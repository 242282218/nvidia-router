package modelcatalog

import (
	"reflect"
	"testing"
)

func TestModelSizeParams(t *testing.T) {
	tests := []struct {
		id       string
		size     int
		detected bool
	}{
		{"nvidia/nemotron-3-ultra-550b-a55b", 550, true},
		{"openai/gpt-oss-120b", 120, true},
		{"google/gemma-4-31b-it", 31, true},
		{"z-ai/glm-5.2", 0, false},
		{"deepseek-ai/deepseek-v4-flash-0731", 0, false},
		{"minimaxai/minimax-m3", 0, false},
		{"stepfun-ai/step-3.7-flash", 0, false},
		{"nvidia/deepseek-r1", 0, false},
	}
	for _, test := range tests {
		size, detected := modelSizeParams(test.id)
		if detected != test.detected || size != test.size {
			t.Errorf("modelSizeParams(%q) = (%d, %v), want (%d, %v)", test.id, size, detected, test.size, test.detected)
		}
	}
}

func TestSortCandidatesGroupsFreeFirstThenNVIDIAThenOther(t *testing.T) {
	input := []Candidate{
		{PublicID: "nvidia/nemotron-3-ultra-550b-a55b", UpstreamID: "nvidia/nemotron-3-ultra-550b-a55b", Provider: ProviderNVIDIA},
		{PublicID: "opencodefree/claude-opus-5", UpstreamID: "claude-opus-5", Provider: ProviderOpenCodeFree},
		{PublicID: "opencodefree/mimo-v2.5-free", UpstreamID: "mimo-v2.5-free", Provider: ProviderOpenCodeFree},
		{PublicID: "z-ai/glm-5.2", UpstreamID: "z-ai/glm-5.2", Provider: ProviderNVIDIA},
		{PublicID: "opencodefree/deepseek-v4-flash-free", UpstreamID: "deepseek-v4-flash-free", Provider: ProviderOpenCodeFree},
		{PublicID: "openai/gpt-oss-120b", UpstreamID: "openai/gpt-oss-120b", Provider: ProviderNVIDIA},
		{PublicID: "opencodefree/laguna-s-2.1-free", UpstreamID: "laguna-s-2.1-free", Provider: ProviderOpenCodeFree},
	}
	sortCandidates(input)
	got := candidateIDs(input)
	want := []string{
		// free OpenCodeFree models lead, alphabetical
		"deepseek-v4-flash-free", "laguna-s-2.1-free", "mimo-v2.5-free",
		// NVIDIA: sized largest first, then unknown-size by vendor
		"nvidia/nemotron-3-ultra-550b-a55b", "openai/gpt-oss-120b", "z-ai/glm-5.2",
		// non-free OpenCodeFree models trail
		"claude-opus-5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted candidates = %v, want %v", got, want)
	}
}

func TestSortCandidatesGroupsNVIDIAUnknownSizeByVendor(t *testing.T) {
	input := []Candidate{
		{PublicID: "stepfun-ai/step-3.7-flash", UpstreamID: "stepfun-ai/step-3.7-flash", Provider: ProviderNVIDIA},
		{PublicID: "z-ai/glm-5.2", UpstreamID: "z-ai/glm-5.2", Provider: ProviderNVIDIA},
		{PublicID: "deepseek-ai/deepseek-v4-flash-0731", UpstreamID: "deepseek-ai/deepseek-v4-flash-0731", Provider: ProviderNVIDIA},
		{PublicID: "minimaxai/minimax-m3", UpstreamID: "minimaxai/minimax-m3", Provider: ProviderNVIDIA},
	}
	sortCandidates(input)
	got := candidateIDs(input)
	want := []string{
		"deepseek-ai/deepseek-v4-flash-0731",
		"minimaxai/minimax-m3",
		"stepfun-ai/step-3.7-flash",
		"z-ai/glm-5.2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted candidates = %v, want %v", got, want)
	}
}

func TestSortCandidatesKeepsSizedBeforeUnsizedWithinNVIDIA(t *testing.T) {
	input := []Candidate{
		{PublicID: "nvidia/nemotron-3-ultra-550b-a55b", UpstreamID: "nvidia/nemotron-3-ultra-550b-a55b", Provider: ProviderNVIDIA},
		{PublicID: "nvidia/deepseek-r1", UpstreamID: "nvidia/deepseek-r1", Provider: ProviderNVIDIA},
		{PublicID: "openai/gpt-oss-120b", UpstreamID: "openai/gpt-oss-120b", Provider: ProviderNVIDIA},
		{PublicID: "nvidia/llama-3.3-70b-instruct", UpstreamID: "nvidia/llama-3.3-70b-instruct", Provider: ProviderNVIDIA},
	}
	sortCandidates(input)
	got := candidateIDs(input)
	want := []string{
		// sized, largest first
		"nvidia/nemotron-3-ultra-550b-a55b", "openai/gpt-oss-120b", "nvidia/llama-3.3-70b-instruct",
		// unsized, by vendor
		"nvidia/deepseek-r1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted candidates = %v, want %v", got, want)
	}
}
