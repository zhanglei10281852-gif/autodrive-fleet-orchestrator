package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/middleware"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

type apiHarness struct {
	handler http.Handler
	clock   *clock.Manual
	store   *sqlite.Store
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	businessClock := clock.NewManual(now)
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ids := idgen.NewSequence(1)
	authService := service.NewAuth(store, businessClock, ids, time.Hour)
	if err := authService.Bootstrap(context.Background(), "admin", "change-me-now"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := New(
		authService,
		service.NewFleet(store, businessClock, ids),
		service.NewDispatch(store, businessClock, ids),
		service.NewOperations(store, businessClock, ids, time.Minute),
		service.NewResource(store, businessClock, ids),
		store,
		logger,
		1<<20,
	)
	handler := middleware.Chain(api.Handler(), middleware.RequestID(ids))
	return &apiHarness{handler: handler, clock: businessClock, store: store}
}

func (h *apiHarness) request(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

func loginToken(t *testing.T, harness *apiHarness) string {
	t.Helper()
	response := harness.request(t, http.MethodPost, "/v1/auth/login", "", map[string]any{"username": "admin", "password": "change-me-now"})
	if response.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
	}
	var result service.LoginResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if result.Token == "" || result.Principal.Role != auth.RoleFleetAdmin || result.Principal.SessionID == "" {
		t.Fatalf("invalid login result: %+v", result)
	}
	return result.Token
}

func TestHealthAndReadiness(t *testing.T) {
	harness := newAPIHarness(t)
	health := harness.request(t, http.MethodGet, "/healthz", "", nil)
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"alive"`) {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	ready := harness.request(t, http.MethodGet, "/readyz", "", nil)
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), `"ready"`) {
		t.Fatalf("ready status=%d body=%s", ready.Code, ready.Body.String())
	}
	if ready.Header().Get("X-Request-ID") == "" {
		t.Fatal("readiness response has no request id")
	}
}

func TestLoginMeLogoutLifecycle(t *testing.T) {
	harness := newAPIHarness(t)
	token := loginToken(t, harness)
	me := harness.request(t, http.MethodGet, "/v1/auth/me", token, nil)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"fleet_admin"`) {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}
	logout := harness.request(t, http.MethodPost, "/v1/auth/logout", token, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	revoked := harness.request(t, http.MethodGet, "/v1/auth/me", token, nil)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(revoked.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if response.Error.Code != "unauthorized" || response.Error.RequestID == "" {
		t.Fatalf("unexpected error contract: %+v", response)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	harness := newAPIHarness(t)
	token := loginToken(t, harness)
	harness.clock.Advance(time.Hour + time.Second)
	response := harness.request(t, http.MethodGet, "/v1/auth/me", token, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired token status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWrongPasswordAndUnknownUserAreIndistinguishable(t *testing.T) {
	harness := newAPIHarness(t)
	tests := []map[string]any{
		{"username": "admin", "password": "wrong-password"},
		{"username": "unknown", "password": "wrong-password"},
	}
	for index, input := range tests {
		response := harness.request(t, http.MethodPost, "/v1/auth/login", "", input)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("case %d status=%d body=%s", index, response.Code, response.Body.String())
		}
		var body ErrorResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != "unauthorized" || body.Error.Message != "authentication is required or has expired" {
			t.Fatalf("case %d leaked identity: %+v", index, body)
		}
	}
}

func TestCreateRoleUsersAndAuthorization(t *testing.T) {
	harness := newAPIHarness(t)
	adminToken := loginToken(t, harness)
	for _, role := range []auth.Role{auth.RoleDispatcher, auth.RoleSafetyOperator} {
		response := harness.request(t, http.MethodPost, "/v1/users", adminToken, map[string]any{
			"username": string(role) + "-one", "password": "strong-password", "role": role,
		})
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s status=%d body=%s", role, response.Code, response.Body.String())
		}
		var user auth.User
		if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
			t.Fatal(err)
		}
		if user.Role != role || user.PasswordHash != "" {
			t.Fatalf("unexpected public user: %+v", user)
		}
	}
	dispatcherLogin := harness.request(t, http.MethodPost, "/v1/auth/login", "", map[string]any{
		"username": "dispatcher-one", "password": "strong-password",
	})
	if dispatcherLogin.Code != http.StatusOK {
		t.Fatalf("dispatcher login: %s", dispatcherLogin.Body.String())
	}
	var login service.LoginResult
	_ = json.Unmarshal(dispatcherLogin.Body.Bytes(), &login)
	forbidden := harness.request(t, http.MethodPost, "/v1/users", login.Token, map[string]any{
		"username": "illegal-user", "password": "strong-password", "role": auth.RoleDispatcher,
	})
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("dispatcher created user status=%d body=%s", forbidden.Code, forbidden.Body.String())
	}
}

func TestJSONContractRejectsUnknownAndTrailingValues(t *testing.T) {
	harness := newAPIHarness(t)
	tests := []string{
		`{"username":"admin","password":"change-me-now","unexpected":true}`,
		`{"username":"admin","password":"change-me-now"} {}`,
		``,
	}
	for _, body := range tests {
		request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		harness.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %q status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
	}
}

func TestProtectedEndpointsRequireBearerToken(t *testing.T) {
	harness := newAPIHarness(t)
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/auth/me"},
		{http.MethodGet, "/v1/vehicles"},
		{http.MethodGet, "/v1/missions"},
		{http.MethodGet, "/v1/incidents"},
		{http.MethodPost, "/v1/regions"},
	}
	for _, item := range paths {
		response := harness.request(t, item.method, item.path, "", nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d", item.method, item.path, response.Code)
		}
	}
}
