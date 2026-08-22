package service

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestContextActorSourceUsesTrustedContextActorAndFallback(t *testing.T) {
	t.Parallel()
	fallback := knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:koder"}
	requestActor := knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:local"}
	source := ContextActorSource(fallback)
	got, err := source(context.Background())
	if err != nil || got != fallback {
		t.Fatalf("fallback actor = %#v, %v", got, err)
	}
	ctx, err := WithActor(context.Background(), requestActor)
	if err != nil {
		t.Fatalf("WithActor() error = %v", err)
	}
	got, err = source(ctx)
	if err != nil || got != requestActor {
		t.Fatalf("request actor = %#v, %v", got, err)
	}
}

func TestContextActorIsCommittedInServerOwnedRevision(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	ids := []string{"01a01688-fc5d-7f7d-8bb8-de244977f8a1", "01a01688-fc5d-7f7d-8bb8-de244977f8a2"}
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:koder"}),
		Now:   func() time.Time { return serviceTime },
		NewID: func() string {
			id := ids[0]
			ids = ids[1:]
			return id
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantActor := knowledge.Actor{Kind: knowledge.ActorKindChat, ID: "chat:voice-assistant"}
	ctx, err := WithActor(context.Background(), wantActor)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	if created.Chunk.Revision.Actor != wantActor {
		t.Fatalf("revision actor = %#v, want %#v", created.Chunk.Revision.Actor, wantActor)
	}
}

func TestWithActorRejectsInvalidOrMissingContext(t *testing.T) {
	t.Parallel()
	if _, err := WithActor(nil, knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:koder"}); err == nil {
		t.Fatal("WithActor(nil) unexpectedly succeeded")
	}
	if _, err := WithActor(context.Background(), knowledge.Actor{}); err == nil {
		t.Fatal("WithActor(invalid actor) unexpectedly succeeded")
	}
	source := ContextActorSource(knowledge.Actor{})
	if _, err := source(context.Background()); err == nil {
		t.Fatal("ContextActorSource(invalid fallback) unexpectedly succeeded")
	}
}
