package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func protocolInput(key, schema, reply string, payload any) MessageInput {
	raw, _ := json.Marshal(payload)
	return MessageInput{DedupeKey: key, Envelope: &MessageEnvelope{Schema: schema, ReplyTo: reply, Payload: raw}}
}

// Descriptor examples use placeholders; materialize authenticated references
// before exercising append in generic schema/case-sensitivity tests.
func materializeProtocolExample(t *testing.T, f fixture, key string, env MessageEnvelope) MessageEnvelope {
	t.Helper()
	ctx := context.Background()
	appendMessage := func(in MessageInput) Message {
		t.Helper()
		in.Envelope.TaskID = env.TaskID
		m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	request := func() Message {
		return appendMessage(protocolInput(key+"-request", RequestSchema, "", RequestPayload{Operation: "review", Instructions: "Review current artifact", ExpectedResponders: []string{f.admin.Session.ID}, Deadline: f.now.Add(time.Hour).Format(time.RFC3339Nano)}))
	}
	switch env.Schema {
	case RequestSchema:
		var p RequestPayload
		_ = json.Unmarshal(env.Payload, &p)
		p.ExpectedResponders = []string{f.admin.Session.ID}
		env.Payload, _ = json.Marshal(p)
	case ReceiptSchema:
		env.ReplyTo = request().ID
	case MergeCertificateSchema:
		req := request()
		approval := appendMessage(protocolInput(key+"-approval", ApprovalSchema, req.ID, ApprovalPayload{ArtifactRefs: []string{"artifact:example"}, Verdict: ApprovalApproved, Checks: []string{"tests"}, BlockingCount: new(int)}))
		var p MergeCertificatePayload
		_ = json.Unmarshal(env.Payload, &p)
		p.ApprovalIDs = []string{approval.ID}
		env.Payload, _ = json.Marshal(p)
		env.ReplyTo = req.ID
	case MergeReceiptSchema:
		req := request()
		approval := appendMessage(protocolInput(key+"-approval", ApprovalSchema, req.ID, ApprovalPayload{ArtifactRefs: []string{"artifact:example"}, Verdict: ApprovalApproved, Checks: []string{"tests"}, BlockingCount: new(int)}))
		var p MergeReceiptPayload
		_ = json.Unmarshal(env.Payload, &p)
		cert := appendMessage(protocolInput(key+"-certificate", MergeCertificateSchema, req.ID, MergeCertificatePayload{PolicyID: "policy", ConsumptionKey: p.OperationID, ArtifactRef: "pr:owner/repo#1@" + p.HeadSHA, HeadSHA: p.HeadSHA, ApprovalIDs: []string{approval.ID}, IssuedAt: f.now.Format(time.RFC3339Nano), ExpiresAt: f.now.Add(time.Hour).Format(time.RFC3339Nano)}))
		env.ReplyTo = cert.ID
	}
	return env
}

func TestDurableRequestsAndControlReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protocol.db")
	f := setup(t, path)
	defer func() { f.store.Close() }()
	ctx := context.Background()
	worker := messageWorker(t, f, "worker", f.context.ID)
	impostor := messageWorker(t, f, "worker", f.context.ID)
	requestInput := protocolInput("pause", RequestSchema, "", RequestPayload{Operation: "control.pause", Instructions: "Checkpoint and pause", ExpectedResponders: []string{worker.Session.ID}, Deadline: f.now.Add(time.Minute).Format(time.RFC3339Nano)})
	request, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, requestInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetInstanceResume(ctx, worker.Token, f.context.ID, &AgentInstance{ID: "watcher"}, json.RawMessage(`{"launch_id":"launch-1","worker_instance":"worker","watcher_instance":"watcher","last_checkpoint":"pending"}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AckMessages(ctx, worker.Token, f.context.ID, request.Sequence, "watcher"); err != nil {
		t.Fatal(err)
	}
	before, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, MessageFilters{ReplyTo: request.ID, Schema: ReceiptSchema})
	if err != nil || len(before) != 0 {
		t.Fatalf("presence or watcher ack became control receipt: %v %v", before, err)
	}
	if cursor, err := f.svc.MessageCursor(ctx, worker.Token, f.context.ID, "worker"); err != nil || cursor != 0 {
		t.Fatalf("watcher moved worker cursor: %d %v", cursor, err)
	}
	receiptInput := protocolInput("pause-accepted", ReceiptSchema, request.ID, ReceiptPayload{Status: "accepted"})
	receiptInput.AgentInstance = &AgentInstance{ID: "worker"}
	if _, err := f.svc.AppendMessage(ctx, impostor.Token, f.context.ID, receiptInput); !errors.Is(err, ErrForbidden) {
		t.Fatalf("spoofed responder: %v", err)
	}
	accepted, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, receiptInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, protocolInput("missing-reply", ReceiptSchema, "", ReceiptPayload{Status: "accepted"})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uncorrelated: %v", err)
	}
	if _, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, protocolInput("wrong-reply", ReceiptSchema, accepted.ID, ReceiptPayload{Status: "accepted"})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nonrequest: %v", err)
	}
	wrongTask := protocolInput("task-mismatch", ReceiptSchema, request.ID, ReceiptPayload{Status: "accepted"})
	task, err := f.svc.CreateTask(ctx, worker.Token, f.context.ID, TaskInput{Title: "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	wrongTask.Envelope.TaskID = task.ID
	if _, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, wrongTask); !errors.Is(err, ErrInvalid) {
		t.Fatalf("task mismatch: %v", err)
	}
	complete := protocolInput("pause-completed", ReceiptSchema, request.ID, ReceiptPayload{Status: "completed", ResultRefs: []string{"checkpoint:launch-1/paused"}})
	done, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, complete)
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(time.Minute) // the deadline boundary is exclusive
	if _, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, protocolInput("late", ReceiptSchema, request.ID, ReceiptPayload{Status: "accepted"})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("late receipt: %v", err)
	}
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, protocolInput("expired-new", RequestSchema, "", RequestPayload{Operation: "control.stop", Instructions: "Stop", ExpectedResponders: []string{worker.Session.ID}, Deadline: f.now.Format(time.RFC3339Nano)})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("past deadline request: %v", err)
	}
	f.store.Close()
	f.store, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	f.svc, err = New(Config{Store: f.store, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if retried, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, complete); err != nil || retried.ID != done.ID {
		t.Fatalf("receipt restart recovery: %v %v", retried, err)
	}
	if retried, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, requestInput); err != nil || retried.ID != request.ID {
		t.Fatalf("request restart recovery: %v %v", retried, err)
	}
	receipts, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, MessageFilters{ReplyTo: request.ID, FromSession: worker.Session.ID, Schema: ReceiptSchema})
	if err != nil || len(receipts) != 2 || receipts[0].ID != accepted.ID || receipts[1].ID != done.ID {
		t.Fatalf("semantic receipt lookup: %v %v", receipts, err)
	}
	complete.Envelope.Payload = []byte(`{"status":"rejected","reason":"different"}`)
	if _, err := f.svc.AppendMessage(ctx, worker.Token, f.context.ID, complete); !errors.Is(err, ErrConflict) {
		t.Fatalf("receipt key conflict: %v", err)
	}
}

func TestProtocolShapeAndRequestReferences(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for name, raw := range map[string]string{
		"duplicate": fmt.Sprintf(`{"operation":"control.stop","instructions":"Stop","expected_responders":[%q,%q],"deadline":"2099-01-01T00:00:00Z"}`, f.admin.Session.ID, f.admin.Session.ID),
		"timestamp": fmt.Sprintf(`{"operation":"control.stop","instructions":"Stop","expected_responders":[%q],"deadline":"later"}`, f.admin.Session.ID),
		"artifact":  fmt.Sprintf(`{"operation":"review","instructions":"Review","expected_responders":[%q],"deadline":"2099-01-01T00:00:00Z","artifact_refs":["git:abc"]}`, f.admin.Session.ID),
	} {
		in := MessageInput{DedupeKey: name, Envelope: &MessageEnvelope{Schema: RequestSchema, Payload: []byte(raw)}}
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	ungrantedPrincipal, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "no-grant")
	if err != nil {
		t.Fatal(err)
	}
	ungranted, err := f.svc.MintSession(ctx, f.admin.Token, ungrantedPrincipal.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	revoked := messageWorker(t, f, "revoked", f.context.ID)
	if err := f.svc.RevokeSession(ctx, f.admin.Token, revoked.Session.ID); err != nil {
		t.Fatal(err)
	}
	for i, sid := range []string{"missing", ungranted.Session.ID, revoked.Session.ID} {
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, protocolInput(fmt.Sprintf("missing-session-%d", i), RequestSchema, "", RequestPayload{Operation: "control.stop", Instructions: "Stop", ExpectedResponders: []string{sid}, Deadline: "2099-01-01T00:00:00Z"})); !errors.Is(err, ErrForbidden) {
			t.Fatalf("request responder: %v", err)
		}
	}
	for schema, raws := range map[string][]string{
		ReceiptSchema:          {`{"status":"completed"}`, `{"status":"rejected","reason":null}`, `{"status":"accepted","result_refs":["git:abc"]}`},
		MergeCertificateSchema: {`{}`},
		MergeReceiptSchema:     {`{}`, fmt.Sprintf(`{"operation_id":"op","status":"not_merged","head_sha":%q,"merge_sha":%q,"evidence_refs":["github:observation"]}`, strings.Repeat("a", 40), strings.Repeat("b", 40))},
	} {
		for _, raw := range raws {
			if err := f.svc.messageSchemas[schema]([]byte(raw)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("shape %s %s: %v", schema, raw, err)
			}
		}
	}
}

func TestMergeCertificateAndReconciliation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	reviewer := messageWorker(t, f, "reviewer", f.context.ID)
	sha := strings.Repeat("a", 40)
	artifact := "pr:owner/repo#1@" + sha
	req, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, protocolInput("review", RequestSchema, "", RequestPayload{Operation: "review", Instructions: "Review exact head", ArtifactRefs: []string{artifact}, ExpectedResponders: []string{reviewer.Session.ID}, Deadline: f.now.Add(time.Hour).Format(time.RFC3339Nano)}))
	if err != nil {
		t.Fatal(err)
	}
	approval, err := f.svc.AppendMessage(ctx, reviewer.Token, f.context.ID, protocolInput("approval", ApprovalSchema, req.ID, ApprovalPayload{ArtifactRefs: []string{artifact}, Verdict: ApprovalApproved, Checks: []string{"race"}, BlockingCount: new(int)}))
	if err != nil {
		t.Fatal(err)
	}
	p := MergeCertificatePayload{PolicyID: "policy", ArtifactRef: artifact, HeadSHA: sha, ApprovalIDs: []string{approval.ID}, IssuedAt: f.now.Format(time.RFC3339Nano), ExpiresAt: f.now.Add(time.Minute).Format(time.RFC3339Nano), ConsumptionKey: "merge-1"}
	certInput := protocolInput("certificate", MergeCertificateSchema, req.ID, p)
	cert, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, certInput)
	if err != nil {
		t.Fatal(err)
	}
	policy := MergePolicy{ID: "policy", IssuerSessionID: f.admin.Session.ID, RequesterSessionID: f.admin.Session.ID, Reviewers: map[string]string{"reviewer": reviewer.Session.ID}, Checks: []string{"race"}}
	if err := VerifyMergeCertificate(cert, req, []Message{approval}, policy, artifact, *f.now); err != nil {
		t.Fatal(err)
	}
	clone := func(m Message) Message {
		raw, _ := json.Marshal(m)
		var out Message
		_ = json.Unmarshal(raw, &out)
		return out
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Message, *Message, *Message, *MergePolicy)
		head   string
		now    time.Time
	}{
		{"wrong-head", nil, "pr:owner/repo#1@" + strings.Repeat("b", 40), *f.now},
		{"expired", nil, artifact, f.now.Add(time.Minute)},
		{"issuer", func(c, r, a *Message, p *MergePolicy) { c.SessionID = reviewer.Session.ID }, artifact, *f.now},
		{"requester", func(c, r, a *Message, p *MergePolicy) { r.SessionID = reviewer.Session.ID }, artifact, *f.now},
		{"role", func(c, r, a *Message, p *MergePolicy) { p.Reviewers = map[string]string{"reviewer": "impostor"} }, artifact, *f.now},
		{"checks", func(c, r, a *Message, p *MergePolicy) { p.Checks = []string{"missing"} }, artifact, *f.now},
		{"reply", func(c, r, a *Message, p *MergePolicy) { a.Envelope.ReplyTo = "other" }, artifact, *f.now},
		{"approval-after-certificate", func(c, r, a *Message, p *MergePolicy) { a.CreatedAt = f.now.Add(time.Second) }, artifact, *f.now},
		{"negative-verdict", func(c, r, a *Message, p *MergePolicy) {
			n := 1
			a.Envelope.Payload, _ = json.Marshal(ApprovalPayload{ArtifactRefs: []string{artifact}, Verdict: ApprovalChangesRequested, Checks: []string{"race"}, BlockingCount: &n})
		}, artifact, *f.now},
		{"self-reported-role", func(c, r, a *Message, p *MergePolicy) {
			a.SessionID = "impostor"
			a.AgentInstance = &AgentInstance{ID: "reviewer"}
		}, artifact, *f.now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r, a := clone(cert), clone(req), clone(approval)
			pol := policy
			if tc.mutate != nil {
				tc.mutate(&c, &r, &a, &pol)
			}
			if err := VerifyMergeCertificate(c, r, []Message{a}, pol, tc.head, tc.now); err == nil {
				t.Fatal("invalid certificate passed")
			}
		})
	}
	if err := VerifyMergeCertificate(cert, req, nil, policy, artifact, *f.now); err == nil {
		t.Fatal("missing approvals accepted")
	}
	merge := MergeReceiptPayload{OperationID: p.ConsumptionKey, HeadSHA: sha, MergeSHA: strings.Repeat("c", 40), Status: "merged", EvidenceRefs: []string{"github:merge-1"}}
	input := protocolInput("merge-receipt", MergeReceiptSchema, cert.ID, merge)
	receipt, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); err != nil || !reflect.DeepEqual(retry, receipt) {
		t.Fatalf("merge reconciliation retry: %v", err)
	}
	merge.HeadSHA = strings.Repeat("b", 40)
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, protocolInput("wrong-head", MergeReceiptSchema, cert.ID, merge)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reconciled head mismatch: %v", err)
	}
	merge.HeadSHA = sha
	merge.OperationID = "other"
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, protocolInput("wrong-operation", MergeReceiptSchema, cert.ID, merge)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("operation mismatch: %v", err)
	}
	certInput.Envelope.ReplyTo = approval.ID
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "wrong-request", Envelope: certInput.Envelope}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("certificate request mismatch: %v", err)
	}
}
