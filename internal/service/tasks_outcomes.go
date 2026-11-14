package service

import (
	"context"
	"crypto/subtle"
	"strings"
	"unicode/utf8"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

func FailTask(ctx context.Context, api model.TaskAPI, raw, tid string, in model.TaskOutcomeInput) (model.Task, error) {
	outcomes, ok := api.(model.TaskOutcomeAPI)
	if !ok {
		return model.Task{}, model.Invalidf("task outcomes unsupported by API")
	}
	return outcomes.FailTask(ctx, raw, tid, in)
}

func CancelTask(ctx context.Context, api model.TaskAPI, raw, tid string, in model.TaskOutcomeInput) (model.Task, error) {
	outcomes, ok := api.(model.TaskOutcomeAPI)
	if !ok {
		return model.Task{}, model.Invalidf("task outcomes unsupported by API")
	}
	return outcomes.CancelTask(ctx, raw, tid, in)
}

func (s *Service) FailTask(ctx context.Context, raw, tid string, in model.TaskOutcomeInput) (model.Task, error) {
	return s.finishTaskOutcome(ctx, raw, tid, in, model.TaskFailed)
}

// CancelTask accepts a live holder's fence or context-admin authority. Both
// paths must name the current version; only admins may cancel pending work.
func (s *Service) CancelTask(ctx context.Context, raw, tid string, in model.TaskOutcomeInput) (model.Task, error) {
	return s.finishTaskOutcome(ctx, raw, tid, in, model.TaskCancelled)
}

func (s *Service) finishTaskOutcome(ctx context.Context, raw, tid string, in model.TaskOutcomeInput, status model.TaskStatus) (model.Task, error) {
	if e := validOperationID(in.OperationID); e != nil {
		return model.Task{}, e
	}
	if in.LeaseVersion < 0 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 || !utf8.ValidString(in.Reason) {
		return model.Task{}, model.Invalidf("nonnegative lease_version and nonempty UTF-8 reason of at most 4096 bytes required")
	}
	var result model.Task
	eventContext := ""
	err := s.store.Update(ctx, func(w model.WriteStore) error {
		t, e := s.privateTask(w, raw, tid)
		if e != nil {
			return e
		}
		a, _, claimErr := s.require(w, raw, t.ContextID, model.PermissionClaim)
		admin := false
		if status == model.TaskCancelled {
			if authorized, _, err := s.require(w, raw, t.ContextID, model.PermissionAdmin); err == nil {
				a, admin = authorized, true
			}
		}
		if !admin && claimErr != nil {
			return claimErr
		}
		fingerprint := taskFingerprint(status, tid, tokenHash(in.ClaimToken), in.LeaseVersion, in.Reason)
		receipt, found, e := taskReceipt(w, a.session.ID, in.OperationID, fingerprint)
		if e != nil {
			return e
		}
		if found {
			result, e = s.holderTaskView(w, a, receipt)
			return e
		}
		if schema.TerminalTaskStatus(t.Status) {
			return model.ErrConflict
		}
		if t.LeaseVersion != in.LeaseVersion {
			return model.ErrStaleClaim
		}
		now := s.now().UTC()
		if !admin && (t.Status != model.TaskLeased || t.LeaseOwnerSessionID != a.session.ID || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(now) || in.ClaimToken == "" || subtle.ConstantTimeCompare([]byte(t.LeaseTokenHash), []byte(tokenHash(in.ClaimToken))) != 1) {
			return model.ErrStaleClaim
		}
		previous := t.Status
		t.Status, t.OutcomeReason = status, in.Reason
		t.LeaseExpiresAt, t.LeaseTokenHash = nil, ""
		t.UpdatedAt = now
		if e := w.PutTask(t); e != nil {
			return e
		}
		if e := putTaskEvent(w, a.session.ID, t, string(status), previous); e != nil {
			return e
		}
		if e := putTaskReceipt(w, a.session.ID, in.OperationID, fingerprint, t); e != nil {
			return e
		}
		eventContext = t.ContextID
		result, e = s.holderTaskView(w, a, t)
		return e
	})
	if err != nil {
		return model.Task{}, err
	}
	s.notifyTaskEvent(eventContext)
	return result, nil
}
