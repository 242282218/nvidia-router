package compat

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type ReasoningLevel string

const (
	ReasoningNone    ReasoningLevel = "none"
	ReasoningAuto    ReasoningLevel = "auto"
	ReasoningMinimal ReasoningLevel = "minimal"
	ReasoningLow     ReasoningLevel = "low"
	ReasoningMedium  ReasoningLevel = "medium"
	ReasoningHigh    ReasoningLevel = "high"
	ReasoningXHigh   ReasoningLevel = "xhigh"
	ReasoningMax     ReasoningLevel = "max"
)

var ErrAmbiguousReasoning = errors.New("reasoning aliases disagree")
var ErrReasoningUnsupported = errors.New("reasoning is not supported by the selected model")

var reasoningBudgets = map[ReasoningLevel]int{
	ReasoningNone:    0,
	ReasoningAuto:    -1,
	ReasoningMinimal: 512,
	ReasoningLow:     1024,
	ReasoningMedium:  8192,
	ReasoningHigh:    24576,
	ReasoningXHigh:   32768,
	ReasoningMax:     128000,
}

var reasoningAliases = map[string]ReasoningLevel{
	"none": ReasoningNone, "off": ReasoningNone, "disabled": ReasoningNone,
	"auto": ReasoningAuto, "default": ReasoningAuto, "on": ReasoningAuto,
	"minimal": ReasoningMinimal, "tiny": ReasoningMinimal,
	"low":    ReasoningLow,
	"medium": ReasoningMedium, "med": ReasoningMedium,
	"high":  ReasoningHigh,
	"xhigh": ReasoningXHigh, "very_high": ReasoningXHigh, "very-high": ReasoningXHigh,
	"max": ReasoningMax, "ultra": ReasoningMax,
}

type ReasoningSpec struct {
	Requested bool
	Level     ReasoningLevel
	Budget    int
	HasBudget bool
	Source    string
}

// RequiresReasoning reports whether the caller asked the model to actually spend
// reasoning tokens. Requested alone does not mean that: reasoning_effort:"none",
// thinking:false and thinking:{"type":"disabled"} all parse as Requested because
// the caller did name the parameter, yet they ask for reasoning to stay off —
// something a model without the capability already satisfies. Treating them as a
// capability requirement made every non-reasoning model answer 501
// not_implemented to clients that send a reasoning parameter as a global default.
func (s ReasoningSpec) RequiresReasoning() bool {
	return s.Requested && s.Level != ReasoningNone
}

// reasoningAliasFields are the mutually redundant request fields that can carry a
// reasoning instruction. Any rewrite has to clear all three, otherwise a stale
// alias contradicts the form actually written.
var reasoningAliasFields = [3]string{"reasoning_effort", "reasoning", "thinking"}

// StripReasoning removes every reasoning alias from a request payload. It is for
// upstreams that cannot reason at all: the fields carry no instruction they could
// act on, and NIM validates the chat schema strictly enough to answer 422 for
// parameters outside it.
func StripReasoning(fields map[string]json.RawMessage) {
	for _, name := range reasoningAliasFields {
		delete(fields, name)
	}
}

type ReasoningProfile struct {
	Supported      bool
	Levels         []ReasoningLevel
	MinBudget      int
	MaxBudget      int
	ZeroAllowed    bool
	DynamicAllowed bool
	WireFormat     string
	// AdvisoryLevels marks upstreams that accept an effort string but do not act
	// on its magnitude. Every enabled level then means the same thing, so the
	// wire value is normalised to one standard level and only on/off is honest.
	AdvisoryLevels bool
}

// advisoryOnLevel is the single standard effort sent to advisory upstreams when
// reasoning is on. It must stay a value the OpenAI-compatible surface accepts —
// "auto" is not one, so it cannot be used here.
const advisoryOnLevel = ReasoningHigh

