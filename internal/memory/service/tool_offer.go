package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
)

var ErrToolOfferDenied = errors.New("memory tool policy denied operation")

// ToolOffer is the candidate model-facing Memory surface. The tool owns
// action spelling; policy may only remove actions and scope kinds from the
// candidate supplied by the runtime.
type ToolOffer struct {
	Actions    []string
	ScopeKinds []memory.ScopeKind
}

// ToolOfferPolicy filters the model-facing Memory contract for an actor.
// Concrete object authorization remains the responsibility of ChunkPolicy.
type ToolOfferPolicy interface {
	FilterMemoryToolOffer(context.Context, memory.Actor, ToolOffer) (ToolOffer, error)
}

type ToolOfferPolicyFunc func(context.Context, memory.Actor, ToolOffer) (ToolOffer, error)

func (fn ToolOfferPolicyFunc) FilterMemoryToolOffer(ctx context.Context, actor memory.Actor, offer ToolOffer) (ToolOffer, error) {
	return fn(ctx, actor, offer)
}

type AllowAllToolOfferPolicy struct{}

func (AllowAllToolOfferPolicy) FilterMemoryToolOffer(_ context.Context, _ memory.Actor, offer ToolOffer) (ToolOffer, error) {
	return cloneToolOffer(offer), nil
}

// FilterToolOffer resolves the trusted actor, asks policy to filter the
// candidate, then intersects the result with that candidate so a faulty policy
// cannot accidentally advertise capabilities the runtime did not implement.
func (s *Service) FilterToolOffer(ctx context.Context, candidate ToolOffer) (ToolOffer, error) {
	if err := ctx.Err(); err != nil {
		return ToolOffer{}, err
	}
	candidate = normalizeToolOffer(candidate)
	actor, err := s.actor(ctx)
	if err != nil {
		return ToolOffer{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return ToolOffer{}, err
	}
	filtered, err := s.toolPolicy.FilterMemoryToolOffer(ctx, actor, cloneToolOffer(candidate))
	if err != nil {
		return ToolOffer{}, fmt.Errorf("filter memory tool offer: %w", err)
	}
	return intersectToolOffer(candidate, normalizeToolOffer(filtered)), nil
}

func normalizeToolOffer(value ToolOffer) ToolOffer {
	value.Actions = slices.Clone(value.Actions)
	for index := range value.Actions {
		value.Actions[index] = strings.TrimSpace(value.Actions[index])
	}
	value.Actions = slices.DeleteFunc(value.Actions, func(action string) bool { return action == "" })
	value.Actions = stableCompact(value.Actions)
	value.ScopeKinds = slices.Clone(value.ScopeKinds)
	value.ScopeKinds = slices.DeleteFunc(value.ScopeKinds, func(scope memory.ScopeKind) bool {
		return scope == memory.ScopeKindUnspecified || !scope.IsAScopeKind()
	})
	value.ScopeKinds = stableCompact(value.ScopeKinds)
	return value
}

func intersectToolOffer(candidate, filtered ToolOffer) ToolOffer {
	actions := make(map[string]struct{}, len(filtered.Actions))
	for _, action := range filtered.Actions {
		actions[action] = struct{}{}
	}
	scopes := make(map[memory.ScopeKind]struct{}, len(filtered.ScopeKinds))
	for _, scope := range filtered.ScopeKinds {
		scopes[scope] = struct{}{}
	}
	result := ToolOffer{}
	for _, action := range candidate.Actions {
		if _, allowed := actions[action]; allowed {
			result.Actions = append(result.Actions, action)
		}
	}
	for _, scope := range candidate.ScopeKinds {
		if _, allowed := scopes[scope]; allowed {
			result.ScopeKinds = append(result.ScopeKinds, scope)
		}
	}
	return result
}

func cloneToolOffer(value ToolOffer) ToolOffer {
	return ToolOffer{Actions: slices.Clone(value.Actions), ScopeKinds: slices.Clone(value.ScopeKinds)}
}

func stableCompact[T comparable](values []T) []T {
	seen := make(map[T]struct{}, len(values))
	result := values[:0]
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
