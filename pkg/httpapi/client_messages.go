package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func (q replayRequest) wait(seconds float64) waitRequest {
	return waitRequest{ExcludeSelf: q.ExcludeSelf, FromCursor: q.FromCursor, Schemas: q.Schemas, ReplyTo: q.ReplyTo, ThreadID: q.ThreadID, FromSession: q.FromSession, ContextID: q.ContextID, After: q.After, MaxBytes: q.MaxBytes, Page: q.Page, AgentInstance: q.AgentInstance, ForInstance: q.ForInstance, FromInstance: q.FromInstance, TaskID: q.TaskID, Schema: q.Schema, TimeoutSeconds: &seconds, StrictRecipients: q.StrictRecipients}
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
		q.ExcludeSelf, q.FromCursor = f.ExcludeSelf, f.FromCursor
		if len(f.Schemas) != 0 {
			q.Schemas = f.Schemas
		}
		q.ReplyTo, q.ThreadID, q.FromSession = f.ReplyTo, f.ThreadID, f.FromSession
		q.MaxBytes, q.StrictRecipients = f.MaxBytes, f.StrictRecipients
		q.ForInstance, q.FromInstance, q.TaskID, q.Schema = f.ForInstance, f.FromInstance, f.TaskID, f.Schema
		if f.AgentInstance != nil {
			instances = []string{*f.AgentInstance}
		}
	}
	var err error
	q.AgentInstance, err = c.InstanceDefaults.ID(instances...)
	return q, err
}

// Servers before strict recipient selection reject the member. Never retry
// without it: that would silently widen the selected recipients.
func strictRecipientsUnsupported(q replayRequest, err error) error {
	for _, field := range []struct {
		name    string
		enabled bool
	}{{"exclude_self", q.ExcludeSelf}, {"from_cursor", q.FromCursor}, {"schemas", len(q.Schemas) != 0}} {
		if field.enabled && unknownField(err, field.name) {
			return fmt.Errorf("%w: %s unsupported by this server; upgrade the server before retrying", comitia.ErrInvalid, field.name)
		}
	}
	for _, field := range []struct{ name, value string }{{"reply_to", q.ReplyTo}, {"thread_id", q.ThreadID}, {"from_session", q.FromSession}} {
		if field.value != "" && unknownField(err, field.name) {
			return fmt.Errorf("%w: scoped message queries unsupported by this server", comitia.ErrInvalid)
		}
	}
	if q.StrictRecipients && errors.Is(err, comitia.ErrInvalid) && strings.Contains(err.Error(), `unknown field "strict_recipients"`) {
		return fmt.Errorf("%w: strict_recipients unsupported by this server", comitia.ErrInvalid)
	}
	return err
}

// Servers before schema discovery have no route. List never reports a missing
// record, so it identifies the server; get reuses it to disambiguate.
func (c *Client) ListMessageSchemas(ctx context.Context, token string) ([]comitia.MessageSchemaDescriptor, error) {
	var out messageSchemasResult
	err := c.call(ctx, token, "messages/schemas/list", emptyRequest{}, &out)
	if errors.Is(err, comitia.ErrNotFound) {
		return nil, fmt.Errorf("%w: message schema discovery unsupported by this server", comitia.ErrInvalid)
	}
	if err != nil {
		return nil, err
	}
	return out.MessageSchemas, nil
}
func (c *Client) GetMessageSchema(ctx context.Context, token, schema string) (comitia.MessageSchemaDescriptor, error) {
	var out messageSchemaResult
	err := c.call(ctx, token, "messages/schemas/get", messageSchemaRequest{schema}, &out)
	if errors.Is(err, comitia.ErrNotFound) {
		if _, listErr := c.ListMessageSchemas(ctx, token); listErr != nil {
			return comitia.MessageSchemaDescriptor{}, listErr
		}
	}
	if err = requiredResult(err, out.MessageSchema); err != nil {
		return comitia.MessageSchemaDescriptor{}, err
	}
	return *out.MessageSchema, nil
}
func (c *Client) AppendMessage(ctx context.Context, token, id string, input comitia.MessageInput) (comitia.Message, error) {
	input.AgentInstance = c.InstanceDefaults.Message(input.AgentInstance)
	var out messageResult
	err := c.call(ctx, token, "messages/append", appendRequest{id, ptr(messageInput(input))}, &out)
	if input.Envelope != nil && errors.Is(err, comitia.ErrInvalid) && strings.Contains(err.Error(), "schema must name a registered message schema") {
		switch input.Envelope.Schema {
		case comitia.RequestSchema, comitia.ReceiptSchema, comitia.MergeCertificateSchema, comitia.MergeReceiptSchema, comitia.StanceSchema:
			return comitia.Message{}, fmt.Errorf("%w: %s unsupported by this server", comitia.ErrInvalid, input.Envelope.Schema)
		}
	}
	if field := findingFieldUnsupported(input.Envelope, err); field != "" {
		return comitia.Message{}, fmt.Errorf("%w: comitia.finding.v1 %s unsupported by this server", comitia.ErrInvalid, field)
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"recipient_session_ids", input.RecipientSessionIDs}, {"recipient_instances", input.RecipientInstances}} {
		if len(field.values) > 0 && unknownField(err, field.name) && !strings.Contains(err.Error(), "payload:") {
			return comitia.Message{}, fmt.Errorf("%w: top-level %s unsupported by this server", comitia.ErrInvalid, field.name)
		}
	}
	if input.TaskFence != nil && unknownField(err, "task_fence") {
		return comitia.Message{}, ErrTaskResultFencingUnsupported
	}
	if err = requiredResult(err, out.Message); err != nil {
		return comitia.Message{}, err
	}
	return comitia.Message(*out.Message), nil
}

