package httpapi

import (
	"context"
	"time"

	"github.com/krimvp/comitia"
)

func (h *handler) routeSessions(ctx context.Context, token, route string, q request, out *response) (found bool, err error) {
	switch route {
	case "principals/create":
		var v comitia.Principal
		v, err = h.api.CreatePrincipal(ctx, token, q.Name)
		out.Principal = &v
	case "sessions/mint":
		var v comitia.Credential
		v, err = h.api.MintSession(ctx, token, q.PrincipalID, q.TTL)
		out.Credential = &v
	case "sessions/delegate":
		if q.Delegate == nil {
			err = comitia.ErrInvalid
			break
		}
		var p comitia.Principal
		var c comitia.Credential
		var g []comitia.Grant
		p, c, g, err = h.api.DelegateSession(ctx, token, *q.Delegate)
		out.Principal = &p
		out.Credential = &c
		out.Grants = g
	case "sessions/revoke":
		err = h.api.RevokeSession(ctx, token, q.SessionID)
	default:
		return false, nil
	}
	return true, err
}

func (c *Client) Bootstrap(context.Context, comitia.BootstrapInput) (comitia.Principal, comitia.Credential, error) {
	return comitia.Principal{}, comitia.Credential{}, ErrBootstrapOffline
}
func (c *Client) CreatePrincipal(x context.Context, t, n string) (comitia.Principal, error) {
	var o response
	e := c.call(x, t, "principals/create", request{Name: n}, &o)
	if o.Principal == nil {
		return comitia.Principal{}, missing(e)
	}
	return *o.Principal, e
}
func (c *Client) MintSession(x context.Context, t, p string, d time.Duration) (comitia.Credential, error) {
	var o response
	e := c.call(x, t, "sessions/mint", request{PrincipalID: p, TTL: d}, &o)
	if o.Credential == nil {
		return comitia.Credential{}, missing(e)
	}
	return *o.Credential, e
}
func (c *Client) DelegateSession(x context.Context, t string, i comitia.DelegateInput) (comitia.Principal, comitia.Credential, []comitia.Grant, error) {
	var o response
	e := c.call(x, t, "sessions/delegate", request{Delegate: &i}, &o)
	if o.Principal == nil || o.Credential == nil {
		return comitia.Principal{}, comitia.Credential{}, nil, missing(e)
	}
	return *o.Principal, *o.Credential, o.Grants, e
}
func (c *Client) RevokeSession(x context.Context, t, id string) error {
	return c.call(x, t, "sessions/revoke", request{SessionID: id}, &response{})
}
