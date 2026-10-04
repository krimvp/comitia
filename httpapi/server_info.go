package httpapi

import (
	"context"
	"github.com/krimvp/comitia"
)

func (h *handler) infoRoutes() map[string]endpoint {
	return map[string]endpoint{
		"server_info": typedEndpoint(func(ctx context.Context, token string, _ emptyRequest) (serverInfoResult, error) {
			v, err := h.api.ServerInfo(ctx, token)
			return serverInfoResult{&v}, err
		}),
		"storage_status": typedEndpoint(func(ctx context.Context, token string, _ emptyRequest) (storageStatusResult, error) {
			api, ok := h.api.(comitia.StorageStatusAPI)
			if !ok {
				return storageStatusResult{}, comitia.ErrNotFound
			}
			v, err := api.StorageStatus(ctx, token)
			return storageStatusResult{&v}, err
		}),
	}
}
func (c *Client) ServerInfo(ctx context.Context, token string) (comitia.ServerInfo, error) {
	var out serverInfoResult
	err := c.call(ctx, token, "server_info", emptyRequest{}, &out)
	if err = requiredResult(err, out.ServerInfo); err != nil {
		return comitia.ServerInfo{}, err
	}
	return *out.ServerInfo, nil
}
func (c *Client) StorageStatus(ctx context.Context, token string) (comitia.StorageStatus, error) {
	var out storageStatusResult
	err := c.call(ctx, token, "storage_status", emptyRequest{}, &out)
	if err = requiredResult(err, out.StorageStatus); err != nil {
		return comitia.StorageStatus{}, err
	}
	return *out.StorageStatus, nil
}
