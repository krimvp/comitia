package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/mergegate"
	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

type gateFixture struct {
	f        fixture
	artifact string
	sha      string
	request  model.Message
}

func newGateFixture(t *testing.T, responders ...string) gateFixture {
	t.Helper()
	f := setup(t, ":memory:")
	t.Cleanup(func() { f.store.Close() })
	sha := strings.Repeat("a", 40)
	g := gateFixture{f: f, sha: sha, artifact: "pr:owner/repo#1@" + sha}
	g.request = g.post(t, f.admin, "request", model.RequestSchema, "", schema.RequestPayload{Operation: "review", Instructions: "Review exact head", ArtifactRefs: []string{g.artifact}, ExpectedResponders: append([]string{f.admin.Session.ID}, responders...), Deadline: f.now.Add(time.Hour).Format(time.RFC3339Nano)})
	return g
}

func (g gateFixture) post(t *testing.T, c model.Credential, key, schema, reply string, payload any) model.Message {
	t.Helper()
	m, err := g.f.svc.AppendMessage(context.Background(), c.Token, g.f.context.ID, protocolInput(key, schema, reply, payload))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (g gateFixture) verdict(t *testing.T, c model.Credential, key string, v schema.ApprovalVerdict) model.Message {
	t.Helper()
	n := 0
	if v == schema.ApprovalChangesRequested {
		n = 1
	}
	return g.post(t, c, key, model.ApprovalSchema, g.request.ID, schema.ApprovalPayload{ArtifactRefs: []string{g.artifact}, Verdict: v, Checks: []string{"race"}, BlockingCount: &n})
}

func (g gateFixture) certificate(t *testing.T, approvals ...model.Message) model.Message {
	t.Helper()
	ids := []string{}
	for _, a := range approvals {
		ids = append(ids, a.ID)
	}
	return g.post(t, g.f.admin, "certificate", model.MergeCertificateSchema, g.request.ID, schema.MergeCertificatePayload{PolicyID: "policy", ArtifactRef: g.artifact, HeadSHA: g.sha, ApprovalIDs: ids, IssuedAt: g.f.now.Format(time.RFC3339Nano), ExpiresAt: g.f.now.Add(time.Minute).Format(time.RFC3339Nano), ConsumptionKey: "merge-1"})
}

func (g gateFixture) policy(reviewers map[string]string) mergegate.MergePolicy {
	return mergegate.MergePolicy{ID: "policy", IssuerSessionID: g.f.admin.Session.ID, RequesterSessionID: g.f.admin.Session.ID, Reviewers: reviewers, Checks: []string{"race"}}
}

// One session as requester, issuer and both reviewers, with a later
// changes-requested verdict before issuance: accepted unless a policy opts in.
func TestMergeGateOptionalReviewerPolicy(t *testing.T) {
	g := newGateFixture(t)
	admin := g.f.admin
	approval := g.verdict(t, admin, "approve", schema.ApprovalApproved)
	changes := g.verdict(t, admin, "changes", schema.ApprovalChangesRequested)
	cert := g.certificate(t, approval)
	policy := g.policy(map[string]string{"security": admin.Session.ID, "correctness": admin.Session.ID})
	approvals := []model.Message{approval}
	if err := mergegate.VerifyMergeCertificate(cert, g.request, approvals, policy, g.artifact, *g.f.now); err != nil {
		t.Fatalf("default path changed: %v", err)
	}
	if err := mergegate.VerifyMergeCertificateWithOptions(cert, g.request, approvals, policy, g.artifact, *g.f.now, mergegate.MergeVerifyOptions{}); err != nil {
		t.Fatalf("zero options changed: %v", err)
	}
	for name, tc := range map[string]struct {
		opts mergegate.MergeVerifyOptions
		want string
	}{
		"distinct":   {mergegate.MergeVerifyOptions{DistinctReviewers: true}, `one session fills roles "correctness" and "security"`},
		"requester":  {mergegate.MergeVerifyOptions{ForbidRequesterReview: true}, "requester session must not act as reviewer"},
		"issuer":     {mergegate.MergeVerifyOptions{ForbidIssuerReview: true}, "issuer session must not act as reviewer"},
		"superseded": {mergegate.MergeVerifyOptions{Replies: []model.Message{approval, changes}}, "superseded by a later changes-requested verdict"},
		"incomplete": {mergegate.MergeVerifyOptions{Replies: []model.Message{changes}}, "replies must include every certificate approval"},
		"unrelated":  {mergegate.MergeVerifyOptions{Replies: []model.Message{approval, cert}}, "replies must be approval.v1 replies"},
	} {
		t.Run(name, func(t *testing.T) {
			err := mergegate.VerifyMergeCertificateWithOptions(cert, g.request, approvals, policy, g.artifact, *g.f.now, tc.opts)
			if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
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
	g.request = g.post(t, f.admin, "request", model.RequestSchema, "", schema.RequestPayload{Operation: "review", Instructions: "Review exact head", ArtifactRefs: []string{g.artifact}, ExpectedResponders: []string{security.Session.ID, correctness.Session.ID, bystander.Session.ID}, Deadline: f.now.Add(time.Hour).Format(time.RFC3339Nano)})
	earlier := g.verdict(t, security, "security-changes", schema.ApprovalChangesRequested)
	a1 := g.verdict(t, security, "security-approve", schema.ApprovalApproved)
	a2 := g.verdict(t, correctness, "correctness-approve", schema.ApprovalApproved)
	uncounted := g.verdict(t, bystander, "bystander-changes", schema.ApprovalChangesRequested)
	cert := g.certificate(t, a1, a2)
	*f.now = f.now.Add(time.Second)
	late := g.verdict(t, correctness, "correctness-late", schema.ApprovalChangesRequested)
	policy := g.policy(map[string]string{"security": security.Session.ID, "correctness": correctness.Session.ID})
	strict := mergegate.MergeVerifyOptions{DistinctReviewers: true, ForbidRequesterReview: true, ForbidIssuerReview: true, Replies: []model.Message{earlier, a1, a2, uncounted, late}}
	if err := mergegate.VerifyMergeCertificateWithOptions(cert, g.request, []model.Message{a1, a2}, policy, g.artifact, *f.now, strict); err != nil {
		t.Fatalf("verdicts before the counted approval, from uncounted sessions or after issuance must not invalidate: %v", err)
	}
}

func TestAuthoritativeMergeReceipt(t *testing.T) {
	g := newGateFixture(t)
	merger := messageWorker(t, g.f, "merger", g.f.context.ID)
	bystander := messageWorker(t, g.f, "bystander", g.f.context.ID)
	cert := g.certificate(t, g.verdict(t, g.f.admin, "approve", schema.ApprovalApproved))
	receipt := func(c model.Credential, key, status string) model.Message {
		p := schema.MergeReceiptPayload{OperationID: "merge-1", Status: status, HeadSHA: g.sha, EvidenceRefs: []string{"github:merge-1"}}
		if status == "merged" {
			p.MergeSHA = strings.Repeat("c", 40)
		}
		return g.post(t, c, key, model.MergeReceiptSchema, cert.ID, p)
	}
	if _, err := mergegate.AuthoritativeMergeReceipt(cert, nil, merger.Session.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("no receipts: %v", err)
	}
	// Append stays open to any writer; only the assigned merger's receipt counts.
	squat := receipt(bystander, "squat", "not_merged")
	merged := receipt(merger, "merged", "merged")
	again := receipt(merger, "merged-again", "merged")
	got, err := mergegate.AuthoritativeMergeReceipt(cert, []model.Message{again, squat, merged}, merger.Session.ID)
	if err != nil || got.ID != merged.ID {
		t.Fatalf("authoritative receipt: %v %v", got.ID, err)
	}
	if _, err := mergegate.AuthoritativeMergeReceipt(cert, []model.Message{squat}, merger.Session.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("bystander receipt counted: %v", err)
	}
	conflict := receipt(merger, "conflict", "not_merged")
	if _, err := mergegate.AuthoritativeMergeReceipt(cert, []model.Message{merged, conflict}, merger.Session.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("conflicting merger receipts: %v", err)
	}
}
