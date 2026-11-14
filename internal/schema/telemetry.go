package schema

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

type executionEventPayload struct {
	RunID             string            `json:"run_id"`
	EventSequence     int64             `json:"event_sequence"`
	Kind              string            `json:"kind"`
	OccurredAt        string            `json:"occurred_at,omitempty"`
	Provider          string            `json:"provider,omitempty"`
	Model             string            `json:"model,omitempty"`
	ToolName          string            `json:"tool_name,omitempty"`
	InputTokens       *int64            `json:"input_tokens,omitempty"`
	OutputTokens      *int64            `json:"output_tokens,omitempty"`
	CachedInputTokens *int64            `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   *int64            `json:"reasoning_tokens,omitempty"`
	DurationMS        *int64            `json:"duration_ms,omitempty"`
	CostMicros        *int64            `json:"cost_micros,omitempty"`
	Currency          string            `json:"currency,omitempty"`
	CostSource        string            `json:"cost_source,omitempty"`
	Status            string            `json:"status,omitempty"`
	Dimensions        map[string]string `json:"dimensions,omitempty"`
}

func validExecutionEvent(raw json.RawMessage) error {
	var p executionEventPayload
	if err := StrictPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.RunID) == "" || p.EventSequence < 1 {
		return model.Invalidf("run_id required and event_sequence must be positive")
	}
	for _, value := range []*int64{p.InputTokens, p.OutputTokens, p.CachedInputTokens, p.ReasoningTokens, p.DurationMS, p.CostMicros} {
		if value != nil && *value < 0 {
			return model.Invalidf("token counts, duration_ms and cost_micros must be nonnegative")
		}
	}
	if p.OccurredAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, p.OccurredAt); err != nil {
			return model.Invalidf("occurred_at must be RFC3339")
		}
	}
	if p.Status != "" && p.Status != "succeeded" && p.Status != "failed" && p.Status != "cancelled" {
		return model.Invalidf("status must be succeeded, failed or cancelled")
	}
	if p.CostMicros == nil {
		if p.Currency != "" || p.CostSource != "" {
			return model.Invalidf("currency and cost_source require cost_micros")
		}
	} else if !telemetryCurrencyCode(p.Currency) || (p.CostSource != "provider_reported" && p.CostSource != "estimated" && p.CostSource != "configured") {
		return model.Invalidf("currency or cost_source invalid for cost_micros")
	}
	if len(p.Dimensions) > 32 {
		return model.Invalidf("dimensions must contain at most 32 entries")
	}
	for key, value := range p.Dimensions {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return model.Invalidf("dimensions keys and values must be nonempty")
		}
	}
	switch p.Kind {
	case "run_started":
		return nil
	case "model_usage":
		if strings.TrimSpace(p.Model) == "" || (p.InputTokens == nil && p.OutputTokens == nil && p.CachedInputTokens == nil && p.ReasoningTokens == nil && p.DurationMS == nil && p.CostMicros == nil) {
			return model.Invalidf("model required and model_usage requires a usage metric")
		}
		return nil
	case "tool_usage":
		if strings.TrimSpace(p.ToolName) == "" {
			return model.Invalidf("tool_name required")
		}
		return nil
	case "run_finished":
		if p.Status == "" {
			return model.Invalidf("status required for run_finished")
		}
		return nil
	default:
		return model.Invalidf("kind must be run_started, model_usage, tool_usage or run_finished")
	}
}

func telemetryCurrencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, c := range value {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
