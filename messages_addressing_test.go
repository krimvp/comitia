package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTopLevelRecipientsPersistenceFiltersAndDedupe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	f := setup(t, path)
	defer f.store.Close()
	ctx := context.Background()
	a := messageWorker(t, f, "a", f.context.ID)
	b := messageWorker(t, f, "b", f.context.ID)
	in := MessageInput{DedupeKey: "routed", Body: "Review this", RecipientSessionIDs: []string{b.Session.ID, a.Session.ID}, RecipientInstances: []string{"reviewer", "assistant"}}
	original := append([]string(nil), in.RecipientSessionIDs...)
	m, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.RecipientSessionIDs, original) || m.Envelope == nil || m.Envelope.Schema != "comitia.discussion.v1" || string(m.Envelope.Payload) != `{}` {
		t.Fatalf("normalization: %+v input=%v", m, in)
	}
	in.RecipientSessionIDs = []string{a.Session.ID, b.Session.ID}
	in.RecipientInstances = []string{"assistant", "reviewer"}
	retry, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, in)
	if err != nil || retry.ID != m.ID {
		t.Fatalf("reordered retry: %+v %v", retry, err)
	}
	retry, err = f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey: in.DedupeKey, Body: in.Body, Envelope: m.Envelope})
	if err != nil || retry.ID != m.ID {
		t.Fatalf("equivalent envelope retry: %+v %v", retry, err)
	}
	in.RecipientInstances = []string{"other"}
	_, err = f.svc.AppendMessage(ctx, a.Token, f.context.ID, in)
	var conflict *DedupeConflictError
	if !errors.As(err, &conflict) || conflict.MessageID != m.ID || !reflect.DeepEqual(conflict.Differs, []string{"envelope"}) {
		t.Fatalf("recipient conflict: %v", err)
	}
	broadcast, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey: "broadcast", Body: "Everyone", RecipientInstances: []string{}})
	if err != nil || broadcast.Envelope != nil {
		t.Fatalf("empty lists: %+v %v", broadcast, err)
	}
	// Session-only and instance-only routing retain the same read semantics.
	sessionOnly, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey: "session", Body: "Session", RecipientSessionIDs: []string{b.Session.ID}})
	if err != nil {
		t.Fatal(err)
	}
	instanceOnly, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey: "instance", Body: "Instance", RecipientInstances: []string{"reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetMessage(ctx, b.Token, f.context.ID, m.ID)
	if err != nil || !sameEnvelope(got.Envelope, m.Envelope) {
		t.Fatalf("readback: %+v %v", got, err)
	}
	for _, tc := range []struct {
		token, instance string
		strict          bool
		ids             []string
	}{
		{b.Token, "reviewer", true, []string{m.ID, broadcast.ID, sessionOnly.ID, instanceOnly.ID}},
		{b.Token, "other", true, []string{broadcast.ID, sessionOnly.ID}},
		{f.admin.Token, "reviewer", true, []string{broadcast.ID, instanceOnly.ID}},
		{f.admin.Token, "reviewer", false, []string{m.ID, broadcast.ID, instanceOnly.ID}},
	} {
		filters := MessageFilters{ForInstance: tc.instance, StrictRecipients: tc.strict}
		for _, wait := range []bool{false, true} {
			var messages []Message
			if wait {
				messages, err = svc.WaitMessages(ctx, tc.token, f.context.ID, 0, 0, filters)
			} else {
				messages, err = svc.ReplayMessages(ctx, tc.token, f.context.ID, 0, 100, filters)
			}
			ids := []string{}
			for _, msg := range messages {
				ids = append(ids, msg.ID)
			}
			if err != nil || !reflect.DeepEqual(ids, tc.ids) {
				t.Fatalf("filters %+v wait=%v: %v %v", filters, wait, ids, err)
			}
		}
	}
}

