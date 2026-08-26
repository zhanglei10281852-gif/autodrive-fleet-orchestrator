package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/middleware"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestRevokedTokenCannotReusePreviousRequestPrincipal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	businessClock := clock.NewManual(now)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "revoked-principal.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ids := idgen.NewSequence(900)
	authService := service.NewAuth(store, businessClock, ids, time.Hour)
	if err := authService.Bootstrap(ctx, "principal-admin", "principal-password"); err != nil {
		t.Fatalf("bootstrap administrator: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := New(authService, service.NewFleet(store, businessClock, ids), service.NewDispatch(store, businessClock, ids),
		service.NewOperations(store, businessClock, ids, time.Minute), service.NewResource(store, businessClock, ids), store, logger, 1<<20)
	handler := middleware.Chain(api.Handler(), middleware.RequestID(ids))
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	login := func() string {
		response := request(http.MethodPost, "/v1/auth/login", "", map[string]any{"username": "principal-admin", "password": "principal-password"})
		if response.Code != http.StatusOK {
			t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
		}
		var result service.LoginResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Token == "" {
			t.Fatalf("decode login: result=%+v err=%v", result, err)
		}
		return result.Token
	}

	revokedToken := login()
	prime := request(http.MethodPost, "/v1/users", revokedToken, map[string]any{
		"username": "before-logout", "password": "strong-password", "role": auth.RoleDispatcher,
	})
	if prime.Code != http.StatusCreated {
		t.Fatalf("prime protected request status=%d body=%s", prime.Code, prime.Body.String())
	}
	logout := request(http.MethodPost, "/v1/auth/logout", revokedToken, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}

	denied := request(http.MethodPost, "/v1/users", revokedToken, map[string]any{
		"username": "created-after-revoke", "password": "strong-password", "role": auth.RoleSafetyOperator,
	})
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token created user: status=%d body=%s", denied.Code, denied.Body.String())
	}
	if _, err := store.UserByUsername(ctx, "created-after-revoke"); !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("revoked request persisted user, lookup error=%v", err)
	}

	freshToken := login()
	allowed := request(http.MethodPost, "/v1/users", freshToken, map[string]any{
		"username": "created-with-fresh-session", "password": "strong-password", "role": auth.RoleSafetyOperator,
	})
	if allowed.Code != http.StatusCreated {
		t.Fatalf("fresh administrator session status=%d body=%s", allowed.Code, allowed.Body.String())
	}
}
