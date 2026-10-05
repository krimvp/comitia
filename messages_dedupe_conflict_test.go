package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDedupeConflictIdentifiesExistingMessage(t *testing.T) {
	f := setup(t, ":memory:")
	ctx := context.Background()
	a := messageWorker(t, f, "worker", f.context.ID)
	in := MessageInput{DedupeKey: "started", Body: "private-original", AgentInstance: &AgentInstance{ID: "w1", Label: "one"}}
	original, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, in); err != nil || retry.ID != original.ID {
		t.Fatalf("exact retry: %+v %v", retry, err)
	}
	for _, tc := range []struct {
		change func(*MessageInput)
		want   []string
	}{
		{func(m *MessageInput) { m.Body = "private-changed" }, []string{"body"}},
		{func(m *MessageInput) { m.AgentInstance = &AgentInstance{ID: "w1", Label: "two"} }, []string{"agent_instance"}},
		{func(m *MessageInput) {
			m.Body = "private-changed"
			m.Envelope = &MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}
		}, []string{"body", "envelope"}},
	} {
		changed := in
		tc.change(&changed)
		_, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, changed)
		var conflict *DedupeConflictError
		if !errors.Is(err, ErrConflict) || !errors.As(err, &conflict) || conflict.MessageID != original.ID || conflict.Sequence != original.Sequence || !reflect.DeepEqual(conflict.Differs, tc.want) {
			t.Fatalf("conflict=%+v err=%v", conflict, err)
		}
		info := SafeError("send_message", err, "request")
		if info.Code != "conflict" || info.Details == nil || info.Details.MessageID != original.ID || info.Details.Sequence != original.Sequence || !strings.Contains(info.Message, original.ID) || !strings.Contains(info.String(), "get_message") || strings.Contains(info.String(), "private-") {
			t.Fatalf("safe info=%+v", info)
		}
	}
	// A different instance scope holds its own key; no cross-scope identification.
	other, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey: "started", Body: "other", AgentInstance: &AgentInstance{ID: "w2"}})
	if err != nil || other.ID == original.ID {
		t.Fatalf("instance scope: %+v %v", other, err)
	}
	b := messageWorker(t, f, "other", f.context.ID)
	if _, err := f.svc.AppendMessage(ctx, b.Token, f.context.ID, in); err != nil {
		t.Fatalf("other session key reuse: %v", err)
	}
}

func TestDedupeKeyLengthCap(t *testing.T) {
	f := setup(t, ":memory:")
	ctx := context.Background()
	a := messageWorker(t, f, "worker", f.context.ID)
	limit := strings.Repeat("k", MaxDedupeKeyBytes)
	if _, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey: limit, Body: "ok"}); err != nil {
		t.Fatalf("key at limit: %v", err)
	}
	for _, key := range []string{limit + "k", strings.Repeat("é", MaxDedupeKeyBytes/2+1), strings.Repeat("x", 20000)} {
		_, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey: key, Body: "too long"})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "at most 256 bytes") {
			t.Fatalf("len %d: %v", len(key), err)
		}
	}
	// Keys stored before the cap remain readable.
	long := strings.Repeat("e", 600)
	if err := f.store.Update(ctx, func(w WriteStore) error {
		_, err := w.PutMessage(Message{ID: id(), ContextID: f.context.ID, SessionID: a.Session.ID, DedupeKey: long, Body: "legacy", CreatedAt: f.now.UTC()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := f.svc.ReplayMessages(ctx, a.Token, f.context.ID, 0, 100)
	if err != nil || len(replay) != 2 || replay[1].DedupeKey != long || replay[1].Body != "legacy" {
		t.Fatalf("legacy key replay: %+v %v", replay, err)
	}
}
