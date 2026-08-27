package mission

import "fmt"

type WriteError struct {
	Operation string
	Cause     error
}

func (e WriteError) Error() string {
	return fmt.Sprintf("mission %s failed: %v", e.Operation, e.Cause)
}
