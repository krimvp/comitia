package service

import (
	"encoding/json"
	"slices"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/schema"
)

// Validate durable references in the same transaction as the append. Identical
// dedupe retries are recovered before this check, even after the deadline.
func (s *Service) validateProtocolReferences(r model.ReadStore, cid, sid string, env *model.MessageEnvelope) error {
	if env == nil {
		return nil
	}
	switch env.Schema {
	case model.RequestSchema:
		var p schema.RequestPayload
		_ = json.Unmarshal(env.Payload, &p)
		deadline, _ := schema.ProtocolTime(p.Deadline)
		if !deadline.After(s.now()) {
			return model.Invalidf("request deadline must be in the future")
		}
		return s.validateReaders(r, cid, "expected_responders", p.ExpectedResponders)
	case model.ReceiptSchema, model.MergeCertificateSchema, model.MergeReceiptSchema:
		if env.ReplyTo == "" {
			return model.Invalidf("reply_to required for receipt or certificate")
		}
		request, err := contextMessage(r, cid, env.ReplyTo)
		if err != nil {
			return err
		}
		if request.Envelope == nil {
			return model.Invalidf("reply_to must identify a protocol message")
		}
		if env.TaskID != request.Envelope.TaskID {
			return model.Invalidf("task_id must match correlated message")
		}
		if env.Schema == model.MergeReceiptSchema {
			if request.Envelope.Schema != model.MergeCertificateSchema {
				return model.Invalidf("merge receipt must reply to a certificate")
			}
			var cert schema.MergeCertificatePayload
			_ = json.Unmarshal(request.Envelope.Payload, &cert)
			var receipt schema.MergeReceiptPayload
			_ = json.Unmarshal(env.Payload, &receipt)
			if receipt.HeadSHA != cert.HeadSHA || receipt.OperationID != cert.ConsumptionKey {
				return model.Invalidf("merge receipt must match certificate head and consumption_key")
			}
			return nil
		}
		if request.Envelope.Schema != model.RequestSchema {
			return model.Invalidf("receipt or certificate must reply to a request")
		}
		var p schema.RequestPayload
		_ = json.Unmarshal(request.Envelope.Payload, &p)
		if env.Schema == model.ReceiptSchema {
			if !slices.Contains(p.ExpectedResponders, sid) {
				return model.ErrForbidden
			}
			deadline, _ := schema.ProtocolTime(p.Deadline)
			if !deadline.After(s.now()) {
				return model.Invalidf("request deadline expired")
			}
		} else {
			var cert schema.MergeCertificatePayload
			_ = json.Unmarshal(env.Payload, &cert)
			for _, mid := range cert.ApprovalIDs {
				approval, err := contextMessage(r, cid, mid)
				if err != nil {
					return err
				}
				if approval.Envelope == nil || approval.Envelope.Schema != model.ApprovalSchema || approval.Envelope.ReplyTo != request.ID || approval.Envelope.TaskID != request.Envelope.TaskID {
					return model.Invalidf("approval must reply to certificate request")
				}
			}
		}
	}
	return nil
}