type ReasoningDecision struct {
	Requested       bool
	Source          string
	RequestedLevel  ReasoningLevel
	RequestedBudget int
	EffectiveLevel  ReasoningLevel
	EffectiveBudget int
	Downgraded      bool
}

func ParseReasoning(fields map[string]json.RawMessage) (ReasoningSpec, error) {
	aliases := []struct {
		name  string
		parse func(json.RawMessage, string) (ReasoningSpec, error)
	}{
		{name: "reasoning_effort", parse: parseReasoningEffort},
		{name: "reasoning", parse: parseReasoningObject},
		{name: "thinking", parse: parseThinking},
	}
	var result ReasoningSpec
	for _, alias := range aliases {
		raw, ok := fields[alias.name]
		if !ok || isNull(raw) {
			continue
		}
		spec, err := alias.parse(raw, alias.name)
		if err != nil {
			return ReasoningSpec{}, err
		}
		if !spec.Requested {
			continue
		}
		if !result.Requested {
			result = spec
			continue
		}
		if reasoningSpecsConflict(result, spec) {
			return ReasoningSpec{}, ErrAmbiguousReasoning
		}
		if result.Source == "" {
			result.Source = spec.Source
		}
		if !result.HasBudget && spec.HasBudget {
			result.Budget, result.HasBudget = spec.Budget, true
		}
	}
	return result, nil
}

func ResolveReasoning(spec ReasoningSpec, profile ReasoningProfile) (ReasoningDecision, error) {
	if !spec.Requested {
		return ReasoningDecision{}, nil
	}
	if !profile.Supported {
		return ReasoningDecision{}, ErrReasoningUnsupported
	}
	levels := availableLevels(profile)
	if len(levels) == 0 {
		return ReasoningDecision{}, ErrReasoningUnsupported
	}
	requestedLevel := spec.Level
	if requestedLevel == "" {
		requestedLevel = levelForBudget(spec.Budget)
	}
	requestedBudget := spec.Budget
	if !spec.HasBudget {
		requestedBudget = budgetForLevel(requestedLevel)
	}
	if _, known := reasoningBudgets[requestedLevel]; !known && profile.DynamicAllowed {
		return ReasoningDecision{
			Requested: true, Source: spec.Source, RequestedLevel: requestedLevel, RequestedBudget: requestedBudget,
			EffectiveLevel: requestedLevel, EffectiveBudget: -1,
		}, nil
	}
	if _, known := reasoningBudgets[requestedLevel]; !known && !profile.DynamicAllowed {
		// Unknown level and dynamic budgets disallowed: the user likely misspelled
		// a standard level (e.g., "hgih" for "high"). Without this block, nearestLevel
		// returns "none" (budgetForLevel returns 0 for unknown levels, and 0 is nearest
		// to "none"), silently turning off thinking when the user intended to enable it.
		// Return invalid_parameter with the model's accepted levels.
		accepted := make([]string, len(levels))
		for i, level := range levels {
			accepted[i] = string(level)
		}
		return ReasoningDecision{}, invalid("invalid_parameter", spec.Source, fmt.Sprintf(
			"The reasoning level %q is not recognized. This model accepts: %s.",
			requestedLevel, strings.Join(accepted, ", "),
		))
	}
	effectiveLevel := nearestLevel(requestedLevel, requestedBudget, levels, profile)
	if effectiveLevel == "" {
		return ReasoningDecision{}, ErrReasoningUnsupported
	}
	effectiveBudget := budgetForLevel(effectiveLevel)
	if effectiveLevel == ReasoningAuto && profile.DynamicAllowed {
		effectiveBudget = -1
	} else if spec.HasBudget && profile.DynamicAllowed && effectiveLevel != ReasoningNone {
		effectiveBudget = clampBudget(spec.Budget, profile)
	}
	if effectiveLevel == ReasoningNone {
		effectiveBudget = 0
	}
	return ReasoningDecision{
		Requested: true, Source: spec.Source, RequestedLevel: requestedLevel, RequestedBudget: requestedBudget,
		EffectiveLevel: effectiveLevel, EffectiveBudget: effectiveBudget,
		Downgraded: requestedLevel != effectiveLevel || (spec.HasBudget && requestedBudget != effectiveBudget),
	}, nil
}

