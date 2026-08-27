package mission

import "fmt"

type WriteError struct {
	Operation string
	Cause     error
}

func (e WriteError) Error() string {
	return fmt.Sprintf("mission %s failed: %v", e.Operation, e.Cause)
}

// Unwrap exposes the underlying cause so that callers can use errors.Is /
// errors.As to distinguish a persistence conflict from other write failures.
// Without it, a wrapped ConflictError would be invisible to errors.Is, which
// breaks the idempotent-retry reconciliation in DispatchService.CreateMission.
func (e WriteError) Unwrap() error { return e.Cause }
