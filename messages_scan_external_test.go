package comitia_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

var _ comitia.MessageScanStore = externalScanReader{}

type externalScanStore struct {
	comitia.Store
	reads   chan int64
	scanner bool
}
type externalScanReader struct {
	comitia.ReadStore
	reads chan int64
}

func (s externalScanStore) View(ctx context.Context, fn func(comitia.ReadStore) error) error {
	return s.Store.View(ctx, func(r comitia.ReadStore) error {
		if s.scanner {
			return fn(externalScanReader{r, s.reads})
		}
		return fn(externalFallbackReader{r, s.reads})
	})
}

type externalFallbackReader struct {
	comitia.ReadStore
	reads chan int64
}

func (r externalFallbackReader) Messages(cid, sid string, after int64, limit int, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	r.reads <- after
	return r.ReadStore.Messages(cid, sid, after, limit, filters...)
}

func (r externalScanReader) MessagesScanned(cid, sid string, after int64, limit int, f comitia.MessageFilters) ([]comitia.Message, int64, error) {
	r.reads <- after
	messages, err := r.Messages(cid, sid, after, limit, f)
	scanned := after
	if err == nil && len(messages) == 0 {
		scanned = max(after, 32)
	}
	return messages, scanned, err
}

func TestExternalStoreFilteredWaitScanner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, admin, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "test", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, admin.Token, "test")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		if _, err := svc.AppendMessage(ctx, admin.Token, wc.ID, comitia.MessageInput{DedupeKey: fmt.Sprint(i), Body: "skip"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scanner := range []bool{true, false} {
		t.Run(fmt.Sprint("scanner=", scanner), func(t *testing.T) {
			waitCtx, stop := context.WithCancel(ctx)
			defer stop()
			reads := make(chan int64, 20)
			reader, err := comitia.New(comitia.Config{Store: externalScanStore{st, reads, scanner}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := reader.WaitMessages(waitCtx, admin.Token, wc.ID, 0, 3*time.Second, comitia.MessageFilters{Schema: "missing"})
				done <- err
			}()
			want := []int64{0, 32, 32}
			if !scanner {
				want = []int64{0, 0, 0}
			}
			for _, want := range want {
				select {
				case after := <-reads:
					if after != want {
						t.Fatalf("read after=%d want=%d", after, want)
					}
				case <-time.After(time.Second):
					t.Fatal("external read was not called")
				}
			}
			stop()
			<-done
		})
	}
	cursor, err := svc.MessageCursor(context.Background(), admin.Token, wc.ID)
	if err != nil || cursor != 0 {
		t.Fatalf("scan acknowledged skipped rows: %d %v", cursor, err)
	}
}
