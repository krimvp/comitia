package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/krimvp/comitia"
)

func watchMessages(ctx context.Context, api comitia.MessageAPI, token, cid, instance string, timeout time.Duration, filters comitia.MessageFilters, ack bool, out io.Writer) error {
	return watchMessageOutput(ctx, api, token, cid, instance, timeout, filters, ack, false, out)
}

func watchMessageOutput(ctx context.Context, api comitia.MessageAPI, token, cid, instance string, timeout time.Duration, filters comitia.MessageFilters, ack, jsonLines bool, out io.Writer) error {
	if err := comitia.CheckMessageQueryAPI(api, filters); err != nil {
		return err
	}
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
			var page comitia.MessagePage
			page, err = comitia.WaitMessagePage(ctx, api, token, cid, after, timeout, filters)
			if err == nil {
				for _, m := range page.Messages {
					if jsonLines {
						if e := json.NewEncoder(out).Encode(m); e != nil {
							return e
						}
					} else {
						sender, schema := "-", "-"
						if m.AgentInstance != nil {
							sender = escapeWatchField(m.AgentInstance.ID)
						}
						if m.Envelope != nil {
							schema = escapeWatchField(m.Envelope.Schema)
						}
						body, _, _ := strings.Cut(m.Body, "\n")
						body = escapeWatchField(strings.TrimSuffix(body, "\r"))
						if _, e := fmt.Fprintf(out, "%d\t%s\t%s\t%s\n", m.Sequence, sender, schema, body); e != nil {
							return e
						}
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
				if err == nil && page.ScannedThrough > after {
					after = page.ScannedThrough
					if ack {
						err = api.AckMessages(ctx, token, cid, after, instance)
						pendingAck = err != nil
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

// escapeWatchField makes sender-controlled text safe for one terminal line.
// Backslash, invalid UTF-8 and every non-graphic rune (C0 including TAB/CR/ESC/
// BEL, DEL, C1, bidi/format and line/paragraph separators) become Go-style
// escapes such as \\, \t, \r, \x1b, \u202e, so no field contains a raw tab or
// terminal control sequence.
func escapeWatchField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\\':
			b.WriteString(`\\`)
		case unicode.IsGraphic(r):
			b.WriteString(s[i : i+n])
		default:
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		}
		i += n
	}
	return b.String()
}
