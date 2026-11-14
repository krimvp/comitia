package legacy

import (
	"context"

	"github.com/krimvp/comitia/pkg/comitia"
)

func (h *handler) routeServerInfo(ctx context.Context, token, route string, _ request, out *response) (bool, error) {
	if route == "storage_status" {
		api, ok := h.api.(comitia.StorageStatusAPI)
		if !ok {
			return true, comitia.ErrNotFound
		}
		status, err := api.StorageStatus(ctx, token)
		out.StorageStatus = &status
		return true, err
	}
	if route != "server_info" {
		return false, nil
	}
	info, err := h.api.ServerInfo(ctx, token)
	out.ServerInfo = &info
	return true, err
}

func (c *Client) ServerInfo(ctx context.Context, token string) (comitia.ServerInfo, error) {
	var out response
	err := c.call(ctx, token, "server_info", request{}, &out)
	if out.ServerInfo == nil {
		return comitia.ServerInfo{}, missing(err)
	}
	return *out.ServerInfo, err
}

func (c *Client) StorageStatus(ctx context.Context, token string) (comitia.StorageStatus, error) {
	var out response
	err := c.call(ctx, token, "storage_status", request{}, &out)
	if out.StorageStatus == nil {
		return comitia.StorageStatus{}, missing(err)
	}
	return *out.StorageStatus, err
}
