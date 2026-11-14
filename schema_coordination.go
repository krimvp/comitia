package comitia

import (
	"encoding/json"
	"strings"
	"time"
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
