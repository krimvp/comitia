package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/krimvp/comitia"
)

func runMessages(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "send":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		key := fs.String("dedupe-key", "", "stable deduplication key")
		body := fs.String("body", "", "message body")
		var recipientSessions, recipientInstances []string
		fs.Func("recipient-session-id", "recipient session ID; repeat for several recipients", func(value string) error {
			recipientSessions = append(recipientSessions, value)
			return nil
		})
		fs.Func("recipient-instance", "recipient instance ID; repeat for several recipients", func(value string) error {
			recipientInstances = append(recipientInstances, value)
			return nil
		})
		instanceID := fs.String("agent-instance", "", "self-reported instance ID (defaults to COMITIA_AGENT_INSTANCE)")
		instanceLabel := fs.String("agent-instance-label", "", "optional self-reported instance label (defaults to COMITIA_AGENT_INSTANCE_LABEL)")
		claimToken := fs.String("claim-token", "", "current task lease token for a fenced result")
		leaseVersion := fs.Int64("lease-version", 0, "current task lease version for a fenced result")
		envelopeFile := fs.String("envelope-file", "", "validated message envelope JSON file")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		envelope, e := readEnvelope(*envelopeFile)
		if e != nil {
			return e
		}
		var fence *comitia.TaskFence
		if flagProvided(fs, "claim-token") || flagProvided(fs, "lease-version") {
			if envelope == nil {
				return comitia.Invalidf("fenced result requires envelope-file with task_id")
			}
			fence = &comitia.TaskFence{TaskID: envelope.TaskID, ClaimToken: *claimToken, LeaseVersion: *leaseVersion}
		}
		instance := messageInstance(cfg, fs, *instanceID, *instanceLabel)
		v, e := api.AppendMessage(ctx, needCredential(cfg), *cid, comitia.MessageInput{RecipientSessionIDs: recipientSessions, RecipientInstances: recipientInstances, TaskFence: fence, DedupeKey: *key, Body: *body, Envelope: envelope, AgentInstance: instance})
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "message-get":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "readable context ID")
		mid := fs.String("message-id", "", "message ID in this context")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, err := comitia.LookupMessage(ctx, api, needCredential(cfg), *cid, *mid)
		if err != nil {
			return err
		}
		return out.Encode(v)
	case "participants":
		fs := newFlags(cmd)
		page := fs.Bool("page", false, "return items, next_after and has_more")
		after := fs.String("after-id", "", "exclusive cursor from next_after (implies --page)")
		cid := fs.String("context-id", "", "work context ID")
		limit := fs.Int("limit", 100, "maximum participants (1-1000)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if *page || *after != "" {
			v, e := comitia.ListParticipantPage(ctx, api, needCredential(cfg), *cid, *after, *limit)
			if e != nil {
				return e
			}
			return out.Encode(v)
		}
		v, e := api.ListParticipants(ctx, needCredential(cfg), *cid, *limit)
		if e != nil {
			return e
		}
		return out.Encode(nonNilList(v))
	case "replay", "wait", "watch":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		after := fs.Int64("after", 0, "exclusive sequence cursor")
		limit := fs.Int("limit", 100, "maximum matching messages for replay")
		timeoutSeconds := fs.Float64("timeout-seconds", 30, "wait timeout seconds (capped at 60)")
		defaultID, _ := cfg.instances.ID()
		instance := fs.String("agent-instance", defaultID, "self-reported instance ID for presence and the watch cursor (defaults to COMITIA_AGENT_INSTANCE)")
		jsonLines := fs.Bool("json-lines", false, "full messages as JSON lines; requires --manual-ack or --no-ack")
		manualAck := fs.Bool("manual-ack", false, "acknowledge separately after durable business handling")
		noAck := fs.Bool("no-ack", false, "do not acknowledge printed watch messages")
		page := fs.Bool("page", false, "return messages, next_after and has_more")
		filters := messageFilterFlags(fs)
		fs.IntVar(&filters.MaxBytes, "max-bytes", 0, "encoded messages-array byte budget (default 1048576, max 4194304; first message intact)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		filters.AgentInstance = instance
		if filters.FromCursor && flagProvided(fs, "after") {
			return comitia.Invalidf("--from-cursor cannot be combined with --after")
		}
		if cmd == "watch" {
			if filters.FromCursor || flagProvided(fs, "after") {
				return comitia.Invalidf("watch always starts from its instance cursor")
			}
			if !flagProvided(fs, "for-instance") {
				filters.ForInstance = *instance
			}
		}
		if err := comitia.CheckMessageQueryAPI(api, *filters); err != nil {
			return err
		}
		if (*jsonLines || *manualAck) && cmd != "watch" {
			return comitia.Invalidf("--json-lines and --manual-ack require watch")
		}
		if *jsonLines && !*manualAck && !*noAck {
			return comitia.Invalidf("--json-lines requires --manual-ack or --no-ack")
		}
		if cmd == "watch" && *page {
			return comitia.Invalidf("--page is supported by replay and wait")
		}
		if cmd == "replay" {
			if *page {
				v, e := comitia.ReplayMessagePage(ctx, api, needCredential(cfg), *cid, *after, *limit, *filters)
				if e != nil {
					return e
				}
				return out.Encode(v)
			}
			v, e := api.ReplayMessages(ctx, needCredential(cfg), *cid, *after, *limit, *filters)
			if e != nil {
				return e
			}
			return out.Encode(nonNilList(v))
		}
		timeout, e := comitia.MessageWaitTimeout(*timeoutSeconds)
		if e != nil {
			return e
		}
		if cmd == "watch" {
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			return watchMessageOutput(ctx, api, needCredential(cfg), *cid, *instance, timeout, *filters, !*noAck && !*manualAck, *jsonLines, os.Stdout)
		}
		if *page {
			v, e := comitia.WaitMessagePage(ctx, api, needCredential(cfg), *cid, *after, timeout, *filters)
			if e != nil {
				return e
			}
			return out.Encode(v)
		}
		v, e := api.WaitMessages(ctx, needCredential(cfg), *cid, *after, timeout, *filters)
		if e != nil {
			return e
		}
		return out.Encode(nonNilList(v))
	case "ack":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		seq := fs.Int64("sequence", 0, "inclusive sequence (required)")
		defaultID, _ := cfg.instances.ID()
		instance := fs.String("agent-instance", defaultID, "self-reported instance ID for this cursor (defaults to COMITIA_AGENT_INSTANCE)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if !flagProvided(fs, "sequence") {
			return comitia.Invalidf("--sequence required")
		}
		if cfg.parseOnly {
			return nil
		}
		if e := api.AckMessages(ctx, needCredential(cfg), *cid, *seq, *instance); e != nil {
			return e
		}
		return out.Encode(map[string]bool{"ok": true})
	case "cursor":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		defaultID, _ := cfg.instances.ID()
		instance := fs.String("agent-instance", defaultID, "self-reported instance ID for this cursor (defaults to COMITIA_AGENT_INSTANCE)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, e := api.MessageCursor(ctx, needCredential(cfg), *cid, *instance)
		if e != nil {
			return e
		}
		return out.Encode(map[string]int64{"sequence": v})
	default:
		return errUnknownCommand
	}
}

func flagProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}

// messageInstance applies explicit flags over COMITIA_AGENT_INSTANCE defaults;
// a label flag alone relabels the default instance.
func messageInstance(cfg config, fs *flag.FlagSet, id, label string) *comitia.AgentInstance {
	instance := cfg.instances.Message(nil)
	if flagProvided(fs, "agent-instance") {
		instance = &comitia.AgentInstance{ID: id, Label: label}
	} else if flagProvided(fs, "agent-instance-label") {
		if instance == nil {
			instance = &comitia.AgentInstance{}
		}
		instance.Label = label
	}
	return instance
}

func messageFilterFlags(fs *flag.FlagSet) *comitia.MessageFilters {
	f := new(comitia.MessageFilters)
	fs.BoolVar(&f.ExcludeSelf, "exclude-self", false, "omit this session's own --agent-instance messages")
	fs.BoolVar(&f.FromCursor, "from-cursor", false, "start at this instance's acknowledgement cursor; omit --after")
	fs.StringVar(&f.ReplyTo, "reply-to", "", "direct replies to this message in this context")
	fs.StringVar(&f.ThreadID, "thread-id", "", "root message and recursive descendants in this context")
	fs.StringVar(&f.FromSession, "from-session", "", "authenticated sender session ID")
	fs.StringVar(&f.ForInstance, "for-instance", "", "recipient instance, caller session with no instance recipients, or broadcast")
	fs.BoolVar(&f.StrictRecipients, "strict-recipients", false, "require both recipient lists to match caller session and --for-instance")
	fs.StringVar(&f.FromInstance, "from-instance", "", "self-reported sender instance")
	fs.StringVar(&f.TaskID, "task-id", "", "envelope task ID")
	fs.Func("schema", "exact envelope schema ID; repeat to match several schemas", func(value string) error {
		if f.Schema == "" && len(f.Schemas) == 0 {
			f.Schema = value
		} else {
			if len(f.Schemas) == 0 {
				f.Schemas = append(f.Schemas, f.Schema)
				f.Schema = ""
			}
			f.Schemas = append(f.Schemas, value)
		}
		return nil
	})
	return f
}
