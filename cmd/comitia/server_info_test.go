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
	for _, mode := range []string{"embedded", "remote"} {
		args := []string{"--db", db, "--credential", owner.Token}
		if mode == "remote" {
			args = base
		}
		status := command(t, append(args, "storage-status")...)
		if status["write_ready"] != true || status["write_attempts"].(float64) < 1 {
			t.Fatalf("%s status=%v", mode, status)
		}
		if err := commandErr(t, append(append([]string{}, args[:len(args)-1]...), "invalid-secret", "storage-status")...); !errors.Is(err, comitia.ErrUnauthenticated) {
			t.Fatalf("%s auth=%v", mode, err)
		}
	}
	principal, err := svc.CreatePrincipal(context.Background(), owner.Token, "non-admin")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := svc.MintSession(context.Background(), owner.Token, principal.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range [][]string{{"--db", db}, {"--server-url", ts.URL}} {
		if err := commandErr(t, append(target, "--credential", reader.Token, "storage-status")...); !errors.Is(err, comitia.ErrForbidden) {
			t.Fatalf("non-admin auth=%v", err)
		}
	}
	for _, args := range [][]string{{"task-create", "--context-id", "unused"}, {"task-list", "--context-id", "unused", "--limit", "0"}, {"task-get"}} {
		err := commandErr(t, append(base, args...)...)
		if !errors.Is(err, comitia.ErrInvalid) || (!strings.Contains(err.Error(), "title") && !strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "task-id")) || strings.Contains(err.Error(), owner.Token) {
			t.Fatalf("validation=%v", err)
		}
	}
}