func ApplyReasoning(fields map[string]json.RawMessage, decision ReasoningDecision, profile ReasoningProfile) error {
	if !decision.Requested {
		return nil
	}
	wireFormat := strings.ToLower(profile.WireFormat)
	preserveNativeThinking := decision.Source == "thinking" && (wireFormat == "" || wireFormat == "openai" || wireFormat == "thinking")
	// The thinking budget is billed against the same completion allowance as the
	// answer, so an unreconciled budget starves the content: upstreams happily
	// spend every token on reasoning and return an empty message with
	// finish_reason=length. Cap it before it reaches the wire.
	budget := capThinkingBudget(decision.EffectiveBudget, outputTokenLimit(fields))
	StripReasoning(fields)
	if preserveNativeThinking {
		encoded, err := marshalThinking(decision, budget)
		if err != nil {
			return fmt.Errorf("marshal native thinking: %w", err)
		}
		fields["thinking"] = encoded
		return nil
	}
	switch wireFormat {
	case "", "openai":
		level := decision.EffectiveLevel
		if profile.AdvisoryLevels && level != ReasoningNone {
			level = advisoryOnLevel
		}
		encoded, err := json.Marshal(string(level))
		if err != nil {
			return fmt.Errorf("marshal reasoning effort: %w", err)
		}
		fields["reasoning_effort"] = encoded
	case "thinking":
		encoded, err := marshalThinking(decision, budget)
		if err != nil {
			return fmt.Errorf("marshal thinking: %w", err)
		}
		fields["thinking"] = encoded
	case "none":
		return fmt.Errorf("%w: the selected model has no reasoning wire format", ErrReasoningUnsupported)
	default:
		return fmt.Errorf("unsupported reasoning wire format %q", profile.WireFormat)
	}
	return nil
}

// thinkingBudgetNumerator/Denominator bound the share of the completion
// allowance reasoning may consume, leaving the remainder for the answer.
const (
	thinkingBudgetNumerator   = 3
	thinkingBudgetDenominator = 4
)

// outputTokenLimit reports the completion allowance the client asked for, or 0
// when it left the limit to the upstream default.
func outputTokenLimit(fields map[string]json.RawMessage) int {
	for _, name := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var value *int
		if json.Unmarshal(raw, &value) != nil || value == nil || *value <= 0 {
			continue
		}
		return *value
	}
	return 0
}

// capThinkingBudget keeps the reasoning budget inside the completion allowance.
// A negative budget means "let the upstream decide" and stays untouched, as does
// every budget when the client set no limit of its own.
func capThinkingBudget(budget, limit int) int {
	if budget <= 0 || limit <= 0 {
		return budget
	}
	allowed := limit * thinkingBudgetNumerator / thinkingBudgetDenominator
	if allowed <= 0 || budget <= allowed {
		return budget
	}
	return allowed
}

func marshalThinking(decision ReasoningDecision, budget int) ([]byte, error) {
	thinking := map[string]any{}
	if decision.EffectiveLevel == ReasoningNone {
		thinking["type"] = "disabled"
	} else {
		thinking["type"] = "enabled"
		if budget >= 0 {
			thinking["budget_tokens"] = budget
		}
	}
	return json.Marshal(thinking)
}

func parseReasoningEffort(raw json.RawMessage, source string) (ReasoningSpec, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ReasoningSpec{}, invalid("invalid_parameter", source, "The reasoning effort must be a string.")
	}
	level, err := parseFlexibleLevel(value, source)
	if err != nil {
		return ReasoningSpec{}, err
	}
	return ReasoningSpec{Requested: true, Level: level, Budget: budgetForLevel(level), Source: source}, nil
}

