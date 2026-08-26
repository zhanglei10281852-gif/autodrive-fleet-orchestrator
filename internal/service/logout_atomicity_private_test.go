package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestLogoutAuditFailureRollsBackSessionRevocation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "logout.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	user := auth.User{ID: "logout-user", Username: "logout-operator", PasswordHash: "password-hash", Role: auth.RoleFleetAdmin, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	conflicted := auth.Session{ID: "session-conflict", UserID: user.ID, TokenHash: "token-hash-conflict", ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeen: now}
	if err := store.CreateSession(ctx, conflicted); err != nil {
		t.Fatalf("create conflicted session: %v", err)
	}
	if err := store.AppendAudit(ctx, audit.Event{ID: "aud_00002000", ActorID: user.ID, ActorRole: string(user.Role), Action: "fixture.reserve", ObjectType: "session", ObjectID: conflicted.ID, Result: audit.ResultSuccess, RequestID: "occupied-audit", Details: []byte("{}"), CreatedAt: now}); err != nil {
		t.Fatalf("reserve generated audit ID: %v", err)
	}

	authService := NewAuth(store, clock.NewManual(now), idgen.NewSequence(2000), time.Hour)
	principal := auth.Principal{UserID: user.ID, Username: user.Username, Role: user.Role, SessionID: conflicted.ID}
	err = authService.Logout(ctx, principal, "logout-conflict")
	if !errors.Is(err, common.ErrConflict) {
		t.Fatalf("logout audit collision error = %v, want conflict", err)
	}
	stored, _, err := store.SessionByTokenHash(ctx, conflicted.TokenHash)
	if err != nil {
		t.Fatalf("load session after failed logout: %v", err)
	}
	if stored.RevokedAt != nil {
		t.Fatalf("failed logout revoked session before audit committed: %+v", stored)
	}
	count, err := store.AuditCountForRequest(ctx, "logout-conflict")
	if err != nil {
		t.Fatalf("count failed logout audit: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed logout left audit count=%d, want 0", count)
	}

	legal := auth.Session{ID: "session-legal", UserID: user.ID, TokenHash: "token-hash-legal", ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeen: now}
	if err := store.CreateSession(ctx, legal); err != nil {
		t.Fatalf("create legal session: %v", err)
	}
	principal.SessionID = legal.ID
	if err := authService.Logout(ctx, principal, "logout-legal"); err != nil {
		t.Fatalf("legal logout failed: %v", err)
	}
	legalStored, _, err := store.SessionByTokenHash(ctx, legal.TokenHash)
	if err != nil {
		t.Fatalf("load legal session: %v", err)
	}
	legalAudits, err := store.AuditCountForRequest(ctx, "logout-legal")
	if err != nil {
		t.Fatalf("count legal logout audit: %v", err)
	}
	if legalStored.RevokedAt == nil || legalAudits != 1 {
		t.Fatalf("legal logout did not commit revocation and audit: session=%+v audits=%d", legalStored, legalAudits)
	}
}
