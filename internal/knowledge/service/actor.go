package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
)

type actorContextKey struct{}

// WithActor attaches an actor established by a trusted transport or runtime boundary.
// Models and tool arguments must not be allowed to choose this value themselves.
func WithActor(ctx context.Context, actor knowledge.Actor) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("knowledge actor context is required")
	}
	if err := actor.Validate(); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, actorContextKey{}, actor), nil
}

// ContextActorSource resolves a trusted per-request actor and otherwise returns fallback.
// Validation occurs when the source is used so New remains free of request-time work.
func ContextActorSource(fallback knowledge.Actor) ActorSource {
	return func(ctx context.Context) (knowledge.Actor, error) {
		if ctx == nil {
			return knowledge.Actor{}, fmt.Errorf("knowledge actor context is required")
		}
		if actor, ok := ctx.Value(actorContextKey{}).(knowledge.Actor); ok {
			if err := actor.Validate(); err != nil {
				return knowledge.Actor{}, err
			}
			return actor, nil
		}
		if err := fallback.Validate(); err != nil {
			return knowledge.Actor{}, fmt.Errorf("validate fallback knowledge actor: %w", err)
		}
		return fallback, nil
	}
}
