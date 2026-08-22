package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
)

var ErrToolOfferDenied = errors.New("knowledge tool policy denied operation")

// ToolOffer is the candidate model-facing Knowledge surface. The tool owns
// action spelling; policy may only remove actions and scope kinds from the
// candidate supplied by the runtime.
type ToolOffer struct {
	Actions    []string
	ScopeKinds []knowledge.ScopeKind
}

// ToolOfferPolicy filters the model-facing Knowledge contract for an actor.
// Concrete object authorization remains the responsibility of ChunkPolicy.
type ToolOfferPolicy interface {
	FilterKnowledgeToolOffer(context.Context, knowledge.Actor, ToolOffer) (ToolOffer, error)
}

type ToolOfferPolicyFunc func(context.Context, knowledge.Actor, ToolOffer) (ToolOffer, error)

func (fn ToolOfferPolicyFunc) FilterKnowledgeToolOffer(ctx context.Context, actor knowledge.Actor, offer ToolOffer) (ToolOffer, error) {
	return fn(ctx, actor, offer)
}

type AllowAllToolOfferPolicy struct{}

func (AllowAllToolOfferPolicy) FilterKnowledgeToolOffer(_ context.Context, _ knowledge.Actor, offer ToolOffer) (ToolOffer, error) {
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
		return ToolOffer{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return ToolOffer{}, err
	}
	filtered, err := s.toolPolicy.FilterKnowledgeToolOffer(ctx, actor, cloneToolOffer(candidate))
	if err != nil {
		return ToolOffer{}, fmt.Errorf("filter knowledge tool offer: %w", err)
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
	value.ScopeKinds = slices.DeleteFunc(value.ScopeKinds, func(scope knowledge.ScopeKind) bool {
		return scope == knowledge.ScopeKindUnspecified || !scope.IsAScopeKind()
	})
	value.ScopeKinds = stableCompact(value.ScopeKinds)
	return value
}

func intersectToolOffer(candidate, filtered ToolOffer) ToolOffer {
	actions := make(map[string]struct{}, len(filtered.Actions))
	for _, action := range filtered.Actions {
		actions[action] = struct{}{}
	}
	scopes := make(map[knowledge.ScopeKind]struct{}, len(filtered.ScopeKinds))
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
