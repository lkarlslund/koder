package service

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestContextActorSourceUsesTrustedContextActorAndFallback(t *testing.T) {
	t.Parallel()
	fallback := memory.Actor{Kind: memory.ActorKindSystem, ID: "system:koder"}
	requestActor := memory.Actor{Kind: memory.ActorKindUser, ID: "user:local"}
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
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	ids := []string{"01a01688-fc5d-7f7d-8bb8-de244977f8a1", "01a01688-fc5d-7f7d-8bb8-de244977f8a2"}
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:koder"}),
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
	wantActor := memory.Actor{Kind: memory.ActorKindChat, ID: "chat:voice-assistant"}
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
	var missingContext context.Context
	if _, err := WithActor(missingContext, memory.Actor{Kind: memory.ActorKindSystem, ID: "system:koder"}); err == nil {
		t.Fatal("WithActor(nil) unexpectedly succeeded")
	}
	if _, err := WithActor(context.Background(), memory.Actor{}); err == nil {
		t.Fatal("WithActor(invalid actor) unexpectedly succeeded")
	}
	source := ContextActorSource(memory.Actor{})
	if _, err := source(context.Background()); err == nil {
		t.Fatal("ContextActorSource(invalid fallback) unexpectedly succeeded")
	}
}
