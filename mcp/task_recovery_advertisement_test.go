package mcp

import (
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestAdvertisedTaskRecoveryAndFenceArguments(t *testing.T) {
	responses := serve(t, &fakeAPI{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	listed := responses[1]["result"].(map[string]any)["tools"].([]any)
	examples := map[string]map[string]any{
		"create_task":   {"context_id": "c", "title": "job", "operation_id": "create"},
		"claim_task":    {"task_id": "t", "operation_id": "claim"},
		"recover_claim": {"task_id": "t", "lease_version": float64(1), "agent_instance": map[string]any{"id": "worker"}},
		"renew_task":    {"task_id": "t", "claim_token": "k", "lease_version": float64(1), "operation_id": "renew"},
		"send_message":  {"context_id": "c", "dedupe_key": "result", "body": "done", "envelope": map[string]any{"schema": "comitia.discussion.v1", "task_id": "t", "payload": map[string]any{}}, "task_fence": map[string]any{"task_id": "t", "claim_token": "k", "lease_version": float64(1)}},
	}
	advertised := map[string]any{}
	for _, entry := range listed {
		tool := entry.(map[string]any)
		advertised[tool["name"].(string)] = tool["inputSchema"]
	}
	for _, draft := range []*jsonschema.Draft{jsonschema.Draft7, jsonschema.Draft2020} {
		t.Run(draft.String(), func(t *testing.T) {
			for name, example := range examples {
				t.Run(name, func(t *testing.T) {
					compiler := jsonschema.NewCompiler()
					compiler.DefaultDraft(draft)
					url := "https://comitia.test/" + name
					if advertised[name] == nil {
						t.Fatal("tool missing from tools/list")
					}
					if err := compiler.AddResource(url, advertised[name]); err != nil {
						t.Fatal(err)
					}
					compiled, err := compiler.Compile(url)
					if err != nil {
						t.Fatal(err)
					}
					if err = compiled.Validate(example); err != nil {
						t.Fatalf("advertised schema rejects recovery/fence input: %v", err)
					}
					legacy := map[string]any{}
					for field, value := range example {
						if field != "operation_id" && field != "task_fence" {
							legacy[field] = value
						}
					}
					if err = compiled.Validate(legacy); err != nil {
						t.Fatalf("advertised schema rejects legacy input: %v", err)
					}
					if name == "send_message" {
						for _, missing := range []string{"task_id", "claim_token", "lease_version"} {
							incomplete := map[string]any{}
							for field, value := range example {
								incomplete[field] = value
							}
							fence := map[string]any{}
							for field, value := range example["task_fence"].(map[string]any) {
								if field != missing {
									fence[field] = value
								}
							}
							incomplete["task_fence"] = fence
							if err = compiled.Validate(incomplete); err == nil {
								t.Fatalf("advertised fence allows missing %s", missing)
							}
						}
					}
				})
			}
		})
	}
}
