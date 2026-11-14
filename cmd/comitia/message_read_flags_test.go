package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// replay, wait and watch share one flag set; a flag that one of them would
// ignore is rejected before storage opens.
func TestMessageReadCommandsRejectInapplicableFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"wait", "--after", "16", "--limit", "1"}, "--limit applies only to replay"},
		{[]string{"watch", "--limit", "5", "--no-ack"}, "--limit applies only to replay"},
		{[]string{"replay", "--timeout-seconds", "5"}, "--timeout-seconds applies only to wait and watch"},
		{[]string{"replay", "--no-ack"}, "--no-ack applies only to watch"},
		{[]string{"wait", "--manual-ack"}, "--manual-ack applies only to watch"},
		{[]string{"replay", "--json-lines", "--no-ack"}, "--json-lines applies only to watch"},
		{[]string{"watch", "--page"}, "--page applies only to replay and wait"},
		{[]string{"watch", "--after", "3"}, "--after applies only to replay and wait"},
		{[]string{"watch", "--from-cursor"}, "--from-cursor applies only to replay and wait"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "flags.db")
			args := append([]string{"--db", db, "--server-url=", tc.args[0], "--context-id", "c"}, tc.args[1:]...)
			err := run(context.Background(), args)
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("rejected command touched storage: %v", err)
			}
		})
	}
	// Documented combinations still parse and reach storage lookup.
	for _, args := range [][]string{
		{"replay", "--after", "0", "--limit", "100", "--page", "--max-bytes", "1024", "--from-cursor=false"},
		{"wait", "--after", "3", "--timeout-seconds", "30", "--for-instance", "w", "--page"},
		{"watch", "--agent-instance", "w", "--for-instance", "w", "--timeout-seconds", "1", "--json-lines", "--manual-ack", "--max-bytes", "1024"},
	} {
		db := filepath.Join(t.TempDir(), "flags.db")
		err := run(context.Background(), append([]string{"--db", db, "--server-url=", args[0], "--context-id", "c"}, args[1:]...))
		if err == nil || !strings.Contains(err.Error(), "no database at") {
			t.Fatalf("%v: err=%v", args, err)
		}
	}
}

func TestPageHelpNamesTheReturnedKey(t *testing.T) {
	for cmd, key := range map[string]string{"task-list": "tasks", "participants": "participants", "instances": "instances", "replay": "messages", "wait": "messages"} {
		old := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr = w
		_ = run(context.Background(), []string{cmd, "--help"})
		_ = w.Close()
		os.Stderr = old
		help, _ := io.ReadAll(r)
		_ = r.Close()
		if !strings.Contains(string(help), "return "+key+", next_after and has_more") {
			t.Errorf("%s --page help: %s", cmd, help)
		}
	}
}
