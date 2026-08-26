package audit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Result string

const (
	ResultSuccess Result = "success"
	ResultDenied  Result = "denied"
	ResultFailure Result = "failure"
)

type Event struct {
	ID         string          `json:"id"`
	ActorID    string          `json:"actor_id"`
	ActorRole  string          `json:"actor_role"`
	Action     string          `json:"action"`
	ObjectType string          `json:"object_type"`
	ObjectID   string          `json:"object_id"`
	Result     Result          `json:"result"`
	RequestID  string          `json:"request_id"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (e Event) Validate() error {
	if e.ID == "" || e.ActorID == "" || e.RequestID == "" {
		return fmt.Errorf("audit id, actor, and request are required: %w", common.ErrInvalid)
	}
	if strings.TrimSpace(e.Action) == "" || strings.TrimSpace(e.ObjectType) == "" || strings.TrimSpace(e.ObjectID) == "" {
		return common.FieldError{Field: "audit", Problem: "action and object identity are required"}
	}
	if e.Result != ResultSuccess && e.Result != ResultDenied && e.Result != ResultFailure {
		return common.FieldError{Field: "result", Problem: "is not supported"}
	}
	if len(e.Details) > 0 && !json.Valid(e.Details) {
		return common.FieldError{Field: "details", Problem: "must be valid JSON"}
	}
	return nil
}

type Filter struct {
	ActorID    string
	Action     string
	ObjectType string
	ObjectID   string
	Result     Result
	From       *time.Time
	To         *time.Time
	Page       common.PageRequest
}

func Details(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode audit details: %w", err)
	}
	return encoded, nil
}
