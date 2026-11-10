package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestMissingDatabaseIsNotCreatedOutsideBootstrap(t *testing.T) {
	for _, args := range [][]string{{"replay", "--context-id", "abc"}, {"server-info"}, {"mcp"}} {
		t.Run(args[0], func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "comitia.db")
			err := run(context.Background(), append([]string{"--db", db, "--server-url=", "--credential", "ct_x"}, args...))
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "no database at "+db) ||
				!strings.Contains(err.Error(), "COMITIA_SERVER_URL") || !strings.Contains(err.Error(), "--db") {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("database created: %v", err)
			}
		})
	}
}

func TestBootstrapStillCreatesDatabase(t *testing.T) {
	db := filepath.Join(t.TempDir(), "new.db")
	command(t, "--db", db, "--server-url=", "--bootstrap-credential", "once", "bootstrap")
	if _, err := os.Stat(db); err != nil {
		t.Fatal(err)
	}
}

func TestServerURLWithV1SuffixExplainsBaseURL(t *testing.T) {
	ts := httptest.NewServer(httpapi.NewHandler(nil))
	defer ts.Close()
	for _, u := range []string{ts.URL + "/v1", ts.URL + "/v1/"} {
		err := run(context.Background(), []string{"--server-url", u, "--credential", "x", "server-info"})
		if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "without /v1") {
			t.Fatalf("%s: err=%v", u, err)
		}
	}
}

func TestRemoteCallsTimeOutWhenServerNeverAnswers(t *testing.T) {
	if remoteTimeout != 70*time.Second || remoteTimeout <= comitia.MaxMessageWait {
		t.Fatalf("remote timeout %v must be the documented 70s, above the %v wait cap", remoteTimeout, comitia.MaxMessageWait)
	}
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer ts.Close()
	defer close(release)
	old := remoteTimeout
	remoteTimeout = 100 * time.Millisecond
	defer func() { remoteTimeout = old }()
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"--server-url", ts.URL, "--credential", "x", "server-info"})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("remote call never timed out")
	}
}
