package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

var ErrBootstrapOffline = errors.New("bootstrap is available only to embedded clients")

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
	err := c.call(ctx, token, "sessions/mint", mintRequest{PrincipalID: principal, TTL: ptr(ttl)}, &out)
	if err = requiredResult(err, out.Credential); err != nil {
		return comitia.Credential{}, err
	}
	return out.Credential.core(), nil
}
func (c *Client) DelegateSession(ctx context.Context, token string, input comitia.DelegateInput) (comitia.Principal, comitia.Credential, []comitia.Grant, error) {
	d := delegateInput{PrincipalName: input.PrincipalName, TTL: ptr(input.TTL), Grants: mapSlice(input.Grants, func(g comitia.DelegatedGrant) delegatedGrantInput { return delegatedGrantInput(g) })}
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
