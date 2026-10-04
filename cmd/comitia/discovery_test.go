package main

import (
	"fmt"
	"strconv"
	"testing"
)

func TestCLIDiscoveryPages(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			for i := 0; i < 3; i++ {
				command(t, append(base, "task-create", "--context-id", cid, "--title", fmt.Sprint(i))...)
			}
			after := ""
			count := 0
			for {
				args := append(base, "task-list", "--context-id", cid, "--limit", "1", "--page", "--status", "pending")
				if after != "" {
					args = append(args, "--after-id", after)
				}
				p := command(t, args...)
				count += len(p["tasks"].([]any))
				if !p["has_more"].(bool) {
					break
				}
				next := p["next_after"].(string)
				if next == after {
					t.Fatal("stalled")
				}
				after = next
			}
			if count != 3 {
				t.Fatalf("task tail: %d", count)
			}
			p := command(t, append(base, "participants", "--context-id", cid, "--page", "--limit", "1")...)
			if len(p["participants"].([]any)) != 1 || p["has_more"] != false {
				t.Fatalf("participants: %v", p)
			}
			p = command(t, append(base, "participants", "--context-id", cid, "--after-id", p["next_after"].(string))...)
			if len(p["participants"].([]any)) != 0 || p["has_more"] != false {
				t.Fatalf("empty continuation: %v", p)
			}
		})
	}
}
