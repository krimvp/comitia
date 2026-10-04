package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

func (h *handler) messageRoutes() map[string]endpoint {
	return map[string]endpoint{
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
			return emptyResult{}, h.api.AckMessages(ctx, token, q.ContextID, q.After, q.AgentInstance)
		}),
		"messages/cursor": typedEndpoint(func(ctx context.Context, token string, q cursorRequest) (cursorResult, error) {
			v, err := h.api.MessageCursor(ctx, token, q.ContextID, q.AgentInstance)
			return cursorResult{&v}, err
		}),
	}
}

func (q replayRequest) messageFilters() comitia.MessageFilters {
	f := comitia.MessageFilters{MaxBytes: q.MaxBytes, ForInstance: q.ForInstance, FromInstance: q.FromInstance, TaskID: q.TaskID, Schema: q.Schema}
	if q.AgentInstance != "" {
		f.AgentInstance = &q.AgentInstance
	}
	return f
}
func (q replayRequest) wait(seconds float64) waitRequest {
	return waitRequest{ContextID: q.ContextID, After: q.After, MaxBytes: q.MaxBytes, Page: q.Page, AgentInstance: q.AgentInstance, ForInstance: q.ForInstance, FromInstance: q.FromInstance, TaskID: q.TaskID, Schema: q.Schema, TimeoutSeconds: &seconds}
}
func (q waitRequest) replay() replayRequest {
	return replayRequest{ContextID: q.ContextID, After: q.After, MaxBytes: q.MaxBytes, Page: q.Page, AgentInstance: q.AgentInstance, ForInstance: q.ForInstance, FromInstance: q.FromInstance, TaskID: q.TaskID, Schema: q.Schema}
}

