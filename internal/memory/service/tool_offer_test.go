package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestFilterToolOfferUsesActorAndCannotEscalateCandidate(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	var gotActor memory.Actor
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: ToolOfferPolicyFunc(func(_ context.Context, actor memory.Actor, offer ToolOffer) (ToolOffer, error) {
			gotActor = actor
			return ToolOffer{
				Actions:    []string{"get", "get", "not-implemented"},
				ScopeKinds: []memory.ScopeKind{memory.ScopeKindProject, memory.ScopeKindProject, memory.ScopeKindPersonal},
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := memory.Actor{Kind: memory.ActorKindChat, ID: "01a01688-fc5d-7f7d-8bb8-de244977fee1"}
	ctx, err := WithActor(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := service.FilterToolOffer(ctx, ToolOffer{
		Actions:    []string{"search", "get"},
		ScopeKinds: []memory.ScopeKind{memory.ScopeKindGlobal, memory.ScopeKindProject},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotActor != actor || len(filtered.Actions) != 1 || filtered.Actions[0] != "get" ||
		len(filtered.ScopeKinds) != 1 || filtered.ScopeKinds[0] != memory.ScopeKindProject {
		t.Fatalf("FilterToolOffer() actor=%#v offer=%#v", gotActor, filtered)
	}
}

func TestFilterToolOfferPropagatesPolicyFailure(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	want := errors.New("policy unavailable")
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: ToolOfferPolicyFunc(func(context.Context, memory.Actor, ToolOffer) (ToolOffer, error) {
			return ToolOffer{}, want
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.FilterToolOffer(context.Background(), ToolOffer{Actions: []string{"get"}}); !errors.Is(err, want) {
		t.Fatalf("FilterToolOffer() error = %v, want policy failure", err)
	}
}
