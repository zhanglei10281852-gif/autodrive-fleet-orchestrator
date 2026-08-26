package httpapi

import (
	"net/http"
	"testing"
	"time"
)

func TestLogoutInvalidatesRefreshedAuthenticationState(t *testing.T) {
	harness := newAPIHarness(t)
	revokedToken := loginToken(t, harness)
	otherToken := loginToken(t, harness)

	harness.clock.Advance(time.Minute + time.Second)
	warm := harness.request(t, http.MethodGet, "/v1/auth/me", revokedToken, nil)
	if warm.Code != http.StatusOK {
		t.Fatalf("warm refreshed authentication status=%d body=%s", warm.Code, warm.Body.String())
	}

	logout := harness.request(t, http.MethodPost, "/v1/auth/logout", revokedToken, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}

	revoked := harness.request(t, http.MethodGet, "/v1/auth/me", revokedToken, nil)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked refreshed token remained authorized: status=%d body=%s", revoked.Code, revoked.Body.String())
	}

	unrelated := harness.request(t, http.MethodGet, "/v1/auth/me", otherToken, nil)
	if unrelated.Code != http.StatusOK {
		t.Fatalf("unrelated active session was invalidated: status=%d body=%s", unrelated.Code, unrelated.Body.String())
	}
}
