package httpapi

import (
	"context"
	"fmt"

	"github.com/krimvp/comitia"
)

var ErrTaskFiltersUnsupported = fmt.Errorf("%w: server does not support task filters; upgrade the server before retrying", comitia.ErrInvalid)

func (q listTasksRequest) filters() comitia.TaskFilters {
	return comitia.TaskFilters{Claimable: q.Claimable, OwnerSession: q.OwnerSession, OwnerInstance: q.OwnerInstance, Mine: q.Mine}
}
func filteredTaskRequest(cid string, status comitia.TaskStatus, f comitia.TaskFilters, after string, limit int, page bool) listTasksRequest {
	return listTasksRequest{ContextID: cid, Status: status, Claimable: f.Claimable, OwnerSession: f.OwnerSession, OwnerInstance: f.OwnerInstance, Mine: f.Mine, AfterID: after, Limit: ptr(limit), Page: page}
}
func taskFilterError(err error) error {
	for _, name := range []string{"claimable", "owner_session", "owner_instance", "mine"} {
		if unknownField(err, name) {
			return ErrTaskFiltersUnsupported
		}
	}
	return err
}
func (c *Client) ListTasksFiltered(ctx context.Context, token, cid string, status comitia.TaskStatus, f comitia.TaskFilters, limit int) ([]comitia.Task, error) {
	f = f.WithInstance(c.InstanceDefaults)
	if f == (comitia.TaskFilters{}) {
		return c.ListTasks(ctx, token, cid, status, limit)
	}
	var out tasksResult
	err := c.call(ctx, token, "tasks/list", filteredTaskRequest(cid, status, f, "", limit, false), &out)
	return mapSlice(out.Tasks, func(t taskDTO) comitia.Task { return comitia.Task(t) }), taskFilterError(err)
}
func (c *Client) ListTasksPageFiltered(ctx context.Context, token, cid string, status comitia.TaskStatus, f comitia.TaskFilters, after string, limit int) (comitia.TaskPage, error) {
	f = f.WithInstance(c.InstanceDefaults)
	if f == (comitia.TaskFilters{}) {
		return c.ListTasksPage(ctx, token, cid, status, after, limit)
	}
	var out struct {
		TaskPage *taskPageDTO `json:"task_page,omitempty"`
	}
	err := c.call(ctx, token, "tasks/list", filteredTaskRequest(cid, status, f, after, limit, true), &out)
	if pagingUnsupported(err) || err == nil && out.TaskPage == nil {
		return comitia.TaskPage{}, ErrTaskFiltersUnsupported
	}
	if err != nil {
		return comitia.TaskPage{}, taskFilterError(err)
	}
	return out.TaskPage.core(), nil
}

var _ comitia.TaskFilterAPI = (*Client)(nil)
