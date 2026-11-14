package httpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/krimvp/comitia"
)

func (h *handler) messageRoutes() map[string]endpoint {
	return map[string]endpoint{
		"messages/query-support": typedEndpoint(func(ctx context.Context, token string, _ emptyRequest) (emptyResult, error) {
			if _, ok := h.api.(comitia.MessageQueryAPI); !ok {
				return emptyResult{}, comitia.Invalidf("scoped message queries unsupported by API")
			}
			_, err := h.api.ServerInfo(ctx, token)
			return emptyResult{}, err
		}),
		"messages/get": typedEndpoint(func(ctx context.Context, token string, q messageGetRequest) (messageResult, error) {
			v, err := comitia.LookupMessage(ctx, h.api, token, q.ContextID, q.MessageID)
			return messageResult{ptr(messageDTO(v))}, err
		}),
		"messages/schemas/list": typedEndpoint(func(ctx context.Context, token string, _ emptyRequest) (messageSchemasResult, error) {
			api, ok := h.api.(comitia.MessageSchemaAPI)
			if !ok {
				return messageSchemasResult{}, comitia.Invalidf("message schema discovery unsupported by API")
			}
			v, err := api.ListMessageSchemas(ctx, token)
			return messageSchemasResult{v}, err
		}),
		"messages/schemas/get": typedEndpoint(func(ctx context.Context, token string, q messageSchemaRequest) (messageSchemaResult, error) {
			api, ok := h.api.(comitia.MessageSchemaAPI)
			if !ok {
				return messageSchemaResult{}, comitia.Invalidf("message schema discovery unsupported by API")
			}
			v, err := api.GetMessageSchema(ctx, token, q.Schema)
			return messageSchemaResult{&v}, err
		}),
		"participants/list": typedEndpoint(func(ctx context.Context, token string, q participantsRequest) (listParticipantsResult, error) {
			limit, err := listLimit(q.Limit)
			if err != nil {
				return listParticipantsResult{}, err
			}
			if q.Page || q.AfterID != "" {
				v, err := comitia.ListParticipantPage(ctx, h.api, token, q.ContextID, q.AfterID, limit)
				if err != nil {
					return listParticipantsResult{}, err
				}
				return listParticipantsResult{ParticipantPage: ptr(wireParticipantPage(v))}, nil
			}
			v, err := h.api.ListParticipants(ctx, token, q.ContextID, limit)
			return listParticipantsResult{Participants: ptr(mapSlice(v, func(p comitia.Participant) participantDTO { return participantDTO(p) }))}, err
		}),
		"messages/append": typedEndpoint(func(ctx context.Context, token string, q appendRequest) (messageResult, error) {
			if q.Message == nil {
				return messageResult{}, comitia.Invalidf("message required")
			}
			v, err := h.api.AppendMessage(ctx, token, q.ContextID, comitia.MessageInput(*q.Message))
			return messageResult{ptr(messageDTO(v))}, err
		}),
		"messages/replay": typedEndpoint(func(ctx context.Context, token string, q replayRequest) (readMessagesResult, error) {
			limit, err := listLimit(q.Limit)
			if err != nil {
				return readMessagesResult{}, err
			}
			if err := comitia.CheckMessageQueryAPI(h.api, q.messageFilters()); err != nil {
				return readMessagesResult{}, err
			}
			if q.Page {
				v, err := comitia.ReplayMessagePage(ctx, h.api, token, q.ContextID, q.After, limit, q.messageFilters())
				if err != nil {
					return readMessagesResult{}, err
				}
				return wirePageResult(v, q.MaxBytes)
			}
			v, err := h.api.ReplayMessages(ctx, token, q.ContextID, q.After, limit, q.messageFilters())
			if err != nil {
				return readMessagesResult{}, err
			}
			return wireReadResult(v, q.MaxBytes)
		}),
		"messages/wait": typedEndpoint(func(ctx context.Context, token string, q waitRequest) (readMessagesResult, error) {
			timeout := 30 * time.Second
			if q.TimeoutSeconds != nil {
				var err error
				timeout, err = comitia.MessageWaitTimeout(*q.TimeoutSeconds)
				if err != nil {
					return readMessagesResult{}, err
				}
			}
			filters := q.replay().messageFilters()
			if err := comitia.CheckMessageQueryAPI(h.api, filters); err != nil {
				return readMessagesResult{}, err
			}
			if q.Page {
				v, err := comitia.WaitMessagePage(ctx, h.api, token, q.ContextID, q.After, timeout, filters)
				if err != nil {
					return readMessagesResult{}, err
				}
				return wirePageResult(v, q.MaxBytes)
			}
			v, err := h.api.WaitMessages(ctx, token, q.ContextID, q.After, timeout, filters)
			if err != nil {
				return readMessagesResult{}, err
			}
			return wireReadResult(v, q.MaxBytes)
		}),
		"messages/ack": typedEndpoint(func(ctx context.Context, token string, q ackRequest) (emptyResult, error) {
			var after int64
			if q.After != nil {
				after = *q.After
			} else if canonicalRequest(ctx) {
				// Legacy clients omit an ack of 0; negotiated callers must name it.
				return emptyResult{}, comitia.Invalidf("after required")
			}
			return emptyResult{}, h.api.AckMessages(ctx, token, q.ContextID, after, q.AgentInstance)
		}),
		"messages/cursor": typedEndpoint(func(ctx context.Context, token string, q cursorRequest) (cursorResult, error) {
			v, err := h.api.MessageCursor(ctx, token, q.ContextID, q.AgentInstance)
			return cursorResult{&v}, err
		}),
	}
}

