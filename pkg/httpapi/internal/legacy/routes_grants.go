package legacy

import (
	"context"

	"github.com/krimvp/comitia/pkg/comitia"
)

func (h *handler) routeGrants(ctx context.Context, token, route string, q request, out *response) (found bool, err error) {
	switch route {
	case "grants/revoke":
		err = h.api.RevokeGrant(ctx, token, q.GrantID)
	case "grants/create":
		if q.Grant == nil {
			err = comitia.Invalidf("grant required")
			break
		}
		var v comitia.Grant
		v, err = h.api.GrantContext(ctx, token, *q.Grant)
		out.Grant = &v
	default:
		return false, nil
	}
	return true, err
}

func (c *Client) RevokeGrant(x context.Context, t, id string) error {
	return c.call(x, t, "grants/revoke", request{GrantID: id}, &response{})
}
func (c *Client) GrantContext(x context.Context, t string, i comitia.GrantInput) (comitia.Grant, error) {
	var o response
	e := c.call(x, t, "grants/create", request{Grant: &i}, &o)
	if o.Grant == nil {
		return comitia.Grant{}, missing(e)
	}
	return *o.Grant, e
}
