package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Role string

const (
	RoleDispatcher     Role = "dispatcher"
	RoleSafetyOperator Role = "safety_operator"
	RoleFleetAdmin     Role = "fleet_admin"
)

func (r Role) Valid() bool {
	switch r {
	case RoleDispatcher, RoleSafetyOperator, RoleFleetAdmin:
		return true
	default:
		return false
	}
}

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (u User) Validate() error {
	if strings.TrimSpace(u.ID) == "" {
		return common.FieldError{Field: "id", Problem: "is required"}
	}
	if len(strings.TrimSpace(u.Username)) < 3 {
		return common.FieldError{Field: "username", Problem: "must contain at least 3 characters"}
	}
	if !u.Role.Valid() {
		return common.FieldError{Field: "role", Problem: "is not supported"}
	}
	if u.PasswordHash == "" {
		return common.FieldError{Field: "password_hash", Problem: "is required"}
	}
	return nil
}

type Session struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	TokenHash string     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	LastSeen  time.Time  `json:"last_seen_at"`
}

func (s Session) Validate(now time.Time) error {
	if s.ID == "" || s.UserID == "" || s.TokenHash == "" {
		return fmt.Errorf("session identity is incomplete: %w", common.ErrInvalid)
	}
	if !s.ExpiresAt.After(s.CreatedAt) {
		return common.FieldError{Field: "expires_at", Problem: "must be after created_at"}
	}
	if !s.ExpiresAt.After(now) {
		return common.ErrExpired
	}
	if s.RevokedAt != nil {
		return common.ErrUnauthorized
	}
	return nil
}

type Principal struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Role      Role   `json:"role"`
	SessionID string `json:"session_id"`
}

func (p Principal) Require(roles ...Role) error {
	for _, role := range roles {
		if p.Role == role {
			return nil
		}
	}
	return common.ErrForbidden
}
