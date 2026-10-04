package comitia

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type legacyTaskAPI interface {
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	GetTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string, string, TaskStatus, int) ([]Task, error)
	ClaimTask(context.Context, string, string, time.Duration, ...AgentInstance) (TaskClaim, error)
	RenewClaim(context.Context, string, string, string, int64, time.Duration) (TaskClaim, error)
	CompleteTask(context.Context, string, string, string, int64) (Task, error)
	ReleaseTask(context.Context, string, string, string, int64) (Task, error)
}
type legacyMessageAPI interface {
	ListParticipants(context.Context, string, string, int) ([]Participant, error)
	AppendMessage(context.Context, string, string, MessageInput) (Message, error)
	ReplayMessages(context.Context, string, string, int64, int, ...MessageFilters) ([]Message, error)
	AckMessages(context.Context, string, string, int64, ...string) error
	MessageCursor(context.Context, string, string, ...string) (int64, error)
	WaitMessages(context.Context, string, string, int64, time.Duration, ...MessageFilters) ([]Message, error)
}
type legacyTaskReadStore interface {
	Task(string) (Task, error)
	Tasks(string, TaskStatus, int) ([]Task, error)
}
type legacyMessageReadStore interface {
	Message(string) (Message, error)
	Participants(string, time.Time, int) ([]Participant, error)
	MessageByDedupe(string, string, string, ...string) (Message, error)
	AckCursor(string, string, string) (int64, error)
	Messages(string, string, int64, int, ...MessageFilters) ([]Message, error)
}
type legacyAPIAdapter struct {
	ServerInfoAPI
	SessionAPI
	ContextAPI
	GrantAPI
	legacyMessageAPI
	legacyTaskAPI
	PresenceAPI
}

var _ API = legacyAPIAdapter{}

type legacyReadAdapter struct {
	SessionReadStore
	ContextReadStore
	GrantReadStore
	legacyMessageReadStore
	legacyTaskReadStore
	PresenceReadStore
}

var _ ReadStore = legacyReadAdapter{}

type legacyTaskAdapter struct{ legacyTaskAPI }

var _ TaskAPI = legacyTaskAdapter{}

type legacyMessageAdapter struct{ legacyMessageAPI }

var _ MessageAPI = legacyMessageAdapter{}

type legacyTaskStoreAdapter struct{ legacyTaskReadStore }

var _ TaskReadStore = legacyTaskStoreAdapter{}

type legacyMessageStoreAdapter struct{ legacyMessageReadStore }

var _ MessageReadStore = legacyMessageStoreAdapter{}

// Hide the optional capabilities just as an existing adapter compiled against
// the original contracts would. The concrete SQLite implementation has them.
type legacyDiscoveryStore struct{ Store }

func legacyRead(r ReadStore) legacyReadAdapter {
	return legacyReadAdapter{r, r, r, r, r, r}
}
func (s legacyDiscoveryStore) View(ctx context.Context, fn func(ReadStore) error) error {
	return s.Store.View(ctx, func(r ReadStore) error { return fn(legacyRead(r)) })
}
func legacyAPI(api API) legacyAPIAdapter {
	return legacyAPIAdapter{api, api, api, api, api, api, api}
}

