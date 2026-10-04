package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

func watchMessages(ctx context.Context, api comitia.MessageAPI, token, cid, instance string, timeout time.Duration, filters comitia.MessageFilters, ack bool, out io.Writer) error {
	// A zero timeout would turn an idle watch into a busy loop.
	if timeout == 0 {
		return fmt.Errorf("%w: watch timeout must be positive", comitia.ErrInvalid)
	}
	var after int64
	loaded, pendingAck := false, false
	backoff := 250 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return nil
		}
		var err error
		switch {
		case !loaded:
			after, err = api.MessageCursor(ctx, token, cid, instance)
			loaded = err == nil
		case pendingAck:
			err = api.AckMessages(ctx, token, cid, after, instance)
			pendingAck = err != nil
		default:
			var messages []comitia.Message
			messages, err = api.WaitMessages(ctx, token, cid, after, timeout, filters)
			if err == nil {
				for _, m := range messages {
					sender, schema := "-", "-"
					if m.AgentInstance != nil {
						sender = m.AgentInstance.ID
					}
					if m.Envelope != nil {
						schema = m.Envelope.Schema
					}
					body, _, _ := strings.Cut(m.Body, "\n")
					body = strings.TrimSuffix(body, "\r")
					if _, e := fmt.Fprintf(out, "%d\t%s\t%s\t%s\n", m.Sequence, sender, schema, body); e != nil {
						return e
					}
					after = m.Sequence
					if ack {
						err = api.AckMessages(ctx, token, cid, after, instance)
						if err != nil {
							pendingAck = true
							break
						}
					}
				}
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			backoff = 250 * time.Millisecond
			continue
		}
		if errors.Is(err, comitia.ErrInvalid) || errors.Is(err, comitia.ErrUnauthenticated) || errors.Is(err, comitia.ErrForbidden) || errors.Is(err, comitia.ErrNotFound) {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}
