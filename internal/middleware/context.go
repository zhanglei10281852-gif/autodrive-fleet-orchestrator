package middleware

import (
	"context"
	"net/http"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
)

type principalKey struct{}

func WithPrincipal(ctx context.Context, principal auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func Principal(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(auth.Principal)
	return principal, ok
}

func RequirePrincipal(r *http.Request) (auth.Principal, bool) {
	return Principal(r.Context())
}
