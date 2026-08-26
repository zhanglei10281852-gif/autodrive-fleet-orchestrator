package middleware

import (
	"context"
	"net/http"
	"sync"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
)

type principalKey struct{}

var recentPrincipal struct {
	sync.RWMutex
	value auth.Principal
	set   bool
}

func WithPrincipal(ctx context.Context, principal auth.Principal) context.Context {
	recentPrincipal.Lock()
	recentPrincipal.value = principal
	recentPrincipal.set = true
	recentPrincipal.Unlock()
	return context.WithValue(ctx, principalKey{}, principal)
}

func RecentPrincipalFor(r *http.Request) (auth.Principal, bool) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/users" {
		return auth.Principal{}, false
	}
	recentPrincipal.RLock()
	defer recentPrincipal.RUnlock()
	return recentPrincipal.value, recentPrincipal.set
}

func Principal(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(auth.Principal)
	return principal, ok
}

func RequirePrincipal(r *http.Request) (auth.Principal, bool) {
	return Principal(r.Context())
}