// Servers before finding priority/size reject them as unknown payload fields.
// Translate only when the request actually sets the field.
func findingFieldUnsupported(env *comitia.MessageEnvelope, err error) string {
	if env == nil || env.Schema != "comitia.finding.v1" || !errors.Is(err, comitia.ErrInvalid) {
		return ""
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(env.Payload, &payload) != nil {
		return ""
	}
	for _, field := range []string{"priority", "size"} {
		if _, ok := payload[field]; ok && strings.Contains(err.Error(), `payload: unknown field "`+field+`"`) {
			return field
		}
	}
	return ""
}
func (c *Client) ReplayMessages(ctx context.Context, token, id string, after int64, limit int, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	q, err := c.messageReadRequest(id, after, filters)
	if err != nil {
		return nil, err
	}
	q.Limit = ptr(limit)
	if q.ReplyTo != "" || q.ThreadID != "" || q.FromSession != "" {
		if err := c.messageQuerySupport(ctx, token); err != nil {
			return nil, err
		}
	}
	var out readMessagesResult
	err = strictRecipientsUnsupported(q, c.call(ctx, token, "messages/replay", q, &out))
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
	if q.ReplyTo != "" || q.ThreadID != "" || q.FromSession != "" {
		if err := c.messageQuerySupport(ctx, token); err != nil {
			return nil, err
		}
	}
	var out readMessagesResult
	waitCtx, cancel := c.waits.context(ctx)
	defer cancel()
	err = strictRecipientsUnsupported(q, c.call(waitCtx, token, "messages/wait", q.wait(timeout.Seconds()), &out))
	if errors.Is(err, context.Canceled) && c.waits.stopped(ctx) {
		return []comitia.Message{}, nil
	}
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
	if q.ReplyTo != "" || q.ThreadID != "" || q.FromSession != "" {
		if err := c.messageQuerySupport(ctx, token); err != nil {
			return comitia.MessagePage{}, err
		}
	}
	var out readMessagesResult
	err = strictRecipientsUnsupported(q, c.call(ctx, token, "messages/replay", q, &out))
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
	if q.ReplyTo != "" || q.ThreadID != "" || q.FromSession != "" {
		if err := c.messageQuerySupport(ctx, token); err != nil {
			return comitia.MessagePage{}, err
		}
	}
	var out readMessagesResult
	waitCtx, cancel := c.waits.context(ctx)
	defer cancel()
	err = strictRecipientsUnsupported(q, c.call(waitCtx, token, "messages/wait", q.wait(timeout.Seconds()), &out))
	if errors.Is(err, context.Canceled) && !q.FromCursor && c.waits.stopped(ctx) {
		return comitia.MessagePage{Messages: []comitia.Message{}, NextAfter: after, ScannedThrough: after}, nil
	}
	if err = requiredResult(err, out.Page); err != nil {
		return comitia.MessagePage{}, err
	}
	return out.Page.core(), nil
}

// EndWaits makes current and later message waits return their timeout result,
// so a stopping adapter can drain long polls promptly. A cursor-relative page
// wait has no known position and returns its cancellation error instead.
func (c *Client) EndWaits() {
	if c.waits != nil {
		c.waits.once.Do(func() { close(c.waits.ended) })
	}
}

type waitDrain struct {
	once  sync.Once
	ended chan struct{}
}

// context ends a wait when EndWaits is called.
func (d *waitDrain) context(ctx context.Context) (context.Context, context.CancelFunc) {
	if d == nil {
		return ctx, func() {}
	}
	waitCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-d.ended:
			cancel()
		case <-waitCtx.Done():
		}
	}()
	return waitCtx, cancel
}

// stopped reports whether EndWaits, rather than the caller, ended a wait.
func (d *waitDrain) stopped(ctx context.Context) bool {
	if d == nil || ctx.Err() != nil {
		return false
	}
	select {
	case <-d.ended:
		return true
	default:
		return false
	}
}
func (c *Client) AckMessages(ctx context.Context, token, id string, sequence int64, instances ...string) error {
	instance, err := c.InstanceDefaults.ID(instances...)
	if err != nil {
		return err
	}
	return c.call(ctx, token, "messages/ack", ackRequest{id, &sequence, instance}, &emptyResult{})
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

// messageQuerySupport caches only success: errors, including an older server,
// are probed again on the next scoped read.
func (c *Client) messageQuerySupport(ctx context.Context, token string) error {
	if c.querySupported.Load() {
		return nil
	}
	var out emptyResult
	err := c.call(ctx, token, "messages/query-support", emptyRequest{}, &out)
	if errors.Is(err, comitia.ErrNotFound) {
		return fmt.Errorf("%w: scoped message queries unsupported by this server", comitia.ErrInvalid)
	}
	if err == nil {
		c.querySupported.Store(true)
	}
	return err
}
func (c *Client) GetMessage(ctx context.Context, token, cid, mid string) (comitia.Message, error) {
	if err := c.messageQuerySupport(ctx, token); err != nil {
		return comitia.Message{}, err
	}
	var out messageResult
	err := c.call(ctx, token, "messages/get", messageGetRequest{cid, mid}, &out)
	if err = requiredResult(err, out.Message); err != nil {
		return comitia.Message{}, err
	}
	return comitia.Message(*out.Message), nil
}

func (*Client) SupportsMessageReadOptions() bool { return true }
