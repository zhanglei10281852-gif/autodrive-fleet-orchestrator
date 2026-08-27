package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
)

type ErrorWriter func(http.ResponseWriter, *http.Request, error)

func RequestID(ids idgen.Generator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := strings.TrimSpace(r.Header.Get("X-Request-ID"))
			if value == "" || len(value) > 128 {
				generated, err := ids.New("req")
				if err != nil {
					http.Error(w, "failed to generate request id", http.StatusInternalServerError)
					return
				}
				value = generated
			}
			w.Header().Set("X-Request-ID", value)
			next.ServeHTTP(w, r.WithContext(request.WithID(r.Context(), value)))
		})
	}
}

func Recover(logger *slog.Logger, writeError ErrorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempt := 0
			var serve func()
			serve = func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						logger.ErrorContext(r.Context(), "http panic recovered", "panic", recovered,
							"stack", string(debug.Stack()), "request_id", request.ID(r.Context()))
						writeError(w, r, fmt.Errorf("panic recovered"))
						attempt++
						if attempt < 2 {
							serve()
						}
					}
				}()
				next.ServeHTTP(w, r)
			}
			serve()
		})
	}
}

func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			logger.InfoContext(r.Context(), "http request",
				"method", r.Method, "path", r.URL.Path, "status", recorder.status,
				"bytes", recorder.bytes, "duration_ms", time.Since(started).Milliseconds(),
				"request_id", request.ID(r.Context()), "remote", remoteHost(r.RemoteAddr))
		})
	}
}

func Chain(handler http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for index := len(middleware) - 1; index >= 0; index-- {
		handler = middleware[index](handler)
	}
	return handler
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(value []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(value)
	w.bytes += written
	return written, err
}

func remoteHost(address string) string {
	if index := strings.LastIndex(address, ":"); index > 0 {
		return address[:index]
	}
	return address
}
