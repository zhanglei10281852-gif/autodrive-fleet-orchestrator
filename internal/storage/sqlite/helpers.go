package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func fsMkdirAll(path string) error { return os.MkdirAll(path, 0o750) }

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse persisted time %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func encodeStrings(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode string list: %w", err)
	}
	return string(encoded), nil
}

func decodeStrings(value string) ([]string, error) {
	var decoded []string
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, fmt.Errorf("decode string list: %w", err)
	}
	if decoded == nil {
		decoded = []string{}
	}
	return decoded, nil
}

func mapSQLError(err error, resource string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", resource, common.ErrNotFound)
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unique constraint") ||
		strings.Contains(lower, "already has") ||
		strings.Contains(lower, "constraint failed") {
		return common.ConflictError{Resource: resource, Reason: err.Error()}
	}
	if strings.Contains(lower, "database is locked") || strings.Contains(lower, "busy") {
		return fmt.Errorf("%s database contention: %w", resource, common.ErrUnavailable)
	}
	return fmt.Errorf("%s persistence: %w", resource, err)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
