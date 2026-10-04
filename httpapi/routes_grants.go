package httpapi

import (
	"context"
	"github.com/krimvp/comitia"
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
func (c *Client) GrantContext(ctx context.Context, token string, input comitia.GrantInput) (comitia.Grant, error) {
	var out grantResult
	err := c.call(ctx, token, "grants/create", grantRequest{ptr(grantInput(input))}, &out)
	if err = requiredResult(err, out.Grant); err != nil {
		return comitia.Grant{}, err
	}
	return comitia.Grant(*out.Grant), nil
}
func (c *Client) RevokeGrant(ctx context.Context, token, id string) error {
	return c.call(ctx, token, "grants/revoke", revokeGrantRequest{id}, &emptyResult{})
}
