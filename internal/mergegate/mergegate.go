package mergegate

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/schema"
)

// MergePolicy is launcher/gate-owned configuration, never supplied by a reviewer.
// Reviewers maps required roles to authenticated sessions. Callers must reconcile
// later invalidations, one-shot reservations and GitHub state outside Comitia.
type MergePolicy struct {
	ID                 string
	IssuerSessionID    string
	RequesterSessionID string
	Reviewers          map[string]string
	Checks             []string
}

// MergeVerifyOptions are opt-in gate rules; the zero value adds none.
// Replies, when non-nil, must hold every approval.v1 reply to the request the
// gate read (at least through issued_at); a changes-requested verdict on the
// current artifact from a counted reviewer, later than that reviewer's last
// counted approval and not after issued_at, fails verification.
type MergeVerifyOptions struct {
	DistinctReviewers     bool
	ForbidRequesterReview bool
	ForbidIssuerReview    bool
	Replies               []model.Message
}

// VerifyMergeCertificate checks a snapshot, not GitHub or one-shot consumption.
// Inputs must come from authenticated Comitia reads, not arbitrary JSON files.
func VerifyMergeCertificate(cert, request model.Message, approvals []model.Message, policy MergePolicy, currentArtifact string, now time.Time) error {
	return VerifyMergeCertificateWithOptions(cert, request, approvals, policy, currentArtifact, now, MergeVerifyOptions{})
}

// VerifyMergeCertificateWithOptions is VerifyMergeCertificate plus opts.
func VerifyMergeCertificateWithOptions(cert, request model.Message, approvals []model.Message, policy MergePolicy, currentArtifact string, now time.Time, opts MergeVerifyOptions) error {
	if cert.Envelope == nil || cert.Envelope.Schema != model.MergeCertificateSchema || request.Envelope == nil || request.Envelope.Schema != model.RequestSchema {
		return model.Invalidf("certificate and request schemas required")
	}
	if err := schema.ValidMergeCertificate(cert.Envelope.Payload); err != nil {
		return err
	}
	if err := schema.ValidRequest(request.Envelope.Payload); err != nil {
		return err
	}
	var p schema.MergeCertificatePayload
	_ = json.Unmarshal(cert.Envelope.Payload, &p)
	var req schema.RequestPayload
	_ = json.Unmarshal(request.Envelope.Payload, &req)
	if policy.ID == "" || policy.IssuerSessionID == "" || policy.RequesterSessionID == "" || len(policy.Reviewers) == 0 || !schema.NonEmpty(policy.Checks) {
		return model.Invalidf("authenticated issuer, requester, reviewer roles and checks required")
	}
	if cert.SessionID != policy.IssuerSessionID || request.SessionID != policy.RequesterSessionID {
		return model.ErrForbidden
	}
	if cert.ContextID != request.ContextID || cert.Envelope.ReplyTo != request.ID || cert.Envelope.TaskID != request.Envelope.TaskID || req.Operation != "review" || p.PolicyID != policy.ID || p.ArtifactRef != currentArtifact || !slices.Contains(req.ArtifactRefs, p.ArtifactRef) {
		return model.Invalidf("certificate correlation, policy or current artifact mismatch")
	}
	issued, _ := schema.ProtocolTime(p.IssuedAt)
	expires, _ := schema.ProtocolTime(p.ExpiresAt)
	deadline, _ := schema.ProtocolTime(req.Deadline)
	if issued.Before(request.CreatedAt) || issued.After(cert.CreatedAt) || cert.CreatedAt.After(now) || !expires.After(now) || !deadline.After(now) {
		return model.Invalidf("certificate or request is stale or future-dated")
	}
	if len(approvals) != len(p.ApprovalIDs) {
		return model.Invalidf("complete approval snapshot required")
	}
	if err := checkReviewerSeparation(approvals, policy, opts); err != nil {
		return err
	}
	seen := map[string]bool{}
	reviewers := map[string]bool{}
	lastApproval := map[string]model.Message{}
	for _, approval := range approvals {
		if seen[approval.ID] || !slices.Contains(p.ApprovalIDs, approval.ID) {
			return model.Invalidf("approval ID mismatch")
		}
		seen[approval.ID] = true
		if approval.ContextID != cert.ContextID || approval.Envelope == nil || approval.Envelope.Schema != model.ApprovalSchema || approval.Envelope.ReplyTo != request.ID || approval.Envelope.TaskID != request.Envelope.TaskID || approval.CreatedAt.Before(request.CreatedAt) || approval.CreatedAt.After(issued) || !slices.Contains(req.ExpectedResponders, approval.SessionID) {
			return model.Invalidf("approval correlation or freshness mismatch")
		}
		if err := schema.ValidApproval(approval.Envelope.Payload); err != nil {
			return err
		}
		var verdict schema.ApprovalPayload
		_ = json.Unmarshal(approval.Envelope.Payload, &verdict)
		if verdict.Verdict != schema.ApprovalApproved || !slices.Contains(verdict.ArtifactRefs, currentArtifact) {
			return model.Invalidf("approval does not approve current artifact")
		}
		for _, check := range policy.Checks {
			if !slices.Contains(verdict.Checks, check) {
				return model.Invalidf("approval missing required check")
			}
		}
		reviewers[approval.SessionID] = true
		if last, ok := lastApproval[approval.SessionID]; !ok || messageAfter(approval, last) {
			lastApproval[approval.SessionID] = approval
		}
	}
	for role, sid := range policy.Reviewers {
		if role == "" || sid == "" || !reviewers[sid] {
			return model.ErrForbidden
		}
	}
	if opts.Replies != nil {
		return checkSupersedingVerdicts(cert, request, opts.Replies, seen, lastApproval, currentArtifact, issued)
	}
	return nil
}

