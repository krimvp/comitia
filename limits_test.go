package comitia

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestContentLimitsRejectOversizedAndInvalidUTF8Writes(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	tok, cid := f.admin.Token, f.context.ID
	send := func(key, body string) error {
		_, err := f.svc.AppendMessage(ctx, tok, cid, MessageInput{DedupeKey: key, Body: body})
		return err
	}
	if err := send("max", strings.Repeat("x", MaxMessageBodyBytes)); err != nil {
		t.Fatalf("maximum body rejected: %v", err)
	}
	for name, err := range map[string]error{
		"message body size":  send("big", strings.Repeat("x", MaxMessageBodyBytes+1)),
		"message body utf8":  send("utf8", "bad \xff\xfe utf8"),
		"task title size":    f.createTask(TaskInput{Title: strings.Repeat("t", MaxTaskTitleBytes+1)}),
		"task title utf8":    f.createTask(TaskInput{Title: "bad \xff"}),
		"task body size":     f.createTask(TaskInput{Title: "t", Body: strings.Repeat("b", MaxTaskBodyBytes+1)}),
		"task body utf8":     f.createTask(TaskInput{Title: "t", Body: "bad \xfe"}),
		"reference kind":     f.addReference(strings.Repeat("k", MaxReferenceKindBytes+1), "https://example.test"),
		"reference uri size": f.addReference("doc", "https://example.test/"+strings.Repeat("u", MaxReferenceURIBytes)),
		"reference uri utf8": f.addReference("doc", "https://example.test/\xff"),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err=%v, want invalid", name, err)
		}
	}
	if err := f.createTask(TaskInput{Title: strings.Repeat("t", MaxTaskTitleBytes), Body: strings.Repeat("b", MaxTaskBodyBytes)}); err != nil {
		t.Fatalf("maximum task rejected: %v", err)
	}
	if err := f.addReference(strings.Repeat("k", MaxReferenceKindBytes), strings.Repeat("u", MaxReferenceURIBytes)); err != nil {
		t.Fatalf("maximum reference rejected: %v", err)
	}
	info, err := f.svc.ServerInfo(ctx, tok)
	if err != nil || info.Limits == nil || *info.Limits != *contentLimits() || info.Limits.MessageBodyBytes != MaxMessageBodyBytes {
		t.Fatalf("server_info limits=%+v err=%v", info.Limits, err)
	}
}

func TestLegacyOversizedRowsStayReadable(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	tok, cid := f.admin.Token, f.context.ID
	big := strings.Repeat("x", 2*MaxMessageBodyBytes)
	task := Task{ID: id(), ContextID: cid, Title: strings.Repeat("t", 2*MaxTaskTitleBytes), Body: big, Status: TaskPending, CreatedAt: *f.now, UpdatedAt: *f.now}
	err := f.store.Update(ctx, func(w WriteStore) error {
		if _, err := w.PutMessage(Message{ID: id(), ContextID: cid, SessionID: f.admin.Session.ID, DedupeKey: "legacy", Body: big, CreatedAt: *f.now}); err != nil {
			return err
		}
		return w.PutTask(task)
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.ReplayMessagesPage(ctx, tok, cid, 0, 10, MessageFilters{MaxBytes: 1024})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Body != big {
		t.Fatalf("legacy message replay: n=%d err=%v", len(page.Messages), err)
	}
	if got, err := f.svc.GetTask(ctx, tok, task.ID); err != nil || got.Body != big || got.Title != task.Title {
		t.Fatalf("legacy task get: err=%v", err)
	}
	if got, err := f.svc.ListTasks(ctx, tok, cid, "", 10); err != nil || len(got) != 1 || got[0].Body != big {
		t.Fatalf("legacy task list: n=%d err=%v", len(got), err)
	}
}

func (f fixture) createTask(in TaskInput) error {
	_, err := f.svc.CreateTask(context.Background(), f.admin.Token, f.context.ID, in)
	return err
}

func (f fixture) addReference(kind, uri string) error {
	_, err := f.svc.AddReference(context.Background(), f.admin.Token, AddReferenceInput{ContextID: f.context.ID, Kind: kind, URI: uri})
	return err
}
