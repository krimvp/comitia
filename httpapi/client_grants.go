package httpapi

import (
	"context"

	"github.com/krimvp/comitia"
)

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
