package comitia

import (
	"encoding/json"
	"slices"
	"time"
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

// VerifyMergeCertificate checks a snapshot, not GitHub or one-shot consumption.
// Inputs must come from authenticated Comitia reads, not arbitrary JSON files.
func VerifyMergeCertificate(cert, request Message, approvals []Message, policy MergePolicy, currentArtifact string, now time.Time) error {
	if cert.Envelope == nil || cert.Envelope.Schema != MergeCertificateSchema || request.Envelope == nil || request.Envelope.Schema != RequestSchema {
		return Invalidf("certificate and request schemas required")
	}
	if err := validMergeCertificate(cert.Envelope.Payload); err != nil {
		return err
	}
	if err := validRequest(request.Envelope.Payload); err != nil {
		return err
	}
	var p MergeCertificatePayload
	_ = json.Unmarshal(cert.Envelope.Payload, &p)
	var req RequestPayload
	_ = json.Unmarshal(request.Envelope.Payload, &req)
	if policy.ID == "" || policy.IssuerSessionID == "" || policy.RequesterSessionID == "" || len(policy.Reviewers) == 0 || !nonempty(policy.Checks) {
		return Invalidf("authenticated issuer, requester, reviewer roles and checks required")
	}
	if cert.SessionID != policy.IssuerSessionID || request.SessionID != policy.RequesterSessionID {
		return ErrForbidden
	}
	if cert.ContextID != request.ContextID || cert.Envelope.ReplyTo != request.ID || cert.Envelope.TaskID != request.Envelope.TaskID || req.Operation != "review" || p.PolicyID != policy.ID || p.ArtifactRef != currentArtifact || !slices.Contains(req.ArtifactRefs, p.ArtifactRef) {
		return Invalidf("certificate correlation, policy or current artifact mismatch")
	}
	issued, _ := protocolTime(p.IssuedAt)
	expires, _ := protocolTime(p.ExpiresAt)
	deadline, _ := protocolTime(req.Deadline)
	if issued.Before(request.CreatedAt) || issued.After(cert.CreatedAt) || cert.CreatedAt.After(now) || !expires.After(now) || !deadline.After(now) {
		return Invalidf("certificate or request is stale or future-dated")
	}
	if len(approvals) != len(p.ApprovalIDs) {
		return Invalidf("complete approval snapshot required")
	}
	seen := map[string]bool{}
	reviewers := map[string]bool{}
	for _, approval := range approvals {
		if seen[approval.ID] || !slices.Contains(p.ApprovalIDs, approval.ID) {
			return Invalidf("approval ID mismatch")
		}
		seen[approval.ID] = true
		if approval.ContextID != cert.ContextID || approval.Envelope == nil || approval.Envelope.Schema != ApprovalSchema || approval.Envelope.ReplyTo != request.ID || approval.Envelope.TaskID != request.Envelope.TaskID || approval.CreatedAt.Before(request.CreatedAt) || approval.CreatedAt.After(issued) || !slices.Contains(req.ExpectedResponders, approval.SessionID) {
			return Invalidf("approval correlation or freshness mismatch")
		}
		if err := validApproval(approval.Envelope.Payload); err != nil {
			return err
		}
		var verdict ApprovalPayload
		_ = json.Unmarshal(approval.Envelope.Payload, &verdict)
		if verdict.Verdict != ApprovalApproved || !slices.Contains(verdict.ArtifactRefs, currentArtifact) {
			return Invalidf("approval does not approve current artifact")
		}
		for _, check := range policy.Checks {
			if !slices.Contains(verdict.Checks, check) {
				return Invalidf("approval missing required check")
			}
		}
		reviewers[approval.SessionID] = true
	}
	for role, sid := range policy.Reviewers {
		if role == "" || sid == "" || !reviewers[sid] {
			return ErrForbidden
		}
	}
	return nil
}