func checkReviewerSeparation(approvals []model.Message, policy MergePolicy, opts MergeVerifyOptions) error {
	if opts.DistinctReviewers {
		roles := map[string]string{}
		for role, sid := range policy.Reviewers {
			if other, ok := roles[sid]; ok {
				return model.Invalidf("distinct reviewers required: one session fills roles %q and %q", min(role, other), max(role, other))
			}
			roles[sid] = role
		}
	}
	sessions := []string{}
	for _, a := range approvals {
		sessions = append(sessions, a.SessionID)
	}
	for _, sid := range policy.Reviewers {
		sessions = append(sessions, sid)
	}
	for _, sid := range sessions {
		if opts.ForbidRequesterReview && sid == policy.RequesterSessionID {
			return model.Invalidf("requester session must not act as reviewer")
		}
		if opts.ForbidIssuerReview && sid == policy.IssuerSessionID {
			return model.Invalidf("certificate issuer session must not act as reviewer")
		}
	}
	return nil
}

func checkSupersedingVerdicts(cert, request model.Message, replies []model.Message, counted map[string]bool, last map[string]model.Message, currentArtifact string, issued time.Time) error {
	present := map[string]bool{}
	for _, r := range replies {
		if r.ContextID != cert.ContextID || r.Envelope == nil || r.Envelope.Schema != model.ApprovalSchema || r.Envelope.ReplyTo != request.ID || r.Envelope.TaskID != request.Envelope.TaskID {
			return model.Invalidf("replies must be approval.v1 replies to the certificate request")
		}
		present[r.ID] = true
	}
	for id := range counted {
		if !present[id] {
			return model.Invalidf("replies must include every certificate approval")
		}
	}
	for _, r := range replies {
		approval, ok := last[r.SessionID]
		if !ok || r.CreatedAt.After(issued) || !messageAfter(r, approval) {
			continue
		}
		var verdict schema.ApprovalPayload
		if json.Unmarshal(r.Envelope.Payload, &verdict) == nil && verdict.Verdict == schema.ApprovalChangesRequested && slices.Contains(verdict.ArtifactRefs, currentArtifact) {
			return model.Invalidf("approval superseded by a later changes-requested verdict from the same reviewer before issuance")
		}
	}
	return nil
}

// messageAfter orders by context sequence when both are stored, else by time.
func messageAfter(a, b model.Message) bool {
	if a.Sequence > 0 && b.Sequence > 0 {
		return a.Sequence > b.Sequence
	}
	return a.CreatedAt.After(b.CreatedAt)
}

// AuthoritativeMergeReceipt applies a fixed rule: only receipts sent by the
// gate's assigned mergerSessionID that match the certificate's reply, task,
// head and consumption key count; others are ignored. It returns the earliest
// one, ErrNotFound if none, or ErrConflict if they disagree on status/merge_sha.
func AuthoritativeMergeReceipt(cert model.Message, receipts []model.Message, mergerSessionID string) (model.Message, error) {
	if cert.Envelope == nil || cert.Envelope.Schema != model.MergeCertificateSchema || mergerSessionID == "" {
		return model.Message{}, model.Invalidf("certificate and merger session required")
	}
	var p schema.MergeCertificatePayload
	_ = json.Unmarshal(cert.Envelope.Payload, &p)
	var chosen model.Message
	var first schema.MergeReceiptPayload
	found := false
	for _, m := range receipts {
		if m.SessionID != mergerSessionID || m.ContextID != cert.ContextID || m.Envelope == nil || m.Envelope.Schema != model.MergeReceiptSchema || m.Envelope.ReplyTo != cert.ID || m.Envelope.TaskID != cert.Envelope.TaskID || schema.ValidMergeReceipt(m.Envelope.Payload) != nil {
			continue
		}
		var r schema.MergeReceiptPayload
		_ = json.Unmarshal(m.Envelope.Payload, &r)
		if r.HeadSHA != p.HeadSHA || r.OperationID != p.ConsumptionKey {
			continue
		}
		if found && (r.Status != first.Status || r.MergeSHA != first.MergeSHA) {
			return model.Message{}, model.ErrConflict
		}
		if !found || messageAfter(chosen, m) {
			chosen, first, found = m, r, true
		}
	}
	if !found {
		return model.Message{}, model.ErrNotFound
	}
	return chosen, nil
}
