package httpapi

import (
	"context"

	"github.com/krimvp/comitia/pkg/comitia"
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
