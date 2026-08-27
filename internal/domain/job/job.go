package job

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusLeased    Status = "leased"
	StatusCompleted Status = "completed"
	StatusDead      Status = "dead"
)

type Outbox struct {
	ID            string          `json:"id"`
	Topic         string          `json:"topic"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	Status        Status          `json:"status"`
	Attempt       int             `json:"attempt"`
	MaxAttempts   int             `json:"max_attempts"`
	AvailableAt   time.Time       `json:"available_at"`
	LeaseOwner    string          `json:"lease_owner,omitempty"`
	LeaseUntil    *time.Time      `json:"lease_until,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (o Outbox) Validate() error {
	if o.ID == "" || strings.TrimSpace(o.Topic) == "" || o.AggregateID == "" {
		return fmt.Errorf("outbox identity, topic, and aggregate are required: %w", common.ErrInvalid)
	}
	if !json.Valid(o.Payload) {
		return common.FieldError{Field: "payload", Problem: "must be valid JSON"}
	}
	if o.MaxAttempts < 1 || o.MaxAttempts > 20 {
		return common.FieldError{Field: "max_attempts", Problem: "must be between 1 and 20"}
	}
	return nil
}

func (o Outbox) CanLease(now time.Time) bool {
	if o.Status == StatusPending && !o.AvailableAt.After(now) {
		return true
	}
	return o.Status == StatusLeased && o.LeaseUntil != nil && !o.LeaseUntil.After(now)
}

func (o Outbox) Lease(owner string, now time.Time, duration time.Duration) (Outbox, error) {
	if strings.TrimSpace(owner) == "" || duration <= 0 {
		return Outbox{}, common.ErrInvalid
	}
	if !o.CanLease(now) {
		return Outbox{}, common.ConflictError{Resource: "outbox", Reason: "event is not leaseable"}
	}
	o.Status = StatusLeased
	o.LeaseOwner = owner
	until := now.Add(duration).UTC()
	o.LeaseUntil = &until
	o.Attempt++
	o.UpdatedAt = now.UTC()
	return o, nil
}

func (o Outbox) Complete(owner string, now time.Time) (Outbox, error) {
	if o.Status != StatusLeased || o.LeaseOwner != owner {
		return Outbox{}, common.ConflictError{Resource: "outbox", Reason: "active lease owner is required"}
	}
	o.Status = StatusCompleted
	o.LeaseOwner = ""
	o.LeaseUntil = nil
	o.LastError = ""
	o.UpdatedAt = now.UTC()
	return o, nil
}

func (o Outbox) Fail(owner string, cause error, now time.Time, backoff time.Duration) (Outbox, error) {
	if o.Status != StatusLeased || o.LeaseOwner != owner {
		return Outbox{}, common.ConflictError{Resource: "outbox", Reason: "active lease owner is required"}
	}
	if cause == nil {
		return Outbox{}, common.FieldError{Field: "cause", Problem: "is required"}
	}
	o.LastError = cause.Error()
	o.LeaseOwner = ""
	o.LeaseUntil = nil
	if o.Attempt >= o.MaxAttempts {
		o.Status = StatusDead
	} else {
		o.Status = StatusPending
		o.AvailableAt = now.Add(backoff).UTC()
	}
	o.UpdatedAt = now.UTC()
	return o, nil
}

type DeliveryError struct {
	Permanent bool
	Cause     error
}

func (e DeliveryError) Error() string {
	if e.Cause == nil {
		return "outbox delivery failed"
	}
	return e.Cause.Error()
}

func (e DeliveryError) Unwrap() error { return e.Cause }
