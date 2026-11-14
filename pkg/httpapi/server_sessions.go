package httpapi

import (
	"context"

	"github.com/krimvp/comitia/pkg/comitia"
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
