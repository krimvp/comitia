package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/krimvp/comitia"
)

func runTasks(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "task-fail", "task-cancel":
		return runTaskOutcome(ctx, cfg, api, out, cmd, args)
	case "task-create":
		fs := newFlags(cmd)
		operationID := fs.String("operation-id", "", "stable session-scoped mutation ID for recovery after lost responses")
		cid := fs.String("context-id", "", "context ID")
		title := fs.String("title", "", "task title")
		body := fs.String("body", "", "task body")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "context-id", "title"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, e := comitia.CreateTaskOperation(ctx, api, needCredential(cfg), *cid, comitia.TaskInput{Title: *title, Body: *body, OperationID: *operationID})
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-get":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "task-id"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, e := api.GetTask(ctx, needCredential(cfg), *tid)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-list":
		fs := newFlags(cmd)
		claimable := fs.Bool("claimable", false, "pending tasks and expired leases")
		ownerSession := fs.String("owner-session", "", "session ID holding live leases")
		ownerInstance := fs.String("owner-instance", "", "instance ID holding live leases")
		mine := fs.Bool("mine", false, "caller live leases, scoped to configured instance when available")
		page := fs.Bool("page", false, "return tasks, next_after and has_more")
		after := fs.String("after-id", "", "exclusive cursor from next_after (implies --page)")
		cid := fs.String("context-id", "", "context ID")
		status := fs.String("status", "", "task status (pending, leased, completed, failed, cancelled)")
		limit := fs.Int("limit", 100, "maximum tasks (1-1000)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "context-id"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if *limit < 1 || *limit > 1000 {
			return comitia.Invalidf("limit must be between 1 and 1000")
		}
		st, e := parseTaskStatus(*status)
		if e != nil {
			return e
		}
		f := (comitia.TaskFilters{Claimable: *claimable, OwnerSession: *ownerSession, OwnerInstance: *ownerInstance, Mine: *mine}).WithInstance(cfg.instances)
		if *page || *after != "" {
			v, e := comitia.ListTaskPageFiltered(ctx, api, needCredential(cfg), *cid, st, f, *after, *limit)
			if e != nil {
				return e
			}
			return out.Encode(v)
		}
		v, e := comitia.ListTasksFiltered(ctx, api, needCredential(cfg), *cid, st, f, *limit)
		if e != nil {
			return e
		}
		if v == nil {
			v = []comitia.Task{}
		}
		return out.Encode(v)
	case "task-claim":
		fs := newFlags(cmd)
		operationID := fs.String("operation-id", "", "stable session-scoped mutation ID for recovery after lost responses")
		tid := fs.String("task-id", "", "task ID")
		lease := fs.Duration("lease", 5*time.Minute, "lease duration")
		openEnded := fs.Bool("open-ended", false, "hold the lease until completed or released (capped at session expiry)")
		instanceID := fs.String("agent-instance", "", "self-reported instance ID holding the claim (defaults to COMITIA_AGENT_INSTANCE)")
		instanceLabel := fs.String("agent-instance-label", "", "optional self-reported instance label (defaults to COMITIA_AGENT_INSTANCE_LABEL)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "task-id"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		owner := cfg.instances.Message(nil)
		if flagProvided(fs, "agent-instance") {
			owner = &comitia.AgentInstance{ID: *instanceID, Label: *instanceLabel}
		} else if flagProvided(fs, "agent-instance-label") {
			if owner == nil {
				owner = &comitia.AgentInstance{}
			}
			owner.Label = *instanceLabel
		}
		var instance []comitia.AgentInstance
		if owner != nil {
			instance = append(instance, *owner)
		}
		v, e := comitia.ClaimTaskOperation(ctx, api, needCredential(cfg), *tid, openLease(*lease, *openEnded), *operationID, instance...)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-recover":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		ver := fs.Int64("lease-version", 0, "current positive lease version from task-get")
		instanceID := fs.String("agent-instance", "", "matching holder instance ID (defaults to COMITIA_AGENT_INSTANCE)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "task-id", "lease-version"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		owner := cfg.instances.Message(nil)
		if flagProvided(fs, "agent-instance") {
			owner = &comitia.AgentInstance{ID: *instanceID}
		}
		var instance []comitia.AgentInstance
		if owner != nil {
			instance = append(instance, *owner)
		}
		v, e := comitia.RecoverClaim(ctx, api, needCredential(cfg), *tid, *ver, instance...)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-renew":
		fs := newFlags(cmd)
		operationID := fs.String("operation-id", "", "stable session-scoped mutation ID for recovery after lost responses")
		tid := fs.String("task-id", "", "task ID")
		token := fs.String("claim-token", "", "claim token")
		ver := fs.Int64("lease-version", 0, "lease version")
		lease := fs.Duration("lease", 5*time.Minute, "lease duration")
		openEnded := fs.Bool("open-ended", false, "hold the lease until completed or released (capped at session expiry)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "task-id", "claim-token", "lease-version"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, e := comitia.RenewClaimOperation(ctx, api, needCredential(cfg), *tid, *token, *ver, openLease(*lease, *openEnded), *operationID)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-complete":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		token := fs.String("claim-token", "", "claim token")
		ver := fs.Int64("lease-version", 0, "lease version")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "task-id", "claim-token", "lease-version"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, e := api.CompleteTask(ctx, needCredential(cfg), *tid, *token, *ver)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-release":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		token := fs.String("claim-token", "", "claim token (optional for a context admin)")
		ver := fs.Int64("lease-version", 0, "lease version")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "task-id", "lease-version"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, e := api.ReleaseTask(ctx, needCredential(cfg), *tid, *token, *ver)
		if e != nil {
			return e
		}
		return out.Encode(v)
	default:
		return errUnknownCommand
	}
}

func parseTaskStatus(s string) (comitia.TaskStatus, error) {
	switch s {
	case "":
		return "", nil
	case string(comitia.TaskPending):
		return comitia.TaskPending, nil
	case string(comitia.TaskLeased):
		return comitia.TaskLeased, nil
	case string(comitia.TaskFailed):
		return comitia.TaskFailed, nil
	case string(comitia.TaskCancelled):
		return comitia.TaskCancelled, nil
	case string(comitia.TaskCompleted):
		return comitia.TaskCompleted, nil
	default:
		return "", comitia.Invalidf("status must be pending, leased, completed, failed or cancelled")
	}
}

func openLease(lease time.Duration, open bool) time.Duration {
	if open {
		return comitia.OpenLease
	}
	return lease
}
