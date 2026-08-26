package request

import "context"

type contextKey string

const (
	requestIDKey contextKey = "request_id"
)

func WithID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

func ID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}
