package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestStrictRecipientSelection(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other := messageWorker(t, f, "other", f.context.ID)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []struct{ instances, sessions []string }{
		{[]string{"worker"}, []string{other.Session.ID}},
		{nil, nil},
		{nil, []string{f.admin.Session.ID}},
		{nil, []string{other.Session.ID}},
		{[]string{"worker"}, nil},
		{[]string{"other"}, []string{f.admin.Session.ID}},
		{[]string{"worker"}, []string{other.Session.ID, f.admin.Session.ID}},
	}
	for i, in := range inputs {
		_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprint(i), Body: "hello", AgentInstance: &AgentInstance{ID: "sender"}, Envelope: &MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, RecipientInstances: in.instances, RecipientSessionIDs: in.sessions, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	var strict MessageFilters
	if err := json.Unmarshal([]byte(`{"for_instance":"worker","strict_recipients":true}`), &strict); err != nil {
		t.Fatal(err)
	}
	for _, indexed := range []string{"", "schema", "task", "sender"} {
		t.Run(indexed, func(t *testing.T) {
			filter := strict
			switch indexed {
			case "schema":
				filter.Schema = "comitia.discussion.v1"
			case "task":
				filter.TaskID = task.ID
			case "sender":
				filter.FromInstance = "sender"
			}
			var got []int64
			var after int64
			for {
				page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, after, 1, filter)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Messages) == 0 {
					break
				}
				after = page.NextAfter
				got = append(got, after)
				if len(got) > len(inputs) {
					t.Fatal("cursor did not advance")
				}
			}
			// Task creation appends a broadcast task event at sequence 1.
			want := []int64{3, 4, 6, 8}
			if indexed == "" || indexed == "task" {
				want = append([]int64{1}, want...)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("strict sequences %v", got)
			}
			waited, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 0, filter)
			if err != nil || len(waited) != len(want) {
				t.Fatalf("wait: %v %v", waited, err)
			}
		})
	}
	legacy, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, MessageFilters{ForInstance: "worker"})
	if err != nil || len(legacy) != 6 || legacy[0].Sequence != 1 {
		t.Fatalf("legacy precedence: %v %v", legacy, err)
	}
	unfiltered, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || len(unfiltered) != 8 {
		t.Fatalf("shared log: %v %v", unfiltered, err)
	}
	if _, err := f.svc.ReplayMessages(ctx, "invalid", f.context.ID, 0, 100, strict); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("authorization: %v", err)
	}
	var missing MessageFilters
	_ = json.Unmarshal([]byte(`{"strict_recipients":true}`), &missing)
	if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, missing); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing selector: %v", err)
	}
}
