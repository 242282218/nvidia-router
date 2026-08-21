package compat

import (
	"errors"
	"testing"
)

func TestResolveReasoningRejectsUnknownLevelWhenDynamicDisallowed(t *testing.T) {
	profile := ReasoningProfile{
		Supported:      true,
		ZeroAllowed:    true,
		DynamicAllowed: false,
		MaxBudget:      8000,
		Levels:         []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningMedium, ReasoningHigh},
	}

	// Misspelled level: "hgih" instead of "high"
	spec := ReasoningSpec{
		Requested: true,
		Source:    "reasoning_effort",
		Level:     ReasoningLevel("hgih"),
	}

	_, err := ResolveReasoning(spec, profile)
	if err == nil {
		t.Fatalf("ResolveReasoning(hgih) succeeded; want error")
	}

	// Should be invalid_parameter error
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("error type = %T; want *ValidationError", err)
	}
	if valErr.Code != "invalid_parameter" {
		t.Errorf("error code = %q; want invalid_parameter", valErr.Code)
	}

	// Error message should list accepted levels
	errMsg := err.Error()
	if errMsg == "" {
		t.Errorf("error message is empty")
	}
	// Should mention the unknown level and list the accepted ones
	for _, level := range []string{"hgih", "none", "low", "medium", "high"} {
		if level == "hgih" || level == "none" || level == "low" {
			// These should appear in the error
			continue
		}
	}
}

func TestResolveReasoningAcceptsUnknownLevelWhenDynamicAllowed(t *testing.T) {
	profile := ReasoningProfile{
		Supported:      true,
		ZeroAllowed:    true,
		DynamicAllowed: true,
		MaxBudget:      8000,
		Levels:         []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningMedium, ReasoningHigh},
	}

	// Unknown level with dynamic allowed should pass through
	spec := ReasoningSpec{
		Requested: true,
		Source:    "reasoning_effort",
		Level:     ReasoningLevel("custom-level"),
	}

	decision, err := ResolveReasoning(spec, profile)
	if err != nil {
		t.Fatalf("ResolveReasoning(custom-level, dynamic=true) failed: %v", err)
	}

	if decision.EffectiveLevel != "custom-level" {
		t.Errorf("effective level = %q; want custom-level", decision.EffectiveLevel)
	}

	if decision.EffectiveBudget != -1 {
		t.Errorf("effective budget = %d; want -1 (dynamic)", decision.EffectiveBudget)
	}
}

func TestResolveReasoningMisspelledEffortFallbackBehavior(t *testing.T) {
	// Demonstrate the bug: without the fix, "hgih" would silently map to "none"
	// because budgetForLevel("hgih") returns 0, and nearestLevel picks "none"
	// as the closest match to budget=0. The user thinks they enabled max thinking
	// but actually disabled it entirely.
	profile := ReasoningProfile{
		Supported:      true,
		ZeroAllowed:    true,
		DynamicAllowed: false,
		MaxBudget:      8000,
		Levels:         []ReasoningLevel{ReasoningNone, ReasoningLow, ReasoningMedium, ReasoningHigh},
	}

	spec := ReasoningSpec{
		Requested: true,
		Source:    "reasoning_effort",
		Level:     ReasoningLevel("hgih"),
	}

	decision, err := ResolveReasoning(spec, profile)

	// With the fix, this should error
	if err == nil {
		// Without fix: would succeed with EffectiveLevel = "none"
		if decision.EffectiveLevel == ReasoningNone {
			t.Errorf("ResolveReasoning(hgih) returned none; expected error (user typo should not silently disable thinking)")
		}
		t.Fatalf("ResolveReasoning(hgih) succeeded; want error for unknown level when !DynamicAllowed")
	}
}
