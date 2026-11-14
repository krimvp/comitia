package service

import (
	"context"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// The pre-PR custom-store facade retains only its existing presence methods.
type reviewLegacyReader struct {
	model.SessionReadStore
	model.ContextReadStore
	model.GrantReadStore
	model.MessageReadStore
	model.TaskReadStore
}

func (reviewLegacyReader) Instances(string, time.Time) ([]model.InstancePresence, error) {
	return nil, nil
}
func (reviewLegacyReader) InstanceLastSeen(string, string, string) (time.Time, error) {
	return time.Time{}, model.ErrNotFound
}

var _ model.ReadStore = reviewLegacyReader{}

type reviewLegacyWriter struct {
	reviewLegacyReader
	model.SessionWriteStore
	model.ContextWriteStore
	model.GrantWriteStore
	model.MessageWriteStore
	model.TaskWriteStore
	model.PresenceWriteStore
}

var _ model.WriteStore = reviewLegacyWriter{}

type legacyPresenceStore struct{ model.Store }
type legacyPresenceWriter struct{ model.WriteStore }

func (s legacyPresenceStore) Update(ctx context.Context, fn func(model.WriteStore) error) error {
	return s.Store.Update(ctx, func(w model.WriteStore) error { return fn(legacyPresenceWriter{w}) })
}

func TestPresenceLabelChangeWithLegacyStore(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	other, _ := delegate(t, f, raw, "other", cid, model.PermissionRead, time.Hour)
	for _, entry := range []struct{ token, label string }{{raw, "Old"}, {other.Token, "Other"}} {
		if err := f.svc.SetInstanceResume(ctx, entry.token, cid, &model.AgentInstance{ID: "worker", Label: entry.label}, []byte(`{"keep":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.store = legacyPresenceStore{f.store}
	if _, ok := any(legacyPresenceWriter{}).(instanceLabelStore); ok {
		t.Fatal("legacy writer exposes optional label lookup")
	}
	*f.now = f.now.Add(time.Second)
	if _, err := f.svc.AppendMessage(ctx, raw, cid, model.MessageInput{DedupeKey: "label", Body: "ready", AgentInstance: &model.AgentInstance{ID: "worker", Label: "New"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := f.svc.ListInstances(ctx, raw, cid)
	if err != nil || len(rows) != 2 {
		t.Fatalf("presence: %v %v", rows, err)
	}
	for _, p := range rows {
		want := "Other"
		if p.SessionID == f.admin.Session.ID {
			want = "New"
			if !p.LastSeen.Equal(*f.now) {
				t.Fatal("changed label was throttled")
			}
		}
		if p.Label != want || string(p.Resume) != `{"keep":true}` {
			t.Fatalf("fallback changed another session or resume: %+v", p)
		}
	}
}
