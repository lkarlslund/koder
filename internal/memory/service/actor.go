package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
)

type actorContextKey struct{}

// WithActor attaches an actor established by a trusted transport or runtime boundary.
// Models and tool arguments must not be allowed to choose this value themselves.
func WithActor(ctx context.Context, actor memory.Actor) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("memory actor context is required")
	}
	if err := actor.Validate(); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, actorContextKey{}, actor), nil
}

// ContextActorSource resolves a trusted per-request actor and otherwise returns fallback.
// Validation occurs when the source is used so New remains free of request-time work.
func ContextActorSource(fallback memory.Actor) ActorSource {
	return func(ctx context.Context) (memory.Actor, error) {
		if ctx == nil {
			return memory.Actor{}, fmt.Errorf("memory actor context is required")
		}
		if actor, ok := ctx.Value(actorContextKey{}).(memory.Actor); ok {
			if err := actor.Validate(); err != nil {
				return memory.Actor{}, err
			}
			return actor, nil
		}
		if err := fallback.Validate(); err != nil {
			return memory.Actor{}, fmt.Errorf("validate fallback memory actor: %w", err)
		}
		return fallback, nil
	}
}