func TestLegacyDiscoveryCallsAndPages(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	err := f.store.Update(ctx, func(w WriteStore) error {
		for _, tid := range []string{"t3", "t1", "t2"} {
			if err := w.PutTask(Task{ID: tid, ContextID: f.context.ID, Title: tid, Status: TaskPending, CreatedAt: *f.now, UpdatedAt: *f.now, LeaseTokenHash: "hidden"}); err != nil {
				return err
			}
		}
		for _, sid := range []string{"s3", "s1", "s2"} {
			if err := w.PutSession(Session{ID: sid, PrincipalID: f.admin.Session.PrincipalID, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}, sid); err != nil {
				return err
			}
			if err := w.PutGrant(Grant{ID: "g" + sid, SessionID: sid, ContextID: f.context.ID, IssuerSessionID: f.admin.Session.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyService, err := New(Config{Store: legacyDiscoveryStore{f.store}, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	for name, api := range map[string]API{"legacy API": legacyAPI(f.svc), "legacy store": legacyService} {
		t.Run(name, func(t *testing.T) {
			tasks, err := api.ListTasks(ctx, f.admin.Token, f.context.ID, "", 1)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("legacy task list: %v %v", tasks, err)
			}
			participants, err := api.ListParticipants(ctx, f.admin.Token, f.context.ID, 1)
			if err != nil || len(participants) != 1 {
				t.Fatalf("legacy participant list: %v %v", participants, err)
			}
			after := ""
			seen := []string{}
			for {
				p, err := ListTaskPage(ctx, api, f.admin.Token, f.context.ID, "", after, 1)
				if err != nil {
					t.Fatal(err)
				}
				for _, task := range p.Tasks {
					if task.LeaseTokenHash != "" {
						t.Fatal("hash leaked")
					}
					seen = append(seen, task.ID)
				}
				after = p.NextAfter
				if !p.HasMore {
					break
				}
			}
			if strings.Join(seen, ",") != "t1,t2,t3" {
				t.Fatalf("task fallback: %v", seen)
			}
			p, err := ListTaskPage(ctx, api, f.admin.Token, f.context.ID, "", after, 1)
			if err != nil || p.Tasks == nil || len(p.Tasks) != 0 || p.HasMore || p.NextAfter != after {
				t.Fatalf("empty task page: %+v %v", p, err)
			}
			after = ""
			ids := map[string]bool{}
			for {
				p, err := ListParticipantPage(ctx, api, f.admin.Token, f.context.ID, after, 1)
				if err != nil {
					t.Fatal(err)
				}
				for _, participant := range p.Participants {
					if ids[participant.SessionID] || participant.SessionID <= after {
						t.Fatal("participant repeated")
					}
					ids[participant.SessionID] = true
				}
				after = p.NextAfter
				if !p.HasMore {
					break
				}
			}
			if len(ids) != 4 {
				t.Fatalf("participants=%v", ids)
			}
			p2, err := ListParticipantPage(ctx, api, f.admin.Token, f.context.ID, after, 1)
			if err != nil || p2.Participants == nil || len(p2.Participants) != 0 || p2.HasMore || p2.NextAfter != after {
				t.Fatalf("empty participant page: %+v %v", p2, err)
			}
			if _, err := ListTaskPage(ctx, api, "bad", f.context.ID, "", "", 1); !errors.Is(err, ErrUnauthenticated) {
				t.Fatal(err)
			}
			if _, err := ListParticipantPage(ctx, api, "bad", f.context.ID, "", 1); !errors.Is(err, ErrUnauthenticated) {
				t.Fatal(err)
			}
			if _, err := ListTaskPage(ctx, api, f.admin.Token, f.context.ID, "", "bad", 1); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
			if _, err := ListParticipantPage(ctx, api, f.admin.Token, f.context.ID, "", 0); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	if _, ok := any(legacyAPI(f.svc)).(TaskPageAPI); ok {
		t.Fatal("fixture accidentally exposes task paging")
	}
	if _, ok := any(legacyAPI(f.svc)).(ParticipantPageAPI); ok {
		t.Fatal("fixture accidentally exposes participant paging")
	}
}

func TestLegacyDiscoveryCeilingExplicit(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	err := f.store.Update(ctx, func(w WriteStore) error {
		for i := 0; i < 1001; i++ {
			id := fmt.Sprintf("legacy-%04d", i)
			if err := w.PutTask(Task{ID: id, ContextID: f.context.ID, Title: id, Status: TaskPending, CreatedAt: *f.now, UpdatedAt: *f.now}); err != nil {
				return err
			}
			if err := w.PutSession(Session{ID: id, PrincipalID: f.admin.Session.PrincipalID, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}, id); err != nil {
				return err
			}
			if err := w.PutGrant(Grant{ID: "g" + id, SessionID: id, ContextID: f.context.ID, IssuerSessionID: f.admin.Session.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyService, err := New(Config{Store: legacyDiscoveryStore{f.store}, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	for name, api := range map[string]API{"legacy API": legacyAPI(f.svc), "legacy store": legacyService} {
		t.Run(name, func(t *testing.T) {
			if rows, err := api.ListTasks(ctx, f.admin.Token, f.context.ID, "", 1000); err != nil || len(rows) != 1000 {
				t.Fatalf("nonpage task list broke: %d %v", len(rows), err)
			}
			if rows, err := api.ListParticipants(ctx, f.admin.Token, f.context.ID, 1000); err != nil || len(rows) != 1000 {
				t.Fatalf("nonpage participant list broke: %d %v", len(rows), err)
			}
			for _, limit := range []int{1, 1000} {
				if _, err := ListTaskPage(ctx, api, f.admin.Token, f.context.ID, "", "", limit); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this") {
					t.Fatalf("task ceiling silently truncated: %v", err)
				}
				if _, err := ListParticipantPage(ctx, api, f.admin.Token, f.context.ID, "", limit); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this") {
					t.Fatalf("participant ceiling silently truncated: %v", err)
				}
			}
		})
	}
}
