package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

func TestServerInfoAndValidationCLI(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cli.db")
	st, err := sqlite.OpenSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(context.Background(), comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(httpapi.NewHandler(svc))
	defer ts.Close()
	base := []string{"--server-url", ts.URL, "--credential", owner.Token}
	embedded := command(t, "--db", db, "--credential", owner.Token, "server-info")
	if embedded["protocol_version"] != comitia.ProtocolVersion {
		t.Fatalf("embedded=%v", embedded)
	}
	info := command(t, append(base, "server-info")...)
	if info["version"] == "" || info["protocol_version"] != comitia.ProtocolVersion || len(info["features"].([]any)) == 0 {
		t.Fatalf("info=%v", info)
	}
	if err := commandErr(t, "--server-url", ts.URL, "--credential", "invalid-secret", "server-info"); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("auth=%v", err)
	}
	for _, args := range [][]string{{"task-create", "--context-id", "unused"}, {"task-list", "--context-id", "unused", "--limit", "0"}, {"task-get"}} {
		err := commandErr(t, append(base, args...)...)
		if !errors.Is(err, comitia.ErrInvalid) || (!strings.Contains(err.Error(), "title") && !strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "task-id")) || strings.Contains(err.Error(), owner.Token) {
			t.Fatalf("validation=%v", err)
		}
	}
}
