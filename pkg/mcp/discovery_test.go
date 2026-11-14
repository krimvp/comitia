package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

type legacyDiscoveryAPI struct{ comitia.API }

func TestDiscoveryPageTools(t *testing.T) {
	for _, mode := range []string{"local", "remote", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			s, svc, owner, c := contextServer(t, mode == "remote")
			if mode == "legacy" {
				s.API = legacyDiscoveryAPI{s.API}
			}
			ctx := context.Background()
			for i := 0; i < 3; i++ {
				if _, err := svc.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: fmt.Sprint(i)}); err != nil {
					t.Fatal(err)
				}
			}
			for _, kind := range []string{"tasks", "participants"} {
				after := ""
				count := 0
				for {
					raw, _ := json.Marshal(map[string]any{"context_id": c.ID, "page": true, "after_id": after, "limit": 1})
					v, err := s.call(ctx, "list_"+kind, raw)
					if err != nil {
						t.Fatal(err)
					}
					more := false
					next := ""
					switch page := v.(type) {
					case comitia.TaskPage:
						count += len(page.Tasks)
						more = page.HasMore
						next = page.NextAfter
					case comitia.ParticipantPage:
						count += len(page.Participants)
						more = page.HasMore
						next = page.NextAfter
					default:
						t.Fatalf("page tool returned %T", v)
					}
					if !more {
						break
					}
					if next == after {
						t.Fatal("stalled")
					}
					after = next
				}
				want := 3
				if kind == "participants" {
					want = 2
				}
				if count != want {
					t.Fatalf("%s count=%d want=%d", kind, count, want)
				}
			}
			// A cursor implies a page even without page:true; legacy calls retain arrays.
			raw, _ := json.Marshal(map[string]any{"context_id": c.ID, "limit": 1})
			v, err := s.call(ctx, "list_tasks", raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := v.([]comitia.Task); !ok {
				t.Fatalf("legacy %T", v)
			}
			raw, _ = json.Marshal(map[string]any{"context_id": c.ID, "after_id": "zzzz", "limit": 1})
			v, err = s.call(ctx, "list_participants", raw)
			if err != nil {
				t.Fatal(err)
			}
			if p, ok := v.(comitia.ParticipantPage); !ok || p.HasMore || len(p.Participants) != 0 {
				t.Fatalf("empty cursor page: %+v", v)
			}
		})
	}
}
