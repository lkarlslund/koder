package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestFilterToolOfferUsesActorAndCannotEscalateCandidate(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	var gotActor knowledge.Actor
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: ToolOfferPolicyFunc(func(_ context.Context, actor knowledge.Actor, offer ToolOffer) (ToolOffer, error) {
			gotActor = actor
			return ToolOffer{
				Actions:    []string{"get", "get", "not-implemented"},
				ScopeKinds: []knowledge.ScopeKind{knowledge.ScopeKindProject, knowledge.ScopeKindProject, knowledge.ScopeKindPersonal},
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := knowledge.Actor{Kind: knowledge.ActorKindChat, ID: "01a01688-fc5d-7f7d-8bb8-de244977fee1"}
	ctx, err := WithActor(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := service.FilterToolOffer(ctx, ToolOffer{
		Actions:    []string{"search", "get"},
		ScopeKinds: []knowledge.ScopeKind{knowledge.ScopeKindGlobal, knowledge.ScopeKindProject},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotActor != actor || len(filtered.Actions) != 1 || filtered.Actions[0] != "get" ||
		len(filtered.ScopeKinds) != 1 || filtered.ScopeKinds[0] != knowledge.ScopeKindProject {
		t.Fatalf("FilterToolOffer() actor=%#v offer=%#v", gotActor, filtered)
	}
}

func TestFilterToolOfferPropagatesPolicyFailure(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	want := errors.New("policy unavailable")
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: ToolOfferPolicyFunc(func(context.Context, knowledge.Actor, ToolOffer) (ToolOffer, error) {
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
