package middleware

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverDoesNotReenterPanickedHandler(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlerCalls := 0
	errorWrites := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		handlerCalls++
		panic(fmt.Sprintf("panic-%d", handlerCalls))
	})
	recovered := Recover(logger, func(w http.ResponseWriter, _ *http.Request, _ error) {
		errorWrites++
		w.WriteHeader(http.StatusInternalServerError)
	})(next)

	response := httptest.NewRecorder()
	recovered.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/missions", nil))

	if handlerCalls != 1 {
		t.Fatalf("panicked handler calls=%d, want exactly one", handlerCalls)
	}
	if errorWrites != 1 {
		t.Fatalf("panic error writes=%d, want exactly one", errorWrites)
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusInternalServerError)
	}
}
