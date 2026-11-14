package schema

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/krimvp/comitia/internal/model"
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
	if !NonEmpty(values) || len(values) > 64 {
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

func ProtocolTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return t, model.Invalidf("timestamp must be RFC3339")
	}
	return t, nil
}

func ValidRequest(raw json.RawMessage) error {
	var p RequestPayload
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Operation) == "" || strings.TrimSpace(p.Instructions) == "" || !uniqueTexts(p.ExpectedResponders) {
		return model.Invalidf("operation, instructions and unique expected_responders required (at most 64)")
	}
	if p.ArtifactRefs != nil {
		for _, ref := range p.ArtifactRefs {
			if !ExactArtifactRef(ref) {
				return model.Invalidf("artifact_refs must identify exact artifacts")
			}
		}
	}
	_, err := ProtocolTime(p.Deadline)
	return err
}

func validReceipt(raw json.RawMessage) error {
	var p ReceiptPayload
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	switch p.Status {
	case "accepted":
	case "completed":
		if !NonEmpty(p.ResultRefs) {
			return model.Invalidf("completed receipt requires result_refs")
		}
	case "rejected":
		if strings.TrimSpace(p.Reason) == "" {
			return model.Invalidf("rejected receipt requires reason")
		}
	default:
		return model.Invalidf("receipt status must be accepted, completed or rejected")
	}
	for _, ref := range p.ResultRefs {
		if !ExactArtifactRef(ref) {
			return model.Invalidf("result_refs must identify exact artifacts")
		}
	}
	return nil
}

func ValidMergeCertificate(raw json.RawMessage) error {
	var p MergeCertificatePayload
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.PolicyID) == "" || strings.TrimSpace(p.ConsumptionKey) == "" || !uniqueTexts(p.ApprovalIDs) || !pullRequestArtifact(p.ArtifactRef, p.HeadSHA) {
		return model.Invalidf("certificate requires policy_id, consumption_key, unique approval_ids and exact pr artifact matching head_sha")
	}
	issued, err := ProtocolTime(p.IssuedAt)
	if err != nil {
		return err
	}
	expires, err := ProtocolTime(p.ExpiresAt)
	if err != nil {
		return err
	}
	if !expires.After(issued) {
		return model.Invalidf("expires_at must follow issued_at")
	}
	return nil
}

func pullRequestArtifact(ref, sha string) bool {
	return fullCommitSHA(sha) && strings.HasPrefix(ref, "pr:") && ExactArtifactRef(ref) && strings.HasSuffix(ref, "@"+sha)
}

func ValidMergeReceipt(raw json.RawMessage) error {
	var p MergeReceiptPayload
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.OperationID) == "" || !fullCommitSHA(p.HeadSHA) || !NonEmpty(p.EvidenceRefs) {
		return model.Invalidf("operation_id, exact head_sha and evidence_refs required")
	}
	if p.Status == "merged" {
		if !fullCommitSHA(p.MergeSHA) {
			return model.Invalidf("merged receipt requires exact merge_sha")
		}
	} else if p.Status != "not_merged" || p.MergeSHA != "" {
		return model.Invalidf("merge receipt status must be merged or not_merged")
	}
	for _, ref := range p.EvidenceRefs {
		if !ExactArtifactRef(ref) {
			return model.Invalidf("evidence_refs must identify exact artifacts")
		}
	}
	return nil
}
