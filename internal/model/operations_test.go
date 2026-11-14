package model

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestSafeRecoveryInformation(t *testing.T) {
	for _, tc := range []struct {
		operation  string
		err        error
		code, hint string
	}{
		{"send_message", ErrConflict, "conflict", "dedupe key"},
		{"claim_task", ErrConflict, "conflict", "get_task"},
		{"release_task", ErrConflict, "conflict", "get_task"},
		{"renew_task", ErrStaleClaim, "stale_claim", "Token and Task.LeaseVersion"},
		{"complete_task", ErrStaleClaim, "stale_claim", "Token and Task.LeaseVersion"},
		{"send_message", errors.New("private-error"), "internal", "outcome may be uncertain"},
		{"send_message", context.DeadlineExceeded, "deadline_exceeded", "storage_status"},
		{"replay_messages", errors.New("private-error"), "internal", "smaller replay page or byte budget"},
		{"wait_messages", errors.New("private-error"), "internal", "smaller replay page or byte budget"},
	} {
		info := SafeError(tc.operation, fmt.Errorf("private-detail: %w", tc.err), "request")
		if info.Code != tc.code || !strings.Contains(info.Hint, tc.hint) || info.RequestID != "request" || strings.Contains(info.String(), "private-") {
			t.Fatalf("info=%+v", info)
		}
	}
	for _, err := range []error{ErrNotFound, ErrUnauthenticated, ErrForbidden} {
		info := SafeError("claim_task", fmt.Errorf("private-task: %w", err), "request")
		if info.Hint != "" || info.Message != err.Error() {
			t.Fatalf("private state disclosure: %+v", info)
		}
	}
}

func TestSafeDiagnosticAndRemoteCorrelation(t *testing.T) {
	id := NewRequestID()
	remote := &OperationalError{Info: ErrorInfo{Message: "internal server error", RequestID: id}, Cause: errors.New("private-error")}
	info := SafeError("send_message", remote, NewRequestID())
	if info.RequestID != id {
		t.Fatalf("remote correlation lost: %+v", info)
	}
	remote.Info.RequestID = "private-secret"
	if info := SafeError("send_message", remote, id); info.RequestID != id {
		t.Fatalf("accepted unsafe remote ID: %+v", info)
	}
	called := 0
	hook := func(d Diagnostic) {
		called++
		if d.Operation != "unknown" || d.ErrorClass != "internal" || d.RequestID != id || d.Duration != time.Second || d.Build.Revision == "" {
			t.Fatalf("diagnostic=%+v", d)
		}
	}
	ReportFailure(hook, "private-operation", remote, id, time.Second)
	ReportFailure(hook, "private-operation", ErrForbidden, id, time.Second)
	if called != 1 {
		t.Fatalf("hook called %d times", called)
	}
}

func TestBuildIdentityFallbackAndStamp(t *testing.T) {
	oldStamp := BuildStamp
	t.Cleanup(func() { BuildStamp = oldStamp })
	stamp := func(revision, dirty string) {
		BuildStamp = func() (string, string, string) { return "", revision, dirty }
	}
	stamp("", "")
	fallback := IdentityFromBuildInfo(&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "revision-from-go"}, {Key: "vcs.modified", Value: "true"}}})
	if fallback.Revision != "revision-from-go" || fallback.Dirty != "true" {
		t.Fatalf("build info lost: %+v", fallback)
	}
	if unknown := IdentityFromBuildInfo(nil); unknown.Revision != "unknown" || unknown.Dirty != "unknown" {
		t.Fatalf("unknown=%+v", unknown)
	}
	stamp("stamped-revision", "false")
	stamped := IdentityFromBuildInfo(&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "ignored"}}})
	if stamped.Revision != "stamped-revision" || stamped.Dirty != "false" {
		t.Fatalf("stamp=%+v", stamped)
	}
}

type codedStorageError int

func (codedStorageError) Error() string { return "private-storage-path" }
func (e codedStorageError) Code() int   { return int(e) }

func TestStorageFailureClassesWithoutRawDetails(t *testing.T) {
	for _, tc := range []struct {
		code  int
		class string
	}{{5, "storage_busy"}, {6, "storage_busy"}, {8, "storage_read_only"}, {10, "storage_io"}, {13, "storage_full"}, {5 + 256, "storage_busy"}, {1, "internal"}} {
		err := fmt.Errorf("private-detail: %w", codedStorageError(tc.code))
		if ErrorClass(err) != "internal" || FailureClass(err) != tc.class {
			t.Fatalf("code=%d class=%s", tc.code, FailureClass(err))
		}
		ReportFailure(func(d Diagnostic) {
			if d.ErrorClass != tc.class || strings.Contains(fmt.Sprint(d), "private-") {
				t.Fatalf("diagnostic=%+v", d)
			}
		}, "send_message", err, NewRequestID(), time.Second)
	}
}
