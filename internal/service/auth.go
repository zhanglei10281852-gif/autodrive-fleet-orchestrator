package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
)

type AuthRepository interface {
	CreateUser(context.Context, auth.User) error
	UpsertBootstrapUser(context.Context, auth.User) error
	UserByUsername(context.Context, string) (auth.User, error)
	UserByID(context.Context, string) (auth.User, error)
	CreateSessionWithAudit(context.Context, auth.Session, string, string) error
	SessionByTokenHash(context.Context, string) (auth.Session, auth.User, error)
	RevokeSession(context.Context, string, string, string, string, time.Time) error
	TouchSession(context.Context, string, time.Time) error
	DeleteExpiredSessions(context.Context, time.Time, int) (int64, error)
}

type AuthService struct {
	repository AuthRepository
	clock      clock.Clock
	ids        idgen.Generator
	ttl        time.Duration
	cache      *sessionPrincipalCache
}

type LoginResult struct {
	Token     string         `json:"token"`
	ExpiresAt time.Time      `json:"expires_at"`
	Principal auth.Principal `json:"principal"`
}

func NewAuth(repository AuthRepository, businessClock clock.Clock, ids idgen.Generator, ttl time.Duration) *AuthService {
	return &AuthService{
		repository: repository,
		clock:      businessClock,
		ids:        ids,
		ttl:        ttl,
		cache:      newSessionPrincipalCache(),
	}
}

func (s *AuthService) Bootstrap(ctx context.Context, username, password string) error {
	if len(strings.TrimSpace(username)) < 3 {
		return common.FieldError{Field: "username", Problem: "must contain at least three characters"}
	}
	if len(password) < 10 {
		return common.FieldError{Field: "password", Problem: "must contain at least ten characters"}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	id, err := s.ids.New("usr")
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.repository.UpsertBootstrapUser(ctx, auth.User{
		ID: id, Username: username, PasswordHash: string(hash), Role: auth.RoleFleetAdmin,
		Active: true, CreatedAt: now, UpdatedAt: now,
	})
}

func (s *AuthService) CreateUser(ctx context.Context, principal auth.Principal, username, password string, role auth.Role) (auth.User, error) {
	if err := principal.Require(auth.RoleFleetAdmin); err != nil {
		return auth.User{}, err
	}
	if len(password) < 10 {
		return auth.User{}, common.FieldError{Field: "password", Problem: "must contain at least ten characters"}
	}
	if !role.Valid() {
		return auth.User{}, common.FieldError{Field: "role", Problem: "is not supported"}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.User{}, fmt.Errorf("hash password: %w", err)
	}
	id, err := s.ids.New("usr")
	if err != nil {
		return auth.User{}, err
	}
	now := s.clock.Now()
	user := auth.User{
		ID: id, Username: strings.TrimSpace(username), PasswordHash: string(hash), Role: role,
		Active: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := user.Validate(); err != nil {
		return auth.User{}, err
	}
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return auth.User{}, err
	}
	return user, nil
}

func (s *AuthService) Login(ctx context.Context, username, password, requestID string) (LoginResult, error) {
	if strings.TrimSpace(requestID) == "" {
		return LoginResult{}, common.FieldError{Field: "request_id", Problem: "is required"}
	}
	user, err := s.repository.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return LoginResult{}, common.ErrUnauthorized
		}
		return LoginResult{}, err
	}
	if !user.Active || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return LoginResult{}, common.ErrUnauthorized
	}
	token, err := randomToken(32)
	if err != nil {
		return LoginResult{}, err
	}
	sessionID, err := s.ids.New("ses")
	if err != nil {
		return LoginResult{}, err
	}
	auditID, err := s.ids.New("aud")
	if err != nil {
		return LoginResult{}, err
	}
	now := s.clock.Now()
	session := auth.Session{
		ID: sessionID, UserID: user.ID, TokenHash: tokenHash(token), ExpiresAt: now.Add(s.ttl),
		CreatedAt: now, LastSeen: now,
	}
	if err := s.repository.CreateSessionWithAudit(ctx, session, auditID, requestID); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		Token: token, ExpiresAt: session.ExpiresAt,
		Principal: auth.Principal{UserID: user.ID, Username: user.Username, Role: user.Role, SessionID: session.ID},
	}, nil
}

func (s *AuthService) Authenticate(ctx context.Context, token string) (auth.Principal, error) {
	if strings.TrimSpace(token) == "" {
		return auth.Principal{}, common.ErrUnauthorized
	}
	now := s.clock.Now()
	hash := tokenHash(token)
	if principal, ok := s.cache.Get(hash, now); ok {
		return principal, nil
	}
	session, user, err := s.repository.SessionByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return auth.Principal{}, common.ErrUnauthorized
		}
		return auth.Principal{}, err
	}
	if !user.Active {
		return auth.Principal{}, common.ErrUnauthorized
	}
	if err := session.Validate(now); err != nil {
		return auth.Principal{}, common.ErrUnauthorized
	}
	principal := auth.Principal{UserID: user.ID, Username: user.Username, Role: user.Role, SessionID: session.ID}
	if now.Sub(session.LastSeen) >= time.Minute {
		if err := s.repository.TouchSession(ctx, session.ID, now); err != nil {
			return auth.Principal{}, err
		}
		s.cache.Put(hash, principal, session.ExpiresAt)
	}
	return principal, nil
}

func (s *AuthService) Logout(ctx context.Context, principal auth.Principal, requestID string) error {
	auditID, err := s.ids.New("aud")
	if err != nil {
		return err
	}
	return s.repository.RevokeSession(ctx, principal.SessionID, principal.UserID, auditID, requestID, s.clock.Now())
}

func (s *AuthService) PurgeExpired(ctx context.Context, limit int) (int64, error) {
	return s.repository.DeleteExpiredSessions(ctx, s.clock.Now(), limit)
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
