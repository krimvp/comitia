package httpapi

import (
	"context"

	"github.com/krimvp/comitia"
)

func (h *handler) routeServerInfo(ctx context.Context, token, route string, _ request, out *response) (bool, error) {
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
