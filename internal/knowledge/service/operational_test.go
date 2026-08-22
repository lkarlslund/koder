package service

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

type blockingMaintenanceStore struct {
	*memory.Store
	started chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (s *blockingMaintenanceStore) RebuildIndexes(ctx context.Context) error {
	close(s.started)
	defer close(s.done)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Store.RebuildIndexes(ctx)
}

func TestShutdownOperationsCancelsAndJoinsIndexRebuild(t *testing.T) {
	t.Parallel()
	store := &blockingMaintenanceStore{
		Store: memory.New(), started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}),
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := service.StartIndexRebuild(context.Background()); err != nil {
		t.Fatalf("StartIndexRebuild() error = %v", err)
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("index rebuild did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.ShutdownOperations(ctx); err != nil {
		t.Fatalf("ShutdownOperations() error = %v", err)
	}
	select {
	case <-store.done:
	default:
		t.Fatal("ShutdownOperations returned before rebuild exited")
	}
	if _, err := service.StartIndexRebuild(context.Background()); !errors.Is(err, knowledgeStore.ErrClosed) {
		t.Fatalf("StartIndexRebuild(after shutdown) error = %v, want closed", err)
	}
}

func TestCancelIndexRebuildStopsOnlyTheActiveRebuild(t *testing.T) {
	t.Parallel()
	store := &blockingMaintenanceStore{
		Store: memory.New(), started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}),
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := New(Config{
		Store: store, Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartIndexRebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("index rebuild did not start")
	}
	canceled, err := service.CancelIndexRebuild(context.Background())
	if err != nil || !canceled.Accepted || !canceled.Status.Running {
		t.Fatalf("CancelIndexRebuild() = %#v, %v", canceled, err)
	}
	select {
	case <-store.done:
	case <-time.After(time.Second):
		t.Fatal("canceled index rebuild did not exit")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := service.CancelIndexRebuild(context.Background()); errors.Is(err, knowledgeStore.ErrConflict) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rebuild remained cancelable after worker exit")
		}
		runtime.Gosched()
	}
	status, err := service.OperationalStatus(context.Background())
	if err != nil || status.Store.IndexGeneration != 1 || status.LexicalIndex == nil || status.LexicalIndex.Running {
		t.Fatalf("status after cancellation = %#v, %v", status, err)
	}
}

func TestOperationalStatusAndAsynchronousIndexRebuild(t *testing.T) {
	t.Parallel()
	store := &blockingMaintenanceStore{
		Store: memory.New(), started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}),
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		Now:   func() time.Time { return serviceTime },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	initial, err := service.OperationalStatus(context.Background())
	if err != nil || initial.Store.Backend != "memory" || initial.Store.IndexGeneration != 1 ||
		!initial.MaintenanceAvailable || initial.LexicalIndex == nil || initial.LexicalIndex.Running ||
		initial.MutationCheckpoint.StreamID == "" {
		t.Fatalf("OperationalStatus() = %#v, %v", initial, err)
	}
	accepted, err := service.StartIndexRebuild(context.Background())
	if err != nil || !accepted.Accepted || !accepted.Status.Running || accepted.Status.TargetGeneration != 2 || !accepted.Status.StartedAt.Equal(serviceTime) {
		t.Fatalf("StartIndexRebuild() = %#v, %v", accepted, err)
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("index rebuild did not start")
	}
	if _, err := service.StartIndexRebuild(context.Background()); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("second StartIndexRebuild() error = %v, want conflict", err)
	}
	running, err := service.OperationalStatus(context.Background())
	if err != nil || running.LexicalIndex == nil || !running.LexicalIndex.Running || running.LexicalIndex.TargetGeneration != 2 {
		t.Fatalf("OperationalStatus(running) = %#v, %v", running, err)
	}
	close(store.release)
	select {
	case <-store.done:
	case <-time.After(time.Second):
		t.Fatal("index rebuild did not finish")
	}
	deadline := time.Now().Add(time.Second)
	for {
		complete, err := service.OperationalStatus(context.Background())
		if err == nil && complete.Store.IndexGeneration == 2 && complete.LexicalIndex != nil && !complete.LexicalIndex.Running && complete.LexicalIndex.ActiveGeneration == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("OperationalStatus(complete) = %#v, %v", complete, err)
		}
		runtime.Gosched()
	}
}

func TestOperationalPolicyCanWithholdStatusAndRebuild(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}),
		Operational: OperationalPolicyFunc(func(_ context.Context, actor knowledge.Actor, action OperationalAction) error {
			if actor.ID != "user:test" || action == "" {
				t.Fatalf("policy actor/action = %#v, %q", actor, action)
			}
			return errors.New("denied by test policy")
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := service.OperationalStatus(context.Background()); !errors.Is(err, ErrOperationalPolicyDenied) {
		t.Fatalf("OperationalStatus() error = %v, want policy denial", err)
	}
	if _, err := service.StartIndexRebuild(context.Background()); !errors.Is(err, ErrOperationalPolicyDenied) {
		t.Fatalf("StartIndexRebuild() error = %v, want policy denial", err)
	}
	classified := ClassifyError(ErrOperationalPolicyDenied)
	if classified.Code != ErrorCodeForbidden {
		t.Fatalf("ClassifyError(policy denial) = %#v", classified)
	}
}
