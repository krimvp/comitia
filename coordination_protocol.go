package comitia

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const (
	RequestSchema          = "comitia.request.v1"
	ReceiptSchema          = "comitia.receipt.v1"
	MergeCertificateSchema = "comitia.merge-certificate.v1"
	MergeReceiptSchema     = "comitia.merge-receipt.v1"
)

// RequestPayload persists a business deadline, independent of a wait timeout.
// Operation may name external steering (control.pause, control.stop, etc.).
type RequestPayload struct {
	Operation          string   `json:"operation"`
	Instructions       string   `json:"instructions"`
	ExpectedResponders []string `json:"expected_responders"`
	Deadline           string   `json:"deadline"`
	ArtifactRefs       []string `json:"artifact_refs,omitempty"`
}
type ReceiptPayload struct {
	Status     string   `json:"status"`
	ResultRefs []string `json:"result_refs,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}
type MergeCertificatePayload struct {
	PolicyID       string   `json:"policy_id"`
	ArtifactRef    string   `json:"artifact_ref"`
	HeadSHA        string   `json:"head_sha"`
	ApprovalIDs    []string `json:"approval_ids"`
	IssuedAt       string   `json:"issued_at"`
	ExpiresAt      string   `json:"expires_at"`
	ConsumptionKey string   `json:"consumption_key"`
}
type MergeReceiptPayload struct {
	OperationID  string   `json:"operation_id"`
	Status       string   `json:"status"`
	HeadSHA      string   `json:"head_sha"`
	MergeSHA     string   `json:"merge_sha,omitempty"`
	EvidenceRefs []string `json:"evidence_refs"`
}

func uniqueTexts(values []string) bool {
	if !nonempty(values) || len(values) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func protocolTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return t, Invalidf("timestamp must be RFC3339")
	}
	return t, nil
}
func validRequest(raw json.RawMessage) error {
	var p RequestPayload
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Operation) == "" || strings.TrimSpace(p.Instructions) == "" || !uniqueTexts(p.ExpectedResponders) {
		return Invalidf("operation, instructions and unique expected_responders required (at most 64)")
	}
	if p.ArtifactRefs != nil {
		for _, ref := range p.ArtifactRefs {
			if !exactArtifactRef(ref) {
				return Invalidf("artifact_refs must identify exact artifacts")
			}
		}
	}
	_, err := protocolTime(p.Deadline)
	return err
}
func validReceipt(raw json.RawMessage) error {
	var p ReceiptPayload
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	switch p.Status {
	case "accepted":
	case "completed":
		if !nonempty(p.ResultRefs) {
			return Invalidf("completed receipt requires result_refs")
		}
	case "rejected":
		if strings.TrimSpace(p.Reason) == "" {
			return Invalidf("rejected receipt requires reason")
		}
	default:
		return Invalidf("receipt status must be accepted, completed or rejected")
	}
	for _, ref := range p.ResultRefs {
		if !exactArtifactRef(ref) {
			return Invalidf("result_refs must identify exact artifacts")
		}
	}
	return nil
}
func validMergeCertificate(raw json.RawMessage) error {
	var p MergeCertificatePayload
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.PolicyID) == "" || strings.TrimSpace(p.ConsumptionKey) == "" || !uniqueTexts(p.ApprovalIDs) || !pullRequestArtifact(p.ArtifactRef, p.HeadSHA) {
		return Invalidf("certificate requires policy_id, consumption_key, unique approval_ids and exact pr artifact matching head_sha")
	}
	issued, err := protocolTime(p.IssuedAt)
	if err != nil {
		return err
	}
	expires, err := protocolTime(p.ExpiresAt)
	if err != nil {
		return err
	}
	if !expires.After(issued) {
		return Invalidf("expires_at must follow issued_at")
	}
	return nil
}
func pullRequestArtifact(ref, sha string) bool {
	return fullCommitSHA(sha) && strings.HasPrefix(ref, "pr:") && exactArtifactRef(ref) && strings.HasSuffix(ref, "@"+sha)
}
func validMergeReceipt(raw json.RawMessage) error {
	var p MergeReceiptPayload
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.OperationID) == "" || !fullCommitSHA(p.HeadSHA) || !nonempty(p.EvidenceRefs) {
		return Invalidf("operation_id, exact head_sha and evidence_refs required")
	}
	if p.Status == "merged" {
		if !fullCommitSHA(p.MergeSHA) {
			return Invalidf("merged receipt requires exact merge_sha")
		}
	} else if p.Status != "not_merged" || p.MergeSHA != "" {
		return Invalidf("merge receipt status must be merged or not_merged")
	}
	for _, ref := range p.EvidenceRefs {
		if !exactArtifactRef(ref) {
			return Invalidf("evidence_refs must identify exact artifacts")
		}
	}
	return nil
}

// Validate durable references in the same transaction as the append. Identical
// dedupe retries are recovered before this check, even after the deadline.
func (s *Service) validateProtocolReferences(r ReadStore, cid, sid string, env *MessageEnvelope) error {
	if env == nil {
		return nil
	}
	switch env.Schema {
	case RequestSchema:
		var p RequestPayload
		_ = json.Unmarshal(env.Payload, &p)
		deadline, _ := protocolTime(p.Deadline)
		if !deadline.After(s.now()) {
			return Invalidf("request deadline must be in the future")
		}
		return s.validateEnvelopeReferences(r, cid, &MessageEnvelope{RecipientSessionIDs: p.ExpectedResponders})
	case ReceiptSchema, MergeCertificateSchema, MergeReceiptSchema:
		if env.ReplyTo == "" {
			return Invalidf("reply_to required for receipt or certificate")
		}
		request, err := contextMessage(r, cid, env.ReplyTo)
		if err != nil {
			return err
		}
		if request.Envelope == nil {
			return Invalidf("reply_to must identify a protocol message")
		}
		if env.TaskID != request.Envelope.TaskID {
			return Invalidf("task_id must match correlated message")
		}
		if env.Schema == MergeReceiptSchema {
			if request.Envelope.Schema != MergeCertificateSchema {
				return Invalidf("merge receipt must reply to a certificate")
			}
			var cert MergeCertificatePayload
			_ = json.Unmarshal(request.Envelope.Payload, &cert)
			var receipt MergeReceiptPayload
			_ = json.Unmarshal(env.Payload, &receipt)
			if receipt.HeadSHA != cert.HeadSHA || receipt.OperationID != cert.ConsumptionKey {
				return Invalidf("merge receipt must match certificate head and consumption_key")
			}
			return nil
		}
		if request.Envelope.Schema != RequestSchema {
			return Invalidf("receipt or certificate must reply to a request")
		}
		var p RequestPayload
		_ = json.Unmarshal(request.Envelope.Payload, &p)
		if env.Schema == ReceiptSchema {
			if !slices.Contains(p.ExpectedResponders, sid) {
				return ErrForbidden
			}
			deadline, _ := protocolTime(p.Deadline)
			if !deadline.After(s.now()) {
				return Invalidf("request deadline expired")
			}
		} else {
			var cert MergeCertificatePayload
			_ = json.Unmarshal(env.Payload, &cert)
			for _, mid := range cert.ApprovalIDs {
				approval, err := contextMessage(r, cid, mid)
				if err != nil {
					return err
				}
				if approval.Envelope == nil || approval.Envelope.Schema != ApprovalSchema || approval.Envelope.ReplyTo != request.ID || approval.Envelope.TaskID != request.Envelope.TaskID {
					return Invalidf("approval must reply to certificate request")
				}
			}
		}
	}
	return nil
}
