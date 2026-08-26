package request

import "context"

type contextKey string

const (
	requestIDKey contextKey = "request_id"
)

type Envelope struct {
	RequestID string
}

func WithID(ctx context.Context, envelope *Envelope) context.Context {
	return context.WithValue(ctx, requestIDKey, envelope)
}

func ID(ctx context.Context) string {
	envelope, _ := ctx.Value(requestIDKey).(*Envelope)
	if envelope == nil {
		return ""
	}
	return envelope.RequestID
}
