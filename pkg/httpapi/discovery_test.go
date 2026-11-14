package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

type legacyDiscoveryAPI struct{ comitia.API }

func TestDiscoveryWireBeyondThousand(t *testing.T) {
	ctx := context.Background()
	st, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	err = st.Update(ctx, func(w comitia.WriteStore) error {
		for i := 1001; i >= 0; i-- {
			sid := fmt.Sprintf("session-%04d", i)
			tid := fmt.Sprintf("task-%04d", i)
			if err := w.PutTask(comitia.Task{ID: tid, ContextID: c.ID, Title: tid, Status: comitia.TaskPending, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			if err := w.PutSession(comitia.Session{ID: sid, PrincipalID: owner.Session.PrincipalID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, sid); err != nil {
				return err
			}
			if err := w.PutGrant(comitia.Grant{ID: "grant-" + sid, SessionID: sid, ContextID: c.ID, IssuerSessionID: owner.Session.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	legacyServer := httptest.NewServer(NewHandler(legacyDiscoveryAPI{svc}))
	defer legacyServer.Close()
	legacyClient, err := NewClient(legacyServer.URL, legacyServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if tasks, err := legacyClient.ListTasks(ctx, owner.Token, c.ID, "", 1000); err != nil || len(tasks) != 1000 {
		t.Fatalf("legacy HTTP task list: %d %v", len(tasks), err)
	}
	if participants, err := legacyClient.ListParticipants(ctx, owner.Token, c.ID, 1000); err != nil || len(participants) != 1000 {
		t.Fatalf("legacy HTTP participant list: %d %v", len(participants), err)
	}
	if _, err := legacyClient.ListTasksPage(ctx, owner.Token, c.ID, "", "", 1); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this API") {
		t.Fatalf("legacy HTTP task paging: %v", err)
	}
	if _, err := legacyClient.ListParticipantsPage(ctx, owner.Token, c.ID, "", 1); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this API") {
		t.Fatalf("legacy HTTP participant paging: %v", err)
	}
	for _, kind := range []string{"tasks", "participants"} {
		t.Run(kind, func(t *testing.T) {
			after := ""
			count := 0
			seen := map[string]bool{}
			for {
				raw, _ := json.Marshal(map[string]any{"context_id": c.ID, "limit": 1000, "page": true, "after_id": after})
				req, err := http.NewRequestWithContext(ctx, "POST", ts.URL+"/v1/"+kind+"/list", bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+owner.Token)
				req.Header.Set("Content-Type", "application/json")
				res, err := ts.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				var out map[string]json.RawMessage
				err = json.NewDecoder(res.Body).Decode(&out)
				res.Body.Close()
				if err != nil || res.StatusCode != 200 {
					t.Fatalf("response=%s status=%d err=%v", out, res.StatusCode, err)
				}
				key := "task_page"
				if kind == "participants" {
					key = "participant_page"
				}
				var p struct {
					Tasks        []comitia.Task        `json:"tasks"`
					Participants []comitia.Participant `json:"participants"`
					NextAfter    string                `json:"next_after"`
					HasMore      bool                  `json:"has_more"`
				}
				if err := json.Unmarshal(out[key], &p); err != nil {
					t.Fatalf("discovery has no continuation page: %v", err)
				}
				for _, v := range p.Tasks {
					if seen[v.ID] {
						t.Fatal("duplicate task")
					}
					seen[v.ID] = true
					count++
				}
				for _, v := range p.Participants {
					if seen[v.SessionID] {
						t.Fatal("duplicate participant")
					}
					seen[v.SessionID] = true
					count++
				}
				if !p.HasMore {
					break
				}
				if p.NextAfter == after {
					t.Fatal("cursor stalled")
				}
				after = p.NextAfter
			}
			want := 1002
			if kind == "participants" {
				want++
			}
			if count != want {
				t.Fatalf("unreachable discovery tail: count=%d want=%d", count, want)
			}
		})
	}
}
