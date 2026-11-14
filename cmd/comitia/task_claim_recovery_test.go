package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestCLIRecoverClaim(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			t.Setenv("COMITIA_AGENT_INSTANCE", "worker")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "")
			task := command(t, append(base, "task-create", "--context-id", cid, "--title", "job")...)
			id := task["ID"].(string)
			lost := command(t, append(base, "task-claim", "--task-id", id)...)
			version := fmt.Sprint(lost["Task"].(map[string]any)["LeaseVersion"])
			for _, args := range [][]string{{"--task-id", id}, {"--lease-version", version}, {"--task-id", id, "--lease-version", version, "--agent-instance", ""}} {
				if err := commandErr(t, append(append(base, "task-recover"), args...)...); !errors.Is(err, comitia.ErrInvalid) {
					t.Fatalf("invalid request: %v", err)
				}
			}
			if err := commandErr(t, append(base, "task-recover", "--task-id", id, "--lease-version", version, "--agent-instance", "other")...); !errors.Is(err, comitia.ErrStaleClaim) {
				t.Fatalf("other instance: %v", err)
			}
			recovered := command(t, append(base, "task-recover", "--task-id", id, "--lease-version", version)...)
			if recovered["Token"] == "" || recovered["Token"] == lost["Token"] || fmt.Sprint(recovered["Task"].(map[string]any)["LeaseVersion"]) != version {
				t.Fatal("bad recovered claim")
			}
			if err := commandErr(t, append(base, "task-complete", "--task-id", id, "--lease-version", version, "--claim-token", lost["Token"].(string))...); !errors.Is(err, comitia.ErrStaleClaim) {
				t.Fatalf("lost token: %v", err)
			}
			done := command(t, append(base, "task-complete", "--task-id", id, "--lease-version", version, "--claim-token", recovered["Token"].(string))...)
			if done["Status"] != "completed" {
				t.Fatal("recovered token not usable")
			}
		})
	}
	if err := runTasks(context.Background(), config{parseOnly: true}, nil, nil, "task-recover", []string{"--task-id", "t", "--lease-version", "1"}); err != nil {
		t.Fatalf("parseOnly: %v", err)
	}
}
