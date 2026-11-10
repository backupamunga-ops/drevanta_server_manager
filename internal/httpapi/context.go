package httpapi

import (
	"context"
)

// ctxKey carries the resolved session through request context.
type ctxKey struct{}

// withSession attaches the resolved session to the request context.
func withSession(ctx context.Context, sess *session) context.Context {
	return context.WithValue(ctx, ctxKey{}, sess)
}

// sessionFrom returns the resolved session, or nil for anonymous requests.
func sessionFrom(ctx context.Context) *session {
	sess, _ := ctx.Value(ctxKey{}).(*session)
	return sess
}
