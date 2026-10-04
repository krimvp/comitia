package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReplayAndWaitDefaultByteBudget(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprint(i), Body: strings.Repeat("x", 500000)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, wait := range []bool{false, true} {
		t.Run(fmt.Sprintf("wait_%v", wait), func(t *testing.T) {
			after := int64(0)
			count := 0
			for {
				var messages []Message
				var err error
				if wait {
					messages, err = f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, after, 0)
				} else {
					messages, err = f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, after, 100)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(messages) == 0 {
					break
				}
				if count == 0 && len(messages) != 2 {
					t.Fatalf("wait=%v: first page has %d messages, want 2", wait, len(messages))
				}
				for _, m := range messages {
					if m.Sequence <= after || len(m.Body) != 500000 {
						t.Fatal("lost body or cursor progress")
					}
					after = m.Sequence
					count++
				}
			}
			if count != 3 {
				t.Fatalf("wait=%v: recovered %d messages", wait, count)
			}
		})
	}
}

func TestMessagePagesEncodedBytesAndLargestProgress(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	bodies := []string{strings.Repeat("\x00<&", 400), "small", "last"}
	var expected []Message
	for i, body := range bodies {
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprintf("skip%d", i), Body: "ignored", AgentInstance: &AgentInstance{ID: "other"}}); err != nil {
			t.Fatal(err)
		}
		m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprint(i), Body: body, AgentInstance: &AgentInstance{ID: "sender"}, Envelope: &MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, m)
	}
	raw, _ := json.Marshal(expected[:2])
	for _, budget := range []int{1, len(raw) - 1, len(raw), MaxMessageBytes} {
		for _, wait := range []bool{false, true} {
			ftr := MessageFilters{MaxBytes: budget, FromInstance: "sender", Schema: "comitia.discussion.v1"}
			var page MessagePage
			var err error
			if wait {
				page, err = f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 0, ftr)
			} else {
				page, err = f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 100, ftr)
			}
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if budget >= len(raw) {
				want = 2
			}
			if budget == MaxMessageBytes {
				want = 3
			}
			if len(page.Messages) != want || page.NextAfter != expected[want-1].Sequence || page.HasMore != (want < 3) {
				t.Fatalf("budget %d wait %v: count=%d next=%d more=%v", budget, wait, len(page.Messages), page.NextAfter, page.HasMore)
			}
			encoded, _ := json.Marshal(page.Messages)
			if len(encoded) > budget && len(page.Messages) != 1 {
				t.Fatal("page exceeds budget without single-message exception")
			}
			// The budget never truncates a message or advances the persisted ack.
			if !reflect.DeepEqual(page.Messages, expected[:want]) {
				t.Fatal("message changed")
			}
			cursor, err := f.svc.MessageCursor(ctx, f.admin.Token, f.context.ID)
			if err != nil || cursor != 0 {
				t.Fatalf("cursor %d: %v", cursor, err)
			}
		}
	}
	page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 6, 100)
	if err != nil || page.NextAfter != 6 || page.HasMore || page.Messages == nil || len(page.Messages) != 0 {
		t.Fatalf("empty page: %+v %v", page, err)
	}
	for _, budget := range []int{-1, MaxMessageBytes + 1} {
		if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, MessageFilters{MaxBytes: budget}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if _, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 0, MessageFilters{MaxBytes: budget}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("wait budget %d: %v", budget, err)
		}
	}
}

type pageWaitProbe struct {
	MessageAPI
	wait   func(context.Context, time.Duration) ([]Message, error)
	replay func(context.Context) ([]Message, error)
}

func (p pageWaitProbe) WaitMessages(ctx context.Context, _, _ string, _ int64, timeout time.Duration, _ ...MessageFilters) ([]Message, error) {
	return p.wait(ctx, timeout)
}
func (p pageWaitProbe) ReplayMessages(ctx context.Context, _, _ string, _ int64, _ int, _ ...MessageFilters) ([]Message, error) {
	return p.replay(ctx)
}

func TestPageWaitBoundsLookaheadAndPreservesMatches(t *testing.T) {
	p := pageWaitProbe{
		wait: func(context.Context, time.Duration) ([]Message, error) {
			return []Message{{Sequence: 9, Body: "already read"}}, nil
		},
		replay: func(ctx context.Context) ([]Message, error) {
			if _, ok := ctx.Deadline(); !ok {
				return nil, errors.New("lookahead has no page wait deadline")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	start := time.Now()
	page, err := WaitMessagePage(context.Background(), p, "token", "context", 8, 20*time.Millisecond)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Body != "already read" || page.NextAfter != 9 || !page.HasMore {
		t.Fatalf("page lost matches at lookahead timeout: %+v %v", page, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("lookahead exceeded deadline: %s", elapsed)
	}
}

func TestPageWaitTimeoutAndParentCancellation(t *testing.T) {
	for _, ownTimeout := range []bool{false, true} {
		t.Run(fmt.Sprint(ownTimeout), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := pageWaitProbe{
				wait: func(readCtx context.Context, _ time.Duration) ([]Message, error) {
					if !ownTimeout {
						cancel()
					} else if _, ok := readCtx.Deadline(); !ok {
						return nil, errors.New("read has no page deadline")
					}
					<-readCtx.Done()
					return nil, readCtx.Err()
				},
				replay: func(context.Context) ([]Message, error) { t.Fatal("lookahead on empty result"); return nil, nil },
			}
			page, err := WaitMessagePage(ctx, p, "token", "context", 42, 20*time.Millisecond)
			if ownTimeout {
				if err != nil || page.Messages == nil || len(page.Messages) != 0 || page.NextAfter != 42 || page.HasMore {
					t.Fatalf("timeout page: %+v %v", page, err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("parent cancellation swallowed: %v", err)
			}
		})
	}
	// Timeout zero retains the authorized initial read without a new deadline.
	p := pageWaitProbe{
		wait: func(ctx context.Context, _ time.Duration) ([]Message, error) {
			if _, ok := ctx.Deadline(); ok {
				t.Fatal("timeout zero acquired deadline")
			}
			return []Message{{Sequence: 1}}, nil
		},
		replay: func(ctx context.Context) ([]Message, error) {
			if _, ok := ctx.Deadline(); ok {
				t.Fatal("timeout zero lookahead acquired deadline")
			}
			return nil, nil
		},
	}
	page, err := WaitMessagePage(context.Background(), p, "token", "context", 0, 0)
	if err != nil || len(page.Messages) != 1 || page.HasMore {
		t.Fatalf("zero timeout: %+v %v", page, err)
	}
}
