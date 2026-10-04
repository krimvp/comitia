package httpapi

import (
	"context"
	"github.com/krimvp/comitia"
	"time"
)

func (h *handler) sessionRoutes() map[string]endpoint {
	return map[string]endpoint{
		"principals/create": typedEndpoint(func(ctx context.Context, token string, q nameRequest) (principalResult, error) {
			v, err := h.api.CreatePrincipal(ctx, token, q.Name)
			return principalResult{ptr(principalDTO(v))}, err
		}),
		"sessions/mint": typedEndpoint(func(ctx context.Context, token string, q mintRequest) (credentialResult, error) {
			ttl, err := durationSeconds(q.TTLSeconds, q.TTL, 0, false)
			if err != nil {
				return credentialResult{}, err
			}
			v, err := h.api.MintSession(ctx, token, q.PrincipalID, ttl)
			return credentialResult{ptr(wireCredential(v))}, err
		}),
		"sessions/delegate": typedEndpoint(func(ctx context.Context, token string, q delegateRequest) (delegateResult, error) {
			if q.Delegate == nil {
				return delegateResult{}, comitia.Invalidf("delegate required")
			}
			d := q.Delegate
			ttl, err := durationSeconds(d.TTLSeconds, d.TTL, 0, false)
			if err != nil {
				return delegateResult{}, err
			}
			input := comitia.DelegateInput{PrincipalName: d.PrincipalName, TTL: ttl, Grants: mapSlice(d.Grants, func(g delegatedGrantInput) comitia.DelegatedGrant { return comitia.DelegatedGrant(g) })}
			p, c, grants, err := h.api.DelegateSession(ctx, token, input)
			return delegateResult{ptr(principalDTO(p)), ptr(wireCredential(c)), mapSlice(grants, func(g comitia.Grant) grantDTO { return grantDTO(g) })}, err
		}),
		"sessions/revoke": typedEndpoint(func(ctx context.Context, token string, q revokeSessionRequest) (emptyResult, error) {
			return emptyResult{}, h.api.RevokeSession(ctx, token, q.SessionID)
		}),
	}
}

func (c *Client) Bootstrap(context.Context, comitia.BootstrapInput) (comitia.Principal, comitia.Credential, error) {
	return comitia.Principal{}, comitia.Credential{}, ErrBootstrapOffline
}
func (c *Client) CreatePrincipal(ctx context.Context, token, name string) (comitia.Principal, error) {
	var out principalResult
	err := c.call(ctx, token, "principals/create", nameRequest{Name: name}, &out)
	if err = requiredResult(err, out.Principal); err != nil {
		return comitia.Principal{}, err
	}
	return comitia.Principal(*out.Principal), nil
}
func (c *Client) MintSession(ctx context.Context, token, principal string, ttl time.Duration) (comitia.Credential, error) {
	var out credentialResult
	err := c.call(ctx, token, "sessions/mint", mintRequest{PrincipalID: principal, TTLSeconds: ptr(ttl.Seconds())}, &out)
	if err = requiredResult(err, out.Credential); err != nil {
		return comitia.Credential{}, err
	}
	return out.Credential.core(), nil
}
func (c *Client) DelegateSession(ctx context.Context, token string, input comitia.DelegateInput) (comitia.Principal, comitia.Credential, []comitia.Grant, error) {
	d := delegateInput{PrincipalName: input.PrincipalName, TTLSeconds: ptr(input.TTL.Seconds()), Grants: mapSlice(input.Grants, func(g comitia.DelegatedGrant) delegatedGrantInput { return delegatedGrantInput(g) })}
	var out delegateResult
	err := c.call(ctx, token, "sessions/delegate", delegateRequest{&d}, &out)
	if err = requiredResult(err, out.Principal, out.Credential); err != nil {
		return comitia.Principal{}, comitia.Credential{}, nil, err
	}
	return comitia.Principal(*out.Principal), out.Credential.core(), mapSlice(out.Grants, func(g grantDTO) comitia.Grant { return comitia.Grant(g) }), nil
}
func (c *Client) RevokeSession(ctx context.Context, token, id string) error {
	return c.call(ctx, token, "sessions/revoke", revokeSessionRequest{id}, &emptyResult{})
}
