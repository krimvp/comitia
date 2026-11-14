package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestTaskOutcomeCommands(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprint(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			for _, action := range []string{"fail", "cancel"} {
				task := command(t, append(base, "task-create", "--context-id", cid, "--title", action)...)
				tid := task["ID"].(string)
				claim := command(t, append(base, "task-claim", "--task-id", tid)...)
				version := fmt.Sprint(claim["Task"].(map[string]any)["LeaseVersion"])
				args := []string{"task-" + action, "--task-id", tid, "--claim-token", claim["Token"].(string), "--lease-version", version, "--reason", "stopped", "--operation-id", action}
				got := command(t, append(base, args...)...)
				want := map[string]string{"fail": "failed", "cancel": "cancelled"}[action]
				if got["Status"] != want || got["outcome_reason"] != "stopped" {
					t.Fatalf("%s: %+v", action, got)
				}
				again := command(t, append(base, args...)...)
				if again["ID"] != tid {
					t.Fatal("receipt retry lost task")
				}
				listed := commandAny(t, append(base, "task-list", "--context-id", cid, "--status", want)...).([]any)
				if len(listed) != 1 {
					t.Fatalf("list: %+v", listed)
				}
			}
			pending := command(t, append(base, "task-create", "--context-id", cid, "--title", "pending")...)
			got := command(t, append(base, "task-cancel", "--task-id", pending["ID"].(string), "--lease-version", "0", "--reason", "unneeded")...)
			if got["Status"] != "cancelled" {
				t.Fatalf("pending: %+v", got)
			}
		})
	}
	for _, cmd := range []string{"task-fail", "task-cancel"} {
		if err := runTasks(context.Background(), config{parseOnly: true}, nil, nil, cmd, []string{"--task-id", "t", "--lease-version", "0", "--reason", "r", "--claim-token", "x"}); err != nil {
			t.Fatalf("parseOnly %s: %v", cmd, err)
		}
		if err := runTasks(context.Background(), config{}, nil, nil, cmd, []string{"--task-id", "t", "--reason", "r"}); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("missing version: %v", err)
		}
	}
}
