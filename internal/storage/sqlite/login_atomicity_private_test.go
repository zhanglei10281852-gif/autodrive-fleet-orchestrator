package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
)

func TestLoginAuditFailureDoesNotLeakSession(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	password := "operator-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	open := func(t *testing.T) *Store {
		t.Helper()
		store, err := Open(ctx, filepath.Join(t.TempDir(), "login.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		user := auth.User{ID: "operator-1", Username: "operator", PasswordHash: string(hash), Role: auth.RoleDispatcher, Active: true, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateUser(ctx, user); err != nil {
			t.Fatalf("create operator: %v", err)
		}
		return store
	}

	t.Run("audit conflict rolls back session", func(t *testing.T) {
		store := open(t)
		if err := store.AppendAudit(ctx, audit.Event{
			ID: "aud_00000002", ActorID: "operator-1", ActorRole: string(auth.RoleDispatcher),
			Action: "seed.conflict", ObjectType: "session", ObjectID: "seed-session",
			Result: audit.ResultSuccess, RequestID: "seed-request", Details: []byte(`{}`), CreatedAt: now,
		}); err != nil {
			t.Fatalf("seed conflicting audit: %v", err)
		}

		authService := service.NewAuth(store, clock.NewManual(now), idgen.NewSequence(1), time.Hour)
		if _, err := authService.Login(ctx, "operator", password, "request-conflict"); !errors.Is(err, common.ErrConflict) {
			t.Fatalf("login error = %v, want conflict", err)
		}
		count, err := store.SessionCount(ctx, "operator-1", true, now)
		if err != nil {
			t.Fatalf("count active sessions: %v", err)
		}
		if count != 0 {
			t.Fatalf("failed audited login leaked %d active session(s), want 0", count)
		}
	})

	t.Run("successful login persists one usable session and audit", func(t *testing.T) {
		store := open(t)
		authService := service.NewAuth(store, clock.NewManual(now), idgen.NewSequence(10), time.Hour)
		result, err := authService.Login(ctx, "operator", password, "request-success")
		if err != nil {
			t.Fatalf("login failed: %v", err)
		}
		principal, err := authService.Authenticate(ctx, result.Token)
		if err != nil {
			t.Fatalf("authenticate returned token: %v", err)
		}
		if principal.UserID != "operator-1" || principal.SessionID == "" {
			t.Fatalf("unexpected principal: %+v", principal)
		}
		if count, err := store.SessionCount(ctx, "operator-1", true, now); err != nil || count != 1 {
			t.Fatalf("active session count = %d, err = %v, want 1", count, err)
		}
		if count, err := store.AuditCountForRequest(ctx, "request-success"); err != nil || count != 1 {
			t.Fatalf("login audit count = %d, err = %v, want 1", count, err)
		}
	})
}
