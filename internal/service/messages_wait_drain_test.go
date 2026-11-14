package service

import (
	"context"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

func TestEndWaitsReturnsTimeoutResultsAfterOneRead(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	waited := make(chan error, 1)
	go func() {
		messages, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 30*time.Second)
		if err == nil && len(messages) != 0 {
			err = model.Invalidf("messages=%v", messages)
		}
		waited <- err
	}()
	time.Sleep(50 * time.Millisecond)
	f.svc.EndWaits()
	f.svc.EndWaits()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait was not ended")
	}
	m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "after", Body: "kept"})
	if err != nil {
		t.Fatal(err)
	}
	// Later waits still perform their read, so available matches are not lost.
	messages, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 30*time.Second)
	if err != nil || len(messages) != 1 || messages[0].ID != m.ID {
		t.Fatalf("messages=%v err=%v", messages, err)
	}
	page, err := f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, m.Sequence, 30*time.Second)
	if err != nil || len(page.Messages) != 0 || page.NextAfter != m.Sequence {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
