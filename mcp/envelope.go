package mcp

func envelopeSchema() map[string]any {
	return schema(map[string]any{
		"schema": str("Versioned schema ID, e.g. comitia.review-request.v1"),
		"recipient_session_ids": map[string]any{"type": "array", "items": str("Exact session ID from list_participants"), "maxItems": 64, "uniqueItems": true},
		"task_id": str("Task in this context; required for task coordination schemas"),
		"reply_to": str("Message ID in this context"),
		"payload": map[string]any{"type": "object", "description": "Payload validated by the selected schema"},
	}, "schema", "payload")
}