func TestTopLevelRecipientValidation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	outsider := messageWorker(t, f, "outsider", other.ID)
	revoked := messageWorker(t, f, "revoked", f.context.ID)
	if err = f.svc.RevokeSession(ctx, f.admin.Token, revoked.Session.ID); err != nil {
		t.Fatal(err)
	}
	_, expired, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "expired", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(2 * time.Minute)
	ids := make([]string, 65)
	for i := range ids {
		ids[i] = fmt.Sprintf("worker-%d", i)
	}
	cases := []struct {
		name                string
		sessions, instances []string
		want                error
	}{
		{"empty session", []string{""}, nil, ErrInvalid},
		{"duplicate session", []string{f.admin.Session.ID, f.admin.Session.ID}, nil, ErrInvalid},
		{"too many sessions", ids, nil, ErrInvalid},
		{"empty instance", nil, []string{""}, ErrInvalid},
		{"invalid instance", nil, []string{"has space"}, ErrInvalid},
		{"duplicate instance", nil, []string{"worker", "worker"}, ErrInvalid},
		{"too many instances", nil, ids, ErrInvalid},
		{"foreign session", []string{outsider.Session.ID}, nil, ErrForbidden},
		{"missing session", []string{"no-such-session"}, nil, ErrForbidden},
		{"revoked session", []string{revoked.Session.ID}, nil, ErrForbidden},
		{"expired read grant", []string{expired.Session.ID}, nil, ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both entry forms must return identical errors, including foreign/missing IDs.
			_, top := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: tc.name, Body: "note", RecipientSessionIDs: tc.sessions, RecipientInstances: tc.instances})
			_, nested := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: tc.name, Body: "note", Envelope: &MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`), RecipientSessionIDs: tc.sessions, RecipientInstances: tc.instances}})
			if !errors.Is(top, tc.want) || nested == nil || top.Error() != nested.Error() {
				t.Fatalf("top=%v nested=%v want=%v", top, nested, tc.want)
			}
		})
	}
	// A sender outside the destination cannot distinguish existing recipients.
	for _, sid := range []string{f.admin.Session.ID, "no-such-session"} {
		_, err = f.svc.AppendMessage(ctx, outsider.Token, f.context.ID, MessageInput{DedupeKey: "unauthorized", Body: "note", RecipientSessionIDs: []string{sid}})
		if !errors.Is(err, ErrForbidden) || err.Error() != ErrForbidden.Error() {
			t.Fatalf("unreadable destination: %v", err)
		}
	}
	for _, body := range []string{"", " "} {
		if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "blank", Body: body, RecipientInstances: []string{"worker"}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("blank body: %v", err)
		}
	}
}

func TestTopLevelRecipientsWithTypedEnvelope(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	env := &MessageEnvelope{Schema: "comitia.finding.v1", Payload: json.RawMessage(`{"severity":"info","summary":"Finding"}`), RecipientInstances: []string{"reviewer"}}
	in := MessageInput{DedupeKey: "typed", Envelope: env, RecipientSessionIDs: []string{f.admin.Session.ID}}
	m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
	if err != nil || m.Envelope.Schema != env.Schema || len(m.Envelope.RecipientSessionIDs) != 1 || len(env.RecipientSessionIDs) != 0 {
		t.Fatalf("typed addressing: %+v %v", m, err)
	}
	in.RecipientInstances = []string{"reviewer"}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous instances: %v", err)
	}
	env.RecipientSessionIDs = []string{f.admin.Session.ID}
	in.RecipientInstances = nil
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous sessions: %v", err)
	}
}

func TestTopLevelRecipientPrivacyForWriteOnlySender(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, writer, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "writer", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	var previous string
	for i, target := range []string{f.admin.Session.ID, "missing-private-target"} {
		for _, nested := range []bool{false, true} {
			in := MessageInput{DedupeKey: fmt.Sprintf("privacy-%d-%t", i, nested), Body: "note", RecipientSessionIDs: []string{target}}
			if nested {
				in.Envelope = &MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`), RecipientSessionIDs: in.RecipientSessionIDs}
				in.RecipientSessionIDs = nil
			}
			_, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, in)
			if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "recipient_session_ids[0]") || strings.Contains(err.Error(), target) {
				t.Fatalf("write-only recipient: %v", err)
			}
			if previous != "" && previous != err.Error() {
				t.Fatalf("recipient existence or entry form leak: %q != %q", previous, err)
			}
			previous = err.Error()
		}
	}
}
