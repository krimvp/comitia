package legacy

import (
	"context"
	"time"

	"github.com/krimvp/comitia"
)

func (h *handler) routeMessages(ctx context.Context, token, route string, q request, out *response) (found bool, err error) {
	switch route {
	case "participants/list":
		out.Participants, err = h.api.ListParticipants(ctx, token, q.ContextID, q.Limit)
	case "messages/append":
		if q.Message == nil {
			err = comitia.Invalidf("message required")
			break
		}
		var v comitia.Message
		v, err = h.api.AppendMessage(ctx, token, q.ContextID, comitia.MessageInput{DedupeKey: q.Message.DedupeKey, Body: q.Message.Body, Envelope: q.Message.Envelope, AgentInstance: q.Message.AgentInstance})
		out.Message = &v
	case "messages/replay":
		if q.Page {
			var page comitia.MessagePage
			page, err = comitia.ReplayMessagePage(ctx, h.api, token, q.ContextID, q.After, q.Limit, q.messageFilters())
			out.Page = &page
		} else {
			out.Messages, err = h.api.ReplayMessages(ctx, token, q.ContextID, q.After, q.Limit, q.messageFilters())
		}
	case "messages/wait":
		timeout := 30 * time.Second
		if q.TimeoutSeconds != nil {
			timeout, err = comitia.MessageWaitTimeout(*q.TimeoutSeconds)
		}
		if err == nil {
			if q.Page {
				var page comitia.MessagePage
				page, err = comitia.WaitMessagePage(ctx, h.api, token, q.ContextID, q.After, timeout, q.messageFilters())
				out.Page = &page
			} else {
				out.Messages, err = h.api.WaitMessages(ctx, token, q.ContextID, q.After, timeout, q.messageFilters())
			}
		}
	case "messages/ack":
		err = h.api.AckMessages(ctx, token, q.ContextID, q.After, q.AgentInstance)
	case "messages/cursor":
		var v int64
		v, err = h.api.MessageCursor(ctx, token, q.ContextID, q.AgentInstance)
		out.Cursor = &v
	default:
		return false, nil
	}
	return true, err
}

func (c *Client) AppendMessage(x context.Context, t, id string, i comitia.MessageInput) (comitia.Message, error) {
	i.AgentInstance = c.InstanceDefaults.Message(i.AgentInstance)
	var o response
	e := c.call(x, t, "messages/append", request{ContextID: id, Message: &messageInput{i.DedupeKey, i.Body, i.Envelope, i.AgentInstance}}, &o)
	if o.Message == nil {
		return comitia.Message{}, missing(e)
	}
	return *o.Message, e
}
func (c *Client) ReplayMessages(x context.Context, t, id string, a int64, l int, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	q, err := c.messageReadRequest(id, a, filters)
	if err != nil {
		return nil, err
	}
	q.Limit = l
	var o response
	e := c.call(x, t, "messages/replay", q, &o)
	return o.Messages, e
}
func (c *Client) AckMessages(x context.Context, t, id string, s int64, instances ...string) error {
	instance, err := c.InstanceDefaults.ID(instances...)
	if err != nil {
		return err
	}
	return c.call(x, t, "messages/ack", request{ContextID: id, After: s, AgentInstance: instance}, &response{})
}
func (c *Client) MessageCursor(x context.Context, t, id string, instances ...string) (int64, error) {
	instance, err := c.InstanceDefaults.ID(instances...)
	if err != nil {
		return 0, err
	}
	var o response
	e := c.call(x, t, "messages/cursor", request{ContextID: id, AgentInstance: instance}, &o)
	if o.Cursor == nil {
		return 0, missing(e)
	}
	return *o.Cursor, e
}
func (c *Client) ListParticipants(ctx context.Context, token, cid string, limit int) ([]comitia.Participant, error) {
	var out response
	err := c.call(ctx, token, "participants/list", request{ContextID: cid, Limit: limit}, &out)
	return out.Participants, err
}

func (q request) messageFilters() comitia.MessageFilters {
	f := comitia.MessageFilters{MaxBytes: q.MaxBytes, ForInstance: q.ForInstance, FromInstance: q.FromInstance, TaskID: q.TaskID, Schema: q.Schema}
	if q.AgentInstance != "" {
		f.AgentInstance = &q.AgentInstance
	}
	return f
}

// A nil AgentInstance uses the client's default presence instance.
func (c *Client) messageReadRequest(cid string, after int64, filters []comitia.MessageFilters) (request, error) {
	if len(filters) > 1 {
		return request{}, comitia.Invalidf("at most one filter set allowed")
	}
	q := request{ContextID: cid, After: after}
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
func (c *Client) WaitMessages(ctx context.Context, token, cid string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	if timeout < 0 {
		return nil, comitia.Invalidf("timeout must be nonnegative")
	}
	if timeout > comitia.MaxMessageWait {
		timeout = comitia.MaxMessageWait
	}
	q, err := c.messageReadRequest(cid, after, filters)
	if err != nil {
		return nil, err
	}
	seconds := timeout.Seconds()
	q.TimeoutSeconds = &seconds
	var out response
	err = c.call(ctx, token, "messages/wait", q, &out)
	if err == nil && out.Messages == nil {
		out.Messages = []comitia.Message{}
	}
	return out.Messages, err
}

func (c *Client) ReplayMessagesPage(ctx context.Context, token, cid string, after int64, limit int, filters ...comitia.MessageFilters) (comitia.MessagePage, error) {
	q, err := c.messageReadRequest(cid, after, filters)
	if err != nil {
		return comitia.MessagePage{}, err
	}
	q.Limit, q.Page = limit, true
	var out response
	err = c.call(ctx, token, "messages/replay", q, &out)
	if out.Page == nil {
		return comitia.MessagePage{}, missing(err)
	}
	return *out.Page, err
}
func (c *Client) WaitMessagesPage(ctx context.Context, token, cid string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) (comitia.MessagePage, error) {
	if timeout < 0 {
		return comitia.MessagePage{}, comitia.Invalidf("timeout must be nonnegative")
	}
	if timeout > comitia.MaxMessageWait {
		timeout = comitia.MaxMessageWait
	}
	q, err := c.messageReadRequest(cid, after, filters)
	if err != nil {
		return comitia.MessagePage{}, err
	}
	seconds := timeout.Seconds()
	q.TimeoutSeconds, q.Page = &seconds, true
	var out response
	err = c.call(ctx, token, "messages/wait", q, &out)
	if out.Page == nil {
		return comitia.MessagePage{}, missing(err)
	}
	return *out.Page, err
}
