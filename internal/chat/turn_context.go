package chat

import "context"

type shouldStopContextKey struct{}

// ShouldStopFunc reports whether the current turn should stop before the next model round.
type ShouldStopFunc func() bool

// WithShouldStop stores the stop predicate on the context.
func WithShouldStop(ctx context.Context, fn ShouldStopFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, shouldStopContextKey{}, fn)
}

// ShouldStop reports whether the turn should stop gracefully.
func ShouldStop(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	fn, _ := ctx.Value(shouldStopContextKey{}).(ShouldStopFunc)
	if fn == nil {
		return false
	}
	return fn()
}
