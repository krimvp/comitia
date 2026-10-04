package mcp

import (
	"net"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestHTTPConfigDoesNotOfferListenerTimeout(t *testing.T) {
	if _, ok := reflect.TypeOf(HTTPConfig{}).FieldByName("ReadHeaderTimeout"); ok {
		t.Fatal("handler config offers an inert listener timeout")
	}
}

func TestHTTPEmbeddingListenerHeaderTimeout(t *testing.T) {
	srv := httptest.NewUnstartedServer(NewHTTPHandler(&stubAPI{}, HTTPConfig{}))
	srv.Config.ReadHeaderTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()
	conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("POST /mcp HTTP/1.1\r\nHost: localhost\r\nX-Slow: ")); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	if _, err := conn.Read(buf[:]); err == nil {
		t.Fatal("incomplete headers reached handler")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("listener did not enforce ReadHeaderTimeout")
	}
}
