package httpapi

import (
	"context"

	"github.com/krimvp/comitia/pkg/comitia"
)

func (h *handler) grantRoutes() map[string]endpoint {
	return map[string]endpoint{
		"grants/create": typedEndpoint(func(ctx context.Context, token string, q grantRequest) (grantResult, error) {
			if q.Grant == nil {
				return grantResult{}, comitia.Invalidf("grant required")
			}
			v, err := h.api.GrantContext(ctx, token, comitia.GrantInput(*q.Grant))
			return grantResult{ptr(grantDTO(v))}, err
		}),
		"grants/revoke": typedEndpoint(func(ctx context.Context, token string, q revokeGrantRequest) (emptyResult, error) {
			return emptyResult{}, h.api.RevokeGrant(ctx, token, q.GrantID)
		}),
	}
}
