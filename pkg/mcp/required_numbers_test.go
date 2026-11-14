package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

type requiredNumberAPI struct {
	comitia.API
	calls  int
	number int64
}

func (f *requiredNumberAPI) AckMessages(_ context.Context, _, _ string, sequence int64, _ ...string) error {
	f.calls++
	f.number = sequence
	return nil
}

func (f *requiredNumberAPI) RenewClaim(_ context.Context, _, _, _ string, version int64, _ time.Duration) (comitia.TaskClaim, error) {
	f.calls++
	f.number = version
	return comitia.TaskClaim{}, nil
}

func (f *requiredNumberAPI) CompleteTask(_ context.Context, _, _, _ string, version int64) (comitia.Task, error) {
	f.calls++
	f.number = version
	return comitia.Task{}, nil
}

func (f *requiredNumberAPI) ReleaseTask(_ context.Context, _, _, _ string, version int64) (comitia.Task, error) {
	f.calls++
	f.number = version
	return comitia.Task{}, nil
}

func TestRequiredNumericArguments(t *testing.T) {
	for _, tool := range []struct{ name, field, base string }{
		{"ack_messages", "sequence", `"context_id":"c"`},
		{"renew_task", "lease_version", `"task_id":"t","claim_token":"k"`},
		{"complete_task", "lease_version", `"task_id":"t","claim_token":"k"`},
		{"release_task", "lease_version", `"task_id":"t"`},
	} {
		t.Run(tool.name, func(t *testing.T) {
			for _, value := range []string{"", "null", `"0"`, "true", "0.5", "9223372036854775808", "0", "7"} {
				t.Run("value="+value, func(t *testing.T) {
					f := &requiredNumberAPI{}
					s := &Server{API: f, Credential: "secret"}
					raw := "{" + tool.base
					if value != "" {
						raw += `,"` + tool.field + `":` + value
					}
					raw += "}"
					_, err := s.call(context.Background(), tool.name, json.RawMessage(raw))
					if value == "0" || value == "7" {
						want := int64(0)
						if value == "7" {
							want = 7
						}
						if err != nil || f.calls != 1 || f.number != want {
							t.Fatalf("explicit integer: calls=%d number=%d err=%v", f.calls, f.number, err)
						}
					} else if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), tool.field) || f.calls != 0 {
						t.Fatalf("invalid integer: calls=%d err=%v", f.calls, err)
					}
				})
			}
		})
	}
}
