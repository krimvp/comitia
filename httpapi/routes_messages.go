package httpapi

import (
	"context"

	"github.com/krimvp/comitia"
)

func (h *handler) routeMessages(ctx context.Context, token, route string, q request, out *response) (found bool, err error) {
	switch route {
	case "participants/list":
		out.Participants, err = h.api.ListParticipants(ctx, token, q.ContextID, q.Limit)
	case "messages/append":
		if q.Message == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.Message
		v, err = h.api.AppendMessage(ctx, token, q.ContextID, *q.Message)
		out.Message = &v
	case "messages/replay":
		out.Messages, err = h.api.ReplayMessages(ctx, token, q.ContextID, q.After, q.Limit)
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
	e := c.call(x, t, "messages/append", request{ContextID: id, Message: &i}, &o)
	if o.Message == nil {
		return comitia.Message{}, missing(e)
	}
	return *o.Message, e
}
func (c *Client) ReplayMessages(x context.Context, t, id string, a int64, l int) ([]comitia.Message, error) {
	var o response
	e := c.call(x, t, "messages/replay", request{ContextID: id, After: a, Limit: l}, &o)
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
