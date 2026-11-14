package httpserve

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoggedReportsMethodAndFirstStatus(t *testing.T) {
	for _, tc := range []struct {
		method, want string
		handler      http.HandlerFunc
		status       int
	}{
		{"POST", "POST", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			w.WriteHeader(http.StatusOK)
		}, http.StatusTeapot},
		{"FOO", "other", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("body")) }, http.StatusOK},
		{"GET", "GET", func(http.ResponseWriter, *http.Request) {}, http.StatusOK},
	} {
		var got Request
		Logged(tc.handler, func(q Request) { got = q }).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, "/x", nil))
		if got.Method != tc.want || got.Status != tc.status || got.R == nil || got.Header == nil {
			t.Fatalf("%s: %+v", tc.method, got)
		}
	}
}

func TestServeReturnsServeErrorOrShutsDown(t *testing.T) {
	failed := errors.New("listen failed")
	if shutdownErr, err := Serve(context.Background(), &http.Server{}, func() error { return failed }, time.Second); shutdownErr != nil || err != failed {
		t.Fatalf("shutdown=%v err=%v", shutdownErr, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler()}
	returned := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shutdownErr, err := Serve(ctx, srv, func() error { defer close(returned); return srv.Serve(listener) }, time.Second)
	select {
	case <-returned:
	default:
		t.Fatal("Serve returned before serve did")
	}
	if shutdownErr != nil || err != nil {
		t.Fatalf("shutdown=%v err=%v", shutdownErr, err)
	}
}
