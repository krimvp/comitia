package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

func TestServeClosesStalledClientsButKeepsLongPolls(t *testing.T) {
	old := serveTimeouts
	t.Cleanup(func() { serveTimeouts = old })
	serveTimeouts.header, serveTimeouts.read, serveTimeouts.idle = 200*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond

	store, err := sqlite.OpenSQLite(filepath.Join(t.TempDir(), "serve.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "test", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- serveListener(ctx, listener, httpapi.NewHandler(svc)) }()
	defer func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	}()
	addr := listener.Addr().String()

	for name, request := range map[string]string{
		"header": "POST /v1/server_info HTTP/1.1\r\nHost: x\r\n",
		"body":   "POST /v1/server_info HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer " + owner.Token + "\r\nContent-Length: 100\r\n\r\n{",
	} {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = io.Copy(io.Discard, conn)
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Errorf("stalled %s kept the connection open", name)
		}
		_ = conn.Close()
	}

	client, err := httpapi.NewClient("http://"+addr, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	messages, err := client.WaitMessages(ctx, owner.Token, c.ID, 0, time.Second)
	if err != nil || len(messages) != 0 || time.Since(started) < 900*time.Millisecond {
		t.Fatalf("long poll: %v %v after %v", messages, err, time.Since(started))
	}
}
