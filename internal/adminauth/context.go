package adminauth

import "context"

// Principal identifies who is performing an admin action and from where. It is
// carried in the request context by the admin auth middleware so downstream
// handlers and the audit recorder can attribute mutations.
type Principal struct {
	SessionID string
	ClientIP  string
}

type principalContextKey struct{}

// ContextWithPrincipal attaches an authenticated admin principal to the
// context. Secret managers must call this after authenticating so that any
// audit trail recorded later can attribute the action.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated admin principal carried in
// the context and whether one is present. An absent principal means the
// request is not authenticated (e.g. a failed login attempt) — callers default
// to an anonymous/empty attribution.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
