package comitia

import (
	"encoding/json"
	"strings"
	"time"
)

// ExecutionEventSchema identifies runner-reported execution telemetry. Events
// use the surrounding message for context, task, authenticated session, durable
// ordering, and idempotent ingestion.
const ExecutionEventSchema = "comitia.execution-event.v1"

func validExecutionEvent(raw json.RawMessage) error {
	var p struct {
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
	if strictPayload(raw, &p) != nil || strings.TrimSpace(p.RunID) == "" || p.EventSequence < 1 {
		return ErrInvalid
	}
	for _, value := range []*int64{p.InputTokens, p.OutputTokens, p.CachedInputTokens, p.ReasoningTokens, p.DurationMS, p.CostMicros} {
		if value != nil && *value < 0 {
			return ErrInvalid
		}
	}
	if p.OccurredAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, p.OccurredAt); err != nil {
			return ErrInvalid
		}
	}
	if p.Status != "" && p.Status != "succeeded" && p.Status != "failed" && p.Status != "cancelled" {
		return ErrInvalid
	}
	if p.CostMicros == nil {
		if p.Currency != "" || p.CostSource != "" {
			return ErrInvalid
		}
	} else if !telemetryCurrencyCode(p.Currency) || (p.CostSource != "provider_reported" && p.CostSource != "estimated" && p.CostSource != "configured") {
		return ErrInvalid
	}
	if len(p.Dimensions) > 32 {
		return ErrInvalid
	}
	for key, value := range p.Dimensions {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return ErrInvalid
		}
	}
	switch p.Kind {
	case "run_started":
		return nil
	case "model_usage":
		if strings.TrimSpace(p.Model) == "" || (p.InputTokens == nil && p.OutputTokens == nil && p.CachedInputTokens == nil && p.ReasoningTokens == nil && p.DurationMS == nil && p.CostMicros == nil) {
			return ErrInvalid
		}
		return nil
	case "tool_usage":
		if strings.TrimSpace(p.ToolName) == "" {
			return ErrInvalid
		}
		return nil
	case "run_finished":
		if p.Status == "" {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
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