func (q replayRequest) messageFilters() comitia.MessageFilters {
	f := comitia.MessageFilters{ExcludeSelf: q.ExcludeSelf, FromCursor: q.FromCursor, Schemas: q.Schemas, ReplyTo: q.ReplyTo, ThreadID: q.ThreadID, FromSession: q.FromSession, MaxBytes: q.MaxBytes, StrictRecipients: q.StrictRecipients, ForInstance: q.ForInstance, FromInstance: q.FromInstance, TaskID: q.TaskID, Schema: q.Schema}
	if q.AgentInstance != "" {
		f.AgentInstance = &q.AgentInstance
	}
	return f
}

func (q waitRequest) replay() replayRequest {
	return replayRequest{ExcludeSelf: q.ExcludeSelf, FromCursor: q.FromCursor, Schemas: q.Schemas, ReplyTo: q.ReplyTo, ThreadID: q.ThreadID, FromSession: q.FromSession, ContextID: q.ContextID, After: q.After, MaxBytes: q.MaxBytes, Page: q.Page, AgentInstance: q.AgentInstance, ForInstance: q.ForInstance, FromInstance: q.FromInstance, TaskID: q.TaskID, Schema: q.Schema, StrictRecipients: q.StrictRecipients}
}

func boundWireMessages(messages []messageDTO, budget int) ([]messageDTO, error) {
	if budget == 0 {
		budget = comitia.DefaultMessageBytes
	}
	used := 2
	for i, message := range messages {
		raw, err := json.Marshal(message)
		if err != nil {
			return nil, err
		}
		size := len(raw)
		if i > 0 {
			size++
			if used+size > budget {
				return messages[:i], nil
			}
		}
		used += size
	}
	return messages, nil
}
func wireReadResult(messages []comitia.Message, budget int) (readMessagesResult, error) {
	values, err := boundWireMessages(mapSlice(messages, func(m comitia.Message) messageDTO { return messageDTO(m) }), budget)
	return readMessagesResult{Messages: &values}, err
}
func wirePageResult(page comitia.MessagePage, budget int) (readMessagesResult, error) {
	value := wirePage(page)
	messages, err := boundWireMessages(value.Messages, budget)
	if err != nil {
		return readMessagesResult{}, err
	}
	if len(messages) < len(value.Messages) {
		value.NextAfter = messages[len(messages)-1].Sequence
		value.ScannedThrough = value.NextAfter
		value.HasMore = true
	}
	value.Messages = messages
	return readMessagesResult{Page: &value}, nil
}