// A nil AgentInstance uses the client's default presence instance.
func (c *Client) messageReadRequest(cid string, after int64, filters []comitia.MessageFilters) (replayRequest, error) {
	if len(filters) > 1 {
		return replayRequest{}, comitia.Invalidf("at most one filter set allowed")
	}
	q := replayRequest{ContextID: cid, After: after}
	var instances []string
	if len(filters) == 1 {
		f := filters[0]
		q.MaxBytes = f.MaxBytes
		q.ForInstance, q.FromInstance, q.TaskID, q.Schema = f.ForInstance, f.FromInstance, f.TaskID, f.Schema
		if f.AgentInstance != nil {
			instances = []string{*f.AgentInstance}
		}
	}
	var err error
	q.AgentInstance, err = c.InstanceDefaults.ID(instances...)
	return q, err
}
func (c *Client) AppendMessage(ctx context.Context, token, id string, input comitia.MessageInput) (comitia.Message, error) {
	input.AgentInstance = c.InstanceDefaults.Message(input.AgentInstance)
	var out messageResult
	err := c.call(ctx, token, "messages/append", appendRequest{id, ptr(messageInput(input))}, &out)
	if input.TaskFence != nil && unknownField(err, "task_fence") {
		return comitia.Message{}, ErrTaskResultFencingUnsupported
	}
	if err = requiredResult(err, out.Message); err != nil {
		return comitia.Message{}, err
	}
	return comitia.Message(*out.Message), nil
}
func (c *Client) ReplayMessages(ctx context.Context, token, id string, after int64, limit int, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	q, err := c.messageReadRequest(id, after, filters)
	if err != nil {
		return nil, err
	}
	q.Limit = ptr(limit)
	var out readMessagesResult
	err = c.call(ctx, token, "messages/replay", q, &out)
	if err = requiredResult(err, out.Messages); err != nil {
		return nil, err
	}
	return mapSlice(*out.Messages, func(m messageDTO) comitia.Message { return comitia.Message(m) }), nil
}
func (c *Client) WaitMessages(ctx context.Context, token, id string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	if timeout < 0 {
		return nil, comitia.Invalidf("timeout must be nonnegative")
	}
	if timeout > comitia.MaxMessageWait {
		timeout = comitia.MaxMessageWait
	}
	q, err := c.messageReadRequest(id, after, filters)
	if err != nil {
		return nil, err
	}
	var out readMessagesResult
	err = c.call(ctx, token, "messages/wait", q.wait(timeout.Seconds()), &out)
	if err = requiredResult(err, out.Messages); err != nil {
		return nil, err
	}
	return mapSlice(*out.Messages, func(m messageDTO) comitia.Message { return comitia.Message(m) }), nil
}
func (c *Client) ReplayMessagesPage(ctx context.Context, token, id string, after int64, limit int, filters ...comitia.MessageFilters) (comitia.MessagePage, error) {
	q, err := c.messageReadRequest(id, after, filters)
	if err != nil {
		return comitia.MessagePage{}, err
	}
	q.Limit, q.Page = ptr(limit), true
	var out readMessagesResult
	err = c.call(ctx, token, "messages/replay", q, &out)
	if err = requiredResult(err, out.Page); err != nil {
		return comitia.MessagePage{}, err
	}
	return out.Page.core(), nil
}
func (c *Client) WaitMessagesPage(ctx context.Context, token, id string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) (comitia.MessagePage, error) {
	if timeout < 0 {
		return comitia.MessagePage{}, comitia.Invalidf("timeout must be nonnegative")
	}
	if timeout > comitia.MaxMessageWait {
		timeout = comitia.MaxMessageWait
	}
	q, err := c.messageReadRequest(id, after, filters)
	if err != nil {
		return comitia.MessagePage{}, err
	}
	q.Page = true
	var out readMessagesResult
	err = c.call(ctx, token, "messages/wait", q.wait(timeout.Seconds()), &out)
	if err = requiredResult(err, out.Page); err != nil {
		return comitia.MessagePage{}, err
	}
	return out.Page.core(), nil
}
func (c *Client) AckMessages(ctx context.Context, token, id string, sequence int64, instances ...string) error {
	instance, err := c.InstanceDefaults.ID(instances...)
	if err != nil {
		return err
	}
	return c.call(ctx, token, "messages/ack", ackRequest{id, sequence, instance}, &emptyResult{})
}
func (c *Client) MessageCursor(ctx context.Context, token, id string, instances ...string) (int64, error) {
	instance, err := c.InstanceDefaults.ID(instances...)
	if err != nil {
		return 0, err
	}
	var out cursorResult
	err = c.call(ctx, token, "messages/cursor", cursorRequest{id, instance}, &out)
	if err = requiredResult(err, out.Cursor); err != nil {
		return 0, err
	}
	return *out.Cursor, nil
}
func (c *Client) ListParticipants(ctx context.Context, token, id string, limit int) ([]comitia.Participant, error) {
	var out participantsResult
	err := c.call(ctx, token, "participants/list", participantsRequest{ContextID: id, Limit: ptr(limit)}, &out)
	return mapSlice(out.Participants, func(p participantDTO) comitia.Participant { return comitia.Participant(p) }), err
}

// ListParticipantsPage falls back to the bounded legacy list when the server
// predates participant paging.
func (c *Client) ListParticipantsPage(ctx context.Context, token, id, after string, limit int) (comitia.ParticipantPage, error) {
	var out struct {
		ParticipantPage *participantPageDTO `json:"participant_page,omitempty"`
	}
	err := c.call(ctx, token, "participants/list", participantsRequest{ContextID: id, AfterID: after, Limit: ptr(limit), Page: true}, &out)
	if pagingUnsupported(err) || err == nil && out.ParticipantPage == nil {
		return comitia.ListParticipantPage(ctx, legacyParticipantLists{c}, token, id, after, limit)
	}
	if err != nil {
		return comitia.ParticipantPage{}, err
	}
	return out.ParticipantPage.core(), nil
}

// legacyParticipantLists hides the Client's paging capability from the fallback.
type legacyParticipantLists struct{ comitia.MessageAPI }

// Servers before task result fencing reject task_fence while decoding, so an
// unfenced copy of the result was not appended either.
var ErrTaskResultFencingUnsupported = fmt.Errorf("%w: server does not support task result fencing (task_fence); upgrade the server before retrying", comitia.ErrInvalid)

// Endpoint-DTO servers before discovery paging reject its request members;
// older union servers ignore them and omit the page.
func pagingUnsupported(err error) bool {
	return errors.Is(err, comitia.ErrInvalid) && (strings.Contains(err.Error(), `unknown field "page"`) || strings.Contains(err.Error(), `unknown field "after_id"`))
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
