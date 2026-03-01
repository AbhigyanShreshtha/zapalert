package ctxmeta

import (
	"context"

	"go.uber.org/zap"
)

// Extractor extracts zap fields from context.
type Extractor func(ctx context.Context) []zap.Field

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	clientIDKey  contextKey = "client_id"
	ipKey        contextKey = "ip"
	userAgentKey contextKey = "user_agent"
)

// WithRequestID stores a request ID in context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// WithClientID stores a client ID in context.
func WithClientID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, clientIDKey, id)
}

// WithIP stores a client IP in context.
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipKey, ip)
}

// WithUserAgent stores a user-agent string in context.
func WithUserAgent(ctx context.Context, ua string) context.Context {
	return context.WithValue(ctx, userAgentKey, ua)
}

// DefaultExtractor emits request_id, client_id, ip, and user_agent when present.
func DefaultExtractor(ctx context.Context) []zap.Field {
	fields := make([]zap.Field, 0, 4)
	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		fields = append(fields, zap.String("request_id", v))
	}
	if v, ok := ctx.Value(clientIDKey).(string); ok && v != "" {
		fields = append(fields, zap.String("client_id", v))
	}
	if v, ok := ctx.Value(ipKey).(string); ok && v != "" {
		fields = append(fields, zap.String("ip", v))
	}
	if v, ok := ctx.Value(userAgentKey).(string); ok && v != "" {
		fields = append(fields, zap.String("user_agent", v))
	}
	return fields
}
