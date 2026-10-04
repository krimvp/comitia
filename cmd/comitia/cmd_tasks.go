package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/krimvp/comitia"
)

func runTasks(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "task-create":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		title := fs.String("title", "", "task title")
		body := fs.String("body", "", "task body")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.CreateTask(ctx, needCredential(cfg), *cid, comitia.TaskInput{Title: *title, Body: *body})
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-get":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *tid == "" {
			return errors.New("task-id is required")
		}
		v, e := api.GetTask(ctx, needCredential(cfg), *tid)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-list":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		status := fs.String("status", "", "task status (pending, leased, completed)")
		limit := fs.Int("limit", 100, "maximum tasks (1-1000)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *cid == "" {
			return errors.New("context-id is required")
		}
		if *limit < 1 || *limit > 1000 {
			return comitia.ErrInvalid
		}
		st, e := parseTaskStatus(*status)
		if e != nil {
			return e
		}
		v, e := api.ListTasks(ctx, needCredential(cfg), *cid, st, *limit)
		if e != nil {
			return e
		}
		if v == nil {
			v = []comitia.Task{}
		}
		return out.Encode(v)
	case "task-claim":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		lease := fs.Duration("lease", 5*time.Minute, "lease duration")
		openEnded := fs.Bool("open-ended", false, "hold the lease until completed or released (capped at session expiry)")
		instanceID := fs.String("agent-instance", "", "self-reported instance ID holding the claim")
		instanceLabel := fs.String("agent-instance-label", "", "optional self-reported instance label")
		if err := fs.Parse(args); err != nil {
			return err
		}
		var instance []comitia.AgentInstance
		if *instanceID != "" || *instanceLabel != "" {
			instance = append(instance, comitia.AgentInstance{ID: *instanceID, Label: *instanceLabel})
		}
		v, e := api.ClaimTask(ctx, needCredential(cfg), *tid, openLease(*lease, *openEnded), instance...)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-renew":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		token := fs.String("claim-token", "", "claim token")
		ver := fs.Int64("lease-version", 0, "lease version")
		lease := fs.Duration("lease", 5*time.Minute, "lease duration")
		openEnded := fs.Bool("open-ended", false, "hold the lease until completed or released (capped at session expiry)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.RenewClaim(ctx, needCredential(cfg), *tid, *token, *ver, openLease(*lease, *openEnded))
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-complete":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		token := fs.String("claim-token", "", "claim token")
		ver := fs.Int64("lease-version", 0, "lease version")
		if err := fs.Parse(args); err != nil {
			return err
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
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *tid == "" {
			return errors.New("task-id is required")
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
	case string(comitia.TaskCompleted):
		return comitia.TaskCompleted, nil
	default:
		return "", fmt.Errorf("invalid task status %q", s)
	}
}

func openLease(lease time.Duration, open bool) time.Duration {
	if open {
		return comitia.OpenLease
	}
	return lease
}
