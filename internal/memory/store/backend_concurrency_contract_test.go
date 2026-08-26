package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestConcurrentBackendContract(t *testing.T) {
	t.Parallel()
	for _, backend := range []struct {
		name string
		open func(*testing.T) memoryStoreAPI.Store
	}{
		{name: "memory", open: openMigrationMemory},
		{name: "pebble", open: openMigrationPebble},
	} {
		backend := backend
		t.Run(backend.name, func(t *testing.T) {
			t.Parallel()
			runConcurrentBackendContract(t, backend.open(t))
		})
	}
}

func runConcurrentBackendContract(t *testing.T, backend memoryStoreAPI.Store) {
	t.Helper()
	ctx := context.Background()
	seedMigrationStore(t, backend)

	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	var writers sync.WaitGroup
	for index := 0; index < contenders; index++ {
		index := index
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			results <- backend.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
				chunk, err := tx.Chunk(ctx, migrationChunkID)
				if err != nil {
					return err
				}
				chunk.Description = fmt.Sprintf("optimistic winner %d", index)
				chunk.UpdatedAt = migrationTime.Add(4 * time.Hour)
				chunk.Revision = memory.Revision{
					Number: 3, ID: memory.RevisionID(fmt.Sprintf("01a02b00-0000-7000-8000-%012x", index+1)),
					Actor: chunk.Revision.Actor, CreatedAt: chunk.UpdatedAt,
				}
				return tx.PutChunk(ctx, chunk, 2)
			})
		}()
	}
	close(start)
	writers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, memoryStoreAPI.ErrConflict):
			conflicts++
		default:
			t.Fatalf("optimistic update error = %v", err)
		}
	}
	if successes != 1 || conflicts != contenders-1 {
		t.Fatalf("optimistic updates = %d successes, %d conflicts", successes, conflicts)
	}

	var readFailures atomic.Int64
	var activity sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		activity.Add(1)
		go func() {
			defer activity.Done()
			for range 32 {
				if err := backend.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
					chunk, err := tx.Chunk(ctx, migrationChunkID)
					if err == nil && chunk.Revision.Number != 3 {
						return fmt.Errorf("observed partial revision %d", chunk.Revision.Number)
					}
					return err
				}); err != nil {
					readFailures.Add(1)
				}
			}
		}()
	}
	activity.Add(1)
	go func() {
		defer activity.Done()
		for index := 0; index < 32; index++ {
			if err := backend.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
				return tx.TouchChunk(ctx, migrationChunkID, migrationTime.Add(time.Duration(5+index)*time.Hour))
			}); err != nil {
				readFailures.Add(1)
			}
		}
	}()
	activity.Wait()
	if failures := readFailures.Load(); failures != 0 {
		t.Fatalf("concurrent reader/writer failures = %d", failures)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	viewCalled, updateCalled := false, false
	if err := backend.View(canceled, func(memoryStoreAPI.ReadTx) error {
		viewCalled = true
		return nil
	}); !errors.Is(err, context.Canceled) || viewCalled {
		t.Fatalf("View(canceled) = %v, called=%v", err, viewCalled)
	}
	if err := backend.Update(canceled, func(memoryStoreAPI.WriteTx) error {
		updateCalled = true
		return nil
	}); !errors.Is(err, context.Canceled) || updateCalled {
		t.Fatalf("Update(canceled) = %v, called=%v", err, updateCalled)
	}

	viewEntered := make(chan struct{})
	releaseView := make(chan struct{})
	viewDone := make(chan error, 1)
	go func() {
		viewDone <- backend.View(ctx, func(memoryStoreAPI.ReadTx) error {
			close(viewEntered)
			<-releaseView
			return nil
		})
	}()
	select {
	case <-viewEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight View() did not start")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- backend.Close() }()
	close(releaseView)
	select {
	case err := <-viewDone:
		if err != nil {
			t.Fatalf("in-flight View() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight View() did not finish")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Close() did not finish")
	}
	if err := backend.Update(ctx, func(memoryStoreAPI.WriteTx) error { return nil }); !errors.Is(err, memoryStoreAPI.ErrClosed) {
		t.Fatalf("Update() after concurrent Close = %v, want ErrClosed", err)
	}
}
