// The message commands: send, message-get, participants, replay, wait, watch,
// ack and cursor, one function each, with their shared flag helpers.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/krimvp/comitia"
)

func runMessages(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "send":
		return runSend(ctx, cfg, api, out, cmd, args)
	case "message-get":
		return runMessageGet(ctx, cfg, api, out, cmd, args)
	case "participants":
		return runParticipants(ctx, cfg, api, out, cmd, args)
	case "replay", "wait", "watch":
		return runMessageRead(ctx, cfg, api, out, cmd, args)
	case "ack":
		return runAck(ctx, cfg, api, out, cmd, args)
	case "cursor":
		return runCursor(ctx, cfg, api, out, cmd, args)
	default:
		return errUnknownCommand
	}
}

func runSend(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	cid := fs.String("context-id", "", "context ID")
	key := fs.String("dedupe-key", "", "stable deduplication key")
	body := fs.String("body", "", "message body")
	bodyFile := fs.String("body-file", "", "message body file, verbatim; - reads stdin")
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
	envelopeFile := fs.String("envelope-file", "", "validated message envelope JSON file; - reads stdin")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireFlags(fs, "context-id", "dedupe-key"); err != nil {
		return err
	}
	if err := requireAny(fs, "body", "body-file", "envelope-file"); err != nil {
		return err
	}
	if flagProvided(fs, "body") && flagProvided(fs, "body-file") {
		return comitia.Invalidf("--body and --body-file are mutually exclusive")
	}
	if *bodyFile == "-" && *envelopeFile == "-" {
		return comitia.Invalidf("only one of --body-file and --envelope-file can read stdin")
	}
	if cfg.parseOnly {
		return nil
	}
	if *bodyFile != "" {
		b, e := readBody(*bodyFile)
		if e != nil {
			return e
		}
		*body = b
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
}

func runMessageGet(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	cid := fs.String("context-id", "", "readable context ID")
	mid := fs.String("message-id", "", "message ID in this context")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireFlags(fs, "context-id", "message-id"); err != nil {
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
}

func runParticipants(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	page := fs.Bool("page", false, "return participants, next_after and has_more")
	after := fs.String("after-id", "", "exclusive cursor from next_after (implies --page)")
	cid := fs.String("context-id", "", "work context ID")
	limit := fs.Int("limit", comitia.DefaultListLimit, listLimitUsage("participants"))
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireFlags(fs, "context-id"); err != nil {
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
}

func runMessageRead(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	cid := fs.String("context-id", "", "context ID")
	after := fs.Int64("after", 0, "exclusive sequence cursor")
	limit := fs.Int("limit", comitia.DefaultListLimit, "maximum matching messages for replay")
	timeoutSeconds := fs.Float64("timeout-seconds", comitia.DefaultMessageWait.Seconds(), fmt.Sprintf("wait timeout seconds (capped at %g)", comitia.MaxMessageWait.Seconds()))
	defaultID, _ := cfg.instances.ID()
	instance := fs.String("agent-instance", defaultID, "self-reported instance ID for presence and the watch cursor (defaults to COMITIA_AGENT_INSTANCE)")
	jsonLines := fs.Bool("json-lines", false, "full messages as JSON lines; requires --manual-ack or --no-ack")
	manualAck := fs.Bool("manual-ack", false, "acknowledge separately after durable business handling")
	noAck := fs.Bool("no-ack", false, "do not acknowledge printed watch messages")
	page := fs.Bool("page", false, "return messages, next_after and has_more")
	filters := messageFilterFlags(fs)
	fs.IntVar(&filters.MaxBytes, "max-bytes", 0, fmt.Sprintf("encoded messages-array byte budget (default %d, max %d; first message intact)", comitia.DefaultMessageBytes, comitia.MaxMessageBytes))
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireFlags(fs, "context-id"); err != nil {
		return err
	}
	if err := messageReadFlagsApply(fs, cmd); err != nil {
		return err
	}
	if filters.FromCursor && flagProvided(fs, "after") {
		return comitia.Invalidf("--from-cursor cannot be combined with --after")
	}
	if *jsonLines && !*manualAck && !*noAck {
		return comitia.Invalidf("--json-lines requires --manual-ack or --no-ack")
	}
	if cfg.parseOnly {
		return nil
	}
	filters.AgentInstance = instance
	if cmd == "watch" && !flagProvided(fs, "for-instance") {
		filters.ForInstance = *instance
	}
	if err := comitia.CheckMessageQueryAPI(api, *filters); err != nil {
		return err
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
}

func runAck(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	cid := fs.String("context-id", "", "context ID")
	seq := fs.Int64("sequence", 0, "inclusive sequence (required)")
	defaultID, _ := cfg.instances.ID()
	instance := fs.String("agent-instance", defaultID, "self-reported instance ID for this cursor (defaults to COMITIA_AGENT_INSTANCE)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireFlags(fs, "context-id", "sequence"); err != nil {
		return err
	}
	if cfg.parseOnly {
		return nil
	}
	if e := api.AckMessages(ctx, needCredential(cfg), *cid, *seq, *instance); e != nil {
		return e
	}
	return out.Encode(map[string]bool{"ok": true})
}

func runCursor(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	cid := fs.String("context-id", "", "context ID")
	defaultID, _ := cfg.instances.ID()
	instance := fs.String("agent-instance", defaultID, "self-reported instance ID for this cursor (defaults to COMITIA_AGENT_INSTANCE)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireFlags(fs, "context-id"); err != nil {
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
}

// messageReadOnly lists the replay, wait and watch flags that only some of
// them use; the shared flag set would otherwise accept them without effect.
var messageReadOnly = map[string][]string{
	"limit": {"replay"}, "timeout-seconds": {"wait", "watch"},
	"after": {"replay", "wait"}, "from-cursor": {"replay", "wait"}, "page": {"replay", "wait"},
	"json-lines": {"watch"}, "manual-ack": {"watch"}, "no-ack": {"watch"},
}

func messageReadFlagsApply(fs *flag.FlagSet, cmd string) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		if only, ok := messageReadOnly[f.Name]; ok && err == nil && !slices.Contains(only, cmd) {
			err = comitia.Invalidf("--%s applies only to %s", f.Name, strings.Join(only, " and "))
		}
	})
	return err
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
