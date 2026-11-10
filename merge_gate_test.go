package comitia

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type gateFixture struct {
	f        fixture
	artifact string
	sha      string
	request  Message
}

func newGateFixture(t *testing.T, responders ...string) gateFixture {
	t.Helper()
	f := setup(t, ":memory:")
	t.Cleanup(func() { f.store.Close() })
	sha := strings.Repeat("a", 40)
	g := gateFixture{f: f, sha: sha, artifact: "pr:owner/repo#1@" + sha}
	g.request = g.post(t, f.admin, "request", RequestSchema, "", RequestPayload{Operation: "review", Instructions: "Review exact head", ArtifactRefs: []string{g.artifact}, ExpectedResponders: append([]string{f.admin.Session.ID}, responders...), Deadline: f.now.Add(time.Hour).Format(time.RFC3339Nano)})
	return g
}

func (g gateFixture) post(t *testing.T, c Credential, key, schema, reply string, payload any) Message {
	t.Helper()
	m, err := g.f.svc.AppendMessage(context.Background(), c.Token, g.f.context.ID, protocolInput(key, schema, reply, payload))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (g gateFixture) verdict(t *testing.T, c Credential, key string, v ApprovalVerdict) Message {
	t.Helper()
	n := 0
	if v == ApprovalChangesRequested {
		n = 1
	}
	return g.post(t, c, key, ApprovalSchema, g.request.ID, ApprovalPayload{ArtifactRefs: []string{g.artifact}, Verdict: v, Checks: []string{"race"}, BlockingCount: &n})
}

func (g gateFixture) certificate(t *testing.T, approvals ...Message) Message {
	t.Helper()
	ids := []string{}
	for _, a := range approvals {
		ids = append(ids, a.ID)
	}
	return g.post(t, g.f.admin, "certificate", MergeCertificateSchema, g.request.ID, MergeCertificatePayload{PolicyID: "policy", ArtifactRef: g.artifact, HeadSHA: g.sha, ApprovalIDs: ids, IssuedAt: g.f.now.Format(time.RFC3339Nano), ExpiresAt: g.f.now.Add(time.Minute).Format(time.RFC3339Nano), ConsumptionKey: "merge-1"})
}

func (g gateFixture) policy(reviewers map[string]string) MergePolicy {
	return MergePolicy{ID: "policy", IssuerSessionID: g.f.admin.Session.ID, RequesterSessionID: g.f.admin.Session.ID, Reviewers: reviewers, Checks: []string{"race"}}
}

// One session as requester, issuer and both reviewers, with a later
// changes-requested verdict before issuance: accepted unless a policy opts in.
func TestMergeGateOptionalReviewerPolicy(t *testing.T) {
	g := newGateFixture(t)
	admin := g.f.admin
	approval := g.verdict(t, admin, "approve", ApprovalApproved)
	changes := g.verdict(t, admin, "changes", ApprovalChangesRequested)
	cert := g.certificate(t, approval)
	policy := g.policy(map[string]string{"security": admin.Session.ID, "correctness": admin.Session.ID})
	approvals := []Message{approval}
	if err := VerifyMergeCertificate(cert, g.request, approvals, policy, g.artifact, *g.f.now); err != nil {
		t.Fatalf("default path changed: %v", err)
	}
	if err := VerifyMergeCertificateWithOptions(cert, g.request, approvals, policy, g.artifact, *g.f.now, MergeVerifyOptions{}); err != nil {
		t.Fatalf("zero options changed: %v", err)
	}
	for name, tc := range map[string]struct {
		opts MergeVerifyOptions
		want string
	}{
		"distinct":   {MergeVerifyOptions{DistinctReviewers: true}, `one session fills roles "correctness" and "security"`},
		"requester":  {MergeVerifyOptions{ForbidRequesterReview: true}, "requester session must not act as reviewer"},
		"issuer":     {MergeVerifyOptions{ForbidIssuerReview: true}, "issuer session must not act as reviewer"},
		"superseded": {MergeVerifyOptions{Replies: []Message{approval, changes}}, "superseded by a later changes-requested verdict"},
		"incomplete": {MergeVerifyOptions{Replies: []Message{changes}}, "replies must include every certificate approval"},
		"unrelated":  {MergeVerifyOptions{Replies: []Message{approval, cert}}, "replies must be approval.v1 replies"},
	} {
		t.Run(name, func(t *testing.T) {
			err := VerifyMergeCertificateWithOptions(cert, g.request, approvals, policy, g.artifact, *g.f.now, tc.opts)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMergeGateStrictPolicyAcceptsSeparatedFreshReviews(t *testing.T) {
	f := setup(t, ":memory:")
	t.Cleanup(func() { f.store.Close() })
	security := messageWorker(t, f, "security", f.context.ID)
	correctness := messageWorker(t, f, "correctness", f.context.ID)
	bystander := messageWorker(t, f, "bystander", f.context.ID)
	sha := strings.Repeat("a", 40)
	g := gateFixture{f: f, sha: sha, artifact: "pr:owner/repo#1@" + sha}
	g.request = g.post(t, f.admin, "request", RequestSchema, "", RequestPayload{Operation: "review", Instructions: "Review exact head", ArtifactRefs: []string{g.artifact}, ExpectedResponders: []string{security.Session.ID, correctness.Session.ID, bystander.Session.ID}, Deadline: f.now.Add(time.Hour).Format(time.RFC3339Nano)})
	earlier := g.verdict(t, security, "security-changes", ApprovalChangesRequested)
	a1 := g.verdict(t, security, "security-approve", ApprovalApproved)
	a2 := g.verdict(t, correctness, "correctness-approve", ApprovalApproved)
	uncounted := g.verdict(t, bystander, "bystander-changes", ApprovalChangesRequested)
	cert := g.certificate(t, a1, a2)
	*f.now = f.now.Add(time.Second)
	late := g.verdict(t, correctness, "correctness-late", ApprovalChangesRequested)
	policy := g.policy(map[string]string{"security": security.Session.ID, "correctness": correctness.Session.ID})
	strict := MergeVerifyOptions{DistinctReviewers: true, ForbidRequesterReview: true, ForbidIssuerReview: true, Replies: []Message{earlier, a1, a2, uncounted, late}}
	if err := VerifyMergeCertificateWithOptions(cert, g.request, []Message{a1, a2}, policy, g.artifact, *f.now, strict); err != nil {
		t.Fatalf("verdicts before the counted approval, from uncounted sessions or after issuance must not invalidate: %v", err)
	}
}

func TestAuthoritativeMergeReceipt(t *testing.T) {
	g := newGateFixture(t)
	merger := messageWorker(t, g.f, "merger", g.f.context.ID)
	bystander := messageWorker(t, g.f, "bystander", g.f.context.ID)
	cert := g.certificate(t, g.verdict(t, g.f.admin, "approve", ApprovalApproved))
	receipt := func(c Credential, key, status string) Message {
		p := MergeReceiptPayload{OperationID: "merge-1", Status: status, HeadSHA: g.sha, EvidenceRefs: []string{"github:merge-1"}}
		if status == "merged" {
			p.MergeSHA = strings.Repeat("c", 40)
		}
		return g.post(t, c, key, MergeReceiptSchema, cert.ID, p)
	}
	if _, err := AuthoritativeMergeReceipt(cert, nil, merger.Session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no receipts: %v", err)
	}
	// Append stays open to any writer; only the assigned merger's receipt counts.
	squat := receipt(bystander, "squat", "not_merged")
	merged := receipt(merger, "merged", "merged")
	again := receipt(merger, "merged-again", "merged")
	got, err := AuthoritativeMergeReceipt(cert, []Message{again, squat, merged}, merger.Session.ID)
	if err != nil || got.ID != merged.ID {
		t.Fatalf("authoritative receipt: %v %v", got.ID, err)
	}
	if _, err := AuthoritativeMergeReceipt(cert, []Message{squat}, merger.Session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bystander receipt counted: %v", err)
	}
	conflict := receipt(merger, "conflict", "not_merged")
	if _, err := AuthoritativeMergeReceipt(cert, []Message{merged, conflict}, merger.Session.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting merger receipts: %v", err)
	}
}
