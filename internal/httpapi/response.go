package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
)

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := classifyError(err)
	writeJSON(w, status, ErrorResponse{Error: APIError{Code: code, Message: message, RequestID: request.ID(r.Context())}})
}

// WriteError lets process-level middleware use the same stable error contract.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	writeError(w, r, err)
}

func classifyError(err error) (int, string, string) {
	switch {
	case errors.Is(err, common.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "authentication is required or has expired"
	case errors.Is(err, common.ErrForbidden):
		return http.StatusForbidden, "forbidden", "the current role cannot perform this operation"
	case errors.Is(err, common.ErrNotFound):
		return http.StatusNotFound, "not_found", "the requested resource was not found"
	case errors.Is(err, common.ErrConflict):
		return http.StatusConflict, "conflict", err.Error()
	case errors.Is(err, common.ErrInvalid):
		return http.StatusUnprocessableEntity, "invalid_input", err.Error()
	case errors.Is(err, common.ErrExpired):
		return http.StatusGone, "expired", err.Error()
	case errors.Is(err, common.ErrUnavailable):
		return http.StatusServiceUnavailable, "unavailable", "a required dependency is unavailable"
	case errors.Is(err, context.Canceled):
		return 499, "cancelled", "the request was cancelled"
	default:
		return http.StatusInternalServerError, "internal_error", "the operation could not be completed"
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return common.FieldError{Field: "body", Problem: "must not be empty"}
		}
		return common.FieldError{Field: "body", Problem: err.Error()}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return common.FieldError{Field: "body", Problem: "must contain one JSON value"}
	}
	return nil
}