func parseReasoningObject(raw json.RawMessage, source string) (ReasoningSpec, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return parseReasoningEffort(raw, source)
	}
	return parseReasoningFields(object, source, false)
}

func parseThinking(raw json.RawMessage, source string) (ReasoningSpec, error) {
	var value bool
	if json.Unmarshal(raw, &value) == nil {
		level := ReasoningAuto
		if !value {
			level = ReasoningNone
		}
		return ReasoningSpec{Requested: true, Level: level, Budget: budgetForLevel(level), Source: source}, nil
	}
	var valueString string
	if json.Unmarshal(raw, &valueString) == nil {
		level, err := parseFlexibleLevel(valueString, source)
		if err != nil {
			return ReasoningSpec{}, err
		}
		return ReasoningSpec{Requested: true, Level: level, Budget: budgetForLevel(level), Source: source}, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return ReasoningSpec{}, invalid("invalid_parameter", source, "The thinking parameter must be an object, boolean, or string.")
	}
	return parseReasoningFields(object, source, true)
}

func parseReasoningFields(object map[string]json.RawMessage, source string, thinking bool) (ReasoningSpec, error) {
	level := ReasoningLevel("")
	enabled := false
	var err error
	for _, name := range []string{"effort", "level", "reasoning_effort"} {
		if raw, ok := object[name]; ok && !isNull(raw) {
			value, valid := stringValueOK(raw)
			if !valid {
				return ReasoningSpec{}, invalid("invalid_parameter", source+"."+name, "The reasoning level must be a string.")
			}
			level, err = parseFlexibleLevel(value, source+"."+name)
			if err != nil {
				return ReasoningSpec{}, err
			}
			break
		}
	}
	if rawType, ok := object["type"]; ok && !isNull(rawType) {
		typeName, valid := stringValueOK(rawType)
		if !valid || (typeName != "enabled" && typeName != "disabled") {
			return ReasoningSpec{}, invalid("invalid_parameter", source+".type", "The reasoning type must be enabled or disabled.")
		}
		if typeName == "disabled" {
			level = ReasoningNone
		}
		if typeName == "enabled" && level == "" {
			enabled = true
		}
	}
	budget, hasBudget, err := readBudget(object, source)
	if err != nil {
		return ReasoningSpec{}, err
	}
	if level == "" && hasBudget {
		level = levelForBudget(budget)
	}
	if level == "" && enabled {
		level = ReasoningAuto
	}
	if level == "" {
		if thinking {
			return ReasoningSpec{}, invalid("invalid_parameter", source, "The thinking parameter must include a type, effort, level, or budget_tokens.")
		}
		return ReasoningSpec{}, invalid("invalid_parameter", source, "The reasoning parameter must include an effort or budget_tokens.")
	}
	if !hasBudget {
		budget = budgetForLevel(level)
	}
	return ReasoningSpec{Requested: true, Level: level, Budget: budget, HasBudget: hasBudget, Source: source}, nil
}

func readBudget(object map[string]json.RawMessage, source string) (int, bool, error) {
	for _, name := range []string{"budget_tokens", "budget"} {
		raw, ok := object[name]
		if !ok || isNull(raw) {
			continue
		}
		var value int64
		if json.Unmarshal(raw, &value) != nil || value < 0 || value > math.MaxInt32 {
			return 0, false, invalid("invalid_parameter", source+"."+name, "The reasoning budget must be a non-negative integer.")
		}
		return int(value), true, nil
	}
	return 0, false, nil
}

func reasoningSpecsConflict(left, right ReasoningSpec) bool {
	if left.Level != right.Level {
		return true
	}
	return left.HasBudget && right.HasBudget && left.Budget != right.Budget
}

