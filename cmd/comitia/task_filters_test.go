package main

import (
	"errors"
	"strconv"
	"testing"

	"github.com/krimvp/comitia"
)

func TestCLITaskFilters(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			t.Setenv("COMITIA_AGENT_INSTANCE", "mine")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "")
			pending := command(t, append(base, "task-create", "--context-id", cid, "--title", "pending")...)
			live := command(t, append(base, "task-create", "--context-id", cid, "--title", "live")...)
			other := command(t, append(base, "task-create", "--context-id", cid, "--title", "other")...)
			claim := command(t, append(base, "task-claim", "--task-id", live["ID"].(string), "--open-ended")...)
			command(t, append(base, "task-claim", "--task-id", other["ID"].(string), "--open-ended", "--agent-instance", "other")...)
			sid := claim["Task"].(map[string]any)["LeaseOwnerSessionID"].(string)
			for _, tc := range []struct {
				flags []string
				count int
			}{{[]string{"--claimable"}, 1}, {[]string{"--mine"}, 1}, {[]string{"--owner-session", sid}, 2}, {[]string{"--owner-instance", "other"}, 1}, {[]string{"--mine", "--owner-instance", "other"}, 1}, {[]string{"--owner-session", "missing"}, 0}} {
				args := append(base, "task-list", "--context-id", cid)
				args = append(args, tc.flags...)
				got := commandAny(t, args...).([]any)
				if len(got) != tc.count {
					t.Fatalf("flags=%v rows=%v", tc.flags, got)
				}
				page := command(t, append(args, "--page")...)
				if len(page["tasks"].([]any)) != tc.count {
					t.Fatalf("page=%v", page)
				}
			}
			rows := commandAny(t, append(base, "task-list", "--context-id", cid, "--claimable")...).([]any)
			if rows[0].(map[string]any)["ID"] != pending["ID"] {
				t.Fatal(rows)
			}

			expired := command(t, append(base, "task-create", "--context-id", cid, "--title", "expired")...)
			command(t, append(base, "task-claim", "--task-id", expired["ID"].(string), "--lease", "1ns")...)
			got := command(t, append(base, "task-get", "--task-id", expired["ID"].(string))...)
			if got["lease_expired"] != true || got["Status"] != "leased" {
				t.Fatalf("expired get=%v", got)
			}
			rows = commandAny(t, append(base, "task-list", "--context-id", cid, "--claimable")...).([]any)
			if len(rows) != 2 || rows[1].(map[string]any)["lease_expired"] != true {
				t.Fatalf("expired list=%v", rows)
			}
			expiredPage := command(t, append(base, "task-list", "--context-id", cid, "--claimable", "--page")...)
			if len(expiredPage["tasks"].([]any)) != 2 {
				t.Fatalf("expired page=%v", expiredPage)
			}
			t.Setenv("COMITIA_AGENT_INSTANCE", "")
			sessionMine := commandAny(t, append(base, "task-list", "--context-id", cid, "--mine")...).([]any)
			if len(sessionMine) != 2 {
				t.Fatalf("session mine=%v", sessionMine)
			}
			for _, flags := range [][]string{{"--mine", "--owner-session", sid}, {"--owner-instance", "bad instance"}} {
				args := append(base, "task-list", "--context-id", cid)
				args = append(args, flags...)
				if err := commandErr(t, args...); !errors.Is(err, comitia.ErrInvalid) {
					t.Fatal(err)
				}
			}
		})
	}
}