func parseFlexibleLevel(value, param string) (ReasoningLevel, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if level, ok := reasoningAliases[trimmed]; ok {
		return level, nil
	}
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n\x00") || len(trimmed) > 64 {
		return "", invalid("invalid_parameter", param, "The reasoning level must be a non-empty string.")
	}
	return ReasoningLevel(trimmed), nil
}

func availableLevels(profile ReasoningProfile) []ReasoningLevel {
	levels := append([]ReasoningLevel(nil), profile.Levels...)
	if len(levels) == 0 {
		levels = []ReasoningLevel{ReasoningNone, ReasoningAuto, ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningMax}
	}
	seen := make(map[ReasoningLevel]struct{}, len(levels))
	result := make([]ReasoningLevel, 0, len(levels))
	for _, level := range levels {
		if _, ok := reasoningBudgets[level]; !ok || level == ReasoningNone && !profile.ZeroAllowed {
			continue
		}
		if _, ok := seen[level]; ok {
			continue
		}
		seen[level] = struct{}{}
		result = append(result, level)
	}
	sort.SliceStable(result, func(i, j int) bool { return budgetOrder(result[i]) < budgetOrder(result[j]) })
	return result
}

func nearestLevel(requested ReasoningLevel, requestedBudget int, levels []ReasoningLevel, profile ReasoningProfile) ReasoningLevel {
	if requested == ReasoningNone && profile.ZeroAllowed {
		for _, level := range levels {
			if level == ReasoningNone {
				return level
			}
		}
	}
	// Auto is a dynamic sentinel, not a fixed budget. When the caller
	// explicitly asked for auto, return it directly if the profile offers
	// it; mapping it to none via budget distance (-1 vs 0) was a
	// regression that collapsed auto -> none under nearest-neighbor.
	if requested == ReasoningAuto {
		for _, level := range levels {
			if level == ReasoningAuto {
				return ReasoningAuto
			}
		}
	}
	best := ReasoningLevel("")
	bestDistance := int64(math.MaxInt64)
	for _, level := range levels {
		budget := budgetForLevel(level)
		if level == ReasoningAuto {
			budget = requestedBudget
			if budget < 0 {
				budget = 0
			}
		}
		if profile.MaxBudget > 0 && budget > profile.MaxBudget {
			continue
		}
		if profile.MinBudget > 0 && level != ReasoningNone && level != ReasoningAuto && budget < profile.MinBudget {
			continue
		}
		distance := int64(absInt(budget - requestedBudget))
		tieBreak := level == requested && best != requested
		if !tieBreak && best == ReasoningAuto && level != ReasoningAuto {
			tieBreak = true
		}
		if !tieBreak && level != ReasoningAuto && best != ReasoningAuto {
			tieBreak = budget < budgetForLevel(best)
		}
		if distance < bestDistance || distance == bestDistance && tieBreak {
			best, bestDistance = level, distance
		}
	}
	return best
}

func clampBudget(value int, profile ReasoningProfile) int {
	if value < 0 {
		return -1
	}
	if profile.MinBudget > 0 && value < profile.MinBudget {
		value = profile.MinBudget
	}
	if profile.MaxBudget > 0 && value > profile.MaxBudget {
		value = profile.MaxBudget
	}
	return value
}

func levelForBudget(value int) ReasoningLevel {
	if value == -1 {
		return ReasoningAuto
	}
	best := ReasoningNone
	bestDistance := int64(math.MaxInt64)
	for level, budget := range reasoningBudgets {
		if level == ReasoningAuto {
			continue
		}
		distance := int64(absInt(value - budget))
		if distance < bestDistance || distance == bestDistance && budget < budgetForLevel(best) {
			best, bestDistance = level, distance
		}
	}
	return best
}

func budgetForLevel(level ReasoningLevel) int {
	return reasoningBudgets[level]
}

func budgetOrder(level ReasoningLevel) int {
	budget := budgetForLevel(level)
	if level == ReasoningAuto {
		return 1
	}
	return budget + 1
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func stringValueOK(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
